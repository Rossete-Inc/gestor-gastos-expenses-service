// Package store grava os gastos no DynamoDB.
//
// Chaves (contrato com o CDK e com o seed do infra-base, docs/local.md):
//
//	gasto   PK=USER#<userId>   SK=EXP#<AAAA-MM-DD>#<ULID>
//
// Tudo de um usuário fica na partição dele, então a partition key isola os
// usuários: um gasto alheio simplesmente não é encontrado. A SK em ordem
// decrescente dá a listagem do mês do mais recente para o mais antigo, e o
// begins_with(SK, "EXP#AAAA-MM-") seleciona o mês. Os filtros por grupo e
// categoria usam FilterExpression; o volume pessoal (dezenas a centenas de
// gastos por mês) cabe nisso sem índice secundário (design.md).
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// TableName é o nome base da tabela; na AWS o CDK acrescenta o ambiente.
const TableName = "gestor-gastos-expenses"

const (
	prefixoGasto = "EXP#"
	formatoData  = "2006-01-02T15:04:05.000Z"
	// itensPorQuery é quantos itens cada Query da listagem avalia. Com
	// filtro, uma página pode precisar de várias Queries.
	itensPorQuery = 100
	// maxLote é o limite de itens do BatchWriteItem.
	maxLote = 25
	// maxTentativas limita as retentativas de itens não processados pelos
	// lotes (throttling).
	maxTentativas = 8
)

// API é o subconjunto do client DynamoDB usado aqui.
type API interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opt ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opt ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opt ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opt ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, opt ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
	TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, opt ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// Dynamo implementa app.Store.
type Dynamo struct {
	api    API
	table  string
	espera func(tentativa int) // pausa entre retentativas; trocada nos testes
}

// NewDynamo cria o store na tabela table.
func NewDynamo(api API, table string) *Dynamo {
	return &Dynamo{api: api, table: table, espera: func(n int) { time.Sleep(time.Duration(n*n) * 20 * time.Millisecond) }}
}

// item são os atributos gravados (docs/plano.md do infra-base). O valor é
// um inteiro em centavos, nunca float.
type item struct {
	PK            string `dynamodbav:"PK"`
	SK            string `dynamodbav:"SK"`
	GastoID       string `dynamodbav:"gastoId"`
	ValorCentavos int64  `dynamodbav:"valorCentavos"`
	Data          string `dynamodbav:"data"`
	Grupo         string `dynamodbav:"grupo"`
	CategoriaID   string `dynamodbav:"categoriaId"`
	Descricao     string `dynamodbav:"descricao,omitempty"`
	Recorrente    bool   `dynamodbav:"recorrente"`
	CriadoEm      string `dynamodbav:"criadoEm"`
	AtualizadoEm  string `dynamodbav:"atualizadoEm,omitempty"`
}

// PK é a partition key do usuário.
func PK(userID string) string { return "USER#" + userID }

// SK é a sort key do gasto id. ok é false se o ID estiver fora do formato.
func SK(id string) (sk string, ok bool) {
	data, ulid, ok := gasto.PartesDoID(id)
	if !ok {
		return "", false
	}
	return prefixoGasto + data + "#" + ulid, true
}

// prefixoMes seleciona os gastos do mês (AAAA-MM).
func prefixoMes(mes string) string { return prefixoGasto + mes + "-" }

func chave(userID, id string) (map[string]types.AttributeValue, error) {
	sk, ok := SK(id)
	if !ok {
		return nil, gasto.ErrNaoEncontrado()
	}
	return map[string]types.AttributeValue{"PK": s(PK(userID)), "SK": s(sk)}, nil
}

func paraItem(g gasto.Gasto) (map[string]types.AttributeValue, error) {
	sk, ok := SK(g.ID)
	if !ok {
		return nil, errs.Internal(fmt.Errorf("gasto com ID fora do formato: %q", g.ID))
	}
	if strings.TrimSpace(g.UserID) == "" {
		return nil, errs.Internal(errors.New("gasto sem userId"))
	}
	it := item{
		PK: PK(g.UserID), SK: sk, GastoID: g.ID,
		ValorCentavos: g.ValorCentavos, Data: g.Data, Grupo: string(g.Grupo), CategoriaID: g.CategoriaID,
		Descricao: g.Descricao, Recorrente: g.Recorrente,
		CriadoEm: g.CriadoEm.UTC().Format(formatoData),
	}
	if !g.AtualizadoEm.IsZero() {
		it.AtualizadoEm = g.AtualizadoEm.UTC().Format(formatoData)
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return nil, errs.Internal(fmt.Errorf("serializar gasto: %w", err))
	}
	return av, nil
}

