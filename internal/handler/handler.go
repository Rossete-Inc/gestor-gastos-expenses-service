// Package handler liga os casos de uso aos transportes da go-commons: o
// invoke.Router (chamadas do BFF) e o events.Router (eventos). Os mesmos
// routers servem o cmd/local e, na tarefa 8.5, a Lambda.
package handler

import (
	"context"
	"log/slog"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
)

// Invoke monta o router das operações do contrato.
func Invoke(svc *app.Service, log *slog.Logger) *invoke.Router {
	r := invoke.NewRouter(log)

	r.Handle(contract.OpCriarGasto, invoke.Typed(func(ctx context.Context, userID string, in contract.CriarGastoIn) (contract.Gasto, error) {
		g, err := svc.CriarGasto(ctx, userID, gasto.DadosDe(in))
		return g.Contract(), err
	}))

	r.Handle(contract.OpObterGasto, invoke.Typed(func(ctx context.Context, userID string, in contract.ObterGastoIn) (contract.Gasto, error) {
		g, err := svc.ObterGasto(ctx, userID, in.ID)
		return g.Contract(), err
	}))

	r.Handle(contract.OpAtualizarGasto, invoke.Typed(func(ctx context.Context, userID string, in contract.AtualizarGastoIn) (contract.Gasto, error) {
		g, err := svc.AtualizarGasto(ctx, userID, in.ID, gasto.DadosDe(in.GastoIn))
		return g.Contract(), err
	}))

	r.Handle(contract.OpExcluirGasto, invoke.Typed(func(ctx context.Context, userID string, in contract.ExcluirGastoIn) (any, error) {
		return nil, svc.ExcluirGasto(ctx, userID, in.ID)
	}))

	r.Handle(contract.OpListarGastos, invoke.Typed(func(ctx context.Context, userID string, in contract.ListarGastosIn) (contract.PaginaGastos, error) {
		pg, err := svc.ListarGastos(ctx, userID, app.Listagem{
			Mes: in.Mes, Grupo: in.Grupo, CategoriaID: in.CategoriaID, Cursor: in.Cursor, Limit: in.Limit,
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
		res, err := svc.ResumoMensal(ctx, userID, in.Mes)
		if err != nil {
			return contract.ResumoMensalOut{}, err
		}
		return res.Contract(), nil
	}))

	return r
}

// Events monta o router dos eventos consumidos.
func Events(svc *app.Service, log *slog.Logger) *events.Router {
	r := events.NewRouter(log)
	r.Handle(events.UserDeleted, events.Typed(func(ctx context.Context, _ events.Event, d events.UserDeletedDetail) error {
		return svc.ConsumirUserDeleted(ctx, d)
	}))
	return r
}
