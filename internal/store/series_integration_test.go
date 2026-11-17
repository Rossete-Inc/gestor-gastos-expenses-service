//go:build integration

package store

import (
	"slices"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/serie"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Séries no DynamoDB Local (parcelamentos-recorrencias, tarefa 1.2).

func novaSerie(t *testing.T, tipo string, mesInicial string) serie.Serie {
	t.Helper()
	relogio = relogio.Add(time.Millisecond)
	return serie.Serie{
		ID: gasto.NovoULID(relogio), UserID: "u-1", Tipo: tipo, ValorCentavos: 1_000,
		CategoriaID: "eletro", Grupo: gasto.Fixo, Dia: 10, MesInicial: mesInicial, PrimeiroNumero: 1,
		CriadoEm: relogio,
	}
}

func gastoDaSerie(t *testing.T, se serie.Serie, n int, mes string) gasto.Gasto {
	t.Helper()
	relogio = relogio.Add(time.Millisecond)
	g, err := gasto.Novo(gasto.NovoULID(relogio), se.UserID, se.Modelo(mes), relogio)
	if err != nil {
		t.Fatal(err)
	}
	g.Serie = se.Posicao(n)
	return g
}

func TestCriarSerieCom72Parcelas(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	se := novaSerie(t, contract.SerieParcelamento, "2026-11")
	se.TotalParcelas = 72
	var gs []gasto.Gasto
	for n := 1; n <= 72; n++ {
		gs = append(gs, gastoDaSerie(t, se, n, serie.SomarMeses("2026-11", n-1)))
	}
	se.PrimeiroGastoID = gs[0].ID
	if err := d.CriarSerie(ctx, se, gs); err != nil {
		t.Fatal(err)
	}
	da, err := d.DaSerie(ctx, "u-1", se.ID)
	if err != nil || len(da) != 72 {
		t.Fatalf("da série = %d, %v", len(da), err)
	}
	if da[0].Serie.Total != 72 || da[0].Serie.Tipo != contract.SerieParcelamento {
		t.Errorf("posição = %+v", da[0].Serie)
	}

	// Repetir a criação (mesma chave de idempotência) não duplica nada.
	outro := gastoDaSerie(t, se, 1, "2026-11")
	if err := d.CriarSerie(ctx, se, []gasto.Gasto{outro}); errs.As(err) == nil || errs.As(err).Code != CodigoSerieExistente {
		t.Errorf("repetir = %v", err)
	}
	if da, _ := d.DaSerie(ctx, "u-1", se.ID); len(da) != 72 {
		t.Errorf("depois de repetir = %d gastos", len(da))
	}
	lida, err := d.ObterSerie(ctx, "u-1", se.ID)
	if err != nil || lida.PrimeiroGastoID != gs[0].ID || lida.TotalParcelas != 72 || lida.Versao != 1 {
		t.Errorf("série lida = %+v, %v", lida, err)
	}
	if _, err := d.ObterSerie(ctx, "u-2", se.ID); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("série de outro usuário = %v", err)
	}
}

func TestGerarOcorrenciasNaoDuplica(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	se := novaSerie(t, contract.SerieRecorrencia, "2026-11")
	se.MesesGerados = []string{"2026-11"}
	primeiro := gastoDaSerie(t, se, 1, "2026-11")
	if err := d.CriarSerie(ctx, se, []gasto.Gasto{primeiro}); err != nil {
		t.Fatal(err)
	}
	meses := []string{"2026-12", "2027-01"}
	gs := []gasto.Gasto{gastoDaSerie(t, se, 2, meses[0]), gastoDaSerie(t, se, 3, meses[1])}
	if err := d.GerarOcorrencias(ctx, se, meses, gs); err != nil {
		t.Fatal(err)
	}
	// Uma segunda leitura com os mesmos meses (outros ULIDs) não grava nada.
	deNovo := []gasto.Gasto{gastoDaSerie(t, se, 2, meses[0]), gastoDaSerie(t, se, 3, meses[1])}
	if err := d.GerarOcorrencias(ctx, se, meses, deNovo); err != nil {
		t.Fatal(err)
	}
	if da, _ := d.DaSerie(ctx, "u-1", se.ID); len(da) != 3 {
		t.Errorf("gastos = %d, quero 3", len(da))
	}
	lida, _ := d.ObterSerie(ctx, "u-1", se.ID)
	slices.Sort(lida.MesesGerados)
	if !slices.Equal(lida.MesesGerados, []string{"2026-11", "2026-12", "2027-01"}) || lida.Versao != 2 {
		t.Errorf("série = %+v", lida)
	}
	if lista, _ := d.ListarSeries(ctx, "u-1"); len(lista) != 1 || lista[0].ID != se.ID {
		t.Errorf("listar séries = %+v", lista)
	}
	// A série não aparece entre os gastos do mês nem conta como gasto.
	if doMes, _ := d.DoMes(ctx, "u-1", "2026-11"); len(doMes) != 1 {
		t.Errorf("novembro = %+v", doMes)
	}
}