func decodificar(raw map[string]types.AttributeValue) (gasto.Gasto, error) {
	var it item
	if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
		return gasto.Gasto{}, errs.Internal(fmt.Errorf("decodificar gasto: %w", err))
	}
	g := gasto.Gasto{
		ID: it.GastoID, UserID: strings.TrimPrefix(it.PK, "USER#"),
		ValorCentavos: it.ValorCentavos, Data: it.Data, Grupo: gasto.Grupo(it.Grupo), CategoriaID: it.CategoriaID,
		Descricao: it.Descricao, Recorrente: it.Recorrente,
	}
	var err error
	if g.CriadoEm, err = time.Parse(formatoData, it.CriadoEm); err != nil {
		return gasto.Gasto{}, errs.Internal(fmt.Errorf("criadoEm %q: %w", it.CriadoEm, err))
	}
	if it.AtualizadoEm != "" {
		if g.AtualizadoEm, err = time.Parse(formatoData, it.AtualizadoEm); err != nil {
			return gasto.Gasto{}, errs.Internal(fmt.Errorf("atualizadoEm %q: %w", it.AtualizadoEm, err))
		}
	}
	return g, nil
}

// Criar implementa app.Store. A condição protege contra sobrescrever outro
// gasto, o que só aconteceria com um ULID repetido.
func (d *Dynamo) Criar(ctx context.Context, g gasto.Gasto) error {
	av, err := paraItem(g)
	if err != nil {
		return err
	}
	_, err = d.api.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(d.table), Item: av, ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		return errs.Internal(fmt.Errorf("gravar gasto: %w", err))
	}
	return nil
}

// Obter implementa app.Store, em leitura consistente para a tela de edição
// ver o gasto recém-lançado.
func (d *Dynamo) Obter(ctx context.Context, userID, id string) (gasto.Gasto, error) {
	k, err := chave(userID, id)
	if err != nil {
		return gasto.Gasto{}, err
	}
	out, err := d.api.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(d.table), Key: k, ConsistentRead: aws.Bool(true)})
	if err != nil {
		return gasto.Gasto{}, errs.Internal(fmt.Errorf("ler gasto: %w", err))
	}
	if len(out.Item) == 0 {
		return gasto.Gasto{}, gasto.ErrNaoEncontrado()
	}
	return decodificar(out.Item)
}

// Substituir implementa app.Store. Com a mesma chave, é um Put condicionado
// à existência do item. Com a data alterada, a chave muda: apaga o antigo e
// grava o novo numa transação, para o gasto nunca sumir nem duplicar.
func (d *Dynamo) Substituir(ctx context.Context, antigo, novo gasto.Gasto) error {
	if antigo.UserID != novo.UserID || antigo.ULID() != novo.ULID() {
		return errs.Internal(errors.New("substituir: o gasto novo tem de ser o mesmo gasto do mesmo usuário"))
	}
	av, err := paraItem(novo)
	if err != nil {
		return err
	}
	kAntiga, err := chave(antigo.UserID, antigo.ID)
	if err != nil {
		return err
	}
	if antigo.ID == novo.ID {
		_, err = d.api.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(d.table), Item: av, ConditionExpression: aws.String("attribute_exists(PK)"),
		})
		var ccf *types.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return gasto.ErrNaoEncontrado()
		}
		if err != nil {
			return errs.Internal(fmt.Errorf("atualizar gasto: %w", err))
		}
		return nil
	}
	_, err = d.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Delete: &types.Delete{TableName: aws.String(d.table), Key: kAntiga, ConditionExpression: aws.String("attribute_exists(PK)")}},
			{Put: &types.Put{TableName: aws.String(d.table), Item: av, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
		},
	})
	if err == nil {
		return nil
	}
	if falhouCondicao(err, 0) {
		return gasto.ErrNaoEncontrado()
	}
	return errs.Internal(fmt.Errorf("mudar a data do gasto: %w", err))
}

// falhouCondicao informa se a transação foi cancelada pela condição do item
// de índice i.
func falhouCondicao(err error, i int) bool {
	var tce *types.TransactionCanceledException
	if !errors.As(err, &tce) || i >= len(tce.CancellationReasons) {
		return false
	}
	return aws.ToString(tce.CancellationReasons[i].Code) == "ConditionalCheckFailed"
}

// Excluir implementa app.Store.
func (d *Dynamo) Excluir(ctx context.Context, userID, id string) error {
	k, err := chave(userID, id)
	if err != nil {
		return err
	}
	_, err = d.api.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(d.table), Key: k, ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	var ccf *types.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return gasto.ErrNaoEncontrado()
	}
	if err != nil {
		return errs.Internal(fmt.Errorf("excluir gasto: %w", err))
	}
	return nil
}

