package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/cartao"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Specs cartoes-credito e lancamento-gastos (cartão no lançamento).

func nubank() cartao.Dados {
	return cartao.Dados{Apelido: "Nubank", Cor: "#8A05BE", DiaFechamento: 25, DiaVencimento: 5, LimiteCentavos: 500_000}
}

func cadastrar(t *testing.T, svc *Service, userID string, d cartao.Dados) cartao.Cartao {
	t.Helper()
	c, err := svc.CriarCartao(ctx, userID, d)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func noCartao(data, categoria string, grupo gasto.Grupo, centavos int64, cartaoID string) gasto.Dados {
	d := dados(data, categoria, grupo, centavos)
	d.CartaoID = cartaoID
	return d
}

func campo(err error) string {
	if e := errs.As(err); e != nil && e.Kind == errs.KindValidation && len(e.Fields) == 1 {
		return e.Fields[0].Field
	}
	return fmt.Sprintf("não é validação de um campo: %v", err)
}

func TestCadastroDeCartao(t *testing.T) {
	svc, _ := montar(t)
	c := cadastrar(t, svc, "u-1", nubank())
	if !gasto.ULIDValido(c.ID) || c.Apelido != "Nubank" || c.Arquivado {
		t.Fatalf("cartão = %+v", c)
	}

	// Cenário "Apelido duplicado".
	d := nubank()
	d.Apelido = " nubank "
	if _, err := svc.CriarCartao(ctx, "u-1", d); campo(err) != "apelido" {
		t.Errorf("duplicado = %v", err)
	}
	// O mesmo apelido em outro usuário é livre.
	cadastrar(t, svc, "u-2", d)

	// Cenário "Dia inválido".
	d = nubank()
	d.Apelido, d.DiaFechamento = "Outro", 0
	if _, err := svc.CriarCartao(ctx, "u-1", d); campo(err) != "diaFechamento" {
		t.Errorf("dia 0 = %v", err)
	}
}

func TestLimiteDeCartoesAtivos(t *testing.T) {
	svc, _ := montar(t)
	var ultimo cartao.Cartao
	for i := range cartao.MaxAtivos {
		d := nubank()
		d.Apelido = fmt.Sprintf("Cartão %02d", i)
		ultimo = cadastrar(t, svc, "u-1", d)
	}
	d := nubank()
	d.Apelido = "Vigésimo primeiro"
	_, err := svc.CriarCartao(ctx, "u-1", d)
	if e := errs.As(err); e == nil || e.Kind != errs.KindConflict || e.Code != contract.CodigoLimiteCartoes {
		t.Fatalf("21º cartão = %v", err)
	}
	// Arquivar libera a vaga; reativar com 20 ativos é recusado.
	if _, err := svc.ArquivarCartao(ctx, "u-1", ultimo.ID, true); err != nil {
		t.Fatal(err)
	}
	cadastrar(t, svc, "u-1", d)
	if _, err := svc.ArquivarCartao(ctx, "u-1", ultimo.ID, false); codigo(err) != contract.CodigoLimiteCartoes {
		t.Errorf("reativar com 20 ativos = %v", err)
	}
}

func TestEditarCartaoNaoRecalculaGastos(t *testing.T) {
	// Cenário "Mudança de fechamento".
	svc, _ := montar(t)
	c := cadastrar(t, svc, "u-1", nubank())
	antes := criar(t, svc, "u-1", noCartao("2026-11-22", "lazer", gasto.Cartao, 100, c.ID))
	if antes.Mes() != "2026-12" {
		t.Fatalf("antes = %s", antes.Mes())
	}
	d := nubank()
	d.DiaFechamento = 20
	if _, err := svc.AtualizarCartao(ctx, "u-1", c.ID, d); err != nil {
		t.Fatal(err)
	}
	if lido, _ := svc.ObterGasto(ctx, "u-1", antes.ID); lido.Mes() != "2026-12" {
		t.Errorf("gasto existente mudou de fatura: %s", lido.Mes())
	}
	if novo := criar(t, svc, "u-1", noCartao("2026-11-22", "lazer", gasto.Cartao, 100, c.ID)); novo.Mes() != "2027-01" {
		t.Errorf("gasto novo depois do dia 20 = %s, quero 2027-01", novo.Mes())
	}
	// Editar mantendo o apelido não conflita consigo mesmo.
	d.Cor = "#000000"
	if _, err := svc.AtualizarCartao(ctx, "u-1", c.ID, d); err != nil {
		t.Errorf("editar a cor = %v", err)
	}
}

func TestGastoNoCartao(t *testing.T) {
	svc, _ := montar(t)
	c := cadastrar(t, svc, "u-1", nubank())

	// Cenário "Gasto de cartão": 2026-11-03 no Nubank (25/5) cai em dezembro.
	g := criar(t, svc, "u-1", noCartao("2026-11-03", "alimentacao", gasto.Cartao, 4590, c.ID))
	if g.Mes() != "2026-12" || g.CartaoID != c.ID || g.Data != "2026-11-03" {
		t.Fatalf("gasto = %+v", g)
	}
	pg, err := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-12"})
	if err != nil || len(pg.Items) != 1 {
		t.Errorf("lista de dezembro = %+v, %v", pg, err)
	}
	if pg, _ := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-11"}); len(pg.Items) != 0 {
		t.Errorf("novembro não deveria ter o gasto: %+v", pg)
	}

	// Cenário "Gasto fixo no cartão".
	if f := criar(t, svc, "u-1", noCartao("2026-11-10", "internet", gasto.Fixo, 3990, c.ID)); f.Mes() != "2026-12" || f.Grupo != gasto.Fixo {
		t.Errorf("fixo no cartão = %+v", f)
	}

	// Cartão inexistente, de outro usuário ou com ID fora do formato.
	alheio := cadastrar(t, svc, "u-2", nubank())
	for _, id := range []string{gasto.NovoULID(svc.agora()), alheio.ID, "x"} {
		if _, err := svc.CriarGasto(ctx, "u-1", noCartao("2026-11-03", "lazer", gasto.Cartao, 1, id)); campo(err) != "cartaoId" {
			t.Errorf("cartão %q = %v", id, err)
		}
	}
}

