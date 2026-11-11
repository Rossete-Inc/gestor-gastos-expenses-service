package gasto

import (
	"strings"
	"testing"
)

func TestNovoFiltro(t *testing.T) {
	f, err := NovoFiltro(" 2026-11 ", " FIXO ", " aluguel ")
	if err != nil {
		t.Fatal(err)
	}
	if f != (Filtro{Mes: "2026-11", Grupo: Fixo, CategoriaID: "aluguel"}) {
		t.Errorf("filtro = %+v", f)
	}
	if f, err := NovoFiltro("2026-11", "", ""); err != nil || f.Grupo != "" || f.CategoriaID != "" {
		t.Errorf("sem filtros = %+v, %v", f, err)
	}

	_, err = NovoFiltro("2026-13", "OUTRO", strings.Repeat("a", 65))
	if got := camposInvalidos(t, err); got != "mes,grupo,categoriaId" {
		t.Errorf("campos = %s", got)
	}
}

func TestFiltroAceita(t *testing.T) {
	g, _ := Novo(ulid, "u-1", almoco(), agora) // CARTAO, alimentacao, 2026-11
	casos := map[Filtro]bool{
		{Mes: "2026-11"}:                                true,
		{Mes: "2026-10"}:                                false,
		{Mes: "2026-11", Grupo: Cartao}:                 true,
		{Mes: "2026-11", Grupo: Fixo}:                   false,
		{Mes: "2026-11", CategoriaID: "alimentacao"}:    true,
		{Mes: "2026-11", CategoriaID: "lazer"}:          false,
		{Mes: "2026-11", Grupo: Fixo, CategoriaID: "x"}: false,
	}
	for f, quero := range casos {
		if f.Aceita(g) != quero {
			t.Errorf("%+v.Aceita = %v", f, !quero)
		}
	}
}

func TestEscopoDistingueListagens(t *testing.T) {
	base := Filtro{Mes: "2026-11"}
	escopos := map[string]bool{}
	for _, e := range []string{
		base.Escopo("u-1"),
		base.Escopo("u-2"),
		Filtro{Mes: "2026-10"}.Escopo("u-1"),
		Filtro{Mes: "2026-11", Grupo: Cartao}.Escopo("u-1"),
		Filtro{Mes: "2026-11", CategoriaID: "lazer"}.Escopo("u-1"),
	} {
		if escopos[e] {
			t.Errorf("escopo repetido: %s", e)
		}
		escopos[e] = true
	}
}
