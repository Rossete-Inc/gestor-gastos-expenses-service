package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/meta"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Metas ficam na partição do dono, ao lado dos gastos (ADR 0011):
// PK=USER#<dono>, SK=META#<categoriaId>. As vigências e os nomes são
// gravados por SalvarMeta; os alertas, só por MarcarAlerta, com update
// condicional, para que duas escritas simultâneas não notifiquem o mesmo
// limiar duas vezes.

const prefixoMeta = "META#"

type itemMeta struct {
	PK            string           `dynamodbav:"PK"`
	SK            string           `dynamodbav:"SK"`
	CategoriaID   string           `dynamodbav:"categoriaId"`
	NomeCategoria string           `dynamodbav:"nomeCategoria"`
	Grupo         string           `dynamodbav:"grupo"`
	Vigencias     []itemVigencia   `dynamodbav:"vigencias"`
	Alertas       map[string][]int `dynamodbav:"alertas"`
}

// itemVigencia grava ValorCentavos nil como NULL (meta removida no mês).
type itemVigencia struct {
	Desde         string `dynamodbav:"desde"`
	ValorCentavos *int64 `dynamodbav:"valorCentavos"`
}

// SKMeta é a sort key da meta da categoria.
func SKMeta(categoriaID string) string { return prefixoMeta + categoriaID }

func chaveMeta(donoID, categoriaID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"PK": s(PK(donoID)), "SK": s(SKMeta(categoriaID))}
}

func decodificarMeta(raw map[string]types.AttributeValue) (meta.Meta, error) {
	var it itemMeta
	if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
		return meta.Meta{}, errs.Internal(fmt.Errorf("decodificar meta: %w", err))
	}
	m := meta.Meta{
		DonoID: strings.TrimPrefix(it.PK, "USER#"), CategoriaID: it.CategoriaID,
		NomeCategoria: it.NomeCategoria, Grupo: it.Grupo, Alertas: it.Alertas,
	}
	for _, v := range it.Vigencias {
		m.Vigencias = append(m.Vigencias, meta.Vigencia{Desde: v.Desde, ValorCentavos: v.ValorCentavos})
	}
	if m.Alertas == nil {
		m.Alertas = map[string][]int{}
	}
	return m, nil
}

// ObterMeta implementa app.Store: a meta da categoria e se ela existe.
func (d *Dynamo) ObterMeta(ctx context.Context, donoID, categoriaID string) (meta.Meta, bool, error) {
	out, err := d.api.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(d.table), Key: chaveMeta(donoID, categoriaID), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return meta.Meta{}, false, errs.Internal(fmt.Errorf("ler meta: %w", err))
	}
	if len(out.Item) == 0 {
		return meta.Meta{}, false, nil
	}
	m, err := decodificarMeta(out.Item)
	return m, err == nil, err
}

// ListarMetas implementa app.Store: todas as metas da conta, inclusive as
// removidas (que guardam o histórico), na ordem da SK.
func (d *Dynamo) ListarMetas(ctx context.Context, donoID string) ([]meta.Meta, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(d.table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :meta)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": s(PK(donoID)), ":meta": s(prefixoMeta),
		},
		ConsistentRead: aws.Bool(true),
	}
	metas := []meta.Meta{}
	for {
		out, err := d.api.Query(ctx, in)
		if err != nil {
			return nil, errs.Internal(fmt.Errorf("listar metas: %w", err))
		}
		for _, raw := range out.Items {
			m, err := decodificarMeta(raw)
			if err != nil {
				return nil, err
			}
			metas = append(metas, m)
		}
		if len(out.LastEvaluatedKey) == 0 {
			return metas, nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// SalvarMeta implementa app.Store: grava nome, grupo e vigências da meta,
// criando o item se preciso, sem mexer nos alertas já marcados.
func (d *Dynamo) SalvarMeta(ctx context.Context, m meta.Meta) error {
	if strings.TrimSpace(m.DonoID) == "" || strings.TrimSpace(m.CategoriaID) == "" {
		return errs.Internal(errors.New("meta sem dono ou categoria"))
	}
	vigencias := make([]itemVigencia, 0, len(m.Vigencias))
	for _, v := range m.Vigencias {
		vigencias = append(vigencias, itemVigencia{Desde: v.Desde, ValorCentavos: v.ValorCentavos})
	}
	av, err := attributevalue.Marshal(vigencias)
	if err != nil {
		return errs.Internal(fmt.Errorf("serializar vigências: %w", err))
	}
	_, err = d.api.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(d.table),
		Key:       chaveMeta(m.DonoID, m.CategoriaID),
		UpdateExpression: aws.String("SET categoriaId = :cat, nomeCategoria = :nome, grupo = :grupo, " +
			"vigencias = :vig, alertas = if_not_exists(alertas, :vazio)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":cat": s(m.CategoriaID), ":nome": s(m.NomeCategoria), ":grupo": s(m.Grupo),
			":vig": av, ":vazio": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}},
		},
	})
	if err != nil {
		return errs.Internal(fmt.Errorf("gravar meta: %w", err))
	}
	return nil
}

// ExcluirMeta implementa app.Store: apaga a meta inteira, com o histórico
// (usado quando a categoria é excluída). Sem meta, não é erro.
func (d *Dynamo) ExcluirMeta(ctx context.Context, donoID, categoriaID string) error {
	_, err := d.api.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(d.table), Key: chaveMeta(donoID, categoriaID),
	})
	if err != nil {
		return errs.Internal(fmt.Errorf("excluir meta: %w", err))
	}
	return nil
}

// MarcarAlerta implementa app.Store: registra o limiar como notificado no
// mês e devolve true só se esta chamada o marcou. Se outra escrita marcou
// antes, ou a meta deixou de existir, devolve false sem erro.
func (d *Dynamo) MarcarAlerta(ctx context.Context, donoID, categoriaID, mes string, limiar int) (bool, error) {
	lim := &types.AttributeValueMemberN{Value: strconv.Itoa(limiar)}
	_, err := d.api.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(d.table),
		Key:              chaveMeta(donoID, categoriaID),
		UpdateExpression: aws.String("SET alertas.#mes = list_append(if_not_exists(alertas.#mes, :nenhum), :lim)"),
		ConditionExpression: aws.String("attribute_exists(PK) AND " +
			"(attribute_not_exists(alertas.#mes) OR NOT contains(alertas.#mes, :limiar))"),
		ExpressionAttributeNames: map[string]string{"#mes": mes},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":nenhum": &types.AttributeValueMemberL{Value: []types.AttributeValue{}},
			":lim":    &types.AttributeValueMemberL{Value: []types.AttributeValue{lim}},
			":limiar": lim,
		},
	})
	var ccf *types.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return false, nil
	}
	if err != nil {
		return false, errs.Internal(fmt.Errorf("marcar alerta da meta: %w", err))
	}
	return true, nil
}