func TestCartaoArquivado(t *testing.T) {
	svc, _ := montar(t)
	c := cadastrar(t, svc, "u-1", nubank())
	g := criar(t, svc, "u-1", noCartao("2026-11-03", "lazer", gasto.Cartao, 100, c.ID))
	sem := criar(t, svc, "u-1", dados("2026-11-04", "lazer", gasto.Cartao, 100))
	if _, err := svc.ArquivarCartao(ctx, "u-1", c.ID, true); err != nil {
		t.Fatal(err)
	}

	// Cenário "Cartão arquivado" do lançamento.
	if _, err := svc.CriarGasto(ctx, "u-1", noCartao("2026-11-05", "lazer", gasto.Cartao, 1, c.ID)); campo(err) != "cartaoId" {
		t.Errorf("lançar no arquivado = %v", err)
	}
	// Editar um gasto que já é do cartão arquivado continua possível.
	d := noCartao("2026-11-03", "lazer", gasto.Cartao, 200, c.ID)
	if _, err := svc.AtualizarGasto(ctx, "u-1", g.ID, d); err != nil {
		t.Errorf("editar gasto do arquivado = %v", err)
	}
	// Mas mover outro gasto para o arquivado não.
	if _, err := svc.AtualizarGasto(ctx, "u-1", sem.ID, noCartao("2026-11-04", "lazer", gasto.Cartao, 100, c.ID)); campo(err) != "cartaoId" {
		t.Errorf("mover para o arquivado = %v", err)
	}
	// Os gastos dele continuam nos totais.
	if r, _ := svc.ResumoMensal(ctx, "u-1", "2026-12"); r.TotalCentavos != 200 {
		t.Errorf("total de dezembro = %d", r.TotalCentavos)
	}
	// Arquivar de novo não muda nada.
	if c2, err := svc.ArquivarCartao(ctx, "u-1", c.ID, true); err != nil || !c2.Arquivado {
		t.Errorf("arquivar de novo = %+v, %v", c2, err)
	}
}

