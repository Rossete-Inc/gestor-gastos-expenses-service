// Package contract é o contrato público do expenses-service: os nomes das
// operações chamadas pelo BFF via invoke e os formatos de entrada e saída.
//
// É o único pacote não interno do módulo. O BFF o importa numa versão
// fixada, então mudar um campo aqui é mudança de contrato.
//
// Toda operação age sobre o userId do envelope, que o BFF tira do token.
// Envelope sem userId: 400 "usuario_ausente". Um gasto de outro usuário
// nunca é encontrado: responde 404 "gasto_nao_encontrado", como se não
// existisse.
//
// A existência e a visibilidade da categoria são checadas pelo BFF no
// categories-service antes de CriarGasto e AtualizarGasto. O BFF manda o
// grupo da categoria, que o serviço grava desnormalizado no gasto.
//
// Cartões (ADR 0009) moram aqui: o serviço valida o cartão do gasto (existe,
// é do usuário e está ativo) e calcula o mês de referência pela fatura. A
// regra "categoria CARTAO exige cartão" fica no BFF, que conhece o grupo.
package contract

import "github.com/Rossete-Inc/gestor-gastos-go-commons/page"

// Operações atendidas pelo invoke.Router do serviço.
const (
	// OpCriarGasto lança um gasto com CriarGastoIn e devolve o GastoComAlertas criado.
	// Dados inválidos: 422 "validacao" com os campos (valorCentavos, data,
	// categoriaId, grupo, descricao, parcelas, parcelaAtual, mesFinal).
	// Parcelado: cria a série e todas as parcelas e devolve a 1ª criada.
	// Recorrente: cria a série e a ocorrência do mês da data, que é a
	// devolvida; as dos meses seguintes nascem na leitura de cada mês. Com
	// ChaveIdempotencia repetida, não cria nada e devolve o mesmo gasto.
	OpCriarGasto = "CriarGasto"
	// OpObterGasto devolve o Gasto de ObterGastoIn.ID. Inexistente ou de
	// outro usuário: 404 "gasto_nao_encontrado".
	OpObterGasto = "ObterGasto"
	// OpAtualizarGasto substitui valor, data, categoria, grupo, descrição e
	// recorrência do gasto AtualizarGastoIn.ID, com as validações de
	// CriarGasto, e devolve o GastoComAlertas atualizado. O ID carrega a data do gasto:
	// se a data muda, o gasto devolvido tem um ID novo e o antigo deixa de
	// existir. Inexistente ou de outro usuário: 404 "gasto_nao_encontrado".
	// Gasto de série exige Escopo (422 "validacao", campo escopo); com
	// PROXIMAS ou TODAS só o dia da data vale para a série, e mudar o mês da
	// data é 422 (campo data).
	OpAtualizarGasto = "AtualizarGasto"
	// OpExcluirGasto exclui o gasto ExcluirGastoIn.ID. Sem payload na
	// resposta. Inexistente ou de outro usuário: 404 "gasto_nao_encontrado".
	// Gasto de série exige Escopo, como em AtualizarGasto.
	OpExcluirGasto = "ExcluirGasto"
	// OpListarGastos devolve PaginaGastos com os gastos do mês, do mais
	// recente para o mais antigo, com filtros opcionais por grupo e
	// categoria. Mês ou grupo inválido: 422 "validacao". Cursor adulterado ou
	// de outra listagem (outro mês, filtro ou usuário): 400 "cursor_invalido".
	OpListarGastos = "ListarGastos"
	// OpExisteGastoNaCategoria informa se o usuário tem algum gasto na
	// categoria. O BFF chama antes de excluir uma categoria (409
	// "categoria_em_uso").
	OpExisteGastoNaCategoria = "ExisteGastoNaCategoria"
	// OpResumoMensal devolve ResumoMensalOut com os totais do mês em
	// centavos e os 5 gastos mais recentes, para o painel. Mês sem gastos:
	// totais zerados e listas vazias. Mês inválido: 422 "validacao" (mes).
	OpResumoMensal = "ResumoMensal"

	// OpListarCartoes devolve ListarCartoesOut com todos os cartões do
	// usuário, ativos e arquivados, em ordem de apelido.
	OpListarCartoes = "ListarCartoes"
	// OpCriarCartao cadastra um cartão com CartaoIn e devolve o Cartao.
	// Dados inválidos ou apelido já usado por cartão ativo: 422 "validacao"
	// (apelido, cor, diaFechamento, diaVencimento, limiteCentavos). Já com
	// 20 cartões ativos: 409 "limite_cartoes".
	OpCriarCartao = "CriarCartao"
	// OpAtualizarCartao substitui os dados do cartão AtualizarCartaoIn.ID,
	// com as validações de CriarCartao. Os gastos já lançados mantêm o mês de
	// referência. Inexistente ou de outro usuário: 404
	// "cartao_nao_encontrado".
	OpAtualizarCartao = "AtualizarCartao"
	// OpArquivarCartao arquiva (Arquivado=true) ou reativa o cartão.
	// Reativar segue as regras de apelido único e de limite de ativos.
	OpArquivarCartao = "ArquivarCartao"
	// OpExcluirCartao exclui um cartão sem gastos. Com gastos: 409
	// "cartao_em_uso". Sem payload na resposta.
	OpExcluirCartao = "ExcluirCartao"
	// OpResumoCartoes devolve ResumoCartoesOut: para cada cartão, a fatura
	// do mês pedido e o limite usado e disponível na data Hoje.
	OpResumoCartoes = "ResumoCartoes"

	// Metas mensais por categoria (spec metas-gastos, ADR 0011). O BFF
	// valida a categoria no categories-service e manda o nome e o grupo dela,
	// e manda o mês corrente no fuso de Brasília.

	// OpListarMetas devolve ListarMetasOut com as metas que valem no mês
	// ListarMetasIn.Mes, com o total gasto em cada categoria. Mês inválido:
	// 422 "validacao" (mes).
	OpListarMetas = "ListarMetas"
	// OpDefinirMeta define ou altera a meta da categoria do mês
	// DefinirMetaIn.Mes em diante e devolve a MetaDoMes desse mês. Valor fora
	// de R$ 0,01 a R$ 1.000.000,00, mês, categoria ou grupo inválidos: 422
	// "validacao" (valorCentavos, mes, categoriaId, nomeCategoria, grupo).
	OpDefinirMeta = "DefinirMeta"
	// OpRemoverMeta tira a meta da categoria do mês RemoverMetaIn.Mes em
	// diante; os meses anteriores mantêm a meta que valia neles. Sem meta
	// valendo nesse mês: 404 "meta_nao_encontrada". Sem payload na resposta.
	OpRemoverMeta = "RemoverMeta"
	// OpExcluirMeta apaga a meta da categoria inteira, com o histórico. O BFF
	// chama ao excluir a categoria. Sem meta, não é erro. Sem payload na
	// resposta.
	OpExcluirMeta = "ExcluirMeta"
)

