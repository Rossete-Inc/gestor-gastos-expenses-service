package app

import (
	"slices"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/memstore"
)

// Spec parcelamentos-recorrencias.

func gastoIn(data, categoria string, grupo gasto.Grupo, centavos int64, cartaoID string) contract.GastoIn {
	return contract.GastoIn{ValorCentavos: centavos, Data: data, CategoriaID: categoria, Grupo: string(grupo), CartaoID: cartaoID, Descricao: categoria}
}

func parcelado(in contract.GastoIn, p contract.ParcelamentoIn) contract.CriarGastoIn {
	return contract.CriarGastoIn{GastoIn: in, Parcelamento: &p}
}

func recorrente(in contract.GastoIn, mesFinal string) contract.CriarGastoIn {
	return contract.CriarGastoIn{GastoIn: in, Recorrencia: &contract.RecorrenciaIn{MesFinal: mesFinal}}
}

func lancar(t *testing.T, svc *Service, in contract.CriarGastoIn) gasto.Gasto {
	t.Helper()
	g, err := svc.Lancar(ctx, "u-1", in)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// doMes devolve os gastos do mês de referência pela listagem (que cria as
// ocorrências), do mais recente para o mais antigo.
func doMes(t *testing.T, svc *Service, mes string) []gasto.Gasto {
	t.Helper()
	pg, err := svc.ListarGastos(ctx, "u-1", Listagem{Mes: mes, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return pg.Items
}

func valores(gs []gasto.Gasto) []int64 {
	var v []int64
	for _, g := range gs {
		v = append(v, g.ValorCentavos)
	}
	return v
}

func umDoMes(t *testing.T, svc *Service, mes string) gasto.Gasto {
	t.Helper()
	gs := doMes(t, svc, mes)
	if len(gs) != 1 {
		t.Fatalf("%s: %d gastos, quero 1: %+v", mes, len(gs), gs)
	}
	return gs[0]
}

func TestLancarParcelado(t *testing.T) {
	svc, st := montar(t)
	k := cadastrar(t, svc, "u-1", nubank())
	g := lancar(t, svc, parcelado(gastoIn("2026-11-10", "eletro", gasto.Cartao, 120_000, k.ID), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}))

	// Cenário "Divisão exata": dezembro, janeiro e fevereiro.
	if g.Mes() != "2026-12" || g.Serie.Numero != 1 || g.Serie.Total != 3 || st.Quantos("u-1") != 3 {
		t.Fatalf("1ª parcela = %+v (%d gastos)", g, st.Quantos("u-1"))
	}
	jan := umDoMes(t, svc, "2027-01")
	if jan.ValorCentavos != 40_000 || jan.Serie.Numero != 2 || jan.Data != "2026-12-10" {
		t.Errorf("janeiro = %+v", jan)
	}
	// Cenário "Identificação da parcela": o contrato leva "n/N".
	if c := jan.Contract(); c.Serie == nil || c.Serie.Numero != 2 || c.Serie.Total != 3 || c.Serie.Tipo != contract.SerieParcelamento {
		t.Errorf("contrato = %+v", c.Serie)
	}
}

func TestLancarIdempotente(t *testing.T) {
	svc, st := montar(t)
	in := parcelado(gastoIn("2026-11-10", "eletro", gasto.Fixo, 10_000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3})
	in.ChaveIdempotencia = "envio-1"
	a := lancar(t, svc, in)
	b := lancar(t, svc, in)
	if a.ID != b.ID || st.Quantos("u-1") != 3 || st.Series("u-1") != 1 {
		t.Errorf("reenvio duplicou: %s %s, %d gastos", a.ID, b.ID, st.Quantos("u-1"))
	}
	if a.ValorCentavos != 3334 {
		t.Errorf("1ª parcela com o resto = %d", a.ValorCentavos)
	}
}

func TestLancarInvalido(t *testing.T) {
	svc, st := montar(t)
	in := parcelado(gastoIn("2026-11-10", "eletro", gasto.Fixo, 10_000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 73})
	if _, err := svc.Lancar(ctx, "u-1", in); campo(err) != "parcelas" {
		t.Errorf("73 parcelas = %v", err)
	}
	in = parcelado(gastoIn("2026-11-10", "", gasto.Fixo, 10_000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3})
	if _, err := svc.Lancar(ctx, "u-1", in); campo(err) != "categoriaId" {
		t.Errorf("sem categoria = %v", err)
	}
	if st.Quantos("u-1") != 0 || st.Series("u-1") != 0 {
		t.Error("gravou algo com dados inválidos")
	}
}

func TestRecorrenciaNasceNaLeitura(t *testing.T) {
	svc, st := montar(t)
	lancar(t, svc, recorrente(gastoIn("2026-11-05", "aluguel", gasto.Fixo, 150_000, ""), ""))

	// Cenário "Painel sem abrir a lista".
	res, err := svc.ResumoMensal(ctx, "u-1", "2026-12")
	if err != nil || res.TotalCentavos != 150_000 {
		t.Fatalf("painel de dezembro = %+v, %v", res, err)
	}
	// Cenário "Ocorrência no mês seguinte"; ler de novo não duplica.
	dez := umDoMes(t, svc, "2026-12")
	if dez.Data != "2026-12-05" || dez.Serie.Numero != 2 || !dez.Contract().Recorrente {
		t.Errorf("dezembro = %+v", dez)
	}
	umDoMes(t, svc, "2026-12")
	if st.Quantos("u-1") != 2 {
		t.Errorf("%d gastos, quero novembro e dezembro", st.Quantos("u-1"))
	}
}

func TestRecorrenciaDia31EMesFinal(t *testing.T) {
	svc, _ := montar(t)
	lancar(t, svc, recorrente(gastoIn("2027-01-31", "academia", gasto.Fixo, 9_990, ""), "2027-03"))
	if fev := umDoMes(t, svc, "2027-02"); fev.Data != "2027-02-28" {
		t.Errorf("fevereiro = %s", fev.Data)
	}
	if abr := doMes(t, svc, "2027-04"); len(abr) != 0 {
		t.Errorf("abril depois do mês final = %+v", abr)
	}
}

func TestEscopoObrigatorio(t *testing.T) {
	svc, _ := montar(t)
	g := lancar(t, svc, recorrente(gastoIn("2026-11-05", "luz", gasto.Fixo, 20_000, ""), ""))
	if _, err := svc.AtualizarGastoNoEscopo(ctx, "u-1", g.ID, g.Dados(), ""); campo(err) != "escopo" {
		t.Errorf("editar sem escopo = %v", err)
	}
	if err := svc.ExcluirGastoNoEscopo(ctx, "u-1", g.ID, "QUALQUER"); campo(err) != "escopo" {
		t.Errorf("excluir com escopo inválido = %v", err)
	}
	// Gasto avulso ignora o escopo.
	a := criar(t, svc, "u-1", dados("2026-11-06", "mercado", gasto.Fixo, 100))
	if err := svc.ExcluirGastoNoEscopo(ctx, "u-1", a.ID, ""); err != nil {
		t.Errorf("avulso = %v", err)
	}
}

func TestAjustarLuzDeUmMes(t *testing.T) {
	svc, _ := montar(t)
	lancar(t, svc, recorrente(gastoIn("2026-11-05", "luz", gasto.Fixo, 20_000, ""), ""))
	dez := umDoMes(t, svc, "2026-12")
	d := dez.Dados()
	d.ValorCentavos = 23_000
	novo, err := svc.AtualizarGastoNoEscopo(ctx, "u-1", dez.ID, d, contract.EscopoEsta)
	if err != nil || !novo.Serie.Ajustado {
		t.Fatalf("ajuste = %+v, %v", novo, err)
	}
	if v := umDoMes(t, svc, "2026-12").ValorCentavos; v != 23_000 {
		t.Errorf("dezembro = %d", v)
	}
	if v := umDoMes(t, svc, "2027-01").ValorCentavos; v != 20_000 {
		t.Errorf("janeiro segue a série = %d", v)
	}
}

func TestReajusteAPartirDeUmMes(t *testing.T) {
	svc, st := montar(t)
	lancar(t, svc, recorrente(gastoIn("2027-01-05", "aluguel", gasto.Fixo, 150_000, ""), ""))
	doMes(t, svc, "2027-04") // cria fevereiro a abril
	mar := umDoMes(t, svc, "2027-03")
	d := mar.Dados()
	d.ValorCentavos = 160_000
	if _, err := svc.AtualizarGastoNoEscopo(ctx, "u-1", mar.ID, d, contract.EscopoProximas); err != nil {
		t.Fatal(err)
	}
	quero := map[string]int64{"2027-01": 150_000, "2027-02": 150_000, "2027-03": 160_000, "2027-04": 160_000, "2027-05": 160_000}
	for mes, v := range quero {
		if got := umDoMes(t, svc, mes).ValorCentavos; got != v {
			t.Errorf("%s = %d, quero %d", mes, got, v)
		}
	}
	// A série original acabou em fevereiro e a nova começou em março.
	if st.Series("u-1") != 2 {
		t.Errorf("%d séries, quero 2", st.Series("u-1"))
	}
}

func TestEncerrarRecorrencia(t *testing.T) {
	svc, _ := montar(t)
	lancar(t, svc, recorrente(gastoIn("2027-01-10", "streaming", gasto.Fixo, 5_590, ""), ""))
	abr := umDoMes(t, svc, "2027-04")
	if err := svc.ExcluirGastoNoEscopo(ctx, "u-1", abr.ID, contract.EscopoProximas); err != nil {
		t.Fatal(err)
	}
	for _, mes := range []string{"2027-04", "2027-05", "2027-08"} {
		if gs := doMes(t, svc, mes); len(gs) != 0 {
			t.Errorf("%s depois de encerrar = %+v", mes, gs)
		}
	}
	umDoMes(t, svc, "2027-03")
}

func TestOcorrenciaExcluidaNaoVolta(t *testing.T) {
	svc, _ := montar(t)
	lancar(t, svc, recorrente(gastoIn("2026-11-05", "luz", gasto.Fixo, 20_000, ""), ""))
	dez := umDoMes(t, svc, "2026-12")
	if err := svc.ExcluirGastoNoEscopo(ctx, "u-1", dez.ID, contract.EscopoEsta); err != nil {
		t.Fatal(err)
	}
	if gs := doMes(t, svc, "2026-12"); len(gs) != 0 {
		t.Errorf("dezembro voltou: %+v", gs)
	}
	umDoMes(t, svc, "2027-01")
}

func parcelasDe(t *testing.T, st *memstore.Store, serieID string) []int {
	t.Helper()
	gs, err := st.DaSerie(ctx, "u-1", serieID)
	if err != nil {
		t.Fatal(err)
	}
	var n []int
	for _, g := range gs {
		n = append(n, g.Serie.Numero)
	}
	slices.Sort(n)
	return n
}

func TestQuitarParcelamentoAntecipado(t *testing.T) {
	svc, st := montar(t)
	g := lancar(t, svc, parcelado(gastoIn("2026-11-10", "eletro", gasto.Fixo, 100_000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 10}))
	sexta := umDoMes(t, svc, "2027-04")
	if sexta.Serie.Numero != 6 {
		t.Fatalf("abril = %+v", sexta)
	}
	if err := svc.ExcluirGastoNoEscopo(ctx, "u-1", sexta.ID, contract.EscopoProximas); err != nil {
		t.Fatal(err)
	}
	if n := parcelasDe(t, st, g.Serie.SerieID); !slices.Equal(n, []int{1, 2, 3, 4, 5}) {
		t.Errorf("parcelas = %v", n)
	}
}

func TestExcluirSerieInteira(t *testing.T) {
	svc, st := montar(t)
	lancar(t, svc, parcelado(gastoIn("2026-11-10", "eletro", gasto.Fixo, 30_000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}))
	segunda := umDoMes(t, svc, "2026-12")
	if err := svc.ExcluirGastoNoEscopo(ctx, "u-1", segunda.ID, contract.EscopoTodas); err != nil {
		t.Fatal(err)
	}
	if st.Quantos("u-1") != 0 || st.Series("u-1") != 0 {
		t.Errorf("sobrou: %d gastos, %d séries", st.Quantos("u-1"), st.Series("u-1"))
	}
}

func TestAjustePreservado(t *testing.T) {
	svc, _ := montar(t)
	nov := lancar(t, svc, recorrente(gastoIn("2026-11-05", "luz", gasto.Fixo, 20_000, ""), ""))
	dez := umDoMes(t, svc, "2026-12")
	d := dez.Dados()
	d.ValorCentavos = 23_000
	if _, err := svc.AtualizarGastoNoEscopo(ctx, "u-1", dez.ID, d, contract.EscopoEsta); err != nil {
		t.Fatal(err)
	}
	d = nov.Dados()
	d.ValorCentavos, d.Descricao = 25_000, "Luz nova"
	if _, err := svc.AtualizarGastoNoEscopo(ctx, "u-1", nov.ID, d, contract.EscopoTodas); err != nil {
		t.Fatal(err)
	}
	quero := map[string]int64{"2026-11": 25_000, "2026-12": 23_000, "2027-01": 25_000}
	for mes, v := range quero {
		if got := umDoMes(t, svc, mes); got.ValorCentavos != v {
			t.Errorf("%s = %d, quero %d", mes, got.ValorCentavos, v)
		}
	}
}

func TestTodasMantemORestoDaPrimeiraParcela(t *testing.T) {
	svc, st := montar(t)
	g := lancar(t, svc, parcelado(gastoIn("2026-11-10", "eletro", gasto.Fixo, 10_000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}))
	d := g.Dados()
	d.Descricao = "Geladeira"
	if _, err := svc.AtualizarGastoNoEscopo(ctx, "u-1", g.ID, d, contract.EscopoTodas); err != nil {
		t.Fatal(err)
	}
	gs, _ := st.DaSerie(ctx, "u-1", g.Serie.SerieID)
	slices.SortFunc(gs, func(a, b gasto.Gasto) int { return a.Serie.Numero - b.Serie.Numero })
	if !slices.Equal(valores(gs), []int64{3334, 3333, 3333}) || gs[2].Descricao != "Geladeira" {
		t.Errorf("parcelas = %+v", gs)
	}
}

func TestProximasNaoMudaOMes(t *testing.T) {
	svc, _ := montar(t)
	g := lancar(t, svc, parcelado(gastoIn("2026-11-10", "eletro", gasto.Fixo, 30_000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}))
	d := g.Dados()
	d.Data = "2026-12-10"
	if _, err := svc.AtualizarGastoNoEscopo(ctx, "u-1", g.ID, d, contract.EscopoProximas); campo(err) != "data" {
		t.Errorf("mudar o mês = %v", err)
	}
	// Só o dia vale para a série.
	d.Data = "2026-11-20"
	if _, err := svc.AtualizarGastoNoEscopo(ctx, "u-1", g.ID, d, contract.EscopoTodas); err != nil {
		t.Fatal(err)
	}
	if jan := umDoMes(t, svc, "2027-01"); jan.Data != "2027-01-20" {
		t.Errorf("3ª parcela = %s", jan.Data)
	}
}

func TestSeriesNoLimiteDoCartao(t *testing.T) {
	svc, _ := montar(t)
	k := cadastrar(t, svc, "u-1", nubank())
	// Cenário "Parcelas futuras no limite".
	lancar(t, svc, parcelado(gastoIn("2026-11-10", "eletro", gasto.Cartao, 120_000, k.ID), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}))
	// Cenário "Recorrência no limite": a assinatura de novembro (fatura de
	// dezembro) conta; as dos meses seguintes, ainda que já criadas, não.
	lancar(t, svc, recorrente(gastoIn("2026-11-12", "streaming", gasto.Cartao, 5_000, k.ID), ""))
	doMes(t, svc, "2027-03")

	res, err := svc.ResumoCartoes(ctx, "u-1", "2026-12", "2026-11-16")
	if err != nil {
		t.Fatal(err)
	}
	if r := res[0]; r.LimiteUsadoCentavos != 125_000 || r.LimiteDisponivelCentavos() != 375_000 || r.FaturaCentavos != 45_000 {
		t.Errorf("resumo = %+v", r)
	}
}

// Change colaboradores: autoria dos lançamentos.

func TestAutoriaEFiltroPorPessoa(t *testing.T) {
	svc, _ := montar(t)
	// Gasto antigo, sem autor gravado: conta como do dono.
	antigo := criar(t, svc, "u-1", dados("2026-11-02", "mercado", gasto.Fixo, 100))
	d := dados("2026-11-03", "mercado", gasto.Fixo, 200)
	d.AutorID = "bruno"
	doBruno := criar(t, svc, "u-1", d)
	if doBruno.CriadoPor != "bruno" || doBruno.Contract().CriadoPor != "bruno" {
		t.Fatalf("criado por = %+v", doBruno)
	}
	d = doBruno.Dados()
	d.ValorCentavos, d.AutorID = 250, "u-1"
	editado, err := svc.AtualizarGasto(ctx, "u-1", doBruno.ID, d)
	if err != nil || editado.CriadoPor != "bruno" || editado.AtualizadoPor != "u-1" {
		t.Fatalf("editado = %+v, %v", editado, err)
	}

	ids := func(autor string) []string {
		pg, err := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", AutorID: autor})
		if err != nil {
			t.Fatal(err)
		}
		var l []string
		for _, g := range pg.Items {
			l = append(l, g.ID)
		}
		return l
	}
	if l := ids("bruno"); len(l) != 1 || l[0] != doBruno.ID {
		t.Errorf("do bruno = %v", l)
	}
	if l := ids("u-1"); len(l) != 1 || l[0] != antigo.ID {
		t.Errorf("do dono = %v", l)
	}
	if l := ids(""); len(l) != 2 {
		t.Errorf("todos = %v", l)
	}

	// A recorrência lançada pelo colaborador cria ocorrências com o autor dele.
	in := recorrente(gastoIn("2026-11-05", "luz", gasto.Fixo, 20_000, ""), "")
	in.AutorID = "bruno"
	lancar(t, svc, in)
	if dez := umDoMes(t, svc, "2026-12"); dez.CriadoPor != "bruno" {
		t.Errorf("ocorrência de dezembro criada por %q", dez.CriadoPor)
	}
}