func TestAplicarMudancas(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	se := novaSerie(t, contract.SerieRecorrencia, "2026-11")
	gs := []gasto.Gasto{gastoDaSerie(t, se, 1, "2026-11"), gastoDaSerie(t, se, 2, "2026-12")}
	if err := d.CriarSerie(ctx, se, gs); err != nil {
		t.Fatal(err)
	}
	lida, _ := d.ObterSerie(ctx, "u-1", se.ID)

	// Regrava a 1ª com outro valor (mesma chave), muda a data da 2ª (chave
	// nova) e encerra a série.
	g1 := gs[0]
	g1.ValorCentavos = 2_000
	nd := gs[1].Dados()
	nd.Data, nd.MesReferencia = "2026-12-20", ""
	g2, err := gs[1].Atualizado(nd, relogio)
	if err != nil {
		t.Fatal(err)
	}
	lida.MesFinal = "2026-12"
	m := serie.Mudancas{UserID: "u-1", Series: []serie.Serie{lida}, Gravar: []gasto.Gasto{g1, g2}, Excluir: []gasto.Gasto{gs[0], gs[1]}}
	if err := d.Aplicar(ctx, m); err != nil {
		t.Fatal(err)
	}
	da, _ := d.DaSerie(ctx, "u-1", se.ID)
	if len(da) != 2 {
		t.Fatalf("gastos = %+v", da)
	}
	if g, err := d.Obter(ctx, "u-1", g1.ID); err != nil || g.ValorCentavos != 2_000 {
		t.Errorf("1ª = %+v, %v", g, err)
	}
	naoEncontrado(t, "chave antiga da 2ª", func() error { _, err := d.Obter(ctx, "u-1", gs[1].ID); return err }())

	// Gravar de novo com a versão antiga é conflito.
	if err := d.Aplicar(ctx, serie.Mudancas{UserID: "u-1", Series: []serie.Serie{lida}}); errs.As(err) == nil || errs.As(err).Code != CodigoSerieAlterada {
		t.Errorf("versão velha = %v", err)
	}

	// Apagar a série e os gastos.
	if err := d.Aplicar(ctx, serie.Mudancas{UserID: "u-1", SeriesExcluidas: []string{se.ID}, Excluir: da}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ObterSerie(ctx, "u-1", se.ID); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("série depois de apagar = %v", err)
	}
	if da, _ := d.DaSerie(ctx, "u-1", se.ID); len(da) != 0 {
		t.Errorf("gastos depois de apagar = %+v", da)
	}
}

func TestAplicarEmVariasTransacoes(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	se := novaSerie(t, contract.SerieParcelamento, "2026-01")
	var gs []gasto.Gasto
	for n := 1; n <= 72; n++ {
		gs = append(gs, gastoDaSerie(t, se, n, serie.SomarMeses("2026-01", n-1)))
	}
	if err := d.CriarSerie(ctx, se, gs); err != nil {
		t.Fatal(err)
	}
	// Mudar o dia de todas: 72 apagadas + 72 gravadas + a série = 145 itens.
	var novos []gasto.Gasto
	for _, g := range gs {
		nd := g.Dados()
		nd.Data, nd.MesReferencia = serie.DataNoMes(g.Data[:7], 20), ""
		n, err := g.Atualizado(nd, relogio)
		if err != nil {
			t.Fatal(err)
		}
		novos = append(novos, n)
	}
	lida, _ := d.ObterSerie(ctx, "u-1", se.ID)
	lida.Dia = 20
	if err := d.Aplicar(ctx, serie.Mudancas{UserID: "u-1", Series: []serie.Serie{lida}, Gravar: novos, Excluir: gs}); err != nil {
		t.Fatal(err)
	}
	da, _ := d.DaSerie(ctx, "u-1", se.ID)
	if len(da) != 72 || da[0].Data[8:] != "20" {
		t.Errorf("gastos = %d, 1º dia %s", len(da), da[0].Data)
	}
}

func TestExcluirDoUsuarioApagaSeries(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	se := novaSerie(t, contract.SerieRecorrencia, "2026-11")
	if err := d.CriarSerie(ctx, se, []gasto.Gasto{gastoDaSerie(t, se, 1, "2026-11")}); err != nil {
		t.Fatal(err)
	}
	if n, err := d.ExcluirDoUsuario(ctx, "u-1"); err != nil || n != 2 {
		t.Errorf("excluir do usuário = %d, %v", n, err)
	}
}