// Estados de uma meta no mês (MetaDoMes.Estado).
const (
	// MetaDentro: abaixo de 80% do valor.
	MetaDentro = "DENTRO"
	// MetaAviso: de 80% até menos de 100%.
	MetaAviso = "AVISO"
	// MetaEstourada: 100% ou mais.
	MetaEstourada = "ESTOURADA"
)

// SemCartao é o valor do filtro ListarGastosIn.CartaoID que seleciona os
// gastos sem cartão.
const SemCartao = "SEM_CARTAO"

// Séries (spec parcelamentos-recorrencias).
const (
	// SerieParcelamento é uma compra parcelada: todas as parcelas são
	// criadas no lançamento.
	SerieParcelamento = "PARCELAMENTO"
	// SerieRecorrencia é um gasto mensal: cada ocorrência é criada na
	// primeira leitura do mês.
	SerieRecorrencia = "RECORRENCIA"

	// ModoTotal divide GastoIn.ValorCentavos (o total) pelas parcelas, com o
	// resto na 1ª.
	ModoTotal = "TOTAL"
	// ModoParcela usa GastoIn.ValorCentavos como o valor de cada parcela,
	// da ParcelaAtual em diante.
	ModoParcela = "PARCELA"

	// EscopoEsta altera só o gasto, que fica "ajustado" e não muda mais com
	// a série.
	EscopoEsta = "ESTA"
	// EscopoProximas altera o gasto e os seguintes da série.
	EscopoProximas = "PROXIMAS"
	// EscopoTodas altera a série inteira.
	EscopoTodas = "TODAS"

	// MinParcelas e MaxParcelas limitam o número de parcelas.
	MinParcelas = 2
	MaxParcelas = 72
)

