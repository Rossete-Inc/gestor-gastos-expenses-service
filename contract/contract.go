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
package contract

import "github.com/Rossete-Inc/gestor-gastos-go-commons/page"

// Operações atendidas pelo invoke.Router do serviço.
const (
	// OpCriarGasto lança um gasto com CriarGastoIn e devolve o Gasto criado.
	// Dados inválidos: 422 "validacao" com os campos (valorCentavos, data,
	// categoriaId, grupo, descricao).
	OpCriarGasto = "CriarGasto"
	// OpObterGasto devolve o Gasto de ObterGastoIn.ID. Inexistente ou de
	// outro usuário: 404 "gasto_nao_encontrado".
	OpObterGasto = "ObterGasto"
	// OpAtualizarGasto substitui valor, data, categoria, grupo, descrição e
	// recorrência do gasto AtualizarGastoIn.ID, com as validações de
	// CriarGasto, e devolve o Gasto atualizado. O ID carrega a data do gasto:
	// se a data muda, o gasto devolvido tem um ID novo e o antigo deixa de
	// existir. Inexistente ou de outro usuário: 404 "gasto_nao_encontrado".
	OpAtualizarGasto = "AtualizarGasto"
	// OpExcluirGasto exclui o gasto ExcluirGastoIn.ID. Sem payload na
	// resposta. Inexistente ou de outro usuário: 404 "gasto_nao_encontrado".
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
)

// Limites de valor, em centavos: de R$ 0,01 a R$ 1.000.000,00 (openapi:
// GastoIn.valorCentavos).
const (
	MinValorCentavos int64 = 1
	MaxValorCentavos int64 = 100_000_000
)

// Gasto é a resposta de CriarGasto, ObterGasto e AtualizarGasto e o item de
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
}

// CriarGastoIn é o payload de CriarGasto.
type CriarGastoIn = GastoIn

// AtualizarGastoIn é o payload de AtualizarGasto: o ID e os dados novos, no
// mesmo nível do JSON.
type AtualizarGastoIn struct {
	ID string `json:"id"`
	GastoIn
}

// ObterGastoIn é o payload de ObterGasto.
type ObterGastoIn struct {
	ID string `json:"id"`
}

// ExcluirGastoIn é o payload de ExcluirGasto.
type ExcluirGastoIn struct {
	ID string `json:"id"`
}

// ListarGastosIn é o payload de ListarGastos.
type ListarGastosIn struct {
	// Mes no formato AAAA-MM. Obrigatório.
	Mes string `json:"mes"`
	// Grupo filtra por CARTAO ou FIXO. Vazio = todos.
	Grupo string `json:"grupo,omitempty"`
	// CategoriaID filtra pela categoria. Vazio = todas.
	CategoriaID string `json:"categoriaId,omitempty"`
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
