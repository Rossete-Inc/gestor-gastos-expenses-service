package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/meta"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
)

// Spec metas-gastos, "Aviso ao salvar um gasto" e "Progresso da meta no mês".

func lancarComAlertas(t *testing.T, svc *Service, in contract.CriarGastoIn) []meta.Alerta {
	t.Helper()
	_, alertas, err := svc.LancarComAlertas(ctx, "u-1", in)
	if err != nil {
		t.Fatal(err)
	}
	return alertas
}

func avulso(data, categoria string, centavos int64) contract.CriarGastoIn {
	return contract.CriarGastoIn{GastoIn: gastoIn(data, categoria, gasto.Cartao, centavos, "")}
}

type alertaEsperado struct {
	mes    string
	limiar int
	total  int64
}

func conferir(t *testing.T, got []meta.Alerta, want ...alertaEsperado) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("alertas = %+v, want %+v", got, want)
	}
	for i, w := range want {
		a := got[i]
		if a.Mes != w.mes || a.Limiar != w.limiar || a.TotalCentavos != w.total || a.CategoriaID != "lazer" || a.NomeCategoria != "Lazer" {
			t.Fatalf("alerta %d = %+v, want %+v", i, a, w)
		}
	}
}

func comMetaDeLazer(t *testing.T, valor int64) *Service {
	t.Helper()
	svc, _ := montar(t)
	definir(t, svc, "u-1", metaIn("lazer", "Lazer", valor, "2026-11"))
	return svc
}

func TestAlertaAoCruzar80(t *testing.T) {
	svc := comMetaDeLazer(t, 100000)
	// spec, "Lançamento que não cruza limiar"
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-02", "lazer", 70000)))
	// spec, "Cruzar 80%": R$ 700,00 + R$ 120,00
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-03", "lazer", 12000)), alertaEsperado{"2026-11", 80, 82000})
	// já em aviso, sem chegar a 100%: nada
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-04", "lazer", 5000)))
	// outra categoria, sem meta: nada
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-04", "alimentacao", 900000)))
	// estoura
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-05", "lazer", 13000)), alertaEsperado{"2026-11", 100, 100000})
	// estourada e mais um gasto: nada
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-06", "lazer", 1)))
}

func TestAlerta80E100DeUmaVez(t *testing.T) {
	svc := comMetaDeLazer(t, 100000)
	lancarComAlertas(t, svc, avulso("2026-11-02", "lazer", 70000))
	// spec, "Cruzar 80% e 100% de uma vez": só o de 100%
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-03", "lazer", 40000)), alertaEsperado{"2026-11", 100, 110000})
	// os dois ficaram marcados: nada de 80% depois
	m, _, _ := svc.store.ObterMeta(ctx, "u-1", "lazer")
	if len(m.Alertas["2026-11"]) != 2 {
		t.Fatalf("alertas marcados = %+v", m.Alertas)
	}
}

func TestAlertaNaoRepeteNoMes(t *testing.T) {
	svc := comMetaDeLazer(t, 100000)
	g, alertas, err := svc.LancarComAlertas(ctx, "u-1", avulso("2026-11-02", "lazer", 85000))
	if err != nil {
		t.Fatal(err)
	}
	conferir(t, alertas, alertaEsperado{"2026-11", 80, 85000})
	// spec, "Limiar já notificado no mês": excluir não avisa, e voltar a 85% também não
	if err := svc.ExcluirGastoNoEscopo(ctx, "u-1", g.ID, ""); err != nil {
		t.Fatal(err)
	}
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-03", "lazer", 85000)))
	// spec, "Novo mês": dezembro começa do zero e avisa de novo
	conferir(t, lancarComAlertas(t, svc, avulso("2026-12-01", "lazer", 90000)), alertaEsperado{"2026-12", 80, 90000})
}

