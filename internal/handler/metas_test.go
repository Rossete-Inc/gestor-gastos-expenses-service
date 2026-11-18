package handler

import (
	"encoding/json"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

func TestMetasPeloContrato(t *testing.T) {
	c, _, _, _ := montar(t)
	if err := c.Call(ctx, contract.OpCriarGasto, "ana", almoco(), nil); err != nil { // 4590 em alimentacao, 2026-11-03
		t.Fatal(err)
	}

	in := contract.DefinirMetaIn{CategoriaID: "alimentacao", NomeCategoria: "Alimentação", Grupo: contract.GrupoCartao, ValorCentavos: 5000, Mes: "2026-11"}
	var m contract.MetaDoMes
	if err := c.Call(ctx, contract.OpDefinirMeta, "ana", in, &m); err != nil {
		t.Fatal(err)
	}
	want := contract.MetaDoMes{CategoriaID: "alimentacao", NomeCategoria: "Alimentação", Grupo: "CARTAO", Mes: "2026-11", ValorCentavos: 5000, TotalCentavos: 4590, Estado: contract.MetaAviso}
	if m != want {
		t.Fatalf("meta = %+v, want %+v", m, want)
	}

	var lista contract.ListarMetasOut
	if err := c.Call(ctx, contract.OpListarMetas, "ana", contract.ListarMetasIn{Mes: "2026-11"}, &lista); err != nil || len(lista.Items) != 1 || lista.Items[0] != want {
		t.Fatalf("listar = %+v, %v", lista, err)
	}

	var resumo contract.ResumoMensalOut
	if err := c.Call(ctx, contract.OpResumoMensal, "ana", contract.ResumoMensalIn{Mes: "2026-11"}, &resumo); err != nil || len(resumo.Metas) != 1 || resumo.Metas[0] != want {
		t.Fatalf("resumo.metas = %+v, %v", resumo.Metas, err)
	}

	// sem metas, a lista vem presente e vazia
	var vazio json.RawMessage
	if err := c.Call(ctx, contract.OpResumoMensal, "bruno", contract.ResumoMensalIn{Mes: "2026-11"}, &vazio); err != nil {
		t.Fatal(err)
	}
	var campos map[string]json.RawMessage
	if err := json.Unmarshal(vazio, &campos); err != nil || string(campos["metas"]) != "[]" {
		t.Fatalf("metas sem metas = %s, %v", campos["metas"], err)
	}

	if err := c.Call(ctx, contract.OpRemoverMeta, "ana", contract.RemoverMetaIn{CategoriaID: "alimentacao", Mes: "2026-12"}, nil); err != nil {
		t.Fatal(err)
	}
	err := c.Call(ctx, contract.OpRemoverMeta, "ana", contract.RemoverMetaIn{CategoriaID: "alimentacao", Mes: "2026-12"}, nil)
	if e := errs.As(err); e == nil || e.Kind != errs.KindNotFound || e.Code != contract.CodigoMetaNaoEncontrada {
		t.Fatalf("remover de novo = %v", err)
	}
	if err := c.Call(ctx, contract.OpExcluirMeta, "ana", contract.ExcluirMetaIn{CategoriaID: "alimentacao"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, contract.OpListarMetas, "ana", contract.ListarMetasIn{Mes: "2026-11"}, &lista); err != nil || len(lista.Items) != 0 {
		t.Fatalf("listar depois de excluir = %+v, %v", lista, err)
	}

	in.ValorCentavos = 0
	err = c.Call(ctx, contract.OpDefinirMeta, "ana", in, nil)
	if e := errs.As(err); e == nil || e.Kind != errs.KindValidation || e.Fields[0].Field != "valorCentavos" {
		t.Fatalf("valor zero = %v", err)
	}
}

func TestAlertasDeMetaNaRespostaDoGasto(t *testing.T) {
	c, _, _, _ := montar(t)
	in := contract.DefinirMetaIn{CategoriaID: "alimentacao", NomeCategoria: "Alimentação", Grupo: contract.GrupoCartao, ValorCentavos: 5000, Mes: "2026-11"}
	if err := c.Call(ctx, contract.OpDefinirMeta, "ana", in, nil); err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	if err := c.Call(ctx, contract.OpCriarGasto, "ana", almoco(), &raw); err != nil { // R$ 45,90 de R$ 50,00
		t.Fatal(err)
	}
	var g contract.GastoComAlertas
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	want := contract.AlertaDeMeta{CategoriaID: "alimentacao", NomeCategoria: "Alimentação", Mes: "2026-11", Limiar: 80, TotalCentavos: 4590, MetaCentavos: 5000}
	if g.ID == "" || len(g.AlertasDeMeta) != 1 || g.AlertasDeMeta[0] != want {
		t.Fatalf("resposta = %s", raw)
	}
	// quem lê como Gasto continua lendo o gasto
	var comoGasto contract.Gasto
	if err := json.Unmarshal(raw, &comoGasto); err != nil || comoGasto.ID != g.ID {
		t.Fatalf("como Gasto = %+v, %v", comoGasto, err)
	}

	// sem cruzar limiar, o campo nem aparece
	var raw2 map[string]json.RawMessage
	if err := c.Call(ctx, contract.OpCriarGasto, "bruno", almoco(), &raw2); err != nil {
		t.Fatal(err)
	}
	if _, tem := raw2["alertasDeMeta"]; tem {
		t.Fatalf("alertasDeMeta sem meta: %s", raw2["alertasDeMeta"])
	}
}
