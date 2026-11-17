package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/serie"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Séries (parcelamentos-recorrencias) ficam na partição do usuário:
// PK=USER#<userId>, SK=SERIE#<ULID>. Cada parcela ou ocorrência é um gasto
// comum (EXP#) com serieId, numero e ajustado.

const (
	prefixoSerie = "SERIE#"
	// maxTransacao é o limite de itens do TransactWriteItems.
	maxTransacao = 100
)

// CodigoSerieAlterada é o 409 de uma série gravada por outra operação entre
// a leitura e a escrita; o app tenta de novo.
const CodigoSerieAlterada = "serie_alterada"

// CodigoSerieExistente é o 409 de CriarSerie com um ID que já existe: o
// mesmo lançamento reenviado (chave de idempotência repetida).
const CodigoSerieExistente = "serie_existente"

type itemSerie struct {
	PK              string   `dynamodbav:"PK"`
	SK              string   `dynamodbav:"SK"`
	SerieID         string   `dynamodbav:"serieId"`
	Tipo            string   `dynamodbav:"tipo"`
	ValorCentavos   int64    `dynamodbav:"valorCentavos"`
	CategoriaID     string   `dynamodbav:"categoriaId"`
	Grupo           string   `dynamodbav:"grupo"`
	CartaoID        string   `dynamodbav:"cartaoId,omitempty"`
	Descricao       string   `dynamodbav:"descricao,omitempty"`
	Dia             int      `dynamodbav:"dia"`
	MesInicial      string   `dynamodbav:"mesInicial"`
	MesFinal        string   `dynamodbav:"mesFinal,omitempty"`
	PrimeiroNumero  int      `dynamodbav:"primeiroNumero"`
	TotalParcelas   int      `dynamodbav:"totalParcelas,omitempty"`
	MesesGerados    []string `dynamodbav:"mesesGerados,stringset,omitempty"`
	PrimeiroGastoID string   `dynamodbav:"primeiroGastoId,omitempty"`
	CriadoPor       string   `dynamodbav:"criadoPor,omitempty"`
	Versao          int      `dynamodbav:"versao"`
	CriadoEm        string   `dynamodbav:"criadoEm"`
	AtualizadoEm    string   `dynamodbav:"atualizadoEm,omitempty"`
}

// SKSerie é a sort key da série.
func SKSerie(id string) string { return prefixoSerie + id }

func chaveSerie(userID, id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"PK": s(PK(userID)), "SK": s(SKSerie(id))}
}

func paraItemSerie(se serie.Serie, versao int) (map[string]types.AttributeValue, error) {
	if strings.TrimSpace(se.UserID) == "" || !gasto.ULIDValido(se.ID) {
		return nil, errs.Internal(fmt.Errorf("série sem userId ou com ID inválido: %q", se.ID))
	}
	it := itemSerie{
		PK: PK(se.UserID), SK: SKSerie(se.ID), SerieID: se.ID, Tipo: se.Tipo,
		ValorCentavos: se.ValorCentavos, CategoriaID: se.CategoriaID, Grupo: string(se.Grupo),
		CartaoID: se.CartaoID, Descricao: se.Descricao, Dia: se.Dia,
		MesInicial: se.MesInicial, MesFinal: se.MesFinal, PrimeiroNumero: se.PrimeiroNumero,
		TotalParcelas: se.TotalParcelas, MesesGerados: se.MesesGerados, PrimeiroGastoID: se.PrimeiroGastoID,
		CriadoPor: se.CriadoPor, Versao: versao, CriadoEm: se.CriadoEm.UTC().Format(formatoData),
	}
	if !se.AtualizadoEm.IsZero() {
		it.AtualizadoEm = se.AtualizadoEm.UTC().Format(formatoData)
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return nil, errs.Internal(fmt.Errorf("serializar série: %w", err))
	}
	return av, nil
}