func TestMetaCriadaJaAcimaDe80(t *testing.T) {
	svc, _ := montar(t)
	criar(t, svc, "u-1", dados("2026-11-02", "lazer", gasto.Cartao, 85000))
	definir(t, svc, "u-1", metaIn("lazer", "Lazer", 100000, "2026-11"))
	// o lançamento não cruzou 80% (já estava acima): nada
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-03", "lazer", 1000)))
	conferir(t, lancarComAlertas(t, svc, avulso("2026-11-04", "lazer", 14000)), alertaEsperado{"2026-11", 100, 100000})
}

func TestAlertaNoMesDaFatura(t *testing.T) {
	svc := comMetaDeLazer(t, 10000)
	k := cadastrar(t, svc, "u-1", nubank()) // fecha dia 25, vence dia 5
	// spec, "Compra no cartão após o fechamento": conta na fatura, não no mês da compra
	conferir(t, lancarComAlertas(t, svc, contract.CriarGastoIn{GastoIn: gastoIn("2026-11-28", "lazer", gasto.Cartao, 10000, k.ID)}),
		alertaEsperado{"2027-01", 100, 10000})
	if nov, _ := svc.ListarMetas(ctx, "u-1", "2026-11"); nov[0].TotalCentavos != 0 {
		t.Fatalf("novembro = %+v", nov)
	}
}

func TestAlertaDeParceladoEmVariosMeses(t *testing.T) {
	svc := comMetaDeLazer(t, 50000)
	lancarComAlertas(t, svc, avulso("2026-12-10", "lazer", 30000))
	lancarComAlertas(t, svc, avulso("2027-01-10", "lazer", 40000))
	// spec, "Gasto parcelado": R$ 600,00 em 3×, R$ 200,00 por mês
	alertas := lancarComAlertas(t, svc, parcelado(gastoIn("2026-11-10", "lazer", gasto.Cartao, 60000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}))
	// novembro 40% (nada), dezembro 100% (estoura), janeiro 120% (estoura)
	conferir(t, alertas, alertaEsperado{"2026-12", 100, 50000}, alertaEsperado{"2027-01", 100, 60000})
}

func TestAlertaAoEditar(t *testing.T) {
	svc := comMetaDeLazer(t, 100000)
	lancarComAlertas(t, svc, avulso("2026-11-02", "lazer", 70000))
	g := lancar(t, svc, avulso("2026-11-03", "alimentacao", 20000))

	// trocar a categoria para Lazer faz cruzar 80%
	_, alertas, err := svc.AtualizarGastoComAlertas(ctx, "u-1", g.ID, dados("2026-11-03", "lazer", gasto.Cartao, 20000), "")
	if err != nil {
		t.Fatal(err)
	}
	conferir(t, alertas, alertaEsperado{"2026-11", 80, 90000})

	// editar só a descrição não muda o total: nada
	d := dados("2026-11-03", "lazer", gasto.Cartao, 20000)
	d.Descricao = "Cinema"
	editado := umDoMesDaCategoria(t, svc, "2026-11", "lazer", 20000)
	if _, alertas, _ = svc.AtualizarGastoComAlertas(ctx, "u-1", editado.ID, d, ""); len(alertas) != 0 {
		t.Fatalf("editar descrição = %+v", alertas)
	}

	// aumentar o valor faz estourar
	d.ValorCentavos = 30000
	if _, alertas, _ = svc.AtualizarGastoComAlertas(ctx, "u-1", editado.ID, d, ""); len(alertas) != 1 || alertas[0].Limiar != 100 {
		t.Fatalf("aumentar valor = %+v", alertas)
	}
}

func TestAlertaAoEditarSerieInteira(t *testing.T) {
	svc := comMetaDeLazer(t, 50000)
	primeira := lancar(t, svc, parcelado(gastoIn("2026-11-10", "lazer", gasto.Cartao, 30000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3}))
	// cada mês tem R$ 100,00 (20%); cada parcela passa a R$ 450,00 (90%)
	d := dados("2026-11-10", "lazer", gasto.Cartao, 45000) // o valor de cada parcela
	_, alertas, err := svc.AtualizarGastoComAlertas(ctx, "u-1", primeira.ID, d, contract.EscopoTodas)
	if err != nil {
		t.Fatal(err)
	}
	conferir(t, alertas,
		alertaEsperado{"2026-11", 80, 45000}, alertaEsperado{"2026-12", 80, 45000}, alertaEsperado{"2027-01", 80, 45000})
}

