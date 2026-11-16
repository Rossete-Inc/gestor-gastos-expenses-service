// Package migracao converte os gastos do formato de chave anterior à change
// cartoes-credito para o atual (scripts/migrar-mes-referencia):
//
//	antes   SK=EXP#<AAAA-MM-DD>#<ULID>            gastoId=AAAAMMDD-<ULID>
//	depois  SK=EXP#<AAAA-MM>#<AAAA-MM-DD>#<ULID>  gastoId=AAAAMM-AAAAMMDD-<ULID>
//	        + mesReferencia=<AAAA-MM> (o mês da data: gastos antigos não têm
//	        cartão, então contam no mês da data, como antes)
//
// Cada gasto é trocado numa transação (apaga o antigo, grava o novo), com
// condições que tornam a migração idempotente: rodar de novo não encontra
// nada no formato antigo, e uma execução interrompida continua de onde
// parou. Os demais atributos são copiados sem mudança.
package migracao

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// API é o subconjunto do client DynamoDB usado pela migração.
type API interface {
	Scan(ctx context.Context, in *dynamodb.ScanInput, opt ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, opt ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// Resultado resume uma execução.
type Resultado struct {
	// Lidos são os itens de gasto (SK EXP#) avaliados.
	Lidos int
	// Legados são os que estavam no formato antigo.
	Legados int
	// Migrados são os gravados no formato atual (0 em simulação).
	Migrados int
	// JaMigrados são os que outra execução migrou no meio desta.
	JaMigrados int
}

var skLegada = regexp.MustCompile(`^EXP#(\d{4}-\d{2}-\d{2})#([0-9A-HJKMNP-TV-Z]{26})$`)

// Migrar percorre a tabela e converte os gastos legados. simular só conta,
// sem gravar. log recebe uma linha por gasto convertido (pode ser nil).
func Migrar(ctx context.Context, api API, tabela string, simular bool, log func(string)) (Resultado, error) {
	if log == nil {
		log = func(string) {}
	}
	var r Resultado
	in := &dynamodb.ScanInput{
		TableName:                 aws.String(tabela),
		FilterExpression:          aws.String("begins_with(SK, :exp)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":exp": &types.AttributeValueMemberS{Value: "EXP#"}},
		ConsistentRead:            aws.Bool(true),
	}
	for {
		out, err := api.Scan(ctx, in)
		if err != nil {
			return r, fmt.Errorf("ler a tabela: %w", err)
		}
		for _, item := range out.Items {
			r.Lidos++
			novo, ok := Converter(item)
			if !ok {
				continue
			}
			r.Legados++
			if simular {
				log(fmt.Sprintf("migraria %s -> %s", texto(item, "SK"), texto(novo, "SK")))
				continue
			}
			err := trocar(ctx, api, tabela, item, novo)
			switch {
			case errors.Is(err, errJaMigrado):
				r.JaMigrados++
			case err != nil:
				return r, err
			default:
				r.Migrados++
				log(fmt.Sprintf("migrado %s -> %s", texto(item, "SK"), texto(novo, "SK")))
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return r, nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// Converter devolve o item no formato atual, ou ok=false se ele não estiver
// no formato legado (já migrado, cartão ou outro tipo de item).
func Converter(item map[string]types.AttributeValue) (novo map[string]types.AttributeValue, ok bool) {
	m := skLegada.FindStringSubmatch(texto(item, "SK"))
	if m == nil || !gasto.DataValida(m[1]) {
		return nil, false
	}
	data, ulid := m[1], m[2]
	mes := data[:len(gasto.LayoutMes)]
	novo = make(map[string]types.AttributeValue, len(item)+1)
	for k, v := range item {
		novo[k] = v
	}
	novo["SK"] = &types.AttributeValueMemberS{Value: "EXP#" + mes + "#" + data + "#" + ulid}
	novo["gastoId"] = &types.AttributeValueMemberS{Value: gasto.IDDe(mes, data, ulid)}
	novo["mesReferencia"] = &types.AttributeValueMemberS{Value: mes}
	return novo, true
}

var errJaMigrado = errors.New("já migrado")

func trocar(ctx context.Context, api API, tabela string, antigo, novo map[string]types.AttributeValue) error {
	_, err := api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Delete: &types.Delete{
				TableName: aws.String(tabela), Key: map[string]types.AttributeValue{"PK": antigo["PK"], "SK": antigo["SK"]},
				ConditionExpression: aws.String("attribute_exists(PK)"),
			}},
			{Put: &types.Put{TableName: aws.String(tabela), Item: novo, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
		},
	})
	var tce *types.TransactionCanceledException
	if errors.As(err, &tce) && len(tce.CancellationReasons) > 0 &&
		aws.ToString(tce.CancellationReasons[0].Code) == "ConditionalCheckFailed" {
		// Outra execução apagou o antigo antes desta.
		return errJaMigrado
	}
	if err != nil {
		return fmt.Errorf("migrar %s: %w", texto(antigo, "SK"), err)
	}
	return nil
}

func texto(item map[string]types.AttributeValue, chave string) string {
	if v, ok := item[chave].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}