func decodificarSerie(raw map[string]types.AttributeValue) (serie.Serie, error) {
	var it itemSerie
	if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
		return serie.Serie{}, errs.Internal(fmt.Errorf("decodificar série: %w", err))
	}
	se := serie.Serie{
		ID: it.SerieID, UserID: strings.TrimPrefix(it.PK, "USER#"), Tipo: it.Tipo,
		ValorCentavos: it.ValorCentavos, CategoriaID: it.CategoriaID, Grupo: gasto.Grupo(it.Grupo),
		CartaoID: it.CartaoID, Descricao: it.Descricao, Dia: it.Dia,
		MesInicial: it.MesInicial, MesFinal: it.MesFinal, PrimeiroNumero: it.PrimeiroNumero,
		TotalParcelas: it.TotalParcelas, MesesGerados: it.MesesGerados, PrimeiroGastoID: it.PrimeiroGastoID,
		CriadoPor: it.CriadoPor, Versao: it.Versao,
	}
	var err error
	if se.CriadoEm, err = time.Parse(formatoData, it.CriadoEm); err != nil {
		return serie.Serie{}, errs.Internal(fmt.Errorf("criadoEm %q: %w", it.CriadoEm, err))
	}
	if it.AtualizadoEm != "" {
		if se.AtualizadoEm, err = time.Parse(formatoData, it.AtualizadoEm); err != nil {
			return serie.Serie{}, errs.Internal(fmt.Errorf("atualizadoEm %q: %w", it.AtualizadoEm, err))
		}
	}
	return se, nil
}

// CriarSerie implementa app.Store: a série e os gastos numa transação
// (até 72 parcelas + a série cabem nos 100 itens). Série já existente:
// errs.Conflict(CodigoSerieExistente), sem gravar nada.
func (d *Dynamo) CriarSerie(ctx context.Context, se serie.Serie, gastos []gasto.Gasto) error {
	if len(gastos)+1 > maxTransacao {
		return errs.Internal(fmt.Errorf("série com %d gastos não cabe numa transação", len(gastos)))
	}
	av, err := paraItemSerie(se, 1)
	if err != nil {
		return err
	}
	itens := []types.TransactWriteItem{{Put: &types.Put{
		TableName: aws.String(d.table), Item: av, ConditionExpression: aws.String("attribute_not_exists(PK)"),
	}}}
	for _, g := range gastos {
		ag, err := paraItem(g)
		if err != nil {
			return err
		}
		itens = append(itens, types.TransactWriteItem{Put: &types.Put{
			TableName: aws.String(d.table), Item: ag, ConditionExpression: aws.String("attribute_not_exists(PK)"),
		}})
	}
	_, err = d.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: itens})
	if falhouCondicao(err, 0) {
		return errs.Conflict(CodigoSerieExistente, "Lançamento já registrado.")
	}
	if err != nil {
		return errs.Internal(fmt.Errorf("criar série: %w", err))
	}
	return nil
}

// ObterSerie implementa app.Store.
func (d *Dynamo) ObterSerie(ctx context.Context, userID, id string) (serie.Serie, error) {
	if !gasto.ULIDValido(id) {
		return serie.Serie{}, serie.ErrNaoEncontrada()
	}
	out, err := d.api.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(d.table), Key: chaveSerie(userID, id), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return serie.Serie{}, errs.Internal(fmt.Errorf("ler série: %w", err))
	}
	if len(out.Item) == 0 {
		return serie.Serie{}, serie.ErrNaoEncontrada()
	}
	return decodificarSerie(out.Item)
}

