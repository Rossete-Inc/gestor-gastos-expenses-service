package memstore

import (
	"context"
	"slices"
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/cartao"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// CriarCartao implementa app.Store.
func (s *Store) CriarCartao(_ context.Context, c cartao.Cartao) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if s.cartoes[c.UserID] == nil {
		s.cartoes[c.UserID] = map[string]cartao.Cartao{}
	}
	if _, ok := s.cartoes[c.UserID][c.ID]; ok {
		return errs.Conflict("cartao_duplicado", "ID repetido")
	}
	s.cartoes[c.UserID][c.ID] = c
	return nil
}

// ObterCartao implementa app.Store.
func (s *Store) ObterCartao(_ context.Context, userID, id string) (cartao.Cartao, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return cartao.Cartao{}, s.Err
	}
	c, ok := s.cartoes[userID][id]
	if !ok {
		return cartao.Cartao{}, cartao.ErrNaoEncontrado()
	}
	return c, nil
}

// ListarCartoes implementa app.Store, na ordem do ID como o Dynamo.
func (s *Store) ListarCartoes(_ context.Context, userID string) ([]cartao.Cartao, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	lista := []cartao.Cartao{}
	for _, c := range s.cartoes[userID] {
		lista = append(lista, c)
	}
	slices.SortFunc(lista, func(a, b cartao.Cartao) int { return strings.Compare(a.ID, b.ID) })
	return lista, nil
}

// SubstituirCartao implementa app.Store.
func (s *Store) SubstituirCartao(_ context.Context, c cartao.Cartao) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if _, ok := s.cartoes[c.UserID][c.ID]; !ok {
		return cartao.ErrNaoEncontrado()
	}
	s.cartoes[c.UserID][c.ID] = c
	return nil
}

// ExcluirCartao implementa app.Store.
func (s *Store) ExcluirCartao(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if _, ok := s.cartoes[userID][id]; !ok {
		return cartao.ErrNaoEncontrado()
	}
	delete(s.cartoes[userID], id)
	return nil
}

// ExisteNoCartao implementa app.Store.
func (s *Store) ExisteNoCartao(_ context.Context, userID, cartaoID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return false, s.Err
	}
	for _, g := range s.gastos[userID] {
		if g.CartaoID == cartaoID {
			return true, nil
		}
	}
	return false, nil
}

// DesdeMes implementa app.Store, do mais recente para o mais antigo.
func (s *Store) DesdeMes(_ context.Context, userID, mes string) ([]gasto.Gasto, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	itens := []gasto.Gasto{}
	for _, g := range s.gastos[userID] {
		if g.Mes() >= mes {
			itens = append(itens, g)
		}
	}
	slices.SortFunc(itens, func(a, b gasto.Gasto) int { return strings.Compare(sk(b.ID), sk(a.ID)) })
	return itens, nil
}