func TestAtribuirCartaoAGastoAntigo(t *testing.T) {
	// Cenário "Atribuir cartão a gasto antigo" (e "Trocar de cartão").
	svc, _ := montar(t)
	g := criar(t, svc, "u-1", dados("2026-11-10", "alimentacao", gasto.Cartao, 100))
	if g.Mes() != "2026-11" {
		t.Fatalf("sem cartão = %s", g.Mes())
	}
	c := cadastrar(t, svc, "u-1", nubank())
	novo, err := svc.AtualizarGasto(ctx, "u-1", g.ID, noCartao("2026-11-10", "alimentacao", gasto.Cartao, 100, c.ID))
	if err != nil || novo.Mes() != "2026-12" || novo.ID == g.ID || novo.ULID() != g.ULID() {
		t.Fatalf("atribuído = %+v, %v", novo, err)
	}
	inter := nubank()
	inter.Apelido, inter.DiaFechamento, inter.DiaVencimento = "Inter", 5, 15
	k2 := cadastrar(t, svc, "u-1", inter)
	trocado, err := svc.AtualizarGasto(ctx, "u-1", novo.ID, noCartao("2026-11-10", "alimentacao", gasto.Cartao, 100, k2.ID))
	if err != nil || trocado.Mes() != "2026-12" || trocado.CartaoID != k2.ID {
		t.Errorf("trocado = %+v, %v", trocado, err)
	}
	if _, err := svc.ObterGasto(ctx, "u-1", g.ID); kind(err) != errs.KindNotFound {
		t.Errorf("ID antigo continua existindo: %v", err)
	}
}

func TestListarPorCartao(t *testing.T) {
	svc, _ := montar(t)
	c := cadastrar(t, svc, "u-1", nubank())
	criar(t, svc, "u-1", noCartao("2026-11-03", "lazer", gasto.Cartao, 100, c.ID))
	criar(t, svc, "u-1", dados("2026-12-01", "aluguel", gasto.Fixo, 200))
	for filtro, quero := range map[string]int{"": 2, c.ID: 1, gasto.SemCartao: 1} {
		pg, err := svc.ListarGastos(ctx, "u-1", Listagem{Mes: "2026-12", CartaoID: filtro})
		if err != nil || len(pg.Items) != quero {
			t.Errorf("filtro %q = %d, %v", filtro, len(pg.Items), err)
		}
	}
}

func TestExcluirCartao(t *testing.T) {
	svc, st := montar(t)
	c := cadastrar(t, svc, "u-1", nubank())
	livre := nubank()
	livre.Apelido = "Livre"
	l := cadastrar(t, svc, "u-1", livre)
	criar(t, svc, "u-1", noCartao("2026-11-03", "lazer", gasto.Cartao, 100, c.ID))

	// Cenário "Excluir cartão com gastos".
	err := svc.ExcluirCartao(ctx, "u-1", c.ID)
	if e := errs.As(err); e == nil || e.Kind != errs.KindConflict || e.Code != contract.CodigoCartaoEmUso {
		t.Errorf("com gastos = %v", err)
	}
	// Cenário "Excluir cartão sem gastos".
	if err := svc.ExcluirCartao(ctx, "u-1", l.ID); err != nil {
		t.Fatal(err)
	}
	if lista, _ := svc.ListarCartoes(ctx, "u-1"); len(lista) != 1 || lista[0].ID != c.ID {
		t.Errorf("lista = %+v", lista)
	}
	// Cenário "Cartão alheio".
	for _, f := range []func() error{
		func() error { return svc.ExcluirCartao(ctx, "u-2", c.ID) },
		func() error { _, err := svc.ArquivarCartao(ctx, "u-2", c.ID, true); return err },
		func() error { _, err := svc.AtualizarCartao(ctx, "u-2", c.ID, nubank()); return err },
		func() error { return svc.ExcluirCartao(ctx, "u-1", "nao-e-ulid") },
	} {
		if e := errs.As(f()); e == nil || e.Code != contract.CodigoCartaoNaoEncontrado {
			t.Errorf("alheio = %v", e)
		}
	}
	_ = st
}

