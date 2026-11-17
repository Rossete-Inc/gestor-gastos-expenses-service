package serie

import (
	"slices"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/cartao"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

var agora = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// nubank fecha dia 25 e vence dia 5, como nos cenários da spec.
func nubank(data string) string { return cartao.MesDaFatura(data, 25, 5) }

func semCartao(string) string { return "" }

func dados(valor int64, data string) gasto.Dados {
	return gasto.Dados{ValorCentavos: valor, Data: data, CategoriaID: "eletro", Grupo: gasto.Cartao, CartaoID: "k1", Descricao: "Geladeira"}
}

func planejar(t *testing.T, in contract.CriarGastoIn, fatura MesDaFatura) (Serie, []Item, error) {
	t.Helper()
	p, err := PedidoDe(in)
	if err != nil {
		return Serie{}, nil, err
	}
	if p == nil {
		t.Fatal("pedido nil")
	}
	d := dados(in.ValorCentavos, in.Data)
	return Planejar(*p, "S1", "u-1", d, fatura, agora)
}

type resumo struct {
	n     int
	valor int64
	data  string
	mes   string
}

func resumir(itens []Item) []resumo {
	var r []resumo
	for _, it := range itens {
		r = append(r, resumo{it.Numero, it.Dados.ValorCentavos, it.Dados.Data, it.Dados.MesReferencia})
	}
	return r
}

func parcelado(valor int64, data string, p contract.ParcelamentoIn) contract.CriarGastoIn {
	return contract.CriarGastoIn{GastoIn: contract.GastoIn{ValorCentavos: valor, Data: data}, Parcelamento: &p}
}

func TestDivisaoExata(t *testing.T) {
	s, itens, err := planejar(t, parcelado(120_000, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}), nubank)
	if err != nil {
		t.Fatal(err)
	}
	quero := []resumo{
		{1, 40_000, "2026-11-10", "2026-12"},
		{2, 40_000, "2026-12-10", "2027-01"},
		{3, 40_000, "2027-01-10", "2027-02"},
	}
	if got := resumir(itens); !slices.Equal(got, quero) {
		t.Errorf("parcelas = %v", got)
	}
	if s.TotalParcelas != 3 || s.PrimeiroNumero != 1 || s.ValorCentavos != 40_000 || s.MesFinal != "2027-01" {
		t.Errorf("série = %+v", s)
	}
}

func TestDivisaoComResto(t *testing.T) {
	if got := DividirTotal(10_000, 3); !slices.Equal(got, []int64{3334, 3333, 3333}) {
		t.Errorf("divisão = %v", got) // => [3334 3333 3333]
	}
	_, itens, err := planejar(t, parcelado(10_000, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}), semCartao)
	if err != nil || itens[0].Dados.ValorCentavos != 3334 || itens[2].Dados.ValorCentavos != 3333 {
		t.Errorf("parcelas = %v, %v", resumir(itens), err)
	}
}

func TestCompraEmAndamento(t *testing.T) {
	_, itens, err := planejar(t, parcelado(19_990, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoParcela, ParcelaAtual: 3, Parcelas: 10}), nubank)
	if err != nil {
		t.Fatal(err)
	}
	if len(itens) != 8 || itens[0].Numero != 3 || itens[7].Numero != 10 {
		t.Fatalf("parcelas = %v", resumir(itens))
	}
	if itens[0].Dados.MesReferencia != "2026-12" || itens[7].Dados.MesReferencia != "2027-07" || itens[7].Dados.ValorCentavos != 19_990 {
		t.Errorf("da fatura de dezembro à de julho: %v", resumir(itens))
	}
}

func TestInformarMesFinal(t *testing.T) {
	s, itens, err := planejar(t, parcelado(5_000, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoParcela, ParcelaAtual: 1, MesFinal: "2027-07"}), nubank)
	if err != nil {
		t.Fatal(err)
	}
	if s.TotalParcelas != 8 || len(itens) != 8 || itens[7].Dados.MesReferencia != "2027-07" {
		t.Errorf("8 parcelas até julho: %+v %v", s, resumir(itens))
	}
}

func TestParcelamentoSemCartao(t *testing.T) {
	_, itens, err := planejar(t, parcelado(60_000, "2026-11-15", contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}), semCartao)
	if err != nil {
		t.Fatal(err)
	}
	datas := []string{itens[0].Dados.Data, itens[1].Dados.Data, itens[2].Dados.Data}
	if !slices.Equal(datas, []string{"2026-11-15", "2026-12-15", "2027-01-15"}) {
		t.Errorf("datas = %v", datas)
	}
}

func TestDia31NasParcelas(t *testing.T) {
	_, itens, _ := planejar(t, parcelado(3_000, "2027-01-31", contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}), semCartao)
	if itens[1].Dados.Data != "2027-02-28" || itens[2].Dados.Data != "2027-03-31" {
		t.Errorf("datas = %v", resumir(itens))
	}
}

