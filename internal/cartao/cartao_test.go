package cartao

import (
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

func TestMesDaFatura(t *testing.T) {
	casos := []struct {
		nome       string
		data       string
		fech, venc int
		quer       string
	}{
		// Cenários da spec cartoes-credito.
		{"compra antes do fechamento", "2026-11-10", 25, 5, "2026-12"},
		{"compra depois do fechamento", "2026-11-28", 25, 5, "2027-01"},
		{"vencimento no mesmo mês do fechamento", "2026-11-02", 3, 10, "2026-11"},
		{"fechamento 31 em fevereiro", "2027-02-28", 31, 10, "2027-03"},
		// Bordas.
		{"compra no dia do fechamento", "2026-11-25", 25, 5, "2026-12"},
		{"vencimento igual ao fechamento vai ao mês seguinte", "2026-11-10", 10, 10, "2026-12"},
		{"virada de ano antes do fechamento", "2026-12-20", 25, 5, "2027-01"},
		{"virada de ano depois do fechamento", "2026-12-26", 25, 5, "2027-02"},
		{"fechamento 31 em mês de 30 dias, compra no dia 30", "2026-11-30", 31, 10, "2026-12"},
		{"fevereiro bissexto, compra no dia 29", "2028-02-29", 30, 10, "2028-03"},
		{"compra depois do fechamento com vencimento no mesmo mês", "2026-11-05", 3, 10, "2026-12"},
		{"data inválida", "2026-02-30", 25, 5, ""},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := MesDaFatura(c.data, c.fech, c.venc); got != c.quer {
				t.Fatalf("MesDaFatura(%s, %d, %d) = %q, quero %q", c.data, c.fech, c.venc, got, c.quer)
			}
		})
	}
}

func TestVencimento(t *testing.T) {
	casos := []struct {
		mes  string
		venc int
		quer string
	}{
		{"2026-12", 5, "2026-12-05"},
		{"2027-02", 31, "2027-02-28"},
		{"2028-02", 30, "2028-02-29"},
		{"2026-11", 31, "2026-11-30"},
	}
	for _, c := range casos {
		if got := Vencimento(c.mes, c.venc).Format(layoutData); got != c.quer {
			t.Errorf("Vencimento(%s, %d) = %s, quero %s", c.mes, c.venc, got, c.quer)
		}
	}
	if !Vencimento("x", 5).IsZero() {
		t.Error("mês inválido deveria dar o zero de time.Time")
	}
}

var agora = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func validos() Dados {
	return Dados{Apelido: "  Nubank ", Cor: "#8a05be", DiaFechamento: 25, DiaVencimento: 5, LimiteCentavos: 500_000}
}

func TestNovoNormaliza(t *testing.T) {
	c, err := Novo("01JC8Z6Q3N4W5X6Y7Z8A9B0C1D", "u-1", validos(), agora)
	if err != nil {
		t.Fatal(err)
	}
	if c.Apelido != "Nubank" || c.Cor != "#8A05BE" || c.Arquivado || !c.CriadoEm.Equal(agora) {
		t.Fatalf("cartão inesperado: %+v", c)
	}
	if got := c.MesDaFatura("2026-11-10"); got != "2026-12" {
		t.Fatalf("MesDaFatura = %s", got)
	}
}

func TestValidacao(t *testing.T) {
	casos := []struct {
		nome  string
		mudar func(*Dados)
		campo string
	}{
		{"apelido vazio", func(d *Dados) { d.Apelido = "  " }, "apelido"},
		{"apelido longo", func(d *Dados) { d.Apelido = strings.Repeat("a", 41) }, "apelido"},
		{"apelido com controle", func(d *Dados) { d.Apelido = "a\nb" }, "apelido"},
		{"cor inválida", func(d *Dados) { d.Cor = "roxo" }, "cor"},
		{"fechamento dia 0", func(d *Dados) { d.DiaFechamento = 0 }, "diaFechamento"},
		{"vencimento dia 32", func(d *Dados) { d.DiaVencimento = 32 }, "diaVencimento"},
		{"limite zero", func(d *Dados) { d.LimiteCentavos = 0 }, "limiteCentavos"},
		{"limite acima do máximo", func(d *Dados) { d.LimiteCentavos = 100_000_001 }, "limiteCentavos"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			d := validos()
			c.mudar(&d)
			_, err := Novo("u", "u-1", d, agora)
			e := errs.As(err)
			if e == nil || e.Kind != errs.KindValidation || len(e.Fields) != 1 || e.Fields[0].Field != c.campo {
				t.Fatalf("quero erro de validação em %s, veio %v", c.campo, err)
			}
		})
	}
}

func TestApelidoLimite40(t *testing.T) {
	d := validos()
	d.Apelido = strings.Repeat("é", 40)
	if _, err := Novo("u", "u-1", d, agora); err != nil {
		t.Fatalf("40 caracteres acentuados devem passar: %v", err)
	}
}

func TestAtualizadoMantemIdentidade(t *testing.T) {
	c, _ := Novo("ulid", "u-1", validos(), agora)
	d := validos()
	d.DiaFechamento = 20
	depois := agora.Add(time.Hour)
	n, err := c.Atualizado(d, depois)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "ulid" || n.UserID != "u-1" || n.DiaFechamento != 20 || !n.CriadoEm.Equal(agora) || !n.AtualizadoEm.Equal(depois) {
		t.Fatalf("atualizado inesperado: %+v", n)
	}
}

func TestApelidoNormalizado(t *testing.T) {
	if ApelidoNormalizado(" NuBank ") != ApelidoNormalizado("nubank") {
		t.Fatal("apelidos deveriam colidir ignorando maiúsculas e espaços")
	}
}
