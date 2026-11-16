package app

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/cartao"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Casos de uso dos cartões (spec cartoes-credito). O apelido único entre os
// ativos e o limite de 20 ativos são conferidos lendo a lista antes de
// gravar; uma corrida entre dois cadastros simultâneos do mesmo usuário pode
// passar, o que é aceito para o volume pessoal.

// comMesDaFatura confere o cartão do gasto e preenche o mês de referência
// (spec lancamento-gastos, "Lançar gasto"). Sem cartão, deixa o mês vazio,
// e o domínio usa o mês da data. cartaoAtual é o cartão do gasto que está
// sendo editado: mantê-lo vale mesmo arquivado.
func (s *Service) comMesDaFatura(ctx context.Context, userID string, d gasto.Dados, cartaoAtual string) (gasto.Dados, error) {
	d.CartaoID = strings.TrimSpace(d.CartaoID)
	d.MesReferencia = ""
	if d.CartaoID == "" {
		return d, nil
	}
	invalido := func(msg string) error {
		return errs.Validation("Dados do gasto inválidos.", errs.FieldError{Field: "cartaoId", Message: msg})
	}
	if !gasto.ULIDValido(d.CartaoID) {
		return gasto.Dados{}, invalido("cartão inexistente")
	}
	c, err := s.store.ObterCartao(ctx, userID, d.CartaoID)
	if errs.KindOf(err) == errs.KindNotFound {
		return gasto.Dados{}, invalido("cartão inexistente")
	}
	if err != nil {
		return gasto.Dados{}, err
	}
	if c.Arquivado && c.ID != cartaoAtual {
		return gasto.Dados{}, invalido("cartão arquivado")
	}
	// Data inválida dá mês vazio; o domínio recusa a data com o erro dela.
	d.MesReferencia = c.MesDaFatura(strings.TrimSpace(d.Data))
	return d, nil
}

// ListarCartoes devolve os cartões do usuário, ativos e arquivados, em ordem
// de apelido.
func (s *Service) ListarCartoes(ctx context.Context, userID string) ([]cartao.Cartao, error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return nil, err
	}
	lista, err := s.store.ListarCartoes(ctx, userID)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(lista, func(a, b cartao.Cartao) int {
		return cmp.Or(
			strings.Compare(cartao.ApelidoNormalizado(a.Apelido), cartao.ApelidoNormalizado(b.Apelido)),
			strings.Compare(a.ID, b.ID),
		)
	})
	return lista, nil
}

// conferirAtivo aplica as regras de um cartão que fica ativo (cadastro,
// edição e reativação): apelido único entre os outros ativos e, se ele
// ainda não era ativo, o limite de cartões ativos.
func (s *Service) conferirAtivo(ctx context.Context, c cartao.Cartao, jaAtivo bool) error {
	lista, err := s.store.ListarCartoes(ctx, c.UserID)
	if err != nil {
		return err
	}
	ativos := 0
	apelido := cartao.ApelidoNormalizado(c.Apelido)
	for _, outro := range lista {
		if outro.Arquivado || outro.ID == c.ID {
			continue
		}
		ativos++
		if cartao.ApelidoNormalizado(outro.Apelido) == apelido {
			return cartao.ErrApelidoDuplicado()
		}
	}
	if !jaAtivo && ativos >= cartao.MaxAtivos {
		return cartao.ErrLimiteDeCartoes()
	}
	return nil
}

// CriarCartao cadastra um cartão (spec, "Cadastro de cartão").
func (s *Service) CriarCartao(ctx context.Context, userID string, d cartao.Dados) (cartao.Cartao, error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return cartao.Cartao{}, err
	}
	agora := s.agora()
	c, err := cartao.Novo(gasto.NovoULID(agora), userID, d, agora)
	if err != nil {
		return cartao.Cartao{}, err
	}
	if err := s.conferirAtivo(ctx, c, false); err != nil {
		return cartao.Cartao{}, err
	}
	if err := s.store.CriarCartao(ctx, c); err != nil {
		return cartao.Cartao{}, err
	}
	s.log.InfoContext(ctx, "cartão criado", "userId", userID, "cartaoId", c.ID)
	return c, nil
}

// obterCartao devolve um cartão do usuário; ID fora do formato é 404.
func (s *Service) obterCartao(ctx context.Context, userID, id string) (cartao.Cartao, error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return cartao.Cartao{}, err
	}
	id = strings.TrimSpace(id)
	if !gasto.ULIDValido(id) {
		return cartao.Cartao{}, cartao.ErrNaoEncontrado()
	}
	c, err := s.store.ObterCartao(ctx, userID, id)
	if err != nil {
		return cartao.Cartao{}, err
	}
	if c.UserID != userID {
		return cartao.Cartao{}, cartao.ErrNaoEncontrado()
	}
	return c, nil
}

// AtualizarCartao edita um cartão (spec, "Edição de cartão"). Os gastos já
// lançados mantêm o mês de referência.
func (s *Service) AtualizarCartao(ctx context.Context, userID, id string, d cartao.Dados) (cartao.Cartao, error) {
	atual, err := s.obterCartao(ctx, userID, id)
	if err != nil {
		return cartao.Cartao{}, err
	}
	novo, err := atual.Atualizado(d, s.agora())
	if err != nil {
		return cartao.Cartao{}, err
	}
	if !novo.Arquivado {
		if err := s.conferirAtivo(ctx, novo, true); err != nil {
			return cartao.Cartao{}, err
		}
	}
	if err := s.store.SubstituirCartao(ctx, novo); err != nil {
		return cartao.Cartao{}, err
	}
	s.log.InfoContext(ctx, "cartão atualizado", "userId", userID, "cartaoId", novo.ID)
	return novo, nil
}