// Grupos de gasto, iguais aos do categories-service.
const (
	GrupoCartao = "CARTAO"
	GrupoFixo   = "FIXO"
)

// Códigos de erro (campo "code" do problem+json).
const (
	CodigoNaoEncontrado  = "gasto_nao_encontrado"
	CodigoUsuarioAusente = "usuario_ausente"
	CodigoCursorInvalido = "cursor_invalido"

	CodigoCartaoNaoEncontrado = "cartao_nao_encontrado"
	CodigoCartaoEmUso         = "cartao_em_uso"
	CodigoLimiteCartoes       = "limite_cartoes"

	CodigoMetaNaoEncontrada = "meta_nao_encontrada"
)

// Limites de valor, em centavos: de R$ 0,01 a R$ 1.000.000,00 (openapi:
// GastoIn.valorCentavos).
const (
	MinValorCentavos int64 = 1
	MaxValorCentavos int64 = 100_000_000
)

// Gasto é a resposta de ObterGasto, a base de GastoComAlertas e o item de
// PaginaGastos. O BFF troca categoriaId pelo resumo da categoria (nome e cor)
// do schema Gasto do openapi.
type Gasto struct {
	ID            string `json:"id"`
	ValorCentavos int64  `json:"valorCentavos"`
	// Data no formato AAAA-MM-DD.
	Data        string `json:"data"`
	Grupo       string `json:"grupo"`
	CategoriaID string `json:"categoriaId"`
	Descricao   string `json:"descricao,omitempty"`
	Recorrente  bool   `json:"recorrente"`
	// CartaoID vazio = sem cartão.
	CartaoID string `json:"cartaoId,omitempty"`
	// MesReferencia (AAAA-MM) é o mês em que o gasto conta: o da fatura do
	// cartão, ou o da data sem cartão.
	MesReferencia string `json:"mesReferencia"`
	// Serie identifica o parcelamento ou a recorrência do gasto; ausente no
	// gasto avulso.
	Serie *SerieGasto `json:"serie,omitempty"`
	// CriadoPor é quem lançou (colaboradores); vazio = o dono da conta, como
	// nos gastos anteriores à autoria.
	CriadoPor string `json:"criadoPor,omitempty"`
	// AtualizadoPor é quem editou por último; vazio = nunca editado ou
	// editado antes da autoria.
	AtualizadoPor string `json:"atualizadoPor,omitempty"`
}

// GastoComAlertas é a resposta de CriarGasto e AtualizarGasto: o Gasto, no
// mesmo nível do JSON, mais as metas que esta escrita fez cruzar 80% ou 100%
// pela primeira vez no mês, uma por meta e mês, com o maior limiar cruzado.
// Quem lê como Gasto ignora o campo novo.
type GastoComAlertas struct {
	Gasto
	AlertasDeMeta []AlertaDeMeta `json:"alertasDeMeta,omitempty"`
}

// AlertaDeMeta é um limiar (80 ou 100) que uma escrita fez uma meta cruzar
// num mês, com os valores em centavos.
type AlertaDeMeta struct {
	CategoriaID   string `json:"categoriaId"`
	NomeCategoria string `json:"nomeCategoria"`
	Mes           string `json:"mes"`
	Limiar        int    `json:"limiar"`
	TotalCentavos int64  `json:"totalCentavos"`
	MetaCentavos  int64  `json:"metaCentavos"`
}

// SerieGasto é a posição de um gasto na série.
type SerieGasto struct {
	ID string `json:"id"`
	// Tipo é SerieParcelamento ou SerieRecorrencia.
	Tipo string `json:"tipo"`
	// Numero é a parcela (n de "n/N") ou, na recorrência, a ocorrência a
	// partir do mês inicial (1 = o primeiro mês).
	Numero int `json:"numero"`
	// Total de parcelas; ausente na recorrência.
	Total int `json:"total,omitempty"`
	// Ajustado indica um gasto editado com EscopoEsta, que não muda mais com
	// a série.
	Ajustado bool `json:"ajustado,omitempty"`
}

