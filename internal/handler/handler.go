// Package handler liga os casos de uso aos transportes da go-commons: o
// invoke.Router (chamadas do BFF) e o events.Router (eventos). Os mesmos
// routers servem o cmd/local e, na tarefa 8.5, a Lambda.
package handler

import (
	"context"
	"log/slog"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/cartao"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/meta"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
)

// Invoke monta o router das operações do contrato.
func Invoke(svc *app.Service, log *slog.Logger) *invoke.Router {
	r := invoke.NewRouter(log)

	r.Handle(contract.OpCriarGasto, invoke.Typed(func(ctx context.Context, userID string, in contract.CriarGastoIn) (contract.GastoComAlertas, error) {
		g, alertas, err := svc.LancarComAlertas(ctx, userID, in)
		return comAlertas(g, alertas), err
	}))

	r.Handle(contract.OpObterGasto, invoke.Typed(func(ctx context.Context, userID string, in contract.ObterGastoIn) (contract.Gasto, error) {
		g, err := svc.ObterGasto(ctx, userID, in.ID)
		return g.Contract(), err
	}))

	r.Handle(contract.OpAtualizarGasto, invoke.Typed(func(ctx context.Context, userID string, in contract.AtualizarGastoIn) (contract.GastoComAlertas, error) {
		g, alertas, err := svc.AtualizarGastoComAlertas(ctx, userID, in.ID, gasto.DadosDe(in.GastoIn), in.Escopo)
		return comAlertas(g, alertas), err
	}))

	r.Handle(contract.OpExcluirGasto, invoke.Typed(func(ctx context.Context, userID string, in contract.ExcluirGastoIn) (any, error) {
		return nil, svc.ExcluirGastoNoEscopo(ctx, userID, in.ID, in.Escopo)
	}))

	r.Handle(contract.OpListarGastos, invoke.Typed(func(ctx context.Context, userID string, in contract.ListarGastosIn) (contract.PaginaGastos, error) {
		pg, err := svc.ListarGastos(ctx, userID, app.Listagem{
			Mes: in.Mes, Grupo: in.Grupo, CategoriaID: in.CategoriaID, CartaoID: in.CartaoID,
			AutorID: in.AutorID, Cursor: in.Cursor, Limit: in.Limit,
		})
		if err != nil {
			return contract.PaginaGastos{}, err
		}
		itens := make([]contract.Gasto, 0, len(pg.Items))
		for _, g := range pg.Items {
			itens = append(itens, g.Contract())
		}
		return page.New(itens, pg.NextCursor), nil
	}))

	r.Handle(contract.OpExisteGastoNaCategoria, invoke.Typed(func(ctx context.Context, userID string, in contract.ExisteGastoNaCategoriaIn) (contract.ExisteGastoNaCategoriaOut, error) {
		existe, err := svc.ExisteGastoNaCategoria(ctx, userID, in.CategoriaID)
		return contract.ExisteGastoNaCategoriaOut{Existe: existe}, err
	}))

	r.Handle(contract.OpResumoMensal, invoke.Typed(func(ctx context.Context, userID string, in contract.ResumoMensalIn) (contract.ResumoMensalOut, error) {
		res, metas, err := svc.ResumoMensalComMetas(ctx, userID, in.Mes)
		if err != nil {
			return contract.ResumoMensalOut{}, err
		}
		out := res.Contract()
		out.Metas = metasDoContrato(metas)
		return out, nil
	}))

	r.Handle(contract.OpListarMetas, invoke.Typed(func(ctx context.Context, userID string, in contract.ListarMetasIn) (contract.ListarMetasOut, error) {
		metas, err := svc.ListarMetas(ctx, userID, in.Mes)
		if err != nil {
			return contract.ListarMetasOut{}, err
		}
		return contract.ListarMetasOut{Items: metasDoContrato(metas)}, nil
	}))

	r.Handle(contract.OpDefinirMeta, invoke.Typed(func(ctx context.Context, userID string, in contract.DefinirMetaIn) (contract.MetaDoMes, error) {
		m, err := svc.DefinirMeta(ctx, userID, in)
		return m.Contract(), err
	}))

	r.Handle(contract.OpRemoverMeta, invoke.Typed(func(ctx context.Context, userID string, in contract.RemoverMetaIn) (any, error) {
		return nil, svc.RemoverMeta(ctx, userID, in.CategoriaID, in.Mes)
	}))

	r.Handle(contract.OpExcluirMeta, invoke.Typed(func(ctx context.Context, userID string, in contract.ExcluirMetaIn) (any, error) {
		return nil, svc.ExcluirMeta(ctx, userID, in.CategoriaID)
	}))

	r.Handle(contract.OpListarCartoes, invoke.Typed(func(ctx context.Context, userID string, _ struct{}) (contract.ListarCartoesOut, error) {
		lista, err := svc.ListarCartoes(ctx, userID)
		if err != nil {
			return contract.ListarCartoesOut{}, err
		}
		out := contract.ListarCartoesOut{Items: make([]contract.Cartao, 0, len(lista))}
		for _, c := range lista {
			out.Items = append(out.Items, c.Contract())
		}
		return out, nil
	}))

	r.Handle(contract.OpCriarCartao, invoke.Typed(func(ctx context.Context, userID string, in contract.CriarCartaoIn) (contract.Cartao, error) {
		c, err := svc.CriarCartao(ctx, userID, cartao.DadosDe(in))
		return c.Contract(), err
	}))

	r.Handle(contract.OpAtualizarCartao, invoke.Typed(func(ctx context.Context, userID string, in contract.AtualizarCartaoIn) (contract.Cartao, error) {
		c, err := svc.AtualizarCartao(ctx, userID, in.ID, cartao.DadosDe(in.CartaoIn))
		return c.Contract(), err
	}))

	r.Handle(contract.OpArquivarCartao, invoke.Typed(func(ctx context.Context, userID string, in contract.ArquivarCartaoIn) (contract.Cartao, error) {
		c, err := svc.ArquivarCartao(ctx, userID, in.ID, in.Arquivado)
		return c.Contract(), err
	}))

	r.Handle(contract.OpExcluirCartao, invoke.Typed(func(ctx context.Context, userID string, in contract.ExcluirCartaoIn) (any, error) {
		return nil, svc.ExcluirCartao(ctx, userID, in.ID)
	}))

	r.Handle(contract.OpResumoCartoes, invoke.Typed(func(ctx context.Context, userID string, in contract.ResumoCartoesIn) (contract.ResumoCartoesOut, error) {
		resumos, err := svc.ResumoCartoes(ctx, userID, in.Mes, in.Hoje)
		if err != nil {
			return contract.ResumoCartoesOut{}, err
		}
		out := contract.ResumoCartoesOut{Mes: in.Mes, Items: make([]contract.ResumoCartao, 0, len(resumos))}
		for _, r := range resumos {
			out.Items = append(out.Items, contract.ResumoCartao{
				Cartao:                   r.Cartao.Contract(),
				FaturaCentavos:           r.FaturaCentavos,
				LimiteUsadoCentavos:      r.LimiteUsadoCentavos,
				LimiteDisponivelCentavos: r.LimiteDisponivelCentavos(),
				Vencimento:               r.Vencimento.Format("2006-01-02"),
			})
		}
		return out, nil
	}))

	return r
}

// comAlertas monta a resposta de CriarGasto e AtualizarGasto.
func comAlertas(g gasto.Gasto, alertas []meta.Alerta) contract.GastoComAlertas {
	out := contract.GastoComAlertas{Gasto: g.Contract()}
	for _, a := range alertas {
		out.AlertasDeMeta = append(out.AlertasDeMeta, a.Contract())
	}
	return out
}

// metasDoContrato converte as metas, com a lista sempre presente.
func metasDoContrato(metas []meta.DoMes) []contract.MetaDoMes {
	out := make([]contract.MetaDoMes, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.Contract())
	}
	return out
}

// Events monta o router dos eventos consumidos.
func Events(svc *app.Service, log *slog.Logger) *events.Router {
	r := events.NewRouter(log)
	r.Handle(events.UserDeleted, events.Typed(func(ctx context.Context, _ events.Event, d events.UserDeletedDetail) error {
		return svc.ConsumirUserDeleted(ctx, d)
	}))
	return r
}
