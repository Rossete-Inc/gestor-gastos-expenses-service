package gasto

import (
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

var agora = time.Date(2026, 11, 11, 15, 4, 5, 123_456_789, time.UTC)

const ulid = "01JC8Z6Q3N4W5X6Y7Z8A9B0C1D"

func almoco() Dados {
	return Dados{ValorCentavos: 4590, Data: "2026-11-03", CategoriaID: "alimentacao", Grupo: Cartao, Descricao: "Almoço"}
}

// camposInvalidos devolve os campos do erro de validação, em ordem.
func camposInvalidos(t *testing.T, err error) string {
	t.Helper()
	e := errs.As(err)
	if e == nil || e.Kind != errs.KindValidation {
		t.Fatalf("erro = %v, quero validação", err)
	}
	var c []string
	for _, f := range e.Fields {
		c = append(c, f.Field)
	}
	return strings.Join(c, ",")
}

func TestGastoDeCartao(t *testing.T) {
	// Cenário "Gasto de cartão": R$ 45,90 em 2026-11-03 na Alimentação.
	g, err := Novo(ulid, "u-1", almoco(), agora)
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != "20261103-"+ulid || g.UserID != "u-1" || g.ValorCentavos != 4590 || g.Grupo != Cartao ||
		g.Mes() != "2026-11" || g.ULID() != ulid || g.Recorrente {
		t.Errorf("gasto = %+v", g)
	}
	if !g.CriadoEm.Equal(agora.Truncate(time.Millisecond)) || !g.AtualizadoEm.IsZero() {
		t.Errorf("datas = %v, %v", g.CriadoEm, g.AtualizadoEm)
	}
}

func TestGastoFixoRecorrente(t *testing.T) {
	// Cenário "Gasto fixo recorrente": R$ 1.500,00 no Aluguel.
	g, err := Novo(ulid, "u-1", Dados{ValorCentavos: 150_000, Data: "2026-11-05", CategoriaID: "aluguel", Grupo: Fixo, Recorrente: true}, agora)
	if err != nil {
		t.Fatal(err)
	}
	if g.Grupo != Fixo || !g.Recorrente || g.Descricao != "" {
		t.Errorf("gasto = %+v", g)
	}
	c := g.Contract()
	if c != (contract.Gasto{ID: g.ID, ValorCentavos: 150_000, Data: "2026-11-05", Grupo: "FIXO", CategoriaID: "aluguel", Recorrente: true}) {
		t.Errorf("contract = %+v", c)
	}
}

func TestValidacaoDoLancamento(t *testing.T) {
	casos := map[string]struct {
		muda   func(*Dados)
		campos string
	}{
		"valor zero (cenário Valor inválido)": {func(d *Dados) { d.ValorCentavos = 0 }, "valorCentavos"},
		"valor negativo":                      {func(d *Dados) { d.ValorCentavos = -1 }, "valorCentavos"},
		"acima de um milhão":                  {func(d *Dados) { d.ValorCentavos = 100_000_001 }, "valorCentavos"},
		"data vazia":                          {func(d *Dados) { d.Data = "" }, "data"},
		"30 de fevereiro":                     {func(d *Dados) { d.Data = "2026-02-30" }, "data"},
		"sem zeros":                           {func(d *Dados) { d.Data = "2026-2-3" }, "data"},
		"formato brasileiro":                  {func(d *Dados) { d.Data = "03/11/2026" }, "data"},
		"ano antigo demais":                   {func(d *Dados) { d.Data = "1899-12-31" }, "data"},
		"categoria vazia":                     {func(d *Dados) { d.CategoriaID = "  " }, "categoriaId"},
		"categoria longa":                     {func(d *Dados) { d.CategoriaID = strings.Repeat("a", 65) }, "categoriaId"},
		"categoria com controle":              {func(d *Dados) { d.CategoriaID = "a\x00b" }, "categoriaId"},
		"grupo inexistente":                   {func(d *Dados) { d.Grupo = "OUTRO" }, "grupo"},
		"grupo vazio":                         {func(d *Dados) { d.Grupo = "" }, "grupo"},
		"descrição de 141":                    {func(d *Dados) { d.Descricao = strings.Repeat("é", 141) }, "descricao"},
		"descrição com quebra de linha":       {func(d *Dados) { d.Descricao = "a\nb" }, "descricao"},
		"tudo errado": {func(d *Dados) { *d = Dados{Grupo: "x", Descricao: strings.Repeat("a", 141)} },
			"valorCentavos,data,categoriaId,grupo,descricao"},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			d := almoco()
			c.muda(&d)
			_, err := Novo(ulid, "u-1", d, agora)
			if got := camposInvalidos(t, err); got != c.campos {
				t.Errorf("campos = %s, quero %s", got, c.campos)
			}
		})
	}
}