func TestListarCartoesEmOrdemDeApelido(t *testing.T) {
	svc, _ := montar(t)
	for _, a := range []string{"inter", "Bradesco", "C6"} {
		d := nubank()
		d.Apelido = a
		cadastrar(t, svc, "u-1", d)
	}
	lista, err := svc.ListarCartoes(ctx, "u-1")
	if err != nil || len(lista) != 3 || lista[0].Apelido != "Bradesco" || lista[1].Apelido != "C6" || lista[2].Apelido != "inter" {
		t.Errorf("lista = %+v, %v", lista, err)
	}
	if vazia, _ := svc.ListarCartoes(ctx, "u-2"); vazia == nil || len(vazia) != 0 {
		t.Errorf("sem cartões = %#v", vazia)
	}
}

func TestResumoCartoes(t *testing.T) {
	svc, _ := montar(t)
	c := cadastrar(t, svc, "u-1", nubank()) // fecha 25, vence 5, limite R$ 5.000,00
	// Fatura de novembro (vence 05/11, já vencida em 16/11).
	criar(t, svc, "u-1", noCartao("2026-10-10", "lazer", gasto.Cartao, 10_000, c.ID))
	// Fatura de dezembro (vence 05/12): R$ 1.200,00 abertos.
	criar(t, svc, "u-1", noCartao("2026-11-03", "lazer", gasto.Cartao, 70_000, c.ID))
	criar(t, svc, "u-1", noCartao("2026-11-20", "lazer", gasto.Cartao, 50_000, c.ID))
	// "Parcela futura" de fevereiro (vence 05/02/2027).
	criar(t, svc, "u-1", noCartao("2026-12-30", "lazer", gasto.Cartao, 3_000, c.ID))
	// Sem cartão não conta.
	criar(t, svc, "u-1", dados("2026-11-03", "aluguel", gasto.Fixo, 150_000))

	rs, err := svc.ResumoCartoes(ctx, "u-1", "2026-12", "2026-11-16")
	if err != nil || len(rs) != 1 {
		t.Fatalf("resumo = %+v, %v", rs, err)
	}
	r := rs[0]
	// Cenário "Limite com fatura aberta" + parcelas futuras contam.
	if r.FaturaCentavos != 120_000 || r.LimiteUsadoCentavos != 123_000 || r.LimiteDisponivelCentavos() != 377_000 {
		t.Errorf("dezembro = fatura %d, usado %d, disponível %d", r.FaturaCentavos, r.LimiteUsadoCentavos, r.LimiteDisponivelCentavos())
	}
	if r.Vencimento.Format(gasto.LayoutData) != "2026-12-05" {
		t.Errorf("vencimento = %v", r.Vencimento)
	}

	// Cenário "Fatura vencida": no dia do vencimento ainda conta; depois, não.
	if rs, _ := svc.ResumoCartoes(ctx, "u-1", "2026-12", "2026-12-05"); rs[0].LimiteUsadoCentavos != 123_000 {
		t.Errorf("no vencimento = %d", rs[0].LimiteUsadoCentavos)
	}
	if rs, _ := svc.ResumoCartoes(ctx, "u-1", "2026-12", "2026-12-06"); rs[0].LimiteUsadoCentavos != 3_000 {
		t.Errorf("depois do vencimento = %d", rs[0].LimiteUsadoCentavos)
	}

	// Fatura de um mês passado continua consultável.
	if rs, _ := svc.ResumoCartoes(ctx, "u-1", "2026-11", "2026-11-16"); rs[0].FaturaCentavos != 10_000 {
		t.Errorf("fatura de novembro = %d", rs[0].FaturaCentavos)
	}

	// Sem cartões: lista vazia. Parâmetros inválidos: 422 nos dois campos.
	if vazio, err := svc.ResumoCartoes(ctx, "u-2", "2026-12", "2026-11-16"); err != nil || len(vazio) != 0 {
		t.Errorf("sem cartões = %+v, %v", vazio, err)
	}
	_, err = svc.ResumoCartoes(ctx, "u-1", "2026-13", "16/11/2026")
	if e := errs.As(err); e == nil || e.Kind != errs.KindValidation || len(e.Fields) != 2 {
		t.Errorf("inválidos = %v", err)
	}
}

