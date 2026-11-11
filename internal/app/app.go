// Package app tem os casos de uso do expenses-service. Não conhece transporte
// (invoke, eventos) nem banco: recebe um Store.
package app

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
)

// Store persiste gastos. Toda operação recebe o userId e só enxerga a
// partição dele: o gasto de outro usuário simplesmente não é encontrado.
type Store interface {
	// Criar grava um gasto novo.
	Criar(ctx context.Context, g gasto.Gasto) error
	// Obter devolve o gasto id do usuário, ou
	// errs.NotFound("gasto_nao_encontrado").
	Obter(ctx context.Context, userID, id string) (gasto.Gasto, error)
	// Substituir troca o gasto antigo pelo novo, que pode ter outro ID (data
	// alterada). Se o antigo já não existir, devolve
	// errs.NotFound("gasto_nao_encontrado") e não grava nada.
	Substituir(ctx context.Context, antigo, novo gasto.Gasto) error
	// Excluir apaga o gasto id do usuário, ou devolve
	// errs.NotFound("gasto_nao_encontrado") se ele não existir.
	Excluir(ctx context.Context, userID, id string) error
	// Listar devolve até limite gastos do usuário aceitos pelo filtro, do mais
	// recente para o mais antigo, a partir da chave inicio (nil = do começo).
	// proximo é a chave para continuar, ou nil quando não há mais gastos.
	Listar(ctx context.Context, userID string, f gasto.Filtro, inicio page.Key, limite int) (itens []gasto.Gasto, proximo page.Key, err error)
	// DoMes devolve todos os gastos do usuário no mês (AAAA-MM), do mais
	// recente para o mais antigo. Mês sem gastos: lista vazia, sem erro.
	DoMes(ctx context.Context, userID, mes string) ([]gasto.Gasto, error)
	// ExisteNaCategoria informa se o usuário tem algum gasto na categoria.
	ExisteNaCategoria(ctx context.Context, userID, categoriaID string) (bool, error)
	// ExcluirDoUsuario apaga todos os gastos do usuário e devolve quantos
	// apagou. Sem nada para apagar, devolve 0 sem erro.
	ExcluirDoUsuario(ctx context.Context, userID string) (int, error)
}

// Service implementa os casos de uso.
type Service struct {
	store    Store
	cursores *page.Codec
	log      *slog.Logger
	agora    func() time.Time
}

// New cria o Service. cursores assina os cursores da listagem; log nil usa
// slog.Default().
func New(store Store, cursores *page.Codec, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, cursores: cursores, log: log, agora: time.Now}
}

// CriarGasto lança um gasto. A categoria já foi validada pelo BFF, que manda
// o grupo dela.
func (s *Service) CriarGasto(ctx context.Context, userID string, d gasto.Dados) (gasto.Gasto, error) {
	agora := s.agora()
	g, err := gasto.Novo(gasto.NovoULID(agora), userID, d, agora)
	if err != nil {
		return gasto.Gasto{}, err
	}
	if err := s.store.Criar(ctx, g); err != nil {
		return gasto.Gasto{}, err
	}
	s.log.InfoContext(ctx, "gasto criado", "userId", userID, "gastoId", g.ID, "grupo", g.Grupo)
	return g, nil
}

// ObterGasto devolve um gasto do usuário.
func (s *Service) ObterGasto(ctx context.Context, userID, id string) (gasto.Gasto, error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return gasto.Gasto{}, err
	}
	id = strings.TrimSpace(id)
	// Um ID fora do formato nunca existe e nem chega ao banco.
	if _, _, ok := gasto.PartesDoID(id); !ok {
		return gasto.Gasto{}, gasto.ErrNaoEncontrado()
	}
	g, err := s.store.Obter(ctx, userID, id)
	if err != nil {
		return gasto.Gasto{}, err
	}
	// O store já filtra pela partition key; a checagem aqui é defesa extra.
	if g.UserID != userID {
		return gasto.Gasto{}, gasto.ErrNaoEncontrado()
	}
	return g, nil
}

// AtualizarGasto substitui os dados de um gasto do usuário, com as mesmas
// validações do lançamento. Se a data mudar, o gasto devolvido tem outro ID.
func (s *Service) AtualizarGasto(ctx context.Context, userID, id string, d gasto.Dados) (gasto.Gasto, error) {
	antigo, err := s.ObterGasto(ctx, userID, id)
	if err != nil {
		return gasto.Gasto{}, err
	}
	novo, err := antigo.Atualizado(d, s.agora())
	if err != nil {
		return gasto.Gasto{}, err
	}
	if err := s.store.Substituir(ctx, antigo, novo); err != nil {
		return gasto.Gasto{}, err
	}
	s.log.InfoContext(ctx, "gasto atualizado", "userId", userID, "gastoId", novo.ID, "idAnterior", antigo.ID, "grupo", novo.Grupo)
	return novo, nil
}