// ListarSeries implementa app.Store: uma Query em begins_with(SK, "SERIE#").
func (d *Dynamo) ListarSeries(ctx context.Context, userID string) ([]serie.Serie, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(d.table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :serie)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": s(PK(userID)), ":serie": s(prefixoSerie),
		},
		ConsistentRead: aws.Bool(true),
	}
	series := []serie.Serie{}
	for {
		out, err := d.api.Query(ctx, in)
		if err != nil {
			return nil, errs.Internal(fmt.Errorf("listar séries: %w", err))
		}
		for _, raw := range out.Items {
			se, err := decodificarSerie(raw)
			if err != nil {
				return nil, err
			}
			series = append(series, se)
		}
		if len(out.LastEvaluatedKey) == 0 {
			return series, nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// GerarOcorrencias implementa app.Store: marca os meses na série (ADD
// mesesGerados) e grava as ocorrências, numa transação condicionada a
// nenhum dos meses já estar marcado. Se outra leitura já os marcou, não
// grava nada e não é erro. Os gastos vêm na ordem dos meses, um por mês.
func (d *Dynamo) GerarOcorrencias(ctx context.Context, se serie.Serie, meses []string, gastos []gasto.Gasto) error {
	if len(meses) != len(gastos) {
		return errs.Internal(errors.New("gerar ocorrências: um gasto por mês"))
	}
	for inicio := 0; inicio < len(meses); inicio += maxTransacao - 1 {
		fim := min(inicio+maxTransacao-1, len(meses))
		if err := d.gerarLote(ctx, se, meses[inicio:fim], gastos[inicio:fim]); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dynamo) gerarLote(ctx context.Context, se serie.Serie, meses []string, gastos []gasto.Gasto) error {
	valores := map[string]types.AttributeValue{
		":meses": &types.AttributeValueMemberSS{Value: meses},
		":um":    &types.AttributeValueMemberN{Value: "1"},
	}
	condicoes := []string{"attribute_exists(PK)"}
	for i, m := range meses {
		k := ":m" + strconv.Itoa(i)
		valores[k] = s(m)
		condicoes = append(condicoes, "NOT contains(mesesGerados, "+k+")")
	}
	itens := []types.TransactWriteItem{{Update: &types.Update{
		TableName:                 aws.String(d.table),
		Key:                       chaveSerie(se.UserID, se.ID),
		UpdateExpression:          aws.String("ADD mesesGerados :meses, versao :um"),
		ConditionExpression:       aws.String(strings.Join(condicoes, " AND ")),
		ExpressionAttributeValues: valores,
	}}}
	for _, g := range gastos {
		av, err := paraItem(g)
		if err != nil {
			return err
		}
		itens = append(itens, types.TransactWriteItem{Put: &types.Put{
			TableName: aws.String(d.table), Item: av, ConditionExpression: aws.String("attribute_not_exists(PK)"),
		}})
	}
	_, err := d.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: itens})
	if falhouCondicao(err, 0) {
		return nil // outra leitura criou estes meses (ou a série foi apagada)
	}
	if err != nil {
		return errs.Internal(fmt.Errorf("gerar ocorrências: %w", err))
	}
	return nil
}

// DaSerie implementa app.Store: os gastos (EXP#) com o serieId, numa Query
// pela partição com filtro.
func (d *Dynamo) DaSerie(ctx context.Context, userID, serieID string) ([]gasto.Gasto, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(d.table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :exp)"),
		FilterExpression:       aws.String("serieId = :serie"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": s(PK(userID)), ":exp": s(prefixoGasto), ":serie": s(serieID),
		},
		ConsistentRead: aws.Bool(true),
	}
	return d.gastos(ctx, in, "ler gastos da série")
}

// Aplicar implementa app.Store: grava as mudanças em transações de até 100
// itens. A série é gravada por cima só da versão lida (nova: só se não
// existir); outra gravação no meio é errs.Conflict(CodigoSerieAlterada).
// Acima de 100 itens as transações seguem uma após a outra; repetir a
// operação depois de uma falha no meio converge.
func (d *Dynamo) Aplicar(ctx context.Context, m serie.Mudancas) error {
	if strings.TrimSpace(m.UserID) == "" {
		return errs.Internal(errors.New("mudanças sem userId"))
	}
	var itens []types.TransactWriteItem
	for _, se := range m.Series {
		av, err := paraItemSerie(se, se.Versao+1)
		if err != nil {
			return err
		}
		put := &types.Put{TableName: aws.String(d.table), Item: av}
		if se.Versao == 0 {
			put.ConditionExpression = aws.String("attribute_not_exists(PK)")
		} else {
			put.ConditionExpression = aws.String("versao = :v")
			put.ExpressionAttributeValues = map[string]types.AttributeValue{":v": &types.AttributeValueMemberN{Value: strconv.Itoa(se.Versao)}}
		}
		itens = append(itens, types.TransactWriteItem{Put: put})
	}
	nSeries := len(itens)
	for _, id := range m.SeriesExcluidas {
		itens = append(itens, types.TransactWriteItem{Delete: &types.Delete{TableName: aws.String(d.table), Key: chaveSerie(m.UserID, id)}})
	}
	gravadas := map[string]bool{}
	for _, g := range m.Gravar {
		av, err := paraItem(g)
		if err != nil {
			return err
		}
		sk, _ := SK(g.ID)
		gravadas[sk] = true
		itens = append(itens, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(d.table), Item: av}})
	}
	for _, g := range m.Excluir {
		k, err := chave(g.UserID, g.ID)
		if err != nil {
			return err
		}
		if sk, _ := SK(g.ID); gravadas[sk] {
			continue
		}
		itens = append(itens, types.TransactWriteItem{Delete: &types.Delete{TableName: aws.String(d.table), Key: k}})
	}
	for inicio := 0; inicio < len(itens); inicio += maxTransacao {
		lote := itens[inicio:min(inicio+maxTransacao, len(itens))]
		_, err := d.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: lote})
		if err == nil {
			continue
		}
		for i := 0; inicio == 0 && i < min(nSeries, len(lote)); i++ {
			if falhouCondicao(err, i) {
				return errs.Conflict(CodigoSerieAlterada, "A série mudou enquanto era alterada. Tente de novo.")
			}
		}
		return errs.Internal(fmt.Errorf("gravar mudanças da série: %w", err))
	}
	return nil
}