func TestResumoMensalPorCartao(t *testing.T) {
	// Cenário "Totais por cartão" da spec painel-gastos.
	svc, _ := montar(t)
	nu := cadastrar(t, svc, "u-1", nubank())
	inter := nubank()
	inter.Apelido = "Inter"
	in := cadastrar(t, svc, "u-1", inter)
	criar(t, svc, "u-1", noCartao("2026-11-03", "alimentacao", gasto.Cartao, 30_000, nu.ID))
	criar(t, svc, "u-1", noCartao("2026-11-04", "lazer", gasto.Cartao, 50_000, nu.ID))
	criar(t, svc, "u-1", noCartao("2026-11-05", "alimentacao", gasto.Cartao, 20_000, in.ID))
	criar(t, svc, "u-1", dados("2026-12-01", "aluguel", gasto.Fixo, 150_000))

	r, err := svc.ResumoMensal(ctx, "u-1", "2026-12")
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalCentavos != 250_000 {
		t.Errorf("total = %d", r.TotalCentavos)
	}
	quero := []gasto.TotalCartao{{CartaoID: "", TotalCentavos: 150_000}, {CartaoID: nu.ID, TotalCentavos: 80_000}, {CartaoID: in.ID, TotalCentavos: 20_000}}
	if fmt.Sprint(r.PorCartao) != fmt.Sprint(quero) {
		t.Errorf("por cartão = %+v", r.PorCartao)
	}
	var alimentacao []gasto.TotalCategoriaCartao
	for _, x := range r.PorCategoriaCartao {
		if x.CategoriaID == "alimentacao" {
			alimentacao = append(alimentacao, x)
		}
	}
	if len(alimentacao) != 2 || alimentacao[0].CartaoID != nu.ID || alimentacao[0].TotalCentavos != 30_000 || alimentacao[1].TotalCentavos != 20_000 {
		t.Errorf("alimentação por cartão = %+v", alimentacao)
	}
}

func TestCartoesErrosDoStore(t *testing.T) {
	svc, st := montar(t)
	c := cadastrar(t, svc, "u-1", nubank())
	st.Err = errs.Internal(errors.New("fora"))
	casos := map[string]error{
		"listar":   func() error { _, err := svc.ListarCartoes(ctx, "u-1"); return err }(),
		"criar":    func() error { _, err := svc.CriarCartao(ctx, "u-1", nubank()); return err }(),
		"arquivar": func() error { _, err := svc.ArquivarCartao(ctx, "u-1", c.ID, true); return err }(),
		"excluir":  svc.ExcluirCartao(ctx, "u-1", c.ID),
		"resumo":   func() error { _, err := svc.ResumoCartoes(ctx, "u-1", "2026-12", "2026-11-16"); return err }(),
		"gasto": func() error {
			_, err := svc.CriarGasto(ctx, "u-1", noCartao("2026-11-03", "l", gasto.Cartao, 1, c.ID))
			return err
		}(),
		"sem userId": func() error { _, err := svc.CriarCartao(ctx, " ", nubank()); return errors.New(codigo(err)) }(),
	}
	for nome, err := range casos {
		if nome == "sem userId" {
			if err.Error() != contract.CodigoUsuarioAusente {
				t.Errorf("sem userId = %v", err)
			}
			continue
		}
		if kind(err) != errs.KindInternal {
			t.Errorf("%s = %v", nome, err)
		}
	}
}