// ExcluirGasto exclui um gasto do usuário.
func (s *Service) ExcluirGasto(ctx context.Context, userID, id string) error {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if _, _, ok := gasto.PartesDoID(id); !ok {
		return gasto.ErrNaoEncontrado()
	}
	if err := s.store.Excluir(ctx, userID, id); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "gasto excluído", "userId", userID, "gastoId", id)
	return nil
}

// Listagem são os parâmetros de ListarGastos.
type Listagem struct {
	Mes         string
	Grupo       string
	CategoriaID string
	Cursor      string
	Limit       int
}

// ListarGastos devolve uma página de gastos do mês, do mais recente para o
// mais antigo. O cursor é assinado com o escopo da listagem (usuário, mês e
// filtros): um cursor adulterado ou de outra listagem responde 400
// "cursor_invalido".
func (s *Service) ListarGastos(ctx context.Context, userID string, in Listagem) (page.Page[gasto.Gasto], error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return page.Page[gasto.Gasto]{}, err
	}
	f, err := gasto.NovoFiltro(in.Mes, in.Grupo, in.CategoriaID)
	if err != nil {
		return page.Page[gasto.Gasto]{}, err
	}
	escopo := f.Escopo(userID)
	inicio, err := s.cursores.Decode(escopo, in.Cursor)
	if err != nil {
		return page.Page[gasto.Gasto]{}, err
	}
	itens, proximo, err := s.store.Listar(ctx, userID, f, inicio, page.Limit(in.Limit))
	if err != nil {
		return page.Page[gasto.Gasto]{}, err
	}
	cursor, err := s.cursores.Encode(escopo, proximo)
	if err != nil {
		return page.Page[gasto.Gasto]{}, errs.Internal(err)
	}
	return page.New(itens, cursor), nil
}

// ResumoMensal soma os gastos do mês para o painel: total, totais por grupo
// e por categoria e os 5 mais recentes. Lê o mês inteiro numa Query e soma
// em memória (design.md).
func (s *Service) ResumoMensal(ctx context.Context, userID, mes string) (gasto.Resumo, error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return gasto.Resumo{}, err
	}
	mes = strings.TrimSpace(mes)
	if !gasto.MesValido(mes) {
		return gasto.Resumo{}, errs.Validation("Mês inválido.", errs.FieldError{Field: "mes", Message: "deve ser um mês válido no formato AAAA-MM"})
	}
	gastos, err := s.store.DoMes(ctx, userID, mes)
	if err != nil {
		return gasto.Resumo{}, err
	}
	// O store já filtra pela partition key; descartar gasto alheio aqui é
	// defesa extra, como em ObterGasto.
	gastos = slices.DeleteFunc(gastos, func(g gasto.Gasto) bool { return g.UserID != userID })
	return gasto.Resumir(mes, gastos), nil
}

// ExisteGastoNaCategoria informa se o usuário tem algum gasto na categoria.
func (s *Service) ExisteGastoNaCategoria(ctx context.Context, userID, categoriaID string) (bool, error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return false, err
	}
	categoriaID = strings.TrimSpace(categoriaID)
	if categoriaID == "" || len(categoriaID) > gasto.MaxCategoriaID {
		return false, errs.Validation("Categoria inválida.", errs.FieldError{Field: "categoriaId", Message: "obrigatório, com no máximo 64 caracteres"})
	}
	return s.store.ExisteNaCategoria(ctx, userID, categoriaID)
}

// ConsumirUserDeleted apaga todos os gastos do usuário excluído. É
// idempotente: o mesmo evento de novo não encontra nada e não falha.
func (s *Service) ConsumirUserDeleted(ctx context.Context, d events.UserDeletedDetail) error {
	userID := strings.TrimSpace(d.UserID)
	if userID == "" {
		return errs.Validation("Evento UserDeleted sem userId.", errs.FieldError{Field: "userId", Message: "obrigatório"})
	}
	n, err := s.store.ExcluirDoUsuario(ctx, userID)
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "gastos do usuário excluído apagados", "userId", userID, "quantidade", n)
	return nil
}
