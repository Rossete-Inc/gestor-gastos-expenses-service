package handler

import (
	"encoding/json"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

func TestCartoesPeloContrato(t *testing.T) {
	c, _, _, _ := montar(t)
	in := contract.CriarCartaoIn{Apelido: "Nubank", Cor: "#8a05be", DiaFechamento: 25, DiaVencimento: 5, LimiteCentavos: 500_000}

	var k contract.Cartao
	if err := c.Call(ctx, contract.OpCriarCartao, "u-1", in, &k); err != nil {
		t.Fatal(err)
	}
	if k.ID == "" || k.Cor != "#8A05BE" || k.Arquivado {
		t.Fatalf("cartão = %+v", k)
	}

	var lista contract.ListarCartoesOut
	if err := c.Call(ctx, contract.OpListarCartoes, "u-1", struct{}{}, &lista); err != nil || len(lista.Items) != 1 || lista.Items[0] != k {
		t.Fatalf("lista = %+v, %v", lista, err)
	}

	gIn := almoco() // 2026-11-03
	gIn.CartaoID = k.ID
	var g contract.Gasto
	if err := c.Call(ctx, contract.OpCriarGasto, "u-1", gIn, &g); err != nil {
		t.Fatal(err)
	}
	if g.CartaoID != k.ID || g.MesReferencia != "2026-12" || g.Data != "2026-11-03" {
		t.Errorf("gasto = %+v", g)
	}

	var pg contract.PaginaGastos
	if err := c.Call(ctx, contract.OpListarGastos, "u-1", contract.ListarGastosIn{Mes: "2026-12", CartaoID: contract.SemCartao}, &pg); err != nil || len(pg.Items) != 0 {
		t.Errorf("sem cartão = %+v, %v", pg, err)
	}

	var resumo contract.ResumoCartoesOut
	if err := c.Call(ctx, contract.OpResumoCartoes, "u-1", contract.ResumoCartoesIn{Mes: "2026-12", Hoje: "2026-11-16"}, &resumo); err != nil {
		t.Fatal(err)
	}
	if len(resumo.Items) != 1 || resumo.Items[0].FaturaCentavos != 4590 || resumo.Items[0].LimiteDisponivelCentavos != 500_000-4590 || resumo.Items[0].Vencimento != "2026-12-05" {
		t.Errorf("resumo = %+v", resumo)
	}

	err := c.Call(ctx, contract.OpExcluirCartao, "u-1", contract.ExcluirCartaoIn{ID: k.ID}, nil)
	if e := errs.As(err); e == nil || e.Kind != errs.KindConflict || e.Code != contract.CodigoCartaoEmUso {
		t.Errorf("excluir em uso = %v", err)
	}

	var arquivado contract.Cartao
	if err := c.Call(ctx, contract.OpArquivarCartao, "u-1", contract.ArquivarCartaoIn{ID: k.ID, Arquivado: true}, &arquivado); err != nil || !arquivado.Arquivado {
		t.Errorf("arquivar = %+v, %v", arquivado, err)
	}

	in.DiaFechamento = 20
	var editado contract.Cartao
	if err := c.Call(ctx, contract.OpAtualizarCartao, "u-1", contract.AtualizarCartaoIn{ID: k.ID, CartaoIn: in}, &editado); err != nil || editado.DiaFechamento != 20 {
		t.Errorf("editar = %+v, %v", editado, err)
	}

	err = c.Call(ctx, contract.OpAtualizarCartao, "u-2", contract.AtualizarCartaoIn{ID: k.ID, CartaoIn: in}, nil)
	if e := errs.As(err); e == nil || e.Kind != errs.KindNotFound || e.Code != contract.CodigoCartaoNaoEncontrado {
		t.Errorf("editar alheio = %v", err)
	}
}

func TestCartaoFormatoJSON(t *testing.T) {
	b, _ := json.Marshal(contract.Cartao{ID: "k", Apelido: "Nubank", Cor: "#8A05BE", DiaFechamento: 25, DiaVencimento: 5, LimiteCentavos: 1})
	quero := `{"id":"k","apelido":"Nubank","cor":"#8A05BE","diaFechamento":25,"diaVencimento":5,"limiteCentavos":1,"arquivado":false}`
	if string(b) != quero {
		t.Errorf("JSON = %s", b)
	}
}
