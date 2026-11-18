package memstore

import (
	"context"
	"slices"
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/meta"
)

// ObterMeta implementa app.Store.
func (s *Store) ObterMeta(_ context.Context, donoID, categoriaID string) (meta.Meta, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return meta.Meta{}, false, s.Err
	}
	m, ok := s.metas[donoID][categoriaID]
	return copiarMeta(m), ok, nil
}

// ListarMetas implementa app.Store, na ordem da categoria como o Dynamo.
func (s *Store) ListarMetas(_ context.Context, donoID string) ([]meta.Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	lista := []meta.Meta{}
	for _, m := range s.metas[donoID] {
		lista = append(lista, copiarMeta(m))
	}
	slices.SortFunc(lista, func(a, b meta.Meta) int { return strings.Compare(a.CategoriaID, b.CategoriaID) })
	return lista, nil
}

// SalvarMeta implementa app.Store: grava tudo menos os alertas já marcados.
func (s *Store) SalvarMeta(_ context.Context, m meta.Meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if s.metas[m.DonoID] == nil {
		s.metas[m.DonoID] = map[string]meta.Meta{}
	}
	alertas := map[string][]int{}
	if atual, ok := s.metas[m.DonoID][m.CategoriaID]; ok {
		alertas = atual.Alertas
	}
	m = copiarMeta(m)
	m.Alertas = alertas
	s.metas[m.DonoID][m.CategoriaID] = m
	return nil
}

// ExcluirMeta implementa app.Store.
func (s *Store) ExcluirMeta(_ context.Context, donoID, categoriaID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	delete(s.metas[donoID], categoriaID)
	return nil
}

// MarcarAlerta implementa app.Store.
func (s *Store) MarcarAlerta(_ context.Context, donoID, categoriaID, mes string, limiar int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return false, s.Err
	}
	m, ok := s.metas[donoID][categoriaID]
	if !ok || slices.Contains(m.Alertas[mes], limiar) {
		return false, nil
	}
	if m.Alertas == nil {
		m.Alertas = map[string][]int{}
	}
	m.Alertas[mes] = append(slices.Clone(m.Alertas[mes]), limiar)
	s.metas[donoID][categoriaID] = m
	return true, nil
}

// copiarMeta evita que quem chama altere o que está guardado.
func copiarMeta(m meta.Meta) meta.Meta {
	m.Vigencias = slices.Clone(m.Vigencias)
	alertas := make(map[string][]int, len(m.Alertas))
	for mes, ls := range m.Alertas {
		alertas[mes] = slices.Clone(ls)
	}
	m.Alertas = alertas
	return m
}