func TestPedidosInvalidos(t *testing.T) {
	casos := map[string]struct {
		in    contract.CriarGastoIn
		campo string
	}{
		"1 parcela":           {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 1}), "parcelas"},
		"73 parcelas":         {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 73}), "parcelas"},
		"sem parcelas":        {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoTotal}), "parcelas"},
		"modo desconhecido":   {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: "X", Parcelas: 3}), "modo"},
		"parcela sem total":   {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoParcela, ParcelaAtual: 2}), "parcelas"},
		"mês final inválido":  {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoParcela, MesFinal: "2027-13"}), "mesFinal"},
		"atual em modo total": {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3, ParcelaAtual: 2}), "modo"},
		"parcelado e recorrente": {contract.CriarGastoIn{GastoIn: contract.GastoIn{ValorCentavos: 1, Data: "2026-11-10"},
			Parcelamento: &contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 2}, Recorrencia: &contract.RecorrenciaIn{}}, "recorrencia"},
	}
	for nome, c := range casos {
		_, err := PedidoDe(c.in)
		if campo := campoDoErro(err); campo != c.campo {
			t.Errorf("%s: campo %q, quero %q (%v)", nome, campo, c.campo, err)
		}
	}

	planos := map[string]struct {
		in    contract.CriarGastoIn
		campo string
	}{
		"parcela atual maior que o total": {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoParcela, ParcelaAtual: 11, Parcelas: 10}), "parcelaAtual"},
		"mês final antes da fatura":       {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoParcela, MesFinal: "2026-11"}), "mesFinal"},
		"mês final com 1 parcela":         {parcelado(100, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoParcela, MesFinal: "2026-12"}), "mesFinal"},
		"total menor que as parcelas":     {parcelado(2, "2026-11-10", contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}), "valorCentavos"},
		"recorrência com fim no passado": {contract.CriarGastoIn{GastoIn: contract.GastoIn{ValorCentavos: 1, Data: "2026-11-10"},
			Recorrencia: &contract.RecorrenciaIn{MesFinal: "2026-10"}}, "mesFinal"},
	}
	for nome, c := range planos {
		_, _, err := planejar(t, c.in, nubank)
		if campo := campoDoErro(err); campo != c.campo {
			t.Errorf("%s: campo %q, quero %q (%v)", nome, campo, c.campo, err)
		}
	}
}

func campoDoErro(err error) string {
	if err == nil {
		return ""
	}
	e := errs.As(err)
	if e == nil || e.Kind != errs.KindValidation || len(e.Fields) == 0 {
		return "?"
	}
	return e.Fields[0].Field
}

func TestAvulsoNaoEPedido(t *testing.T) {
	if p, err := PedidoDe(contract.CriarGastoIn{}); p != nil || err != nil {
		t.Errorf("avulso = %v, %v", p, err)
	}
}

func TestRecorrencia(t *testing.T) {
	in := contract.CriarGastoIn{GastoIn: contract.GastoIn{ValorCentavos: 150_000, Data: "2026-01-31"}, Recorrencia: &contract.RecorrenciaIn{MesFinal: "2026-04"}}
	s, itens, err := planejar(t, in, semCartao)
	if err != nil {
		t.Fatal(err)
	}
	if len(itens) != 1 || itens[0].Numero != 1 || !slices.Equal(s.MesesGerados, []string{"2026-01"}) || s.Dia != 31 {
		t.Fatalf("série %+v, itens %v", s, resumir(itens))
	}
	// Cenário "Dia 31" e "Mês final": fevereiro no dia 28; nada depois de abril.
	if got := s.MesesFaltando("2026-06"); !slices.Equal(got, []string{"2026-02", "2026-03", "2026-04"}) {
		t.Errorf("faltando = %v", got)
	}
	if d := s.Modelo("2026-02"); d.Data != "2026-02-28" || d.ValorCentavos != 150_000 {
		t.Errorf("modelo de fevereiro = %+v", d)
	}
	s.MesesGerados = append(s.MesesGerados, "2026-03") // março excluído com "só esta" não volta
	if got := s.MesesFaltando("2026-03"); !slices.Equal(got, []string{"2026-02"}) {
		t.Errorf("faltando = %v", got)
	}
	if s.NumeroDoMes("2026-03") != 3 || s.MesDoNumero(3) != "2026-03" {
		t.Errorf("numeração: %d %s", s.NumeroDoMes("2026-03"), s.MesDoNumero(3))
	}
	if got := s.MesesFaltando("2025-12"); got != nil {
		t.Errorf("antes do início = %v", got)
	}
}

func TestIDDaChaveEstavel(t *testing.T) {
	a, b := IDDaChave("u-1", "k"), IDDaChave("u-1", "k")
	if a != b || !gasto.ULIDValido(a) || a == IDDaChave("u-2", "k") {
		t.Errorf("ids: %s %s", a, b)
	}
}

func TestMeses(t *testing.T) {
	if SomarMeses("2026-11", 3) != "2027-02" || MesesEntre("2026-12", "2027-07") != 7 || MesesEntre("2027-01", "2026-12") != -1 {
		t.Error("contas de meses")
	}
	if DataNoMes("2028-02", 31) != "2028-02-29" || DataNoMes("2026-04", 31) != "2026-04-30" {
		t.Error("último dia do mês")
	}
}
