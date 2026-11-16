// Package gasto é o domínio do expenses-service: gastos de cartão e fixos do
// usuário, com valor em centavos, data, categoria, grupo desnormalizado da
// categoria, cartão opcional, mês de referência (a fatura do cartão, ou o
// mês da data), descrição opcional e o flag informativo de recorrência
// (specs lancamento-gastos e cartoes-credito).
package gasto

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"golang.org/x/text/unicode/norm"
)

// Grupo de gasto: o grupo da categoria escolhida, gravado no gasto para
// filtrar e somar sem consultar o categories-service.
type Grupo string

const (
	// Cartao reúne os gastos feitos no cartão.
	Cartao Grupo = contract.GrupoCartao
	// Fixo reúne os gastos fixos.
	Fixo Grupo = contract.GrupoFixo
)

// Grupos lista os grupos válidos.
var Grupos = []Grupo{Cartao, Fixo}

// Valido informa se o grupo existe.
func (g Grupo) Valido() bool { return slices.Contains(Grupos, g) }

const (
	// MaxDescricao é o tamanho máximo da descrição, em caracteres.
	MaxDescricao = 140
	// MaxCategoriaID é o tamanho máximo do ID de uma categoria (openapi:
	// GastoIn.categoriaId).
	MaxCategoriaID = 64
	// MaxCartaoID é o tamanho máximo do ID de um cartão no filtro e no
	// payload; os IDs gerados são ULIDs de 26 caracteres.
	MaxCartaoID = 64
	// MinAno é o menor ano aceito numa data. O formato já exige 4 dígitos.
	MinAno = 1900

	// LayoutData é o formato da data do gasto.
	LayoutData = "2006-01-02"
	// LayoutMes é o formato do mês das listagens.
	LayoutMes = "2006-01"
)

// Gasto de um usuário.
type Gasto struct {
	// ID é o mês de referência, a data e o ULID (IDDe). Muda se o mês ou a
	// data mudarem.
	ID            string
	UserID        string
	ValorCentavos int64
	// Data da compra, no formato AAAA-MM-DD.
	Data string
	// MesReferencia (AAAA-MM) é o mês em que o gasto conta: o da fatura do
	// cartão, ou o da data se não houver cartão.
	MesReferencia string
	Grupo         Grupo
	CategoriaID   string
	// CartaoID vazio = sem cartão.
	CartaoID   string
	Descricao  string
	Recorrente bool
	CriadoEm   time.Time
	// AtualizadoEm é zero até a primeira edição.
	AtualizadoEm time.Time
}

// Dados são os campos que o usuário informa ao lançar ou editar um gasto,
// mais o mês de referência, que o app calcula pelo cartão.
type Dados struct {
	ValorCentavos int64
	Data          string
	CategoriaID   string
	Grupo         Grupo
	CartaoID      string
	Descricao     string
	Recorrente    bool
	// MesReferencia vazio = mês da data (gasto sem cartão).
	MesReferencia string
}

// Mes devolve o mês em que o gasto conta (o de referência), no formato
// AAAA-MM.
func (g Gasto) Mes() string {
	if g.MesReferencia != "" || len(g.Data) < len(LayoutMes) {
		// Gasto vazio (caminho de erro) devolve "", sem entrar em pânico.
		return g.MesReferencia
	}
	return g.Data[:len(LayoutMes)]
}

// ULID devolve a parte do ID que não muda na edição.
func (g Gasto) ULID() string {
	p, _ := PartesDoID(g.ID)
	return p.ULID
}

// Novo valida os dados e cria um gasto do usuário com o ULID informado (ver
// NovoULID).
func Novo(ulid, userID string, d Dados, agora time.Time) (Gasto, error) {
	if err := ExigirUsuario(userID); err != nil {
		return Gasto{}, err
	}
	d, err := validar(d)
	if err != nil {
		return Gasto{}, err
	}
	return Gasto{
		ID:            IDDe(d.MesReferencia, d.Data, ulid),
		UserID:        userID,
		ValorCentavos: d.ValorCentavos,
		Data:          d.Data,
		MesReferencia: d.MesReferencia,
		Grupo:         d.Grupo,
		CategoriaID:   d.CategoriaID,
		CartaoID:      d.CartaoID,
		Descricao:     d.Descricao,
		Recorrente:    d.Recorrente,
		CriadoEm:      agora.UTC().Truncate(time.Millisecond),
	}, nil
}

// Atualizado devolve o gasto com os dados novos, já validados, mantendo o
// ULID, o dono e a data de criação. Se o mês de referência ou a data mudarem,
// ou se o ID ainda estiver no formato legado, o ID muda junto.
func (g Gasto) Atualizado(d Dados, agora time.Time) (Gasto, error) {
	d, err := validar(d)
	if err != nil {
		return Gasto{}, err
	}
	g.ID = IDDe(d.MesReferencia, d.Data, g.ULID())
	g.ValorCentavos = d.ValorCentavos
	g.Data = d.Data
	g.MesReferencia = d.MesReferencia
	g.Grupo = d.Grupo
	g.CategoriaID = d.CategoriaID
	g.CartaoID = d.CartaoID
	g.Descricao = d.Descricao
	g.Recorrente = d.Recorrente
	g.AtualizadoEm = agora.UTC().Truncate(time.Millisecond)
	return g, nil
}

