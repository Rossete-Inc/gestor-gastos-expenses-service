//go:build integration

package store

import (
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/cartao"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// lancarNoCartao grava um gasto com cartão e mês de referência informados
// (o app calcula o mês; aqui ele vem pronto).
func lancarNoCartao(t *testing.T, d *Dynamo, userID, data, mesRef, cartaoID string, centavos int64) gasto.Gasto {
	t.Helper()
	relogio = relogio.Add(time.Millisecond)
	g, err := gasto.Novo(gasto.NovoULID(relogio), userID, gasto.Dados{
		ValorCentavos: centavos, Data: data, CategoriaID: "lazer", Grupo: gasto.Cartao,
		CartaoID: cartaoID, MesReferencia: mesRef,
	}, relogio)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Criar(ctx, g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestMesDeReferenciaDaFatura(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	semCartao := lancar(t, d, "u-1", "2026-11-03", "lazer", gasto.Cartao, 100)
	atual := lancarNoCartao(t, d, "u-1", "2026-10-28", "2026-11", "k1", 200) // fatura de novembro

	lido, err := d.Obter(ctx, "u-1", semCartao.ID)
	if err != nil || lido.Mes() != "2026-11" || lido.CartaoID != "" || lido.ValorCentavos != 100 {
		t.Fatalf("obter sem cartão = %+v, %v", lido, err)
	}

	doMes, err := d.DoMes(ctx, "u-1", "2026-11")
	if err != nil || len(doMes) != 2 {
		t.Fatalf("do mês = %v, %v", doMes, err)
	}
	pagina, _, err := d.Listar(ctx, "u-1", gasto.Filtro{Mes: "2026-11"}, nil, 20)
	if err != nil || len(pagina) != 2 {
		t.Fatalf("listar = %v, %v", pagina, err)
	}
	if fora, _ := d.DoMes(ctx, "u-1", "2026-10"); len(fora) != 0 {
		t.Errorf("a compra de 28/10 na fatura de novembro não conta em outubro: %v", fora)
	}

	// Atribuir um cartão muda o mês de referência, a chave e o ID; a antiga some.
	novo := semCartao
	novo.ID = gasto.IDDe("2026-12", "2026-11-03", semCartao.ULID())
	novo.MesReferencia, novo.CartaoID = "2026-12", "k1"
	if err := d.Substituir(ctx, semCartao, novo); err != nil {
		t.Fatal(err)
	}
	naoEncontrado(t, "obter o ID antigo depois de editar", func() error { _, err := d.Obter(ctx, "u-1", semCartao.ID); return err }())
	if g, err := d.Obter(ctx, "u-1", novo.ID); err != nil || g.Mes() != "2026-12" || g.CartaoID != "k1" {
		t.Errorf("obter editado = %+v, %v", g, err)
	}
	_ = atual
}

func TestCartoesCRUDEIsolamento(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	agora := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	k, err := cartao.Novo(gasto.NovoULID(agora), "u-1", cartao.Dados{
		Apelido: "Nubank", Cor: "#8A05BE", DiaFechamento: 25, DiaVencimento: 5, LimiteCentavos: 500_000,
	}, agora)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CriarCartao(ctx, k); err != nil {
		t.Fatal(err)
	}
	lancarNoCartao(t, d, "u-1", "2026-11-10", "2026-12", k.ID, 100)

	if lido, err := d.ObterCartao(ctx, "u-1", k.ID); err != nil || lido != k {
		t.Fatalf("obter = %+v, %v", lido, err)
	}
	lista, err := d.ListarCartoes(ctx, "u-1")
	if err != nil || len(lista) != 1 || lista[0].ID != k.ID {
		t.Fatalf("listar = %v, %v", lista, err)
	}
	// Cartões não aparecem nas Queries de gastos.
	if gs, _ := d.DesdeMes(ctx, "u-1", "2000-01"); len(gs) != 1 {
		t.Errorf("gastos desde 2000 = %v", gs)
	}

	// Outro usuário não vê, não edita e não exclui (spec, "Cartão alheio").
	if _, err := d.ObterCartao(ctx, "u-2", k.ID); errs.As(err) == nil || errs.As(err).Code != "cartao_nao_encontrado" {
		t.Errorf("obter alheio = %v", err)
	}
	if outros, _ := d.ListarCartoes(ctx, "u-2"); len(outros) != 0 {
		t.Errorf("listar alheio = %v", outros)
	}
	alheio := k
	alheio.UserID = "u-2"
	if err := d.SubstituirCartao(ctx, alheio); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("substituir alheio = %v", err)
	}
	if err := d.ExcluirCartao(ctx, "u-2", k.ID); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("excluir alheio = %v", err)
	}

	k.Arquivado = true
	if err := d.SubstituirCartao(ctx, k); err != nil {
		t.Fatal(err)
	}
	if lido, _ := d.ObterCartao(ctx, "u-1", k.ID); !lido.Arquivado {
		t.Error("arquivado não gravou")
	}
	if err := d.ExcluirCartao(ctx, "u-1", k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ObterCartao(ctx, "u-1", k.ID); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("obter excluído = %v", err)
	}

	// A exclusão do usuário apaga cartões e gastos (mesma partição).
	k2, _ := cartao.Novo(gasto.NovoULID(agora.Add(time.Second)), "u-1", cartao.Dados{
		Apelido: "Inter", Cor: "#FF7A00", DiaFechamento: 1, DiaVencimento: 10, LimiteCentavos: 1,
	}, agora)
	_ = d.CriarCartao(ctx, k2)
	if n, err := d.ExcluirDoUsuario(ctx, "u-1"); err != nil || n != 2 {
		t.Errorf("excluir do usuário = %d, %v (quero o gasto e o cartão)", n, err)
	}
}

func TestFiltroEExistenciaPorCartao(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	k1 := lancarNoCartao(t, d, "u-1", "2026-11-10", "2026-12", "k1", 100)
	lancarNoCartao(t, d, "u-1", "2026-11-11", "2026-12", "k2", 200)
	sem := lancar(t, d, "u-1", "2026-12-03", "aluguel", gasto.Fixo, 300)
	lancar(t, d, "u-1", "2026-12-04", "lazer", gasto.Cartao, 400) // sem cartão

	casos := []struct {
		cartao string
		quero  int
	}{{"", 4}, {"k1", 1}, {"k3", 0}, {gasto.SemCartao, 2}}
	for _, caso := range casos {
		todos, _ := listarTudo(t, d, "u-1", gasto.Filtro{Mes: "2026-12", CartaoID: caso.cartao}, 20)
		if len(todos) != caso.quero {
			t.Errorf("cartão %q = %d gastos, quero %d", caso.cartao, len(todos), caso.quero)
		}
	}
	if todos, _ := listarTudo(t, d, "u-1", gasto.Filtro{Mes: "2026-12", CartaoID: "k1"}, 20); len(todos) != 1 || todos[0].ID != k1.ID {
		t.Errorf("filtro k1 = %v", todos)
	}
	_ = sem

	if ok, err := d.ExisteNoCartao(ctx, "u-1", "k1"); err != nil || !ok {
		t.Errorf("existe no k1 = %v, %v", ok, err)
	}
	if ok, err := d.ExisteNoCartao(ctx, "u-1", "k3"); err != nil || ok {
		t.Errorf("existe no k3 = %v, %v", ok, err)
	}
	if ok, _ := d.ExisteNoCartao(ctx, "u-2", "k1"); ok {
		t.Error("gasto de outro usuário contou")
	}
}

func TestDesdeMes(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	lancarNoCartao(t, d, "u-1", "2026-09-10", "2026-10", "k1", 1)
	lancar(t, d, "u-1", "2026-11-03", "lazer", gasto.Cartao, 2)
	lancarNoCartao(t, d, "u-1", "2026-11-10", "2026-12", "k1", 3)
	lancarNoCartao(t, d, "u-1", "2026-12-10", "2027-01", "k1", 4)
	lancar(t, d, "u-2", "2026-12-01", "lazer", gasto.Cartao, 5)

	gs, err := d.DesdeMes(ctx, "u-1", "2026-11")
	if err != nil {
		t.Fatal(err)
	}
	var soma int64
	for _, g := range gs {
		soma += g.ValorCentavos
	}
	if len(gs) != 3 || soma != 2+3+4 {
		t.Errorf("desde novembro = %v (soma %d)", gs, soma)
	}
}