// GastoIn são os dados de um gasto, iguais ao GastoIn do openapi mais o
// grupo da categoria, que o BFF obtém do categories-service.
type GastoIn struct {
	ValorCentavos int64  `json:"valorCentavos"`
	Data          string `json:"data"`
	CategoriaID   string `json:"categoriaId"`
	Grupo         string `json:"grupo"`
	Descricao     string `json:"descricao,omitempty"`
	Recorrente    bool   `json:"recorrente"`
	// CartaoID é um cartão ativo do usuário. Vazio = sem cartão. Cartão
	// inexistente, alheio ou arquivado: 422 "validacao" (cartaoId). Na
	// edição, manter o cartão atual vale mesmo se ele estiver arquivado.
	CartaoID string `json:"cartaoId,omitempty"`
	// AutorID é quem age (o sub do token), que o BFF manda quando um
	// colaborador lança ou edita na conta do dono. Vazio = o próprio dono.
	AutorID string `json:"autorId,omitempty"`
}

// CriarGastoIn é o payload de CriarGasto: os dados do gasto e, no máximo,
// um entre Parcelamento e Recorrencia.
type CriarGastoIn struct {
	GastoIn
	Parcelamento *ParcelamentoIn `json:"parcelamento,omitempty"`
	Recorrencia  *RecorrenciaIn  `json:"recorrencia,omitempty"`
	// ChaveIdempotencia é o Idempotency-Key do front, repassado pelo BFF:
	// reenviar o mesmo lançamento parcelado ou recorrente não duplica a
	// série. Vazio = sem idempotência.
	ChaveIdempotencia string `json:"chaveIdempotencia,omitempty"`
}

// ParcelamentoIn descreve um lançamento parcelado.
type ParcelamentoIn struct {
	// Modo é ModoTotal ou ModoParcela.
	Modo string `json:"modo"`
	// Parcelas é o total de parcelas (MinParcelas a MaxParcelas). No
	// ModoParcela pode vir vazio quando há MesFinal.
	Parcelas int `json:"parcelas,omitempty"`
	// ParcelaAtual (ModoParcela) é a parcela da data informada; padrão 1.
	ParcelaAtual int `json:"parcelaAtual,omitempty"`
	// MesFinal (AAAA-MM, ModoParcela) é o mês de referência da última
	// parcela, que define Parcelas.
	MesFinal string `json:"mesFinal,omitempty"`
}

// RecorrenciaIn descreve um lançamento recorrente.
type RecorrenciaIn struct {
	// MesFinal (AAAA-MM) é o mês da data da última ocorrência; vazio = até
	// ser encerrada.
	MesFinal string `json:"mesFinal,omitempty"`
}

// AtualizarGastoIn é o payload de AtualizarGasto: o ID e os dados novos, no
// mesmo nível do JSON.
type AtualizarGastoIn struct {
	ID string `json:"id"`
	GastoIn
	// Escopo é EscopoEsta, EscopoProximas ou EscopoTodas; obrigatório no
	// gasto de série e ignorado no avulso.
	Escopo string `json:"escopo,omitempty"`
}

// ObterGastoIn é o payload de ObterGasto.
type ObterGastoIn struct {
	ID string `json:"id"`
}

// ExcluirGastoIn é o payload de ExcluirGasto.
type ExcluirGastoIn struct {
	ID string `json:"id"`
	// Escopo, como em AtualizarGastoIn.
	Escopo string `json:"escopo,omitempty"`
}

// ListarGastosIn é o payload de ListarGastos.
type ListarGastosIn struct {
	// Mes no formato AAAA-MM. Obrigatório.
	Mes string `json:"mes"`
	// Grupo filtra por CARTAO ou FIXO. Vazio = todos.
	Grupo string `json:"grupo,omitempty"`
	// CategoriaID filtra pela categoria. Vazio = todas.
	CategoriaID string `json:"categoriaId,omitempty"`
	// CartaoID filtra pelo cartão; SemCartao = só os sem cartão. Vazio =
	// todos.
	CartaoID string `json:"cartaoId,omitempty"`
	// AutorID filtra por quem lançou; o próprio dono inclui os gastos sem
	// autor gravado. Vazio = todos.
	AutorID string `json:"autorId,omitempty"`
	// Cursor é o NextCursor da página anterior. Vazio = primeira página.
	Cursor string `json:"cursor,omitempty"`
	// Limit de itens por página: padrão 20, máximo 100.
	Limit int `json:"limit,omitempty"`
}

