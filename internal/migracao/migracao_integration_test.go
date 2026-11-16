//go:build integration

package migracao

import (
	"context"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/dynamotest"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Tarefa 2.5: rodar duas vezes contra o DynamoDB Local; a segunda não muda
// nada, e os gastos continuam legíveis pelo store no mesmo mês de antes.
func TestMigrarDuasVezes(t *testing.T) {
	ctx := context.Background()
	c, tabela := dynamotest.ExpensesTable(t, store.TableName)
	d := store.NewDynamo(c, tabela)

	var legados []string
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i, data := range []string{"2026-10-31", "2026-11-03", "2026-11-03", "2026-12-01"} {
		u := gasto.NovoULID(t0.Add(time.Duration(i) * time.Millisecond))
		_, err := c.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(tabela), Item: map[string]types.AttributeValue{
			"PK": s("USER#u-1"), "SK": s("EXP#" + data + "#" + u), "gastoId": s(gasto.IDLegado(data, u)),
			"valorCentavos": &types.AttributeValueMemberN{Value: "100"}, "data": s(data), "grupo": s("CARTAO"),
			"categoriaId": s("lazer"), "recorrente": &types.AttributeValueMemberBOOL{Value: false},
			"criadoEm": s("2026-11-01T12:00:00.000Z"),
		}})
		if err != nil {
			t.Fatal(err)
		}
		legados = append(legados, gasto.IDLegado(data, u))
	}
	// Um gasto já no formato atual não é tocado.
	atual, err := gasto.Novo(gasto.NovoULID(t0.Add(time.Second)), "u-1", gasto.Dados{
		ValorCentavos: 1, Data: "2026-11-10", CategoriaID: "lazer", Grupo: gasto.Cartao,
		CartaoID: "01JC8Z6Q3N4W5X6Y7Z8A9B0CAR", MesReferencia: "2026-12",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Criar(ctx, atual); err != nil {
		t.Fatal(err)
	}

	simulado, err := Migrar(ctx, c, tabela, true, nil)
	if err != nil || simulado.Lidos != 5 || simulado.Legados != 4 || simulado.Migrados != 0 {
		t.Fatalf("simulação = %+v, %v", simulado, err)
	}
	r, err := Migrar(ctx, c, tabela, false, func(s string) { t.Log(s) })
	if err != nil || r.Migrados != 4 {
		t.Fatalf("primeira = %+v, %v", r, err)
	}
	r, err = Migrar(ctx, c, tabela, false, nil)
	if err != nil || r.Lidos != 5 || r.Legados != 0 || r.Migrados != 0 {
		t.Fatalf("segunda = %+v, %v", r, err)
	}

	novembro, err := d.DoMes(ctx, "u-1", "2026-11")
	if err != nil || len(novembro) != 2 {
		t.Fatalf("novembro = %v, %v", novembro, err)
	}
	for _, g := range novembro {
		p, ok := gasto.PartesDoID(g.ID)
		if !ok || p.Legado || g.MesReferencia != "2026-11" {
			t.Errorf("gasto migrado = %+v", g)
		}
	}
	for _, id := range legados {
		if _, err := d.Obter(ctx, "u-1", id); err == nil {
			t.Errorf("o ID legado %s ainda existe", id)
		}
	}
	// Dezembro: o migrado de 01/12 e o atual na fatura de dezembro.
	if dez, _ := d.DoMes(ctx, "u-1", "2026-12"); len(dez) != 2 {
		t.Errorf("dezembro = %v", dez)
	}
}
