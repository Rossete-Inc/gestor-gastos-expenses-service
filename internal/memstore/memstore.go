// Package memstore é um app.Store em memória com a mesma semântica do store
// DynamoDB, inclusive as chaves de paginação. Só é usado pelos testes
// unitários de app, handler e cmd/local.
package memstore

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/store"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
)

// Store guarda os gastos por usuário e sort key.
type Store struct {
	mu     sync.Mutex
	gastos map[string]map[string]gasto.Gasto // userId -> SK -> gasto
	// Err, se não nil, é devolvido por todas as operações.
	Err error
}

// New cria um Store vazio.
func New() *Store { return &Store{gastos: map[string]map[string]gasto.Gasto{}} }

func sk(id string) string {
	k, _ := store.SK(id)
	return k
}

// Criar implementa app.Store.
func (s *Store) Criar(_ context.Context, g gasto.Gasto) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if s.gastos[g.UserID] == nil {
		s.gastos[g.UserID] = map[string]gasto.Gasto{}
	}
	if _, ok := s.gastos[g.UserID][sk(g.ID)]; ok {
		return errs.Conflict("gasto_duplicado", "ID repetido")
	}
	s.gastos[g.UserID][sk(g.ID)] = g
	return nil
}

// Obter implementa app.Store.
func (s *Store) Obter(_ context.Context, userID, id string) (gasto.Gasto, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return gasto.Gasto{}, s.Err
	}
	g, ok := s.gastos[userID][sk(id)]
	if !ok {
		return gasto.Gasto{}, gasto.ErrNaoEncontrado()
	}
	return g, nil
}

// Substituir implementa app.Store.
func (s *Store) Substituir(_ context.Context, antigo, novo gasto.Gasto) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if _, ok := s.gastos[antigo.UserID][sk(antigo.ID)]; !ok {
		return gasto.ErrNaoEncontrado()
	}
	delete(s.gastos[antigo.UserID], sk(antigo.ID))
	s.gastos[novo.UserID][sk(novo.ID)] = novo
	return nil
}

// Excluir implementa app.Store.
func (s *Store) Excluir(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if _, ok := s.gastos[userID][sk(id)]; !ok {
		return gasto.ErrNaoEncontrado()
	}
	delete(s.gastos[userID], sk(id))
	return nil
}

// Listar implementa app.Store, com as chaves no mesmo formato do Dynamo.
func (s *Store) Listar(_ context.Context, userID string, f gasto.Filtro, inicio page.Key, limite int) ([]gasto.Gasto, page.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, nil, s.Err
	}
	chaves := make([]string, 0, len(s.gastos[userID]))
	for k := range s.gastos[userID] {
		chaves = append(chaves, k)
	}
	slices.Sort(chaves)
	slices.Reverse(chaves)

	itens := []gasto.Gasto{}
	for _, k := range chaves {
		if inicio != nil && k >= inicio["SK"] {
			continue
		}
		g := s.gastos[userID][k]
		if !f.Aceita(g) {
			continue
		}
		if len(itens) == limite {
			ultimo := itens[len(itens)-1]
			return itens, page.Key{"PK": store.PK(userID), "SK": sk(ultimo.ID)}, nil
		}
		itens = append(itens, g)
	}
	return itens, nil, nil
}

// DoMes implementa app.Store, do mais recente para o mais antigo como o
// Dynamo.
func (s *Store) DoMes(_ context.Context, userID, mes string) ([]gasto.Gasto, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	itens := []gasto.Gasto{}
	for _, g := range s.gastos[userID] {
		if g.Mes() == mes {
			itens = append(itens, g)
		}
	}
	slices.SortFunc(itens, func(a, b gasto.Gasto) int { return strings.Compare(sk(b.ID), sk(a.ID)) })
	return itens, nil
}

// ExisteNaCategoria implementa app.Store.
func (s *Store) ExisteNaCategoria(_ context.Context, userID, categoriaID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return false, s.Err
	}
	for _, g := range s.gastos[userID] {
		if g.CategoriaID == categoriaID {
			return true, nil
		}
	}
	return false, nil
}

// ExcluirDoUsuario implementa app.Store.
func (s *Store) ExcluirDoUsuario(_ context.Context, userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return 0, s.Err
	}
	n := len(s.gastos[userID])
	delete(s.gastos, userID)
	return n, nil
}

// Quantos devolve quantos gastos o usuário tem, para os testes.
func (s *Store) Quantos(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.gastos[userID])
}

// IDs devolve os IDs do usuário em ordem decrescente de chave, para os
// testes.
func (s *Store) IDs(userID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, g := range s.gastos[userID] {
		ids = append(ids, g.ID)
	}
	slices.SortFunc(ids, func(a, b string) int { return strings.Compare(sk(b), sk(a)) })
	return ids
}