// ArquivarCartao arquiva ou reativa um cartão (spec, "Arquivar e excluir
// cartão"). Reativar confere o apelido e o limite de ativos.
func (s *Service) ArquivarCartao(ctx context.Context, userID, id string, arquivado bool) (cartao.Cartao, error) {
	c, err := s.obterCartao(ctx, userID, id)
	if err != nil {
		return cartao.Cartao{}, err
	}
	if c.Arquivado == arquivado {
		return c, nil
	}
	if !arquivado {
		if err := s.conferirAtivo(ctx, c, false); err != nil {
			return cartao.Cartao{}, err
		}
	}
	c.Arquivado = arquivado
	c.AtualizadoEm = s.agora().UTC().Truncate(time.Millisecond)
	if err := s.store.SubstituirCartao(ctx, c); err != nil {
		return cartao.Cartao{}, err
	}
	s.log.InfoContext(ctx, "cartão arquivado", "userId", userID, "cartaoId", c.ID, "arquivado", arquivado)
	return c, nil
}

// ExcluirCartao exclui um cartão sem gastos; com gastos, 409
// "cartao_em_uso". A corrida entre conferir e excluir é aceita, como na
// exclusão de categoria: um gasto órfão continua nos totais como "Sem
// cartão" no BFF.
func (s *Service) ExcluirCartao(ctx context.Context, userID, id string) error {
	c, err := s.obterCartao(ctx, userID, id)
	if err != nil {
		return err
	}
	emUso, err := s.store.ExisteNoCartao(ctx, userID, c.ID)
	if err != nil {
		return err
	}
	if emUso {
		return cartao.ErrEmUso()
	}
	if err := s.store.ExcluirCartao(ctx, userID, c.ID); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "cartão excluído", "userId", userID, "cartaoId", c.ID)
	return nil
}

// ResumoCartao é a fatura do mês e o limite de um cartão.
type ResumoCartao struct {
	Cartao         cartao.Cartao
	FaturaCentavos int64
	// LimiteUsadoCentavos soma os gastos do cartão em faturas que ainda não
	// venceram, inclusive as futuras (parcelas).
	LimiteUsadoCentavos int64
	// Vencimento da fatura do mês pedido.
	Vencimento time.Time
}

// LimiteDisponivelCentavos é o limite menos o usado; pode ser negativo.
func (r ResumoCartao) LimiteDisponivelCentavos() int64 {
	return r.Cartao.LimiteCentavos - r.LimiteUsadoCentavos
}

// ResumoCartoes devolve, para cada cartão, a fatura do mês e o limite usado
// na data hoje (spec, "Limite disponível" e "Visão por cartão"). Uma fatura
// conta no limite até o dia do vencimento, inclusive; o sistema não controla
// pagamentos. Lê numa Query os gastos do menor entre mes e o mês de hoje em
// diante.
func (s *Service) ResumoCartoes(ctx context.Context, userID, mes, hoje string) ([]ResumoCartao, error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return nil, err
	}
	mes, hoje = strings.TrimSpace(mes), strings.TrimSpace(hoje)
	var campos []errs.FieldError
	if !gasto.MesValido(mes) {
		campos = append(campos, errs.FieldError{Field: "mes", Message: "deve ser um mês válido no formato AAAA-MM"})
	}
	if !gasto.DataValida(hoje) {
		campos = append(campos, errs.FieldError{Field: "hoje", Message: "deve ser uma data válida no formato AAAA-MM-DD"})
	}
	if len(campos) > 0 {
		return nil, errs.Validation("Parâmetros do resumo de cartões inválidos.", campos...)
	}
	dia, _ := time.Parse(gasto.LayoutData, hoje)

	cartoes, err := s.ListarCartoes(ctx, userID)
	if err != nil {
		return nil, err
	}
	resumos := make([]ResumoCartao, 0, len(cartoes))
	if len(cartoes) == 0 {
		return resumos, nil
	}
	// Faturas de meses anteriores ao de hoje já venceram; a do mês pedido
	// pode ser antiga.
	gastos, err := s.store.DesdeMes(ctx, userID, min(mes, hoje[:len(gasto.LayoutMes)]))
	if err != nil {
		return nil, err
	}
	indice := make(map[string]int, len(cartoes))
	for i, c := range cartoes {
		indice[c.ID] = i
		resumos = append(resumos, ResumoCartao{Cartao: c, Vencimento: c.Vencimento(mes)})
	}
	for _, g := range gastos {
		i, ok := indice[g.CartaoID]
		if !ok || g.UserID != userID {
			continue
		}
		r := &resumos[i]
		if g.Mes() == mes {
			r.FaturaCentavos += g.ValorCentavos
		}
		if !r.Cartao.Vencimento(g.Mes()).Before(dia) {
			r.LimiteUsadoCentavos += g.ValorCentavos
		}
	}
	return resumos, nil
}
