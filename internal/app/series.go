package app

import (
	"context"
	"slices"
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/meta"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/serie"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Casos de uso das séries (spec parcelamentos-recorrencias): lançar
// parcelado ou recorrente, criar as ocorrências recorrentes na leitura de
// cada mês e editar ou excluir com o alcance "só esta", "esta e as
// próximas" ou "todas".

const (
	codigoSerieExistente = "serie_existente"
	codigoSerieAlterada  = "serie_alterada"
)

// Lancar lança um gasto avulso, parcelado ou recorrente. O parcelado cria
// todas as parcelas e devolve a primeira; o recorrente cria a ocorrência do
// mês da data. Com chave de idempotência repetida, devolve o gasto do
// primeiro envio sem criar nada.
func (s *Service) Lancar(ctx context.Context, userID string, in contract.CriarGastoIn) (gasto.Gasto, error) {
	g, _, err := s.lancar(ctx, userID, in)
	return g, err
}

// LancarComAlertas é o Lancar mais as metas que o lançamento fez cruzar 80%
// ou 100% (spec metas-gastos, "Aviso ao salvar um gasto").
func (s *Service) LancarComAlertas(ctx context.Context, userID string, in contract.CriarGastoIn) (gasto.Gasto, []meta.Alerta, error) {
	g, criados, err := s.lancar(ctx, userID, in)
	if err != nil {
		return gasto.Gasto{}, nil, err
	}
	return g, s.alertasDaEscrita(ctx, userID, gasto.DadosDe(in.GastoIn).AutorID, nil, criados), nil
}

// lancar devolve o gasto do lançamento e todos os gastos criados por ele
// (nenhum no reenvio idempotente).
func (s *Service) lancar(ctx context.Context, userID string, in contract.CriarGastoIn) (gasto.Gasto, []gasto.Gasto, error) {
	if err := gasto.ExigirUsuario(userID); err != nil {
		return gasto.Gasto{}, nil, err
	}
	pedido, err := serie.PedidoDe(in)
	if err != nil {
		return gasto.Gasto{}, nil, err
	}
	if pedido == nil {
		g, err := s.CriarGasto(ctx, userID, gasto.DadosDe(in.GastoIn))
		if err != nil {
			return gasto.Gasto{}, nil, err
		}
		return g, []gasto.Gasto{g}, nil
	}
	d, err := s.comMesDaFatura(ctx, userID, gasto.DadosDe(in.GastoIn), "")
	if err != nil {
		return gasto.Gasto{}, nil, err
	}
	agora := s.agora()
	// Valida os dados do gasto (e os normaliza) antes de planejar a série.
	modelo, err := gasto.Novo(gasto.NovoULID(agora), userID, d, agora)
	if err != nil {
		return gasto.Gasto{}, nil, err
	}
	fatura, err := s.faturaDoCartao(ctx, userID, d.CartaoID)
	if err != nil {
		return gasto.Gasto{}, nil, err
	}
	id := gasto.NovoULID(agora)
	if chave := strings.TrimSpace(in.ChaveIdempotencia); chave != "" {
		id = serie.IDDaChave(userID, chave)
	}
	dados := modelo.Dados()
	dados.MesReferencia = ""
	dados.AutorID = d.AutorID
	se, itens, err := serie.Planejar(*pedido, id, userID, dados, fatura, agora)
	if err != nil {
		return gasto.Gasto{}, nil, err
	}
	gastos := make([]gasto.Gasto, 0, len(itens))
	for _, it := range itens {
		g, err := gasto.Novo(gasto.NovoULID(agora), userID, it.Dados, agora)
		if err != nil {
			return gasto.Gasto{}, nil, err
		}
		g.Serie = se.Posicao(it.Numero)
		gastos = append(gastos, g)
	}
	se.PrimeiroGastoID = gastos[0].ID
	err = s.store.CriarSerie(ctx, se, gastos)
	if errs.KindOf(err) == errs.KindConflict && errs.As(err).Code == codigoSerieExistente {
		// Reenvio do mesmo lançamento: devolve o gasto do primeiro envio.
		existente, err := s.store.ObterSerie(ctx, userID, id)
		if err != nil {
			return gasto.Gasto{}, nil, err
		}
		primeiro, err := s.store.Obter(ctx, userID, existente.PrimeiroGastoID)
		return primeiro, nil, err
	}
	if err != nil {
		return gasto.Gasto{}, nil, err
	}
	s.log.InfoContext(ctx, "série criada", "userId", userID, "serieId", se.ID, "tipo", se.Tipo, "gastos", len(gastos))
	return gastos[0], gastos, nil
}

// faturaDoCartao devolve o cálculo do mês de referência para o cartão (""
// = sem cartão, mês da data). Cartão que não existe mais vira sem cartão.
func (s *Service) faturaDoCartao(ctx context.Context, userID, cartaoID string) (serie.MesDaFatura, error) {
	semCartao := func(string) string { return "" }
	if cartaoID == "" {
		return semCartao, nil
	}
	c, err := s.store.ObterCartao(ctx, userID, cartaoID)
	if errs.KindOf(err) == errs.KindNotFound {
		return semCartao, nil
	}
	if err != nil {
		return nil, err
	}
	return c.MesDaFatura, nil
}

// gerarRecorrencias cria as ocorrências das recorrências do usuário que
// ainda faltam até o mês ate (AAAA-MM), inclusive (spec, "Recorrência
// mensal" e "Painel sem abrir a lista"). Uma leitura repetida ou ao mesmo
// tempo não duplica: o store só grava meses ainda não marcados na série.
func (s *Service) gerarRecorrencias(ctx context.Context, userID, ate string) error {
	series, err := s.store.ListarSeries(ctx, userID)
	if err != nil {
		return err
	}
	agora := s.agora()
	for _, se := range series {
		meses := se.MesesFaltando(ate)
		if len(meses) == 0 {
			continue
		}
		fatura, err := s.faturaDoCartao(ctx, userID, se.CartaoID)
		if err != nil {
			return err
		}
		gastos := make([]gasto.Gasto, 0, len(meses))
		for _, m := range meses {
			d := se.Modelo(m)
			d.MesReferencia = fatura(d.Data)
			g, err := gasto.Novo(gasto.NovoULID(agora), userID, d, agora)
			if err != nil {
				return err
			}
			g.Serie = se.Posicao(se.NumeroDoMes(m))
			gastos = append(gastos, g)
		}
		if err := s.store.GerarOcorrencias(ctx, se, meses, gastos); err != nil {
			return err
		}
		s.log.InfoContext(ctx, "ocorrências criadas", "userId", userID, "serieId", se.ID, "meses", len(meses))
	}
	return nil
}

// escopoDe confere o alcance pedido para um gasto de série.
func escopoDe(escopo string) (string, error) {
	escopo = strings.TrimSpace(escopo)
	switch escopo {
	case contract.EscopoEsta, contract.EscopoProximas, contract.EscopoTodas:
		return escopo, nil
	}
	return "", errs.Validation("Escolha o alcance da alteração.",
		errs.FieldError{Field: "escopo", Message: "obrigatório num parcelamento ou recorrência: ESTA, PROXIMAS ou TODAS"})
}

// comRetentativa repete a operação uma vez se a série mudou no meio.
func comRetentativa[T any](f func() (T, error)) (T, error) {
	v, err := f()
	if errs.KindOf(err) == errs.KindConflict && errs.As(err).Code == codigoSerieAlterada {
		return f()
	}
	return v, err
}

// AtualizarGastoNoEscopo edita um gasto. No gasto de série, o escopo diz o
// alcance (spec, "Alcance da edição e da exclusão em séries"):
//   - ESTA: só o gasto, que fica ajustado;
//   - PROXIMAS: o gasto e os seguintes não ajustados; na recorrência, a
//     série se divide no mês do gasto;
//   - TODAS: a série e todos os gastos não ajustados.
//
// Em PROXIMAS e TODAS só o dia da data vale para a série; mudar o mês é 422.
// Se o valor não muda, cada parcela mantém o seu (a 1ª leva o resto da
// divisão).
func (s *Service) AtualizarGastoNoEscopo(ctx context.Context, userID, id string, d gasto.Dados, escopo string) (gasto.Gasto, error) {
	e, err := s.atualizarNoEscopo(ctx, userID, id, d, escopo)
	return e.gasto, err
}

// AtualizarGastoComAlertas é o AtualizarGastoNoEscopo mais as metas que a
// edição fez cruzar 80% ou 100% (spec metas-gastos, "Aviso ao salvar um
// gasto").
func (s *Service) AtualizarGastoComAlertas(ctx context.Context, userID, id string, d gasto.Dados, escopo string) (gasto.Gasto, []meta.Alerta, error) {
	e, err := s.atualizarNoEscopo(ctx, userID, id, d, escopo)
	if err != nil {
		return gasto.Gasto{}, nil, err
	}
	return e.gasto, s.alertasDaEscrita(ctx, userID, d.AutorID, e.antigos, e.novos), nil
}

// edicao é o resultado de uma edição: o gasto pedido e todos os gastos
// substituídos (antigos) e gravados (novos) por ela.
type edicao struct {
	gasto          gasto.Gasto
	antigos, novos []gasto.Gasto
}

func (s *Service) atualizarNoEscopo(ctx context.Context, userID, id string, d gasto.Dados, escopo string) (edicao, error) {
	antigo, err := s.ObterGasto(ctx, userID, id)
	if err != nil {
		return edicao{}, err
	}
	if !antigo.DaSerie() {
		novo, err := s.AtualizarGasto(ctx, userID, id, d)
		return edicao{novo, []gasto.Gasto{antigo}, []gasto.Gasto{novo}}, err
	}
	if escopo, err = escopoDe(escopo); err != nil {
		return edicao{}, err
	}
	if escopo == contract.EscopoEsta {
		novo, err := s.ajustarSoEsta(ctx, userID, antigo, d)
		return edicao{novo, []gasto.Gasto{antigo}, []gasto.Gasto{novo}}, err
	}
	return comRetentativa(func() (edicao, error) {
		return s.atualizarSerie(ctx, userID, antigo.ID, d, escopo)
	})
}

func (s *Service) ajustarSoEsta(ctx context.Context, userID string, antigo gasto.Gasto, d gasto.Dados) (gasto.Gasto, error) {
	d, err := s.comMesDaFatura(ctx, userID, d, antigo.CartaoID)
	if err != nil {
		return gasto.Gasto{}, err
	}
	novo, err := antigo.Atualizado(d, s.agora())
	if err != nil {
		return gasto.Gasto{}, err
	}
	novo.Serie.Ajustado = true
	if err := s.store.Substituir(ctx, antigo, novo); err != nil {
		return gasto.Gasto{}, err
	}
	s.log.InfoContext(ctx, "gasto da série ajustado", "userId", userID, "gastoId", novo.ID, "serieId", novo.Serie.SerieID)
	return novo, nil
}

func (s *Service) atualizarSerie(ctx context.Context, userID, id string, d gasto.Dados, escopo string) (edicao, error) {
	antigo, err := s.ObterGasto(ctx, userID, id)
	if err != nil {
		return edicao{}, err
	}
	d, err = s.comMesDaFatura(ctx, userID, d, antigo.CartaoID)
	if err != nil {
		return edicao{}, err
	}
	agora := s.agora()
	editado, err := antigo.Atualizado(d, agora)
	if err != nil {
		return edicao{}, err
	}
	if editado.Data[:len(gasto.LayoutMes)] != antigo.Data[:len(gasto.LayoutMes)] {
		return edicao{}, errs.Validation("Dados do gasto inválidos.",
			errs.FieldError{Field: "data", Message: "para mudar o mês, altere só esta"})
	}
	se, err := s.store.ObterSerie(ctx, userID, antigo.Serie.SerieID)
	if err != nil {
		return edicao{}, err
	}
	itens, err := s.store.DaSerie(ctx, userID, se.ID)
	if err != nil {
		return edicao{}, err
	}
	modelo := se.ComModelo(editado.Dados(), agora)
	fatura, err := s.faturaDoCartao(ctx, userID, modelo.CartaoID)
	if err != nil {
		return edicao{}, err
	}
	n := antigo.Serie.Numero
	m := serie.Mudancas{UserID: userID}

	// Na recorrência, "esta e as próximas" a partir de um mês que não é o
	// primeiro encerra a série no mês anterior e começa outra no mês do
	// gasto; as ocorrências seguintes passam para ela.
	destino := modelo
	divide := escopo == contract.EscopoProximas && se.Recorrente() && n > se.PrimeiroNumero
	if divide {
		mes := se.MesDoNumero(n)
		original := se
		original.MesFinal = serie.SomarMeses(mes, -1)
		original.MesesGerados = slices.DeleteFunc(slices.Clone(se.MesesGerados), func(x string) bool { return x >= mes })
		original.AtualizadoEm = modelo.AtualizadoEm
		destino = modelo
		destino.ID, destino.Versao = gasto.NovoULID(agora), 0
		destino.MesInicial, destino.PrimeiroNumero = mes, 1
		destino.MesesGerados = slices.DeleteFunc(slices.Clone(se.MesesGerados), func(x string) bool { return x < mes })
		destino.PrimeiroGastoID, destino.CriadoEm = antigo.ID, modelo.AtualizadoEm
		m.Series = append(m.Series, original, destino)
	} else if escopo == contract.EscopoTodas || n <= se.PrimeiroNumero {
		m.Series = append(m.Series, modelo)
	}

	var resultado gasto.Gasto
	mudouValor := editado.ValorCentavos != antigo.ValorCentavos
	for _, it := range itens {
		if escopo == contract.EscopoProximas && it.Serie.Numero < n {
			continue
		}
		if it.Serie.Ajustado && it.ID != antigo.ID {
			continue
		}
		mes := it.Data[:len(gasto.LayoutMes)]
		nd := destino.Modelo(mes)
		nd.AutorID = d.AutorID // quem edita a série
		if !mudouValor {
			nd.ValorCentavos = it.ValorCentavos
		}
		nd.MesReferencia = fatura(nd.Data)
		novo, err := it.Atualizado(nd, agora)
		if err != nil {
			return edicao{}, err
		}
		novo.Serie = it.Serie
		novo.Serie.Ajustado = false
		if divide {
			novo.Serie = destino.Posicao(destino.NumeroDoMes(mes))
		}
		m.Gravar = append(m.Gravar, novo)
		m.Excluir = append(m.Excluir, it)
		if it.ID == antigo.ID {
			resultado = novo
		}
	}
	if err := s.store.Aplicar(ctx, m); err != nil {
		return edicao{}, err
	}
	s.log.InfoContext(ctx, "série atualizada", "userId", userID, "serieId", se.ID, "escopo", escopo, "gastos", len(m.Gravar))
	return edicao{resultado, m.Excluir, m.Gravar}, nil
}

// ExcluirGastoNoEscopo exclui um gasto. No gasto de série, o escopo diz o
// alcance: ESTA apaga só ele (a ocorrência recorrente não volta); PROXIMAS
// apaga ele e os seguintes, encerrando a recorrência no mês anterior;
// TODAS apaga a série e todos os gastos dela.
func (s *Service) ExcluirGastoNoEscopo(ctx context.Context, userID, id, escopo string) error {
	g, err := s.ObterGasto(ctx, userID, id)
	if err != nil {
		return err
	}
	if !g.DaSerie() {
		return s.ExcluirGasto(ctx, userID, id)
	}
	if escopo, err = escopoDe(escopo); err != nil {
		return err
	}
	if escopo == contract.EscopoEsta {
		return s.ExcluirGasto(ctx, userID, id)
	}
	_, err = comRetentativa(func() (struct{}, error) {
		return struct{}{}, s.excluirDaSerie(ctx, userID, g, escopo)
	})
	return err
}

func (s *Service) excluirDaSerie(ctx context.Context, userID string, g gasto.Gasto, escopo string) error {
	se, err := s.store.ObterSerie(ctx, userID, g.Serie.SerieID)
	if errs.KindOf(err) == errs.KindNotFound {
		// Série já apagada: sobra só o gasto.
		return s.ExcluirGasto(ctx, userID, g.ID)
	}
	if err != nil {
		return err
	}
	itens, err := s.store.DaSerie(ctx, userID, se.ID)
	if err != nil {
		return err
	}
	n := g.Serie.Numero
	m := serie.Mudancas{UserID: userID}
	tudo := escopo == contract.EscopoTodas || n <= se.PrimeiroNumero
	for _, it := range itens {
		if tudo || it.Serie.Numero >= n {
			m.Excluir = append(m.Excluir, it)
		}
	}
	switch {
	case tudo:
		m.SeriesExcluidas = []string{se.ID}
	case se.Recorrente():
		se.MesFinal = serie.SomarMeses(se.MesDoNumero(n), -1)
		se.AtualizadoEm = s.agora().UTC()
		m.Series = []serie.Serie{se}
	}
	if err := s.store.Aplicar(ctx, m); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "gastos da série excluídos", "userId", userID, "serieId", se.ID, "escopo", escopo, "gastos", len(m.Excluir))
	return nil
}