// PaginaGastos é a resposta de ListarGastos: {"items":[...],"nextCursor":"..."},
// com items sempre presente (vazio como []) e nextCursor ausente na última
// página.
type PaginaGastos = page.Page[Gasto]

// ExisteGastoNaCategoriaIn é o payload de ExisteGastoNaCategoria.
type ExisteGastoNaCategoriaIn struct {
	CategoriaID string `json:"categoriaId"`
}

// ExisteGastoNaCategoriaOut é a resposta de ExisteGastoNaCategoria.
type ExisteGastoNaCategoriaOut struct {
	Existe bool `json:"existe"`
}

// ResumoMensalIn é o payload de ResumoMensal.
type ResumoMensalIn struct {
	// Mes no formato AAAA-MM. Obrigatório.
	Mes string `json:"mes"`
}

// ResumoMensalOut é a resposta de ResumoMensal. Os totais são somas exatas
// em centavos: a soma de TotaisPorGrupo e a de TotaisPorCategoria são iguais
// a TotalCentavos. Os percentuais (maior resto) e o nome e a cor das
// categorias ficam com o BFF. As listas vêm sempre presentes, vazias como [].
type ResumoMensalOut struct {
	Mes           string `json:"mes"`
	TotalCentavos int64  `json:"totalCentavos"`
	// TotaisPorGrupo traz sempre CARTAO e FIXO, nessa ordem, mesmo zerados.
	TotaisPorGrupo []TotalGrupo `json:"totaisPorGrupo"`
	// TotaisPorCategoria só tem categorias com gasto no mês, do maior total
	// para o menor (empates por grupo e ID). A chave é o par categoria e
	// grupo, o grupo gravado em cada gasto.
	TotaisPorCategoria []TotalCategoria `json:"totaisPorCategoria"`
	// TotaisPorCartao só tem cartões com gasto no mês, mais CartaoID ""
	// para os gastos sem cartão, do maior total para o menor. A soma é
	// TotalCentavos.
	TotaisPorCartao []TotalCartao `json:"totaisPorCartao"`
	// TotaisPorCategoriaECartao divide cada TotalCategoria por cartão
	// (CartaoID "" = sem cartão), para o detalhe da fatia do gráfico.
	TotaisPorCategoriaECartao []TotalCategoriaCartao `json:"totaisPorCategoriaECartao"`
	// Recentes são até 5 gastos, do mais recente para o mais antigo, como
	// em ListarGastos.
	Recentes []Gasto `json:"recentes"`
	// Metas são as metas que valem no mês, com o total de cada categoria,
	// na ordem do ID da categoria. A ordem por estado fica com o BFF.
	Metas []MetaDoMes `json:"metas"`
}

// MetaDoMes é uma meta num mês: o valor que vale nele e o total gasto na
// categoria, somando todos os gastos da conta no mês de referência.
type MetaDoMes struct {
	CategoriaID   string `json:"categoriaId"`
	NomeCategoria string `json:"nomeCategoria"`
	Grupo         string `json:"grupo"`
	Mes           string `json:"mes"`
	ValorCentavos int64  `json:"valorCentavos"`
	TotalCentavos int64  `json:"totalCentavos"`
	// Estado é MetaDentro, MetaAviso ou MetaEstourada.
	Estado string `json:"estado"`
}

// ListarMetasIn é o payload de ListarMetas.
type ListarMetasIn struct {
	// Mes no formato AAAA-MM. Obrigatório.
	Mes string `json:"mes"`
}

// ListarMetasOut é a resposta de ListarMetas, com items sempre presente.
type ListarMetasOut struct {
	Items []MetaDoMes `json:"items"`
}

// DefinirMetaIn é o payload de DefinirMeta.
type DefinirMetaIn struct {
	CategoriaID   string `json:"categoriaId"`
	NomeCategoria string `json:"nomeCategoria"`
	Grupo         string `json:"grupo"`
	ValorCentavos int64  `json:"valorCentavos"`
	// Mes (AAAA-MM) é o mês corrente, de onde a meta passa a valer.
	Mes string `json:"mes"`
}

// RemoverMetaIn é o payload de RemoverMeta.
type RemoverMetaIn struct {
	CategoriaID string `json:"categoriaId"`
	// Mes (AAAA-MM) é o mês corrente, de onde a meta deixa de valer.
	Mes string `json:"mes"`
}

