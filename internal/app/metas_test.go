package app

import (
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/meta"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

func metaIn(categoria, nome string, valor int64, mes string) contract.DefinirMetaIn {
	return contract.DefinirMetaIn{CategoriaID: categoria, NomeCategoria: nome, Grupo: contract.GrupoCartao, ValorCentavos: valor, Mes: mes}
}

func definir(t *testing.T, svc *Service, dono string, in contract.DefinirMetaIn) meta.DoMes {
	t.Helper()
	m, err := svc.DefinirMeta(ctx, dono, in)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDefinirEListarMetas(t *testing.T) {
	svc, _ := montar(t)
	// spec, "Gastos de várias pessoas": a meta soma os gastos de todos da conta
	criar(t, svc, "ana", gasto.Dados{ValorCentavos: 30000, Data: "2026-11-03", CategoriaID: "lazer", Grupo: gasto.Cartao})
	criar(t, svc, "ana", gasto.Dados{ValorCentavos: 20000, Data: "2026-11-05", CategoriaID: "lazer", Grupo: gasto.Cartao, AutorID: "bruno"})
	criar(t, svc, "ana", dados("2026-11-05", "alimentacao", gasto.Cartao, 9999))

	// spec, "Criar meta"
	m := definir(t, svc, "ana", metaIn("lazer", "Lazer", 100000, "2026-11"))
	if m.ValorCentavos != 100000 || m.TotalCentavos != 50000 || m.Estado() != meta.Dentro || m.Mes != "2026-11" {
		t.Fatalf("meta definida = %+v", m)
	}

	lista, err := svc.ListarMetas(ctx, "ana", "2026-11")
	if err != nil || len(lista) != 1 || lista[0].TotalCentavos != 50000 || lista[0].Meta.NomeCategoria != "Lazer" {
		t.Fatalf("listar = %+v, %v", lista, err)
	}
	// a meta vale nos meses seguintes e não nos anteriores
	if dez, _ := svc.ListarMetas(ctx, "ana", "2026-12"); len(dez) != 1 || dez[0].TotalCentavos != 0 {
		t.Fatalf("dezembro = %+v", dez)
	}
	if out, _ := svc.ListarMetas(ctx, "ana", "2026-10"); len(out) != 0 {
		t.Fatalf("outubro = %+v", out)
	}
	// metas de outra conta não aparecem
	if outra, _ := svc.ListarMetas(ctx, "bruno", "2026-11"); len(outra) != 0 {
		t.Fatalf("conta do bruno = %+v", outra)
	}

	// spec, "Segunda meta para a mesma categoria": substitui
	definir(t, svc, "ana", metaIn("lazer", "Lazer", 60000, "2026-11"))
	lista, _ = svc.ListarMetas(ctx, "ana", "2026-11")
	if len(lista) != 1 || lista[0].ValorCentavos != 60000 || lista[0].Estado() != meta.Aviso {
		t.Fatalf("depois de substituir = %+v", lista)
	}
}

func TestAlterarERemoverMeta(t *testing.T) {
	svc, _ := montar(t)
	// spec, "Alterar o valor"
	definir(t, svc, "ana", metaIn("combustivel", "Combustível", 90000, "2026-10"))
	definir(t, svc, "ana", metaIn("combustivel", "Combustível", 110000, "2026-11"))
	out, _ := svc.ListarMetas(ctx, "ana", "2026-10")
	nov, _ := svc.ListarMetas(ctx, "ana", "2026-11")
	if out[0].ValorCentavos != 90000 || nov[0].ValorCentavos != 110000 {
		t.Fatalf("outubro = %+v, novembro = %+v", out, nov)
	}

	// spec, "Remover a meta"
	if err := svc.RemoverMeta(ctx, "ana", "combustivel", "2026-12"); err != nil {
		t.Fatal(err)
	}
	if dez, _ := svc.ListarMetas(ctx, "ana", "2026-12"); len(dez) != 0 {
		t.Fatalf("dezembro depois de remover = %+v", dez)
	}
	if nov, _ := svc.ListarMetas(ctx, "ana", "2026-11"); len(nov) != 1 {
		t.Fatalf("novembro depois de remover = %+v", nov)
	}
	// remover de novo, ou sem meta: 404
	for _, cat := range []string{"combustivel", "lazer"} {
		err := svc.RemoverMeta(ctx, "ana", cat, "2026-12")
		if kind(err) != errs.KindNotFound || codigo(err) != contract.CodigoMetaNaoEncontrada {
			t.Errorf("remover %s = %v", cat, err)
		}
	}

	// spec categorias, "Excluir categoria remove a meta": apaga o histórico
	if err := svc.ExcluirMeta(ctx, "ana", "combustivel"); err != nil {
		t.Fatal(err)
	}
	if nov, _ := svc.ListarMetas(ctx, "ana", "2026-11"); len(nov) != 0 {
		t.Fatalf("novembro depois de excluir = %+v", nov)
	}
	if err := svc.ExcluirMeta(ctx, "ana", "combustivel"); err != nil {
		t.Fatalf("excluir sem meta = %v", err)
	}
}

func TestDefinirMetaInvalida(t *testing.T) {
	svc, _ := montar(t)
	casos := []struct {
		nome  string
		in    contract.DefinirMetaIn
		campo string
	}{
		// spec, "Valor inválido"
		{"valor zero", metaIn("lazer", "Lazer", 0, "2026-11"), "valorCentavos"},
		{"valor negativo", metaIn("lazer", "Lazer", -100, "2026-11"), "valorCentavos"},
		{"sem categoria", metaIn(" ", "Lazer", 100, "2026-11"), "categoriaId"},
		{"sem nome", metaIn("lazer", "", 100, "2026-11"), "nomeCategoria"},
		{"mês inválido", metaIn("lazer", "Lazer", 100, "2026-13"), "mes"},
		{"grupo inválido", contract.DefinirMetaIn{CategoriaID: "lazer", NomeCategoria: "Lazer", Grupo: "X", ValorCentavos: 100, Mes: "2026-11"}, "grupo"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := svc.DefinirMeta(ctx, "ana", c.in)
			if campo(err) != c.campo {
				t.Fatalf("erro = %v, want campo %s", err, c.campo)
			}
		})
	}
	if _, err := svc.DefinirMeta(ctx, "", metaIn("lazer", "Lazer", 100, "2026-11")); codigo(err) != contract.CodigoUsuarioAusente {
		t.Fatalf("sem usuário = %v", err)
	}
	if _, err := svc.ListarMetas(ctx, "ana", "nov"); campo(err) != "mes" {
		t.Fatalf("listar com mês inválido = %v", err)
	}
}

func TestResumoMensalComMetas(t *testing.T) {
	svc, _ := montar(t)
	criar(t, svc, "ana", dados("2026-11-03", "lazer", gasto.Cartao, 105000))
	criar(t, svc, "ana", dados("2026-11-04", "combustivel", gasto.Cartao, 36000))
	definir(t, svc, "ana", metaIn("lazer", "Lazer", 100000, "2026-11"))
	definir(t, svc, "ana", metaIn("combustivel", "Combustível", 90000, "2026-11"))

	res, metas, err := svc.ResumoMensalComMetas(ctx, "ana", "2026-11")
	if err != nil || res.TotalCentavos != 141000 || len(metas) != 2 {
		t.Fatalf("resumo = %+v, metas = %+v, %v", res, metas, err)
	}
	estados := map[string]meta.Estado{}
	for _, m := range metas {
		estados[m.Meta.CategoriaID] = m.Estado()
	}
	if estados["lazer"] != meta.Estourada || estados["combustivel"] != meta.Dentro {
		t.Fatalf("estados = %v", estados)
	}
}