// Listar implementa app.Store.
//
// O Limit do DynamoDB conta os itens avaliados antes do filtro, então a
// página é montada aqui: Queries de até itensPorQuery itens, em ordem
// decrescente, até juntar limite gastos aceitos ou acabar o mês. A chave de
// continuação é a do último gasto devolvido, e não o LastEvaluatedKey, que
// pode estar adiante dele. Quando a página enche exatamente no fim de uma
// Query que ainda tinha continuação, devolve a chave mesmo sem saber se há
// mais gastos; a página seguinte pode então vir vazia, sem cursor.
func (d *Dynamo) Listar(ctx context.Context, userID string, f gasto.Filtro, inicio page.Key, limite int) ([]gasto.Gasto, page.Key, error) {
	pk := PK(userID)
	in := &dynamodb.QueryInput{
		TableName:              aws.String(d.table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :mes)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": s(pk), ":mes": s(prefixoMes(f.Mes)),
		},
		ScanIndexForward: aws.Bool(false),
		ConsistentRead:   aws.Bool(true),
		Limit:            aws.Int32(itensPorQuery),
	}
	var filtros []string
	if f.Grupo != "" {
		filtros = append(filtros, "grupo = :grupo")
		in.ExpressionAttributeValues[":grupo"] = s(string(f.Grupo))
	}
	if f.CategoriaID != "" {
		filtros = append(filtros, "categoriaId = :categoria")
		in.ExpressionAttributeValues[":categoria"] = s(f.CategoriaID)
	}
	if len(filtros) > 0 {
		in.FilterExpression = aws.String(strings.Join(filtros, " AND "))
	}
	if inicio != nil {
		// O cursor é assinado pelo app; conferir a chave aqui é defesa extra
		// para nunca ler fora da partição e do mês pedidos.
		if inicio["PK"] != pk || !strings.HasPrefix(inicio["SK"], prefixoMes(f.Mes)) || len(inicio) != 2 {
			return nil, nil, errs.BadRequest("cursor_invalido", "Cursor de paginação inválido. Volte para a primeira página.")
		}
		in.ExclusiveStartKey = map[string]types.AttributeValue{"PK": s(inicio["PK"]), "SK": s(inicio["SK"])}
	}

	itens := make([]gasto.Gasto, 0, limite)
	for {
		out, err := d.api.Query(ctx, in)
		if err != nil {
			return nil, nil, errs.Internal(fmt.Errorf("listar gastos: %w", err))
		}
		for i, raw := range out.Items {
			g, err := decodificar(raw)
			if err != nil {
				return nil, nil, err
			}
			itens = append(itens, g)
			if len(itens) == limite {
				if i == len(out.Items)-1 && len(out.LastEvaluatedKey) == 0 {
					return itens, nil, nil
				}
				sk, _ := SK(g.ID)
				return itens, page.Key{"PK": pk, "SK": sk}, nil
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return itens, nil, nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// ExisteNaCategoria implementa app.Store: percorre a partição do usuário só
// com as chaves e para no primeiro gasto da categoria. Leitura consistente,
// para um gasto recém-lançado bloquear a exclusão da categoria.
func (d *Dynamo) ExisteNaCategoria(ctx context.Context, userID, categoriaID string) (bool, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(d.table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :exp)"),
		FilterExpression:       aws.String("categoriaId = :categoria"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": s(PK(userID)), ":exp": s(prefixoGasto), ":categoria": s(categoriaID),
		},
		ProjectionExpression: aws.String("PK, SK"),
		ConsistentRead:       aws.Bool(true),
	}
	for {
		out, err := d.api.Query(ctx, in)
		if err != nil {
			return false, errs.Internal(fmt.Errorf("procurar gasto na categoria: %w", err))
		}
		if len(out.Items) > 0 {
			return true, nil
		}
		if len(out.LastEvaluatedKey) == 0 {
			return false, nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// ExcluirDoUsuario implementa app.Store: lê só as chaves da partição do
// usuário e apaga tudo em lotes de 25. Rodar de novo não encontra nada.
func (d *Dynamo) ExcluirDoUsuario(ctx context.Context, userID string) (int, error) {
	if strings.TrimSpace(userID) == "" {
		return 0, errs.Internal(errors.New("excluir do usuário: userId vazio"))
	}
	in := &dynamodb.QueryInput{
		TableName:                 aws.String(d.table),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(PK(userID))},
		ProjectionExpression:      aws.String("PK, SK"),
		ConsistentRead:            aws.Bool(true),
	}
	var pedidos []types.WriteRequest
	for {
		out, err := d.api.Query(ctx, in)
		if err != nil {
			return 0, errs.Internal(fmt.Errorf("listar gastos do usuário: %w", err))
		}
		for _, raw := range out.Items {
			pedidos = append(pedidos, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: map[string]types.AttributeValue{"PK": raw["PK"], "SK": raw["SK"]}}})
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
	if err := d.emLotes(ctx, pedidos); err != nil {
		return 0, err
	}
	return len(pedidos), nil
}

// emLotes envia os pedidos em lotes de 25, reenviando os não processados.
func (d *Dynamo) emLotes(ctx context.Context, pedidos []types.WriteRequest) error {
	for inicio := 0; inicio < len(pedidos); inicio += maxLote {
		lote := pedidos[inicio:min(inicio+maxLote, len(pedidos))]
		for tentativa := 1; len(lote) > 0; tentativa++ {
			if tentativa > maxTentativas {
				return errs.Internal(errors.New("apagar em lote: itens não processados após as retentativas"))
			}
			if tentativa > 1 {
				d.espera(tentativa)
			}
			out, err := d.api.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
				RequestItems: map[string][]types.WriteRequest{d.table: lote},
			})
			if err != nil {
				return errs.Internal(fmt.Errorf("apagar em lote: %w", err))
			}
			lote = out.UnprocessedItems[d.table]
		}
	}
	return nil
}

func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