// ExcluirMetaIn é o payload de ExcluirMeta.
type ExcluirMetaIn struct {
	CategoriaID string `json:"categoriaId"`
}

// TotalGrupo é a soma dos gastos de um grupo no mês.
type TotalGrupo struct {
	Grupo         string `json:"grupo"`
	TotalCentavos int64  `json:"totalCentavos"`
}

// TotalCategoria é a soma dos gastos de uma categoria, dentro de um grupo,
// no mês.
type TotalCategoria struct {
	CategoriaID   string `json:"categoriaId"`
	Grupo         string `json:"grupo"`
	TotalCentavos int64  `json:"totalCentavos"`
}

// TotalCartao é a soma dos gastos de um cartão no mês. CartaoID "" = sem
// cartão.
type TotalCartao struct {
	CartaoID      string `json:"cartaoId"`
	TotalCentavos int64  `json:"totalCentavos"`
}

// TotalCategoriaCartao é a soma de uma categoria (no grupo) num cartão.
type TotalCategoriaCartao struct {
	CategoriaID   string `json:"categoriaId"`
	Grupo         string `json:"grupo"`
	CartaoID      string `json:"cartaoId"`
	TotalCentavos int64  `json:"totalCentavos"`
}

// Cartao é a resposta de CriarCartao, AtualizarCartao e ArquivarCartao e o
// item de ListarCartoesOut.
type Cartao struct {
	ID             string `json:"id"`
	Apelido        string `json:"apelido"`
	Cor            string `json:"cor"`
	DiaFechamento  int    `json:"diaFechamento"`
	DiaVencimento  int    `json:"diaVencimento"`
	LimiteCentavos int64  `json:"limiteCentavos"`
	Arquivado      bool   `json:"arquivado"`
}

// CartaoIn são os dados de um cartão.
type CartaoIn struct {
	Apelido        string `json:"apelido"`
	Cor            string `json:"cor"`
	DiaFechamento  int    `json:"diaFechamento"`
	DiaVencimento  int    `json:"diaVencimento"`
	LimiteCentavos int64  `json:"limiteCentavos"`
}

// CriarCartaoIn é o payload de CriarCartao.
type CriarCartaoIn = CartaoIn

// AtualizarCartaoIn é o payload de AtualizarCartao.
type AtualizarCartaoIn struct {
	ID string `json:"id"`
	CartaoIn
}

// ArquivarCartaoIn é o payload de ArquivarCartao.
type ArquivarCartaoIn struct {
	ID        string `json:"id"`
	Arquivado bool   `json:"arquivado"`
}

// ExcluirCartaoIn é o payload de ExcluirCartao.
type ExcluirCartaoIn struct {
	ID string `json:"id"`
}

// ListarCartoesOut é a resposta de ListarCartoes, com items sempre presente.
type ListarCartoesOut struct {
	Items []Cartao `json:"items"`
}

// ResumoCartoesIn é o payload de ResumoCartoes.
type ResumoCartoesIn struct {
	// Mes (AAAA-MM) da fatura exibida. Obrigatório.
	Mes string `json:"mes"`
	// Hoje (AAAA-MM-DD) na data local do usuário, que o BFF calcula em
	// America/Sao_Paulo. Faturas com vencimento antes de Hoje não contam no
	// limite usado.
	Hoje string `json:"hoje"`
}

// ResumoCartoesOut é a resposta de ResumoCartoes, um item por cartão (ativos
// e arquivados), na ordem de ListarCartoes.
type ResumoCartoesOut struct {
	Mes   string         `json:"mes"`
	Items []ResumoCartao `json:"items"`
}

// ResumoCartao é a fatura do mês e o limite de um cartão. O disponível é
// LimiteCentavos menos LimiteUsadoCentavos e pode ser negativo.
type ResumoCartao struct {
	Cartao                   Cartao `json:"cartao"`
	FaturaCentavos           int64  `json:"faturaCentavos"`
	LimiteUsadoCentavos      int64  `json:"limiteUsadoCentavos"`
	LimiteDisponivelCentavos int64  `json:"limiteDisponivelCentavos"`
	// Vencimento (AAAA-MM-DD) da fatura do mês pedido.
	Vencimento string `json:"vencimento"`
}
