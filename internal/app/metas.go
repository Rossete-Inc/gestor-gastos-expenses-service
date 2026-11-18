package app

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/meta"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// maxNomeCategoria é o tamanho máximo do nome de categoria (o mesmo do
// categories-service).
const maxNomeCategoria = 40

// ListarMetas devolve as metas que valem no mês, com o total gasto em cada
// categoria. Como o painel, cria antes as ocorrências recorrentes do mês.
func (s *Service) ListarMetas(ctx context.Context, donoID, mes string) ([]meta.DoMes, error) {
	if err := gasto.ExigirUsuario(donoID); err != nil {
		return nil, err
	}
	mes = strings.TrimSpace(mes)
	if err := meta.ValidarMes("mes", mes); err != nil {
		return nil, err
	}
	if err := s.gerarRecorrencias(ctx, donoID, mes); err != nil {
		return nil, err
	}
	gastos, err := s.store.DoMes(ctx, donoID, mes)
	if err != nil {
		return nil, err
	}
	return s.metasDoMes(ctx, donoID, mes, gastos)
}

// metasDoMes combina as metas que valem no mês com os gastos já lidos dele.
func (s *Service) metasDoMes(ctx context.Context, donoID, mes string, gastos []gasto.Gasto) ([]meta.DoMes, error) {
	metas, err := s.store.ListarMetas(ctx, donoID)
	if err != nil {
		return nil, err
	}
	out := []meta.DoMes{}
	for _, m := range metas {
		valor, ok := m.ValorNoMes(mes)
		if !ok {
			continue
		}
		out = append(out, meta.DoMes{
			Meta: m, Mes: mes, ValorCentavos: valor, TotalCentavos: meta.TotalDaCategoria(gastos, m.CategoriaID),
		})
	}
	return out, nil
}

// DefinirMeta define ou altera a meta da categoria do mês in.Mes em diante
// (spec metas-gastos, "Definir meta mensal por categoria"). A categoria já
// foi validada pelo BFF, que manda o nome e o grupo dela.
func (s *Service) DefinirMeta(ctx context.Context, donoID string, in contract.DefinirMetaIn) (meta.DoMes, error) {
	if err := gasto.ExigirUsuario(donoID); err != nil {
		return meta.DoMes{}, err
	}
	in.CategoriaID = strings.TrimSpace(in.CategoriaID)
	in.NomeCategoria = strings.TrimSpace(in.NomeCategoria)
	in.Mes = strings.TrimSpace(in.Mes)
	if err := validarDefinicao(in); err != nil {
		return meta.DoMes{}, err
	}

	m, existe, err := s.store.ObterMeta(ctx, donoID, in.CategoriaID)
	if err != nil {
		return meta.DoMes{}, err
	}
	if !existe {
		m = meta.Meta{DonoID: donoID, CategoriaID: in.CategoriaID}
	}
	m.NomeCategoria, m.Grupo = in.NomeCategoria, in.Grupo
	m = m.Definida(in.Mes, in.ValorCentavos)
	if err := s.store.SalvarMeta(ctx, m); err != nil {
		return meta.DoMes{}, err
	}

	gastos, err := s.store.DoMes(ctx, donoID, in.Mes)
	if err != nil {
		return meta.DoMes{}, err
	}
	return meta.DoMes{
		Meta: m, Mes: in.Mes, ValorCentavos: in.ValorCentavos, TotalCentavos: meta.TotalDaCategoria(gastos, in.CategoriaID),
	}, nil
}

func validarDefinicao(in contract.DefinirMetaIn) error {
	var campos []errs.FieldError
	add := func(campo, msg string) { campos = append(campos, errs.FieldError{Field: campo, Message: msg}) }
	if in.CategoriaID == "" || len(in.CategoriaID) > gasto.MaxCategoriaID {
		add("categoriaId", "obrigatório, com no máximo 64 caracteres")
	}
	if in.NomeCategoria == "" || utf8.RuneCountInString(in.NomeCategoria) > maxNomeCategoria {
		add("nomeCategoria", "obrigatório, com no máximo 40 caracteres")
	}
	if !gasto.Grupo(in.Grupo).Valido() {
		add("grupo", "deve ser CARTAO ou FIXO")
	}
	if !gasto.MesValido(in.Mes) {
		add("mes", "deve estar no formato AAAA-MM")
	}
	if e := errs.As(meta.ValidarValor(in.ValorCentavos)); e != nil {
		campos = append(campos, e.Fields...)
	}
	if len(campos) > 0 {
		return errs.Validation("Dados da meta inválidos.", campos...)
	}
	return nil
}

