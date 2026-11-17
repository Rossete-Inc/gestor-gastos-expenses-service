package memstore

import (
	"context"
	"slices"
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/serie"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/store"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// CriarSerie implementa app.Store.
func (s *Store) CriarSerie(_ context.Context, se serie.Serie, gastos []gasto.Gasto) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if _, ok := s.series[se.UserID][se.ID]; ok {
		return errs.Conflict(store.CodigoSerieExistente, "Lançamento já registrado.")
	}
	if s.series[se.UserID] == nil {
		s.series[se.UserID] = map[string]serie.Serie{}
	}
	if s.gastos[se.UserID] == nil {
		s.gastos[se.UserID] = map[string]gasto.Gasto{}
	}
	se.Versao = 1
	s.series[se.UserID][se.ID] = se
	for _, g := range gastos {
		s.gastos[g.UserID][sk(g.ID)] = g
	}
	return nil
}

// ObterSerie implementa app.Store.
func (s *Store) ObterSerie(_ context.Context, userID, id string) (serie.Serie, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return serie.Serie{}, s.Err
	}
	se, ok := s.series[userID][id]
	if !ok {
		return serie.Serie{}, serie.ErrNaoEncontrada()
	}
	se.MesesGerados = slices.Clone(se.MesesGerados)
	return se, nil
}

// ListarSeries implementa app.Store, em ordem de ID como a Query.
func (s *Store) ListarSeries(_ context.Context, userID string) ([]serie.Serie, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	lista := []serie.Serie{}
	for _, se := range s.series[userID] {
		se.MesesGerados = slices.Clone(se.MesesGerados)
		lista = append(lista, se)
	}
	slices.SortFunc(lista, func(a, b serie.Serie) int { return strings.Compare(a.ID, b.ID) })
	return lista, nil
}

// GerarOcorrencias implementa app.Store.
func (s *Store) GerarOcorrencias(_ context.Context, se serie.Serie, meses []string, gastos []gasto.Gasto) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	atual, ok := s.series[se.UserID][se.ID]
	if !ok {
		return nil
	}
	for _, m := range meses {
		if slices.Contains(atual.MesesGerados, m) {
			return nil
		}
	}
	atual.MesesGerados = append(slices.Clone(atual.MesesGerados), meses...)
	atual.Versao++
	s.series[se.UserID][se.ID] = atual
	for _, g := range gastos {
		s.gastos[g.UserID][sk(g.ID)] = g
	}
	return nil
}

// DaSerie implementa app.Store.
func (s *Store) DaSerie(_ context.Context, userID, serieID string) ([]gasto.Gasto, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	itens := []gasto.Gasto{}
	for _, g := range s.gastos[userID] {
		if g.Serie.SerieID == serieID {
			itens = append(itens, g)
		}
	}
	slices.SortFunc(itens, func(a, b gasto.Gasto) int { return strings.Compare(sk(b.ID), sk(a.ID)) })
	return itens, nil
}

// Aplicar implementa app.Store, com a mesma conferência de versão do Dynamo.
func (s *Store) Aplicar(_ context.Context, m serie.Mudancas) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	for _, se := range m.Series {
		atual, existe := s.series[m.UserID][se.ID]
		if (se.Versao == 0 && existe) || (se.Versao != 0 && (!existe || atual.Versao != se.Versao)) {
			return errs.Conflict(store.CodigoSerieAlterada, "A série mudou enquanto era alterada. Tente de novo.")
		}
	}
	if s.series[m.UserID] == nil {
		s.series[m.UserID] = map[string]serie.Serie{}
	}
	for _, se := range m.Series {
		se.Versao++
		s.series[m.UserID][se.ID] = se
	}
	for _, id := range m.SeriesExcluidas {
		delete(s.series[m.UserID], id)
	}
	for _, g := range m.Excluir {
		delete(s.gastos[m.UserID], sk(g.ID))
	}
	for _, g := range m.Gravar {
		s.gastos[m.UserID][sk(g.ID)] = g
	}
	return nil
}

// Series devolve quantas séries o usuário tem, para os testes.
func (s *Store) Series(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.series[userID])
}