func TestLimitesAceitos(t *testing.T) {
	casos := map[string]func(*Dados){
		"um centavo":           func(d *Dados) { d.ValorCentavos = 1 },
		"um milhão exato":      func(d *Dados) { d.ValorCentavos = 100_000_000 },
		"29 de fevereiro":      func(d *Dados) { d.Data = "2028-02-29" },
		"descrição de 140":     func(d *Dados) { d.Descricao = strings.Repeat("é", 140) },
		"categoria de 64":      func(d *Dados) { d.CategoriaID = strings.Repeat("a", 64) },
		"descrição só espaços": func(d *Dados) { d.Descricao = "   " },
		"campos com espaço ao redor": func(d *Dados) {
			d.Data, d.CategoriaID, d.Grupo = " 2026-11-03 ", " alimentacao ", " CARTAO "
		},
	}
	for nome, muda := range casos {
		t.Run(nome, func(t *testing.T) {
			d := almoco()
			muda(&d)
			if _, err := Novo(ulid, "u-1", d, agora); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestNormalizacao(t *testing.T) {
	d := almoco()
	d.Descricao = "  Café da manhã  " // "é" com acento combinado
	d.CategoriaID = " alimentacao "
	g, err := Novo(ulid, "u-1", d, agora)
	if err != nil {
		t.Fatal(err)
	}
	if g.Descricao != "Café da manhã" || g.CategoriaID != "alimentacao" {
		t.Errorf("descrição = %q, categoria = %q", g.Descricao, g.CategoriaID)
	}
}

func TestSemUsuario(t *testing.T) {
	_, err := Novo(ulid, " ", almoco(), agora)
	if e := errs.As(err); e == nil || e.Kind != errs.KindBadRequest || e.Code != contract.CodigoUsuarioAusente {
		t.Errorf("erro = %v", err)
	}
}

func TestAtualizado(t *testing.T) {
	g, _ := Novo(ulid, "u-1", almoco(), agora)
	depois := agora.Add(time.Hour)

	// Cenário "Troca de categoria entre grupos": Alimentação → Internet.
	d := almoco()
	d.CategoriaID, d.Grupo = "internet", Fixo
	novo, err := g.Atualizado(d, depois)
	if err != nil {
		t.Fatal(err)
	}
	if novo.ID != g.ID || novo.Grupo != Fixo || novo.CategoriaID != "internet" || !novo.CriadoEm.Equal(g.CriadoEm) ||
		!novo.AtualizadoEm.Equal(depois.Truncate(time.Millisecond)) || novo.UserID != "u-1" {
		t.Errorf("atualizado = %+v", novo)
	}

	// Data nova: o ID muda, o ULID fica.
	d.Data = "2026-10-31"
	novo, err = g.Atualizado(d, depois)
	if err != nil {
		t.Fatal(err)
	}
	if novo.ID != "20261031-"+ulid || novo.ULID() != ulid || novo.Mes() != "2026-10" {
		t.Errorf("ID = %s", novo.ID)
	}

	d.ValorCentavos = 0
	if _, err := g.Atualizado(d, depois); camposInvalidos(t, err) != "valorCentavos" {
		t.Errorf("erro = %v", err)
	}
}

func TestDadosDe(t *testing.T) {
	in := contract.GastoIn{ValorCentavos: 10, Data: "2026-11-01", CategoriaID: "c", Grupo: "FIXO", Descricao: "d", Recorrente: true}
	if got := DadosDe(in); got != (Dados{ValorCentavos: 10, Data: "2026-11-01", CategoriaID: "c", Grupo: Fixo, Descricao: "d", Recorrente: true}) {
		t.Errorf("dados = %+v", got)
	}
}

func TestErrNaoEncontrado(t *testing.T) {
	e := errs.As(ErrNaoEncontrado())
	if e.Kind != errs.KindNotFound || e.Code != contract.CodigoNaoEncontrado {
		t.Errorf("erro = %+v", e)
	}
}

func TestMesValido(t *testing.T) {
	for s, ok := range map[string]bool{"2026-11": true, "2026-13": false, "2026-1": false, "26-11": false, "": false, "1899-12": false, "2026-11-01": false} {
		if MesValido(s) != ok {
			t.Errorf("MesValido(%q) = %v", s, !ok)
		}
	}
}