func TestReenvioIdempotenteNaoAvisa(t *testing.T) {
	svc := comMetaDeLazer(t, 50000)
	in := parcelado(gastoIn("2026-11-10", "lazer", gasto.Cartao, 150000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3})
	in.ChaveIdempotencia = "form-1"
	if len(lancarComAlertas(t, svc, in)) != 3 {
		t.Fatal("o primeiro envio deveria avisar os três meses")
	}
	conferir(t, lancarComAlertas(t, svc, in))
}

func umDoMesDaCategoria(t *testing.T, svc *Service, mes, categoria string, valor int64) gasto.Gasto {
	t.Helper()
	for _, g := range doMes(t, svc, mes) {
		if g.CategoriaID == categoria && g.ValorCentavos == valor {
			return g
		}
	}
	t.Fatalf("sem gasto de %d em %s/%s", valor, categoria, mes)
	return gasto.Gasto{}
}

// publicadorFalso guarda os eventos; com falha, recusa todos.
type publicadorFalso struct {
	eventos []events.MetasAtingidasDetail
	falha   error
}

func (p *publicadorFalso) Publish(_ context.Context, detailType string, detail any) error {
	if p.falha != nil {
		return p.falha
	}
	if detailType != events.MetasAtingidas {
		return fmt.Errorf("detail-type %q", detailType)
	}
	p.eventos = append(p.eventos, detail.(events.MetasAtingidasDetail))
	return nil
}

func TestPublicaUmEventoPorEscrita(t *testing.T) {
	svc := comMetaDeLazer(t, 50000)
	pub := &publicadorFalso{}
	svc.ComEventos(pub)

	// sem alerta, sem evento
	lancarComAlertas(t, svc, avulso("2026-11-02", "lazer", 1000))
	if len(pub.eventos) != 0 {
		t.Fatalf("eventos sem alerta = %+v", pub.eventos)
	}

	// spec, "Um lançamento cruza limiares em vários meses": um evento, todos os alertas
	in := parcelado(gastoIn("2026-11-10", "lazer", gasto.Cartao, 150000, ""), contract.ParcelamentoIn{Modo: contract.ModoTotal, Parcelas: 3})
	in.AutorID = "bruno" // colaborador lançando na conta da u-1
	if n := len(lancarComAlertas(t, svc, in)); n != 3 {
		t.Fatalf("alertas = %d", n)
	}
	if len(pub.eventos) != 1 {
		t.Fatalf("eventos = %+v", pub.eventos)
	}
	e := pub.eventos[0]
	if e.DonoID != "u-1" || e.AutorID != "bruno" || len(e.Alertas) != 3 {
		t.Fatalf("evento = %+v", e)
	}
	if a := e.Alertas[0]; a.CategoriaID != "lazer" || a.NomeCategoria != "Lazer" || a.Mes != "2026-11" || a.Limiar != 100 || a.MetaCentavos != 50000 {
		t.Fatalf("alerta = %+v", a)
	}
}

func TestFalhaAoPublicarNaoDesfazOGasto(t *testing.T) {
	svc := comMetaDeLazer(t, 50000)
	svc.ComEventos(&publicadorFalso{falha: errors.New("barramento fora")})
	g, alertas, err := svc.LancarComAlertas(ctx, "u-1", avulso("2026-11-02", "lazer", 60000))
	if err != nil || g.ID == "" || len(alertas) != 1 {
		t.Fatalf("lançamento = %+v, %+v, %v", g, alertas, err)
	}
	// o dono é o autor quando ninguém mais é informado
	pub := &publicadorFalso{}
	svc2 := comMetaDeLazer(t, 50000)
	svc2.ComEventos(pub)
	lancarComAlertas(t, svc2, avulso("2026-11-02", "lazer", 60000))
	if len(pub.eventos) != 1 || pub.eventos[0].AutorID != "u-1" {
		t.Fatalf("eventos = %+v", pub.eventos)
	}
}
