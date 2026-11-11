package gasto

import (
	"slices"
	"testing"
	"time"
)

var relogioResumo = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// lancado cria um gasto; cada chamada avança o relógio 1 ms, então o ULID
// cresce na ordem de lançamento.
func lancado(t *testing.T, data, categoria string, grupo Grupo, centavos int64) Gasto {
	t.Helper()
	relogioResumo = relogioResumo.Add(time.Millisecond)
	g, err := Novo(NovoULID(relogioResumo), "u-1", Dados{ValorCentavos: centavos, Data: data, CategoriaID: categoria, Grupo: grupo}, relogioResumo)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestResumirTotaisPorGrupo(t *testing.T) {
	// Cenário "Totais por grupo": R$ 300,00 de cartão e R$ 1.700,00 fixos.
	gs := []Gasto{
		lancado(t, "2026-11-03", "alimentacao", Cartao, 10_000),
		lancado(t, "2026-11-04", "transporte", Cartao, 20_000),
		lancado(t, "2026-11-05", "aluguel", Fixo, 150_000),
		lancado(t, "2026-11-10", "internet", Fixo, 20_000),
	}
	r := Resumir("2026-11", gs)
	if r.Mes != "2026-11" || r.TotalCentavos != 200_000 {
		t.Errorf("total = %+v", r)
	}
	quero := []TotalGrupo{{Cartao, 30_000}, {Fixo, 170_000}}
	if !slices.Equal(r.PorGrupo, quero) {
		t.Errorf("por grupo = %+v, quero %+v", r.PorGrupo, quero)
	}
}

func TestResumirSomaExataEmCentavos(t *testing.T) {
	// Valores que em float64 não somam exato (0,1 + 0,2 != 0,3).
	gs := []Gasto{
		lancado(t, "2026-11-01", "lazer", Cartao, 10),
		lancado(t, "2026-11-02", "lazer", Cartao, 20),
		lancado(t, "2026-11-03", "lazer", Cartao, 1),
		lancado(t, "2026-11-04", "aluguel", Fixo, 99_999_999),
	}
	r := Resumir("2026-11", gs)
	if r.TotalCentavos != 100_000_030 {
		t.Errorf("total = %d", r.TotalCentavos)
	}
	var somaGrupos, somaCategorias int64
	for _, g := range r.PorGrupo {
		somaGrupos += g.TotalCentavos
	}
	for _, c := range r.PorCategoria {
		somaCategorias += c.TotalCentavos
	}
	if somaGrupos != r.TotalCentavos || somaCategorias != r.TotalCentavos {
		t.Errorf("somas: grupos %d, categorias %d, total %d", somaGrupos, somaCategorias, r.TotalCentavos)
	}
	if lazer := r.PorCategoria[1]; lazer != (TotalCategoria{"lazer", Cartao, 31}) {
		t.Errorf("lazer = %+v", lazer)
	}
}

func TestResumirPorCategoria(t *testing.T) {
	gs := []Gasto{
		lancado(t, "2026-11-01", "transporte", Cartao, 25_000),
		lancado(t, "2026-11-02", "alimentacao", Cartao, 50_000),
		lancado(t, "2026-11-03", "alimentacao", Cartao, 25_000),
		lancado(t, "2026-11-04", "internet", Fixo, 25_000),
		lancado(t, "2026-11-05", "agua", Fixo, 25_000),
	}
	r := Resumir("2026-11", gs)
	// Maior total primeiro; empate em ordem de grupo e depois de ID.
	quero := []TotalCategoria{
		{"alimentacao", Cartao, 75_000},
		{"transporte", Cartao, 25_000},
		{"agua", Fixo, 25_000},
		{"internet", Fixo, 25_000},
	}
	if !slices.Equal(r.PorCategoria, quero) {
		t.Errorf("por categoria =\n%+v\nquero\n%+v", r.PorCategoria, quero)
	}
}

func TestResumirMesmaCategoriaEmDoisGrupos(t *testing.T) {
	// O grupo vem gravado em cada gasto; se a categoria aparecer nos dois,
	// cada grupo fica com a sua parte e as somas continuam fechando.
	gs := []Gasto{
		lancado(t, "2026-11-01", "outros", Cartao, 100),
		lancado(t, "2026-11-02", "outros", Fixo, 300),
	}
	r := Resumir("2026-11", gs)
	quero := []TotalCategoria{{"outros", Fixo, 300}, {"outros", Cartao, 100}}
	if !slices.Equal(r.PorCategoria, quero) {
		t.Errorf("por categoria = %+v", r.PorCategoria)
	}
}

func TestResumirRecentes(t *testing.T) {
	// Cenário "Lista resumida": 12 gastos, 5 mais recentes.
	var gs []Gasto
	for _, data := range []string{"2026-11-05", "2026-11-01", "2026-11-30", "2026-11-05", "2026-11-15", "2026-11-05", "2026-11-01", "2026-11-02", "2026-11-03", "2026-11-04", "2026-11-06", "2026-11-07"} {
		gs = append(gs, lancado(t, data, "lazer", Cartao, 1))
	}
	entrada := slices.Clone(gs)
	r := Resumir("2026-11", gs)
	if !slices.Equal(gs, entrada) {
		t.Error("Resumir alterou a ordem da entrada")
	}
	// 30, 15, 07, 06 e o último dos três do dia 05 (índice 5).
	quero := []Gasto{gs[2], gs[4], gs[11], gs[10], gs[5]}
	if !slices.Equal(r.Recentes, quero) {
		t.Errorf("recentes = %v, quero %v", ids(r.Recentes), ids(quero))
	}
}

func TestResumirPoucosGastos(t *testing.T) {
	a := lancado(t, "2026-11-01", "lazer", Cartao, 1)
	b := lancado(t, "2026-11-02", "lazer", Cartao, 1)
	r := Resumir("2026-11", []Gasto{a, b})
	if !slices.Equal(r.Recentes, []Gasto{b, a}) {
		t.Errorf("recentes = %v", ids(r.Recentes))
	}
}

func TestResumirMesVazio(t *testing.T) {
	for nome, gs := range map[string][]Gasto{
		"sem gastos":      nil,
		"só de outro mês": {lancado(t, "2026-10-31", "lazer", Cartao, 500), lancado(t, "2026-12-01", "aluguel", Fixo, 900)},
	} {
		r := Resumir("2026-11", gs)
		if r.Mes != "2026-11" || r.TotalCentavos != 0 {
			t.Errorf("%s: total = %+v", nome, r)
		}
		if !slices.Equal(r.PorGrupo, []TotalGrupo{{Cartao, 0}, {Fixo, 0}}) {
			t.Errorf("%s: por grupo = %+v", nome, r.PorGrupo)
		}
		if r.PorCategoria == nil || len(r.PorCategoria) != 0 || r.Recentes == nil || len(r.Recentes) != 0 {
			t.Errorf("%s: listas = %#v, %#v", nome, r.PorCategoria, r.Recentes)
		}
	}
}

func ids(gs []Gasto) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.ID)
	}
	return out
}
