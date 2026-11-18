// Package meta é a meta mensal de gastos de uma categoria (spec
// metas-gastos, ADR 0011). A meta vale todo mês a partir de quando foi
// definida, até ser alterada ou removida; por isso guarda vigências por mês,
// e meses passados continuam com o valor que valia neles. Também guarda os
// limiares já notificados em cada mês, para notificar cada um uma vez só.
package meta

import (
	"sort"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Limiares fixos, em porcentagem da meta (spec, "Estados da meta").
const (
	LimiarAviso   = 80
	LimiarEstouro = 100
)

// Limiares em ordem crescente.
var Limiares = []int{LimiarAviso, LimiarEstouro}

// Estado da meta num mês.
type Estado string

const (
	Dentro    Estado = "DENTRO"
	Aviso     Estado = "AVISO"
	Estourada Estado = "ESTOURADA"
)

// Vigencia diz quanto vale a meta a partir de um mês. ValorCentavos nil
// significa que, desde aquele mês, a categoria não tem meta (foi removida).
type Vigencia struct {
	Desde         string // AAAA-MM
	ValorCentavos *int64
}

// Meta de uma categoria na conta do dono.
type Meta struct {
	DonoID        string
	CategoriaID   string
	NomeCategoria string
	Grupo         string
	// Vigencias em ordem crescente de Desde, sem meses repetidos.
	Vigencias []Vigencia
	// Alertas são os limiares já notificados, por mês.
	Alertas map[string][]int
}

// ValidarValor recusa valor zero, negativo ou acima do máximo de um gasto.
func ValidarValor(valorCentavos int64) error {
	switch {
	case valorCentavos < contract.MinValorCentavos:
		return errs.Validation("Valor da meta inválido.",
			errs.FieldError{Field: "valorCentavos", Message: "deve ser maior que zero"})
	case valorCentavos > contract.MaxValorCentavos:
		return errs.Validation("Valor da meta inválido.",
			errs.FieldError{Field: "valorCentavos", Message: "deve ser no máximo R$ 1.000.000,00"})
	}
	return nil
}

// ValidarMes recusa um mês fora do formato AAAA-MM.
func ValidarMes(campo, mes string) error {
	if !gasto.MesValido(mes) {
		return errs.Validation("Mês inválido.", errs.FieldError{Field: campo, Message: "deve estar no formato AAAA-MM"})
	}
	return nil
}

// ValorNoMes devolve o valor da meta no mês e se havia meta nele.
func (m Meta) ValorNoMes(mes string) (int64, bool) {
	var valor *int64
	for _, v := range m.Vigencias {
		if v.Desde > mes {
			break
		}
		valor = v.ValorCentavos
	}
	if valor == nil {
		return 0, false
	}
	return *valor, true
}

// Definida devolve a meta valendo valorCentavos do mês em diante. Uma
// vigência que já começava naquele mês é substituída, e as que começariam
// depois deixam de existir (spec, "Alterar e remover meta").
func (m Meta) Definida(mes string, valorCentavos int64) Meta {
	v := valorCentavos
	return m.comVigencia(Vigencia{Desde: mes, ValorCentavos: &v})
}

// Removida devolve a meta sem valor do mês em diante; os meses anteriores
// continuam com a meta que valia neles.
func (m Meta) Removida(mes string) Meta {
	return m.comVigencia(Vigencia{Desde: mes})
}

func (m Meta) comVigencia(nova Vigencia) Meta {
	var vs []Vigencia
	for _, v := range m.Vigencias {
		if v.Desde < nova.Desde {
			vs = append(vs, v)
		}
	}
	m.Vigencias = append(vs, nova)
	return m
}

// EstadoDe classifica o total do mês contra o valor da meta.
func EstadoDe(totalCentavos, metaCentavos int64) Estado {
	switch {
	case atingiu(totalCentavos, metaCentavos, LimiarEstouro):
		return Estourada
	case atingiu(totalCentavos, metaCentavos, LimiarAviso):
		return Aviso
	default:
		return Dentro
	}
}

// atingiu compara em inteiros (total*100 >= meta*limiar) para não depender
// de ponto flutuante: R$ 800,00 de R$ 1.000,00 é exatamente 80%.
func atingiu(totalCentavos, metaCentavos int64, limiar int) bool {
	return totalCentavos*100 >= metaCentavos*int64(limiar)
}

// LimiaresCruzados devolve, em ordem crescente, os limiares que uma escrita
// fez o total cruzar (abaixo antes, atingido depois) e que ainda não foram
// notificados no mês.
func LimiaresCruzados(antesCentavos, depoisCentavos, metaCentavos int64, jaNotificados []int) []int {
	var out []int
	for _, l := range Limiares {
		if !atingiu(antesCentavos, metaCentavos, l) && atingiu(depoisCentavos, metaCentavos, l) && !contem(jaNotificados, l) {
			out = append(out, l)
		}
	}
	return out
}

// ParaAvisar escolhe, entre os limiares cruzados numa escrita, o que vai
// para o aviso e o push: só o maior (spec, "Cruzar 80% e 100% de uma vez").
func ParaAvisar(cruzados []int) (int, bool) {
	if len(cruzados) == 0 {
		return 0, false
	}
	s := append([]int(nil), cruzados...)
	sort.Ints(s)
	return s[len(s)-1], true
}

func contem(xs []int, x int) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// DoMes é a meta num mês: o valor que vale nele e o total gasto na
// categoria.
type DoMes struct {
	Meta          Meta
	Mes           string
	ValorCentavos int64
	TotalCentavos int64
}

// Estado da meta no mês.
func (d DoMes) Estado() Estado { return EstadoDe(d.TotalCentavos, d.ValorCentavos) }

// Contract converte para o formato do contrato.
func (d DoMes) Contract() contract.MetaDoMes {
	return contract.MetaDoMes{
		CategoriaID: d.Meta.CategoriaID, NomeCategoria: d.Meta.NomeCategoria, Grupo: d.Meta.Grupo,
		Mes: d.Mes, ValorCentavos: d.ValorCentavos, TotalCentavos: d.TotalCentavos, Estado: string(d.Estado()),
	}
}

// TotalDaCategoria soma os gastos da categoria, de qualquer grupo e de
// qualquer pessoa da conta (spec, "Gastos de várias pessoas").
func TotalDaCategoria(gastos []gasto.Gasto, categoriaID string) int64 {
	var total int64
	for _, g := range gastos {
		if g.CategoriaID == categoriaID {
			total += g.ValorCentavos
		}
	}
	return total
}

// Alerta é um limiar que uma escrita fez a meta cruzar num mês.
type Alerta struct {
	CategoriaID   string
	NomeCategoria string
	Mes           string
	Limiar        int
	TotalCentavos int64
	MetaCentavos  int64
}

// Contract converte para o formato do contrato.
func (a Alerta) Contract() contract.AlertaDeMeta {
	return contract.AlertaDeMeta{
		CategoriaID: a.CategoriaID, NomeCategoria: a.NomeCategoria, Mes: a.Mes,
		Limiar: a.Limiar, TotalCentavos: a.TotalCentavos, MetaCentavos: a.MetaCentavos,
	}
}

// ErrNaoEncontrada é a meta inexistente ou sem valor no mês.
func ErrNaoEncontrada() error {
	return errs.NotFound(contract.CodigoMetaNaoEncontrada, "Meta não encontrada.")
}