// RemoverMeta tira a meta da categoria do mês em diante; os meses
// anteriores continuam com a meta que valia neles (spec, "Remover a meta").
func (s *Service) RemoverMeta(ctx context.Context, donoID, categoriaID, mes string) error {
	if err := gasto.ExigirUsuario(donoID); err != nil {
		return err
	}
	mes = strings.TrimSpace(mes)
	if err := meta.ValidarMes("mes", mes); err != nil {
		return err
	}
	m, existe, err := s.store.ObterMeta(ctx, donoID, strings.TrimSpace(categoriaID))
	if err != nil {
		return err
	}
	if _, vale := m.ValorNoMes(mes); !existe || !vale {
		return meta.ErrNaoEncontrada()
	}
	return s.store.SalvarMeta(ctx, m.Removida(mes))
}

// ExcluirMeta apaga a meta inteira, com o histórico: a categoria foi
// excluída (spec categorias, "Excluir categoria remove a meta").
func (s *Service) ExcluirMeta(ctx context.Context, donoID, categoriaID string) error {
	if err := gasto.ExigirUsuario(donoID); err != nil {
		return err
	}
	categoriaID = strings.TrimSpace(categoriaID)
	if categoriaID == "" {
		return errs.Validation("Categoria inválida.", errs.FieldError{Field: "categoriaId", Message: "obrigatório"})
	}
	return s.store.ExcluirMeta(ctx, donoID, categoriaID)
}

// parMeta é uma categoria num mês de referência.
type parMeta struct{ categoriaID, mes string }

// alertasDaEscrita avalia as metas das categorias e meses que uma escrita
// aumentou: compara o total antes e depois dela, marca os limiares cruzados
// (cada um uma vez por mês, com update condicional) e devolve um alerta por
// meta e mês, com o maior limiar cruzado. O gasto já foi gravado: uma falha
// aqui só é registrada em log e não desfaz a escrita.
func (s *Service) alertasDaEscrita(ctx context.Context, donoID, autorID string, antigos, novos []gasto.Gasto) []meta.Alerta {
	delta := map[parMeta]int64{}
	for _, g := range novos {
		delta[parMeta{g.CategoriaID, g.Mes()}] += g.ValorCentavos
	}
	for _, g := range antigos {
		p := parMeta{g.CategoriaID, g.Mes()}
		if _, mudou := delta[p]; mudou {
			delta[p] -= g.ValorCentavos
		}
	}
	pares := make([]parMeta, 0, len(delta))
	for p, d := range delta {
		if d > 0 {
			pares = append(pares, p)
		}
	}
	slices.SortFunc(pares, func(a, b parMeta) int {
		return cmp.Or(strings.Compare(a.mes, b.mes), strings.Compare(a.categoriaID, b.categoriaID))
	})

	var alertas []meta.Alerta
	metas := map[string]*meta.Meta{} // nil = categoria sem meta
	gastosDoMes := map[string][]gasto.Gasto{}
	for _, p := range pares {
		a, err := s.avaliarMeta(ctx, donoID, p, delta[p], metas, gastosDoMes)
		if err != nil {
			s.log.WarnContext(ctx, "falha ao avaliar meta; o gasto foi gravado", "userId", donoID, "categoriaId", p.categoriaID, "mes", p.mes, "erro", err)
			continue
		}
		if a != nil {
			alertas = append(alertas, *a)
		}
	}
	return alertas
}

func (s *Service) avaliarMeta(ctx context.Context, donoID string, p parMeta, delta int64, metas map[string]*meta.Meta, gastosDoMes map[string][]gasto.Gasto) (*meta.Alerta, error) {
	m, lida := metas[p.categoriaID]
	if !lida {
		obtida, existe, err := s.store.ObterMeta(ctx, donoID, p.categoriaID)
		if err != nil {
			return nil, err
		}
		if existe {
			m = &obtida
		}
		metas[p.categoriaID] = m
	}
	if m == nil {
		return nil, nil
	}
	valor, vale := m.ValorNoMes(p.mes)
	if !vale {
		return nil, nil
	}
	gastos, lidos := gastosDoMes[p.mes]
	if !lidos {
		var err error
		if gastos, err = s.store.DoMes(ctx, donoID, p.mes); err != nil {
			return nil, err
		}
		gastosDoMes[p.mes] = gastos
	}
	depois := meta.TotalDaCategoria(gastos, p.categoriaID)
	var marcados []int
	for _, l := range meta.LimiaresCruzados(depois-delta, depois, valor, m.Alertas[p.mes]) {
		ok, err := s.store.MarcarAlerta(ctx, donoID, p.categoriaID, p.mes, l)
		if err != nil {
			return nil, err
		}
		if ok { // false: outra escrita simultânea já notificou este limiar
			marcados = append(marcados, l)
		}
	}
	limiar, ok := meta.ParaAvisar(marcados)
	if !ok {
		return nil, nil
	}
	return &meta.Alerta{
		CategoriaID: p.categoriaID, NomeCategoria: m.NomeCategoria, Mes: p.mes,
		Limiar: limiar, TotalCentavos: depois, MetaCentavos: valor,
	}, nil
}
