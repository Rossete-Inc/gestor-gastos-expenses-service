package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/cartao"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Cartões ficam na partição do usuário, ao lado dos gastos (ADR 0009):
// PK=USER#<userId>, SK=CARD#<ULID>. O prefixo diferente mantém os cartões
// fora das Queries de gastos (EXP#).

const prefixoCartao = "CARD#"

// itemCartao são os atributos gravados de um cartão. O limite é inteiro em
// centavos.
type itemCartao struct {
	PK             string `dynamodbav:"PK"`
	SK             string `dynamodbav:"SK"`
	CartaoID       string `dynamodbav:"cartaoId"`
	Apelido        string `dynamodbav:"apelido"`
	Cor            string `dynamodbav:"cor"`
	DiaFechamento  int    `dynamodbav:"diaFechamento"`
	DiaVencimento  int    `dynamodbav:"diaVencimento"`
	LimiteCentavos int64  `dynamodbav:"limiteCentavos"`
	Arquivado      bool   `dynamodbav:"arquivado"`
	CriadoEm       string `dynamodbav:"criadoEm"`
	AtualizadoEm   string `dynamodbav:"atualizadoEm,omitempty"`
}

// SKCartao é a sort key do cartão.
func SKCartao(id string) string { return prefixoCartao + id }

func chaveCartao(userID, id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"PK": s(PK(userID)), "SK": s(SKCartao(id))}
}

func paraItemCartao(c cartao.Cartao) (map[string]types.AttributeValue, error) {
	if strings.TrimSpace(c.UserID) == "" || strings.TrimSpace(c.ID) == "" {
		return nil, errs.Internal(errors.New("cartão sem userId ou ID"))
	}
	it := itemCartao{
		PK: PK(c.UserID), SK: SKCartao(c.ID), CartaoID: c.ID,
		Apelido: c.Apelido, Cor: c.Cor, DiaFechamento: c.DiaFechamento, DiaVencimento: c.DiaVencimento,
		LimiteCentavos: c.LimiteCentavos, Arquivado: c.Arquivado,
		CriadoEm: c.CriadoEm.UTC().Format(formatoData),
	}
	if !c.AtualizadoEm.IsZero() {
		it.AtualizadoEm = c.AtualizadoEm.UTC().Format(formatoData)
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return nil, errs.Internal(fmt.Errorf("serializar cartão: %w", err))
	}
	return av, nil
}

func decodificarCartao(raw map[string]types.AttributeValue) (cartao.Cartao, error) {
	var it itemCartao
	if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
		return cartao.Cartao{}, errs.Internal(fmt.Errorf("decodificar cartão: %w", err))
	}
	c := cartao.Cartao{
		ID: it.CartaoID, UserID: strings.TrimPrefix(it.PK, "USER#"),
		Apelido: it.Apelido, Cor: it.Cor, DiaFechamento: it.DiaFechamento, DiaVencimento: it.DiaVencimento,
		LimiteCentavos: it.LimiteCentavos, Arquivado: it.Arquivado,
	}
	var err error
	if c.CriadoEm, err = time.Parse(formatoData, it.CriadoEm); err != nil {
		return cartao.Cartao{}, errs.Internal(fmt.Errorf("criadoEm %q: %w", it.CriadoEm, err))
	}
	if it.AtualizadoEm != "" {
		if c.AtualizadoEm, err = time.Parse(formatoData, it.AtualizadoEm); err != nil {
			return cartao.Cartao{}, errs.Internal(fmt.Errorf("atualizadoEm %q: %w", it.AtualizadoEm, err))
		}
	}
	return c, nil
}

// CriarCartao implementa app.Store.
func (d *Dynamo) CriarCartao(ctx context.Context, c cartao.Cartao) error {
	av, err := paraItemCartao(c)
	if err != nil {
		return err
	}
	_, err = d.api.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(d.table), Item: av, ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		return errs.Internal(fmt.Errorf("gravar cartão: %w", err))
	}
	return nil
}

// ObterCartao implementa app.Store, em leitura consistente.
func (d *Dynamo) ObterCartao(ctx context.Context, userID, id string) (cartao.Cartao, error) {
	out, err := d.api.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(d.table), Key: chaveCartao(userID, id), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return cartao.Cartao{}, errs.Internal(fmt.Errorf("ler cartão: %w", err))
	}
	if len(out.Item) == 0 {
		return cartao.Cartao{}, cartao.ErrNaoEncontrado()
	}
	return decodificarCartao(out.Item)
}

// ListarCartoes implementa app.Store: todos os cartões do usuário, ativos e
// arquivados, na ordem da SK (ULID, ou seja, de cadastro).
func (d *Dynamo) ListarCartoes(ctx context.Context, userID string) ([]cartao.Cartao, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(d.table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :card)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": s(PK(userID)), ":card": s(prefixoCartao),
		},
		ConsistentRead: aws.Bool(true),
	}
	cartoes := []cartao.Cartao{}
	for {
		out, err := d.api.Query(ctx, in)
		if err != nil {
			return nil, errs.Internal(fmt.Errorf("listar cartões: %w", err))
		}
		for _, raw := range out.Items {
			c, err := decodificarCartao(raw)
			if err != nil {
				return nil, err
			}
			cartoes = append(cartoes, c)
		}
		if len(out.LastEvaluatedKey) == 0 {
			return cartoes, nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// SubstituirCartao implementa app.Store: grava o cartão por cima do
// existente. Se ele já não existir, devolve 404.
func (d *Dynamo) SubstituirCartao(ctx context.Context, c cartao.Cartao) error {
	av, err := paraItemCartao(c)
	if err != nil {
		return err
	}
	_, err = d.api.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(d.table), Item: av, ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	var ccf *types.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return cartao.ErrNaoEncontrado()
	}
	if err != nil {
		return errs.Internal(fmt.Errorf("atualizar cartão: %w", err))
	}
	return nil
}

// ExcluirCartao implementa app.Store. Quem chama confere antes que o cartão
// não tem gastos.
func (d *Dynamo) ExcluirCartao(ctx context.Context, userID, id string) error {
	_, err := d.api.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(d.table), Key: chaveCartao(userID, id), ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	var ccf *types.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return cartao.ErrNaoEncontrado()
	}
	if err != nil {
		return errs.Internal(fmt.Errorf("excluir cartão: %w", err))
	}
	return nil
}
