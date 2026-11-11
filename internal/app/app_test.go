package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/memstore"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/money"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
)

var ctx = context.Background()

func codec(t *testing.T, segredo string) *page.Codec {
	t.Helper()
	c, err := page.NewCodec([]byte(segredo))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const segredo = "0123456789abcdef0123456789abcdef"

func montar(t *testing.T) (*Service, *memstore.Store) {
	t.Helper()
	st := memstore.New()
	svc := New(st, codec(t, segredo), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	// Relógio que anda 1 ms por chamada: ULIDs em ordem de lançamento.
	t0 := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	n := 0
	svc.agora = func() time.Time { n++; return t0.Add(time.Duration(n) * time.Millisecond) }
	return svc, st
}

func dados(data, categoria string, grupo gasto.Grupo, centavos int64) gasto.Dados {
	return gasto.Dados{ValorCentavos: centavos, Data: data, CategoriaID: categoria, Grupo: grupo}
}

func criar(t *testing.T, svc *Service, userID string, d gasto.Dados) gasto.Gasto {
	t.Helper()
	g, err := svc.CriarGasto(ctx, userID, d)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func kind(err error) errs.Kind { return errs.KindOf(err) }

func codigo(err error) string {
	if e := errs.As(err); e != nil {
		return e.Code
	}
	return ""
}

func TestCriarEObter(t *testing.T) {
	svc, st := montar(t)
	g := criar(t, svc, "u-1", gasto.Dados{ValorCentavos: 4590, Data: "2026-11-03", CategoriaID: "alimentacao", Grupo: gasto.Cartao, Descricao: "Almoço"})
	if !strings.HasPrefix(g.ID, "20261103-") || st.Quantos("u-1") != 1 {
		t.Fatalf("gasto = %+v", g)
	}
	lido, err := svc.ObterGasto(ctx, "u-1", " "+g.ID+" ")
	if err != nil || lido != g {
		t.Errorf("obter = %+v, %v", lido, err)
	}
}

func TestCriarInvalido(t *testing.T) {
	svc, st := montar(t)
	_, err := svc.CriarGasto(ctx, "u-1", dados("2026-11-03", "alimentacao", gasto.Cartao, 0))
	if kind(err) != errs.KindValidation || st.Quantos("u-1") != 0 {
		t.Errorf("erro = %v", err)
	}
}

func TestIsolamentoEntreUsuarios(t *testing.T) {
	// Cenário "Acesso a gasto alheio": A não lê, não edita e não exclui o de B.
	svc, st := montar(t)
	deB := criar(t, svc, "u-b", dados("2026-11-03", "alimentacao", gasto.Cartao, 100))

	if _, err := svc.ObterGasto(ctx, "u-a", deB.ID); codigo(err) != "gasto_nao_encontrado" {
		t.Errorf("obter = %v", err)
	}
	if _, err := svc.AtualizarGasto(ctx, "u-a", deB.ID, dados("2026-11-03", "lazer", gasto.Cartao, 1)); codigo(err) != "gasto_nao_encontrado" {
		t.Errorf("atualizar = %v", err)
	}
	if err := svc.ExcluirGasto(ctx, "u-a", deB.ID); codigo(err) != "gasto_nao_encontrado" {
		t.Errorf("excluir = %v", err)
	}
	if lido, err := svc.ObterGasto(ctx, "u-b", deB.ID); err != nil || lido != deB || st.Quantos("u-b") != 1 {
		t.Errorf("o gasto de B mudou: %+v, %v", lido, err)
	}
	pg, err := svc.ListarGastos(ctx, "u-a", Listagem{Mes: "2026-11"})
	if err != nil || len(pg.Items) != 0 {
		t.Errorf("A lista o gasto de B: %+v, %v", pg, err)
	}
	if existe, _ := svc.ExisteGastoNaCategoria(ctx, "u-a", "alimentacao"); existe {
		t.Error("A vê a categoria em uso por B")
	}
}

func TestIDInvalidoNemChegaAoStore(t *testing.T) {
	svc, st := montar(t)
	st.Err = errors.New("não devia ser chamado")
	for _, id := range []string{"", "x", "../20261103", strings.Repeat("9", 200)} {
		if _, err := svc.ObterGasto(ctx, "u-1", id); codigo(err) != "gasto_nao_encontrado" {
			t.Errorf("obter %q = %v", id, err)
		}
		if err := svc.ExcluirGasto(ctx, "u-1", id); codigo(err) != "gasto_nao_encontrado" {
			t.Errorf("excluir %q = %v", id, err)
		}
	}
}

func TestSemUsuario(t *testing.T) {
	svc, _ := montar(t)
	id := "20261103-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D"
	checa := func(nome string, err error) {
		t.Helper()
		if codigo(err) != "usuario_ausente" {
			t.Errorf("%s = %v", nome, err)
		}
	}
	_, err := svc.CriarGasto(ctx, "", dados("2026-11-03", "c", gasto.Cartao, 1))
	checa("criar", err)
	_, err = svc.ObterGasto(ctx, "", id)
	checa("obter", err)
	_, err = svc.AtualizarGasto(ctx, "", id, dados("2026-11-03", "c", gasto.Cartao, 1))
	checa("atualizar", err)
	checa("excluir", svc.ExcluirGasto(ctx, "", id))
	_, err = svc.ListarGastos(ctx, "", Listagem{Mes: "2026-11"})
	checa("listar", err)
	_, err = svc.ExisteGastoNaCategoria(ctx, "", "c")
	checa("existe", err)
}

func TestAtualizarTrocaDeGrupo(t *testing.T) {
	// Cenário "Troca de categoria entre grupos": Alimentação → Internet.
	svc, _ := montar(t)
	g := criar(t, svc, "u-1", dados("2026-11-03", "alimentacao", gasto.Cartao, 4590))
	novo, err := svc.AtualizarGasto(ctx, "u-1", g.ID, dados("2026-11-03", "internet", gasto.Fixo, 9990))
	if err != nil {
		t.Fatal(err)
	}
	if novo.ID != g.ID || novo.Grupo != gasto.Fixo || novo.ValorCentavos != 9990 || novo.AtualizadoEm.IsZero() || !novo.CriadoEm.Equal(g.CriadoEm) {
		t.Errorf("atualizado = %+v", novo)
	}
	fixos, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", Grupo: "FIXO"})
	cartao, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", Grupo: "CARTAO"})
	if len(fixos.Items) != 1 || len(cartao.Items) != 0 {
		t.Errorf("fixos = %d, cartão = %d", len(fixos.Items), len(cartao.Items))
	}
}

func TestAtualizarMudaDataEID(t *testing.T) {
	svc, st := montar(t)
	g := criar(t, svc, "u-1", dados("2026-11-03", "alimentacao", gasto.Cartao, 4590))
	novo, err := svc.AtualizarGasto(ctx, "u-1", g.ID, dados("2026-10-31", "alimentacao", gasto.Cartao, 4590))
	if err != nil {
		t.Fatal(err)
	}
	if novo.ID == g.ID || novo.ULID() != g.ULID() || st.Quantos("u-1") != 1 {
		t.Errorf("novo = %s, antigo = %s, quantos = %d", novo.ID, g.ID, st.Quantos("u-1"))
	}
	if _, err := svc.ObterGasto(ctx, "u-1", g.ID); codigo(err) != "gasto_nao_encontrado" {
		t.Errorf("o ID antigo ainda existe: %v", err)
	}
	out, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-10"})
	if len(out.Items) != 1 || out.Items[0].ID != novo.ID {
		t.Errorf("outubro = %+v", out.Items)
	}
}

func TestAtualizarInvalido(t *testing.T) {
	svc, _ := montar(t)
	g := criar(t, svc, "u-1", dados("2026-11-03", "alimentacao", gasto.Cartao, 4590))
	if _, err := svc.AtualizarGasto(ctx, "u-1", g.ID, dados("2026-11-03", "alimentacao", gasto.Cartao, 0)); kind(err) != errs.KindValidation {
		t.Errorf("erro = %v", err)
	}
	if lido, _ := svc.ObterGasto(ctx, "u-1", g.ID); lido != g {
		t.Errorf("o gasto mudou: %+v", lido)
	}
}

func TestExcluir(t *testing.T) {
	// Cenário "Exclusão confirmada": some da lista.
	svc, _ := montar(t)
	g := criar(t, svc, "u-1", dados("2026-11-03", "alimentacao", gasto.Cartao, 4590))
	if err := svc.ExcluirGasto(ctx, "u-1", g.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ExcluirGasto(ctx, "u-1", g.ID); codigo(err) != "gasto_nao_encontrado" {
		t.Errorf("excluir de novo = %v", err)
	}
	pg, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11"})
	if len(pg.Items) != 0 {
		t.Errorf("lista = %+v", pg.Items)
	}
}

func TestSomaDeCentavos(t *testing.T) {
	// Cenário "Soma de centavos": R$ 0,10 + R$ 0,20 = exatamente R$ 0,30.
	svc, _ := montar(t)
	criar(t, svc, "u-1", dados("2026-11-01", "lazer", gasto.Cartao, 10))
	criar(t, svc, "u-1", dados("2026-11-02", "lazer", gasto.Cartao, 20))
	pg, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11"})
	total, err := money.SumBy(pg.Items, func(g gasto.Gasto) int64 { return g.ValorCentavos })
	if err != nil || total != 30 {
		t.Errorf("total = %d, %v", total, err)
	}
}

// lancarMes lança n gastos em novembro, alternando CARTAO e FIXO, em dias
// crescentes e, no mesmo dia, em ordem de lançamento.
func lancarMes(t *testing.T, svc *Service, userID string, n int) []gasto.Gasto {
	t.Helper()
	var out []gasto.Gasto
	for i := range n {
		grupo, cat := gasto.Cartao, "lazer"
		if i%2 == 1 {
			grupo, cat = gasto.Fixo, "aluguel"
		}
		out = append(out, criar(t, svc, userID, dados(fmt.Sprintf("2026-11-%02d", i/3+1), cat, grupo, int64(i+1))))
	}
	return out
}

func ids(gs []gasto.Gasto) string {
	var s []string
	for _, g := range gs {
		s = append(s, g.ID)
	}
	return strings.Join(s, ",")
}

func TestListarOrdemEPaginacao(t *testing.T) {
	svc, st := montar(t)
	lancarMes(t, svc, "u-1", 7)
	criar(t, svc, "u-1", dados("2026-10-31", "lazer", gasto.Cartao, 1)) // outro mês
	criar(t, svc, "u-1", dados("2026-12-01", "lazer", gasto.Cartao, 1)) // outro mês

	var todos []gasto.Gasto
	cursor := ""
	paginas := 0
	for {
		pg, err := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", Cursor: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		paginas++
		todos = append(todos, pg.Items...)
		if pg.NextCursor == "" {
			break
		}
		cursor = pg.NextCursor
	}
	// Do mais recente para o mais antigo: data decrescente e, no mesmo dia,
	// o último lançado primeiro.
	quero := st.IDs("u-1")[1:8]
	if ids(todos) != strings.Join(quero, ",") || paginas != 3 {
		t.Errorf("páginas = %d\nlista = %s\nquero = %s", paginas, ids(todos), strings.Join(quero, ","))
	}
}

func TestListarFiltros(t *testing.T) {
	// Cenário "Filtro por grupo": só FIXO de novembro.
	svc, _ := montar(t)
	lancados := lancarMes(t, svc, "u-1", 9)

	fixos, err := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", Grupo: "FIXO"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixos.Items) != 4 || fixos.NextCursor != "" {
		t.Errorf("fixos = %d, cursor %q", len(fixos.Items), fixos.NextCursor)
	}
	for _, g := range fixos.Items {
		if g.Grupo != gasto.Fixo {
			t.Errorf("gasto fora do filtro: %+v", g)
		}
	}
	porCategoria, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", CategoriaID: "lazer", Limit: 2})
	if len(porCategoria.Items) != 2 || porCategoria.NextCursor == "" || porCategoria.Items[0].ID != lancados[8].ID {
		t.Errorf("lazer = %s", ids(porCategoria.Items))
	}
	nada, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", Grupo: "FIXO", CategoriaID: "lazer"})
	if len(nada.Items) != 0 {
		t.Errorf("FIXO + lazer = %s", ids(nada.Items))
	}
}

func TestListarMesVazio(t *testing.T) {
	// Cenário "Mês sem gastos": lista vazia (serializada como []), sem cursor.
	svc, _ := montar(t)
	pg, err := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11"})
	if err != nil || pg.Items == nil || len(pg.Items) != 0 || pg.NextCursor != "" {
		t.Errorf("página = %+v, %v", pg, err)
	}
}

func TestListarLimitePadrao(t *testing.T) {
	svc, _ := montar(t)
	lancarMes(t, svc, "u-1", page.DefaultLimit+1)
	pg, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11"})
	if len(pg.Items) != page.DefaultLimit || pg.NextCursor == "" {
		t.Errorf("itens = %d", len(pg.Items))
	}
}

func TestListarParametrosInvalidos(t *testing.T) {
	svc, _ := montar(t)
	_, err := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-13", Grupo: "X"})
	if kind(err) != errs.KindValidation {
		t.Errorf("erro = %v", err)
	}
}

func TestCursorDeOutraListagem(t *testing.T) {
	svc, _ := montar(t)
	lancarMes(t, svc, "u-1", 4)
	lancarMes(t, svc, "u-2", 4)
	pg, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", Limit: 1})
	if pg.NextCursor == "" {
		t.Fatal("sem cursor")
	}
	outras := map[string]struct {
		user string
		in   Listagem
	}{
		"outro usuário": {"u-2", Listagem{Mes: "2026-11"}},
		"outro mês":     {"u-1", Listagem{Mes: "2026-10"}},
		"outro grupo":   {"u-1", Listagem{Mes: "2026-11", Grupo: "CARTAO"}},
		"outra categ.":  {"u-1", Listagem{Mes: "2026-11", CategoriaID: "lazer"}},
		"adulterado":    {"u-1", Listagem{Mes: "2026-11"}},
	}
	for nome, o := range outras {
		t.Run(nome, func(t *testing.T) {
			o.in.Cursor = pg.NextCursor
			if nome == "adulterado" {
				o.in.Cursor = pg.NextCursor[:len(pg.NextCursor)-2] + "xx"
			}
			if _, err := svc.ListarGastos(ctx, o.user, o.in); codigo(err) != "cursor_invalido" || kind(err) != errs.KindBadRequest {
				t.Errorf("erro = %v", err)
			}
		})
	}
	// Outro segredo (troca do SSM) também invalida o cursor.
	outro := New(memstore.New(), codec(t, strings.Repeat("z", 32)), nil)
	if _, err := outro.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11", Cursor: pg.NextCursor}); codigo(err) != "cursor_invalido" {
		t.Errorf("outro segredo = %v", err)
	}
}

func TestExisteGastoNaCategoria(t *testing.T) {
	svc, _ := montar(t)
	criar(t, svc, "u-1", dados("2026-03-10", "pet", gasto.Cartao, 100))
	if existe, err := svc.ExisteGastoNaCategoria(ctx, "u-1", " pet "); err != nil || !existe {
		t.Errorf("pet = %v, %v", existe, err)
	}
	if existe, err := svc.ExisteGastoNaCategoria(ctx, "u-1", "lazer"); err != nil || existe {
		t.Errorf("lazer = %v, %v", existe, err)
	}
	for _, id := range []string{"", strings.Repeat("a", 65)} {
		if _, err := svc.ExisteGastoNaCategoria(ctx, "u-1", id); kind(err) != errs.KindValidation {
			t.Errorf("categoria %q = %v", id, err)
		}
	}
}

func TestConsumirUserDeleted(t *testing.T) {
	svc, st := montar(t)
	lancarMes(t, svc, "u-1", 5)
	lancarMes(t, svc, "u-2", 2)
	if err := svc.ConsumirUserDeleted(ctx, events.UserDeletedDetail{UserID: "u-1"}); err != nil {
		t.Fatal(err)
	}
	// Idempotente: o mesmo evento de novo não falha.
	if err := svc.ConsumirUserDeleted(ctx, events.UserDeletedDetail{UserID: "u-1"}); err != nil {
		t.Fatal(err)
	}
	if st.Quantos("u-1") != 0 || st.Quantos("u-2") != 2 {
		t.Errorf("u-1 = %d, u-2 = %d", st.Quantos("u-1"), st.Quantos("u-2"))
	}
	if err := svc.ConsumirUserDeleted(ctx, events.UserDeletedDetail{UserID: " "}); kind(err) != errs.KindValidation {
		t.Errorf("sem userId = %v", err)
	}
}

func TestErrosDoStore(t *testing.T) {
	svc, st := montar(t)
	g := criar(t, svc, "u-1", dados("2026-11-03", "lazer", gasto.Cartao, 1))
	falha := errs.Internal(errors.New("dynamo fora"))
	st.Err = falha

	_, err := svc.CriarGasto(ctx, "u-1", dados("2026-11-03", "lazer", gasto.Cartao, 1))
	checar := func(nome string, err error) {
		t.Helper()
		if !errors.Is(err, falha) {
			t.Errorf("%s = %v", nome, err)
		}
	}
	checar("criar", err)
	_, err = svc.ObterGasto(ctx, "u-1", g.ID)
	checar("obter", err)
	_, err = svc.AtualizarGasto(ctx, "u-1", g.ID, dados("2026-11-03", "lazer", gasto.Cartao, 2))
	checar("atualizar", err)
	checar("excluir", svc.ExcluirGasto(ctx, "u-1", g.ID))
	_, err = svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11"})
	checar("listar", err)
	_, err = svc.ExisteGastoNaCategoria(ctx, "u-1", "lazer")
	checar("existe", err)
	checar("user deleted", svc.ConsumirUserDeleted(ctx, events.UserDeletedDetail{UserID: "u-1"}))
}

// storeAlheio devolve o gasto de outro usuário, simulando um store com bug.
type storeAlheio struct{ *memstore.Store }

func (s storeAlheio) Obter(_ context.Context, _, id string) (gasto.Gasto, error) {
	return gasto.Gasto{ID: id, UserID: "outro"}, nil
}

func (s storeAlheio) Substituir(context.Context, gasto.Gasto, gasto.Gasto) error {
	return errs.Internal(errors.New("não devia substituir"))
}

func (s storeAlheio) DoMes(context.Context, string, string) ([]gasto.Gasto, error) {
	return []gasto.Gasto{{ID: "20261103-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D", UserID: "outro", Data: "2026-11-03", Grupo: gasto.Cartao, CategoriaID: "lazer", ValorCentavos: 999}}, nil
}

func TestDefesaContraStoreComBug(t *testing.T) {
	svc := New(storeAlheio{memstore.New()}, codec(t, segredo), nil)
	id := "20261103-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D"
	if _, err := svc.ObterGasto(ctx, "u-1", id); codigo(err) != "gasto_nao_encontrado" {
		t.Errorf("obter = %v", err)
	}
	if _, err := svc.AtualizarGasto(ctx, "u-1", id, dados("2026-11-03", "lazer", gasto.Cartao, 1)); codigo(err) != "gasto_nao_encontrado" {
		t.Errorf("atualizar = %v", err)
	}
	if r, err := svc.ResumoMensal(ctx, "u-1", "2026-11"); err != nil || r.TotalCentavos != 0 || len(r.Recentes) != 0 {
		t.Errorf("resumo = %+v, %v", r, err)
	}
}

func TestResumoMensal(t *testing.T) {
	svc, _ := montar(t)
	criar(t, svc, "u-1", dados("2026-11-03", "alimentacao", gasto.Cartao, 10_000))
	criar(t, svc, "u-1", dados("2026-11-04", "transporte", gasto.Cartao, 20_000))
	criar(t, svc, "u-1", dados("2026-11-05", "aluguel", gasto.Fixo, 170_000))
	criar(t, svc, "u-1", dados("2026-10-31", "aluguel", gasto.Fixo, 999)) // outro mês
	criar(t, svc, "u-2", dados("2026-11-05", "aluguel", gasto.Fixo, 777)) // outro usuário

	r, err := svc.ResumoMensal(ctx, "u-1", " 2026-11 ")
	if err != nil {
		t.Fatal(err)
	}
	if r.Mes != "2026-11" || r.TotalCentavos != 200_000 || len(r.Recentes) != 3 || len(r.PorCategoria) != 3 {
		t.Errorf("resumo = %+v", r)
	}
	if r.PorGrupo[0] != (gasto.TotalGrupo{Grupo: gasto.Cartao, TotalCentavos: 30_000}) || r.PorGrupo[1] != (gasto.TotalGrupo{Grupo: gasto.Fixo, TotalCentavos: 170_000}) {
		t.Errorf("por grupo = %+v", r.PorGrupo)
	}
	if r.Recentes[0].Data != "2026-11-05" {
		t.Errorf("mais recente = %+v", r.Recentes[0])
	}
}

func TestResumoMensalReflete(t *testing.T) {
	// Cenário "Novo gasto refletido": o resumo lê o store a cada chamada.
	svc, _ := montar(t)
	g := criar(t, svc, "u-1", dados("2026-11-03", "lazer", gasto.Cartao, 100))
	if _, err := svc.AtualizarGasto(ctx, "u-1", g.ID, dados("2026-11-03", "lazer", gasto.Cartao, 250)); err != nil {
		t.Fatal(err)
	}
	novo := criar(t, svc, "u-1", dados("2026-11-04", "lazer", gasto.Cartao, 50))
	r, err := svc.ResumoMensal(ctx, "u-1", "2026-11")
	if err != nil || r.TotalCentavos != 300 || r.Recentes[0].ID != novo.ID {
		t.Fatalf("resumo = %+v, %v", r, err)
	}
	if err := svc.ExcluirGasto(ctx, "u-1", novo.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ = svc.ResumoMensal(ctx, "u-1", "2026-11"); r.TotalCentavos != 250 || len(r.Recentes) != 1 {
		t.Errorf("depois de excluir = %+v", r)
	}
}

func TestResumoMensalMesVazio(t *testing.T) {
	svc, _ := montar(t)
	criar(t, svc, "u-1", dados("2026-10-31", "lazer", gasto.Cartao, 100))
	r, err := svc.ResumoMensal(ctx, "u-1", "2026-11")
	if err != nil || r.TotalCentavos != 0 || len(r.PorGrupo) != 2 || r.PorCategoria == nil || len(r.PorCategoria) != 0 || r.Recentes == nil || len(r.Recentes) != 0 {
		t.Errorf("mês vazio = %#v, %v", r, err)
	}
}

func TestResumoMensalInvalido(t *testing.T) {
	svc, _ := montar(t)
	for _, mes := range []string{"", "2026-13", "2026-1", "1899-12", "nov/2026"} {
		_, err := svc.ResumoMensal(ctx, "u-1", mes)
		if e := errs.As(err); e == nil || e.Kind != errs.KindValidation || len(e.Fields) != 1 || e.Fields[0].Field != "mes" {
			t.Errorf("mes %q = %v", mes, err)
		}
	}
	if _, err := svc.ResumoMensal(ctx, " ", "2026-11"); codigo(err) != "usuario_ausente" {
		t.Errorf("sem usuário = %v", err)
	}
}

func TestResumoMensalErroDoStore(t *testing.T) {
	svc, st := montar(t)
	falha := errs.Internal(errors.New("dynamo fora"))
	st.Err = falha
	if _, err := svc.ResumoMensal(ctx, "u-1", "2026-11"); !errors.Is(err, falha) {
		t.Errorf("resumo = %v", err)
	}
}
