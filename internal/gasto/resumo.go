package gasto

import (
	"cmp"
	"slices"
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
)

// MaxRecentes é quantos gastos o resumo traz em Recentes (spec painel-gastos,
// "Últimos lançamentos").
const MaxRecentes = 5

// TotalGrupo é a soma dos gastos de um grupo no mês.
type TotalGrupo struct {
	Grupo         Grupo
	TotalCentavos int64
}

// TotalCategoria é a soma dos gastos de uma categoria no mês, dentro de um
// grupo. A chave é o par (categoria, grupo): o grupo é o gravado em cada
// gasto, então a soma das categorias de um grupo é sempre o total do grupo.
type TotalCategoria struct {
	CategoriaID   string
	Grupo         Grupo
	TotalCentavos int64
}

// Resumo é o resumo mensal do painel: total do mês, totais por grupo e por
// categoria e os gastos mais recentes. Os totais são somas exatas em
// centavos; os percentuais ficam com o BFF (design.md).
type Resumo struct {
	// Mes no formato AAAA-MM.
	Mes           string
	TotalCentavos int64
	// PorGrupo traz sempre todos os grupos, na ordem de Grupos, inclusive os
	// zerados.
	PorGrupo []TotalGrupo
	// PorCategoria vem do maior total para o menor; empates em ordem de
	// grupo e de ID. Só entram categorias com gasto no mês.
	PorCategoria []TotalCategoria
	// Recentes são até MaxRecentes gastos, do mais recente para o mais
	// antigo: data decrescente e, no mesmo dia, o último lançado primeiro.
	Recentes []Gasto
}

// Resumir soma os gastos do mês. Gastos de outro mês são ignorados, e a
// ordem de entrada não importa. Mês sem gastos dá totais zerados e listas
// vazias (não nulas).
func Resumir(mes string, gastos []Gasto) Resumo {
	r := Resumo{Mes: mes, PorCategoria: []TotalCategoria{}, Recentes: []Gasto{}}

	porGrupo := map[Grupo]int64{}
	type chaveCategoria struct {
		id    string
		grupo Grupo
	}
	porCategoria := map[chaveCategoria]int64{}
	doMes := make([]Gasto, 0, len(gastos))
	for _, g := range gastos {
		if g.Mes() != mes {
			continue
		}
		doMes = append(doMes, g)
		r.TotalCentavos += g.ValorCentavos
		porGrupo[g.Grupo] += g.ValorCentavos
		porCategoria[chaveCategoria{g.CategoriaID, g.Grupo}] += g.ValorCentavos
	}

	r.PorGrupo = make([]TotalGrupo, 0, len(Grupos))
	for _, grupo := range Grupos {
		r.PorGrupo = append(r.PorGrupo, TotalGrupo{Grupo: grupo, TotalCentavos: porGrupo[grupo]})
	}

	for k, total := range porCategoria {
		r.PorCategoria = append(r.PorCategoria, TotalCategoria{CategoriaID: k.id, Grupo: k.grupo, TotalCentavos: total})
	}
	slices.SortFunc(r.PorCategoria, func(a, b TotalCategoria) int {
		return cmp.Or(
			cmp.Compare(b.TotalCentavos, a.TotalCentavos),
			strings.Compare(string(a.Grupo), string(b.Grupo)),
			strings.Compare(a.CategoriaID, b.CategoriaID),
		)
	})

	slices.SortFunc(doMes, MaisRecentePrimeiro)
	r.Recentes = append(r.Recentes, doMes[:min(MaxRecentes, len(doMes))]...)
	return r
}

// Contract converte para o formato do contrato público.
func (r Resumo) Contract() contract.ResumoMensalOut {
	out := contract.ResumoMensalOut{
		Mes:                r.Mes,
		TotalCentavos:      r.TotalCentavos,
		TotaisPorGrupo:     make([]contract.TotalGrupo, 0, len(r.PorGrupo)),
		TotaisPorCategoria: make([]contract.TotalCategoria, 0, len(r.PorCategoria)),
		Recentes:           make([]contract.Gasto, 0, len(r.Recentes)),
	}
	for _, g := range r.PorGrupo {
		out.TotaisPorGrupo = append(out.TotaisPorGrupo, contract.TotalGrupo{Grupo: string(g.Grupo), TotalCentavos: g.TotalCentavos})
	}
	for _, c := range r.PorCategoria {
		out.TotaisPorCategoria = append(out.TotaisPorCategoria, contract.TotalCategoria{CategoriaID: c.CategoriaID, Grupo: string(c.Grupo), TotalCentavos: c.TotalCentavos})
	}
	for _, g := range r.Recentes {
		out.Recentes = append(out.Recentes, g.Contract())
	}
	return out
}

// MaisRecentePrimeiro ordena gastos como a listagem: data decrescente e, no
// mesmo dia, ULID decrescente (o último lançado primeiro).
func MaisRecentePrimeiro(a, b Gasto) int {
	return cmp.Or(strings.Compare(b.Data, a.Data), strings.Compare(b.ULID(), a.ULID()))
}
