//go:build integration

package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/dynamotest"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

func portaLivre(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	_, porta, _ := net.SplitHostPort(l.Addr().String())
	return porta
}

// TestPontaAPonta sobe o cmd/local de verdade, contra uma tabela temporária
// no DynamoDB Local, e o usa como o BFF e o publisher de eventos usarão: o
// roteiro da fatia de lançamento de gastos do lado do serviço.
func TestPontaAPonta(t *testing.T) {
	_, tabela := dynamotest.ExpensesTable(t, "gestor-gastos-expenses")
	porta := portaLivre(t)
	base := "http://127.0.0.1:" + porta

	ctx, cancel := context.WithCancel(context.Background())
	terminou := make(chan error, 1)
	go func() {
		terminou <- run(ctx, env(map[string]string{
			"RUNTIME": "local", "PORT": porta, "LOG_LEVEL": "error",
			"DYNAMODB_ENDPOINT": dynamotest.Endpoint(), "EXPENSES_TABLE": tabela,
		}))
	}()

	pronto := false
	for range 50 {
		if resp, err := http.Get(base + "/healthz"); err == nil {
			_ = resp.Body.Close()
			pronto = resp.StatusCode == http.StatusOK
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !pronto {
		cancel()
		t.Fatalf("cmd/local não subiu: %v", <-terminou)
	}

	bff := invoke.NewClient(invoke.NewHTTP(base, nil))
	chamar := func(t *testing.T, op, user string, in, out any) {
		t.Helper()
		if err := bff.Call(ctx, op, user, in, out); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
	listar := func(t *testing.T, user string, in contract.ListarGastosIn) contract.PaginaGastos {
		t.Helper()
		var pg contract.PaginaGastos
		chamar(t, contract.OpListarGastos, user, in, &pg)
		return pg
	}

	var almoco, aluguel contract.Gasto
	t.Run("lançar gasto de cartão e fixo", func(t *testing.T) {
		chamar(t, contract.OpCriarGasto, "u-a", contract.GastoIn{ValorCentavos: 4590, Data: "2026-11-03", CategoriaID: "alimentacao", Grupo: "CARTAO", Descricao: "Almoço"}, &almoco)
		chamar(t, contract.OpCriarGasto, "u-a", contract.GastoIn{ValorCentavos: 150_000, Data: "2026-11-05", CategoriaID: "aluguel", Grupo: "FIXO", Recorrente: true}, &aluguel)
		pg := listar(t, "u-a", contract.ListarGastosIn{Mes: "2026-11"})
		if len(pg.Items) != 2 || pg.Items[0] != aluguel || pg.Items[1] != almoco {
			t.Errorf("novembro = %+v", pg.Items)
		}
	})

	t.Run("valor inválido", func(t *testing.T) {
		err := bff.Call(ctx, contract.OpCriarGasto, "u-a", contract.GastoIn{ValorCentavos: 0, Data: "2026-11-03", CategoriaID: "alimentacao", Grupo: "CARTAO"}, nil)
		if e := errs.As(err); e == nil || e.Kind != errs.KindValidation || len(e.Fields) != 1 || e.Fields[0].Field != "valorCentavos" {
			t.Errorf("erro = %v", err)
		}
	})

	t.Run("filtro por grupo e paginação", func(t *testing.T) {
		if pg := listar(t, "u-a", contract.ListarGastosIn{Mes: "2026-11", Grupo: "FIXO"}); len(pg.Items) != 1 || pg.Items[0].ID != aluguel.ID {
			t.Errorf("FIXO = %+v", pg.Items)
		}
		p1 := listar(t, "u-a", contract.ListarGastosIn{Mes: "2026-11", Limit: 1})
		if len(p1.Items) != 1 || p1.NextCursor == "" {
			t.Fatalf("página 1 = %+v", p1)
		}
		p2 := listar(t, "u-a", contract.ListarGastosIn{Mes: "2026-11", Limit: 1, Cursor: p1.NextCursor})
		if len(p2.Items) != 1 || p2.Items[0].ID != almoco.ID {
			t.Errorf("página 2 = %+v", p2)
		}
		err := bff.Call(ctx, contract.OpListarGastos, "u-b", contract.ListarGastosIn{Mes: "2026-11", Cursor: p1.NextCursor}, nil)
		if e := errs.As(err); e == nil || e.Code != contract.CodigoCursorInvalido {
			t.Errorf("cursor de outro usuário = %v", err)
		}
	})

	t.Run("outro usuário recebe 404", func(t *testing.T) {
		err := bff.Call(ctx, contract.OpObterGasto, "u-b", contract.ObterGastoIn{ID: almoco.ID}, nil)
		if e := errs.As(err); e == nil || e.Kind != errs.KindNotFound {
			t.Errorf("obter = %v", err)
		}
		err = bff.Call(ctx, contract.OpExcluirGasto, "u-b", contract.ExcluirGastoIn{ID: almoco.ID}, nil)
		if e := errs.As(err); e == nil || e.Kind != errs.KindNotFound {
			t.Errorf("excluir = %v", err)
		}
	})

	t.Run("editar trocando de grupo e de data", func(t *testing.T) {
		var editado contract.Gasto
		chamar(t, contract.OpAtualizarGasto, "u-a", contract.AtualizarGastoIn{ID: almoco.ID, GastoIn: contract.GastoIn{
			ValorCentavos: 9990, Data: "2026-10-30", CategoriaID: "internet", Grupo: "FIXO",
		}}, &editado)
		if editado.ID == almoco.ID || editado.Grupo != "FIXO" || editado.Data != "2026-10-30" {
			t.Errorf("editado = %+v", editado)
		}
		if pg := listar(t, "u-a", contract.ListarGastosIn{Mes: "2026-11"}); len(pg.Items) != 1 {
			t.Errorf("novembro = %+v", pg.Items)
		}
		if pg := listar(t, "u-a", contract.ListarGastosIn{Mes: "2026-10"}); len(pg.Items) != 1 || pg.Items[0] != editado {
			t.Errorf("outubro = %+v", pg.Items)
		}
		almoco = editado
	})

	t.Run("categoria em uso", func(t *testing.T) {
		var out contract.ExisteGastoNaCategoriaOut
		chamar(t, contract.OpExisteGastoNaCategoria, "u-a", contract.ExisteGastoNaCategoriaIn{CategoriaID: "internet"}, &out)
		if !out.Existe {
			t.Error("internet devia estar em uso")
		}
		chamar(t, contract.OpExisteGastoNaCategoria, "u-a", contract.ExisteGastoNaCategoriaIn{CategoriaID: "alimentacao"}, &out)
		if out.Existe {
			t.Error("alimentação não devia estar em uso depois da edição")
		}
	})

	t.Run("excluir", func(t *testing.T) {
		chamar(t, contract.OpExcluirGasto, "u-a", contract.ExcluirGastoIn{ID: almoco.ID}, nil)
		if pg := listar(t, "u-a", contract.ListarGastosIn{Mes: "2026-10"}); len(pg.Items) != 0 {
			t.Errorf("outubro = %+v", pg.Items)
		}
	})

	t.Run("UserDeleted pelo publisher local", func(t *testing.T) {
		chamar(t, contract.OpCriarGasto, "u-c", contract.GastoIn{ValorCentavos: 1, Data: "2026-11-01", CategoriaID: "lazer", Grupo: "CARTAO"}, nil)
		pub := events.NewLocal([]string{base + events.Path}, "gestor-gastos.teste", nil, nil)
		for range 2 { // idempotente
			if err := pub.Publish(ctx, events.UserDeleted, events.UserDeletedDetail{UserID: "u-c"}); err != nil {
				t.Fatal(err)
			}
		}
		if pg := listar(t, "u-c", contract.ListarGastosIn{Mes: "2026-11"}); len(pg.Items) != 0 {
			t.Errorf("sobrou = %+v", pg.Items)
		}
		if pg := listar(t, "u-a", contract.ListarGastosIn{Mes: "2026-11"}); len(pg.Items) != 1 {
			t.Errorf("apagou de outro usuário: %+v", pg.Items)
		}
	})

	cancel()
	select {
	case err := <-terminou:
		if err != nil {
			t.Errorf("encerramento: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("cmd/local não encerrou")
	}
}