// validar confere os dados (cenários "Validação do lançamento") e devolve-os
// normalizados: espaços nas pontas removidos, descrição em NFC e, sem mês de
// referência, o mês da data.
func validar(d Dados) (Dados, error) {
	d.Data = strings.TrimSpace(d.Data)
	d.CategoriaID = strings.TrimSpace(d.CategoriaID)
	d.CartaoID = strings.TrimSpace(d.CartaoID)
	d.MesReferencia = strings.TrimSpace(d.MesReferencia)
	d.Grupo = Grupo(strings.TrimSpace(string(d.Grupo)))
	d.Descricao = strings.TrimSpace(norm.NFC.String(d.Descricao))

	var campos []errs.FieldError
	add := func(campo, msg string) { campos = append(campos, errs.FieldError{Field: campo, Message: msg}) }

	switch {
	case d.ValorCentavos < contract.MinValorCentavos:
		add("valorCentavos", "deve ser maior que zero")
	case d.ValorCentavos > contract.MaxValorCentavos:
		add("valorCentavos", "deve ser no máximo R$ 1.000.000,00")
	}
	if !DataValida(d.Data) {
		add("data", "deve ser uma data válida no formato AAAA-MM-DD")
	}
	if msg := validarCategoriaID(d.CategoriaID); msg != "" {
		add("categoriaId", msg)
	}
	if msg := ValidarCartaoID(d.CartaoID); msg != "" {
		add("cartaoId", msg)
	}
	if !d.Grupo.Valido() {
		add("grupo", "deve ser CARTAO ou FIXO")
	}
	if msg := validarDescricao(d.Descricao); msg != "" {
		add("descricao", msg)
	}
	if len(campos) > 0 {
		return Dados{}, errs.Validation("Dados do gasto inválidos.", campos...)
	}
	if d.MesReferencia == "" {
		d.MesReferencia = d.Data[:len(LayoutMes)]
	}
	if !MesValido(d.MesReferencia) {
		// Só o app preenche o mês de referência: inválido aqui é bug.
		return Dados{}, errs.Internal(fmt.Errorf("mês de referência inválido: %q", d.MesReferencia))
	}
	return d, nil
}

// DataValida informa se s é uma data do calendário no formato AAAA-MM-DD, a
// partir de MinAno. "2026-02-30" e "2026-2-3" são recusadas.
func DataValida(s string) bool {
	t, err := time.Parse(LayoutData, s)
	return err == nil && t.Year() >= MinAno
}

// MesValido informa se s é um mês no formato AAAA-MM, a partir de MinAno.
func MesValido(s string) bool {
	t, err := time.Parse(LayoutMes, s)
	return err == nil && t.Year() >= MinAno
}

func validarCategoriaID(id string) string {
	switch {
	case id == "":
		return "obrigatório"
	case len(id) > MaxCategoriaID:
		return "deve ter no máximo 64 caracteres"
	case temControle(id):
		return "contém caracteres inválidos"
	}
	return ""
}

// ValidarCartaoID devolve a mensagem de erro de um ID de cartão, ou "" se ele
// for aceitável (vazio = sem cartão).
func ValidarCartaoID(id string) string {
	switch {
	case len(id) > MaxCartaoID:
		return "deve ter no máximo 64 caracteres"
	case temControle(id):
		return "contém caracteres inválidos"
	}
	return ""
}

func validarDescricao(s string) string {
	switch {
	case utf8.RuneCountInString(s) > MaxDescricao:
		return "deve ter no máximo 140 caracteres"
	case temControle(s):
		return "contém caracteres inválidos"
	}
	return ""
}

func temControle(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }

// ExigirUsuario recusa um userId vazio, que só chega por bug no BFF: as
// operações sempre agem sobre o próprio usuário.
func ExigirUsuario(userID string) error {
	if strings.TrimSpace(userID) == "" {
		return errs.BadRequest(contract.CodigoUsuarioAusente, "Requisição sem usuário.")
	}
	return nil
}

// ErrNaoEncontrado é o 404 de gasto inexistente ou de outro usuário. Os dois
// casos têm a mesma resposta, para não revelar gastos alheios (cenário
// "Acesso a gasto alheio").
func ErrNaoEncontrado() error {
	return errs.NotFound(contract.CodigoNaoEncontrado, "Gasto não encontrado.")
}

// Contract converte para o formato do contrato público.
func (g Gasto) Contract() contract.Gasto {
	return contract.Gasto{
		ID:            g.ID,
		ValorCentavos: g.ValorCentavos,
		Data:          g.Data,
		MesReferencia: g.Mes(),
		Grupo:         string(g.Grupo),
		CategoriaID:   g.CategoriaID,
		CartaoID:      g.CartaoID,
		Descricao:     g.Descricao,
		Recorrente:    g.Recorrente,
	}
}

// DadosDe converte o payload do contrato.
func DadosDe(in contract.GastoIn) Dados {
	return Dados{
		ValorCentavos: in.ValorCentavos,
		Data:          in.Data,
		CategoriaID:   in.CategoriaID,
		Grupo:         Grupo(in.Grupo),
		CartaoID:      in.CartaoID,
		Descricao:     in.Descricao,
		Recorrente:    in.Recorrente,
	}
}
