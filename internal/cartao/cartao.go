// Package cartao é o cartão de crédito do usuário (spec cartoes-credito):
// apelido, cor, dias de fechamento e vencimento e limite. O cartão define o
// mês de referência (a fatura) dos gastos lançados nele. Mora no
// expenses-service, na mesma partição dos gastos (ADR 0009).
package cartao

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"golang.org/x/text/unicode/norm"
)

const (
	// MaxApelido é o tamanho máximo do apelido, em caracteres.
	MaxApelido = 40
	// MaxAtivos é quantos cartões ativos um usuário pode ter.
	MaxAtivos = 20
	// MinDia e MaxDia limitam os dias de fechamento e vencimento.
	MinDia = 1
	MaxDia = 31
)

var padraoCor = regexp.MustCompile(`^#[0-9A-F]{6}$`)

// Cartao de crédito de um usuário.
type Cartao struct {
	// ID é um ULID.
	ID     string
	UserID string
	// Apelido como o usuário digitou (sem espaços nas pontas, em NFC).
	Apelido        string
	Cor            string
	DiaFechamento  int
	DiaVencimento  int
	LimiteCentavos int64
	// Arquivado some dos formulários, mas os gastos continuam nos totais.
	Arquivado bool
	CriadoEm  time.Time
	// AtualizadoEm é zero até a primeira edição.
	AtualizadoEm time.Time
}

// Dados são os campos que o usuário informa ao cadastrar ou editar.
type Dados struct {
	Apelido        string
	Cor            string
	DiaFechamento  int
	DiaVencimento  int
	LimiteCentavos int64
}

// ApelidoNormalizado é a chave de unicidade do apelido entre os cartões
// ativos: sem espaços nas pontas, NFC e minúsculas ("nubank" = "Nubank").
func ApelidoNormalizado(apelido string) string {
	return strings.ToLower(strings.TrimSpace(norm.NFC.String(apelido)))
}

// Novo valida os dados e cria o cartão do usuário com o ULID informado.
func Novo(ulid, userID string, d Dados, agora time.Time) (Cartao, error) {
	d, err := validar(d)
	if err != nil {
		return Cartao{}, err
	}
	return Cartao{
		ID: ulid, UserID: userID,
		Apelido: d.Apelido, Cor: d.Cor, DiaFechamento: d.DiaFechamento, DiaVencimento: d.DiaVencimento,
		LimiteCentavos: d.LimiteCentavos,
		CriadoEm:       agora.UTC().Truncate(time.Millisecond),
	}, nil
}

// Atualizado devolve o cartão com os dados novos, já validados. Mudar os
// dias não recalcula os gastos já lançados (spec, "Mudança de fechamento").
func (c Cartao) Atualizado(d Dados, agora time.Time) (Cartao, error) {
	d, err := validar(d)
	if err != nil {
		return Cartao{}, err
	}
	c.Apelido, c.Cor = d.Apelido, d.Cor
	c.DiaFechamento, c.DiaVencimento = d.DiaFechamento, d.DiaVencimento
	c.LimiteCentavos = d.LimiteCentavos
	c.AtualizadoEm = agora.UTC().Truncate(time.Millisecond)
	return c, nil
}

// MesDaFatura devolve o mês de referência de uma compra na data informada.
func (c Cartao) MesDaFatura(data string) string {
	return MesDaFatura(data, c.DiaFechamento, c.DiaVencimento)
}

// Vencimento devolve o vencimento da fatura do mês de referência.
func (c Cartao) Vencimento(mesReferencia string) time.Time {
	return Vencimento(mesReferencia, c.DiaVencimento)
}

func validar(d Dados) (Dados, error) {
	d.Apelido = strings.TrimSpace(norm.NFC.String(d.Apelido))
	d.Cor = strings.ToUpper(strings.TrimSpace(d.Cor))

	var campos []errs.FieldError
	add := func(campo, msg string) { campos = append(campos, errs.FieldError{Field: campo, Message: msg}) }

	switch {
	case d.Apelido == "":
		add("apelido", "obrigatório")
	case utf8.RuneCountInString(d.Apelido) > MaxApelido:
		add("apelido", "deve ter no máximo 40 caracteres")
	case strings.IndexFunc(d.Apelido, unicode.IsControl) >= 0:
		add("apelido", "contém caracteres inválidos")
	}
	if !padraoCor.MatchString(d.Cor) {
		add("cor", "deve ser uma cor no formato #RRGGBB")
	}
	if d.DiaFechamento < MinDia || d.DiaFechamento > MaxDia {
		add("diaFechamento", "deve ser um dia de 1 a 31")
	}
	if d.DiaVencimento < MinDia || d.DiaVencimento > MaxDia {
		add("diaVencimento", "deve ser um dia de 1 a 31")
	}
	switch {
	case d.LimiteCentavos < contract.MinValorCentavos:
		add("limiteCentavos", "deve ser maior que zero")
	case d.LimiteCentavos > contract.MaxValorCentavos:
		add("limiteCentavos", "deve ser no máximo R$ 1.000.000,00")
	}
	if len(campos) > 0 {
		return Dados{}, errs.Validation("Dados do cartão inválidos.", campos...)
	}
	return d, nil
}

// ErrNaoEncontrado é o 404 de cartão inexistente ou de outro usuário (spec,
// "Cartão alheio").
func ErrNaoEncontrado() error {
	return errs.NotFound(contract.CodigoCartaoNaoEncontrado, "Cartão não encontrado.")
}

// ErrApelidoDuplicado é o 422 de apelido já usado por outro cartão ativo.
func ErrApelidoDuplicado() error {
	return errs.Validation("Dados do cartão inválidos.", errs.FieldError{Field: "apelido", Message: "já existe um cartão ativo com esse apelido"})
}

// ErrLimiteDeCartoes é o 409 de quem já tem MaxAtivos cartões ativos.
func ErrLimiteDeCartoes() error {
	return errs.Conflict(contract.CodigoLimiteCartoes, "Você já tem 20 cartões ativos. Arquive um para cadastrar outro.")
}

// ErrEmUso é o 409 de excluir cartão com gastos (spec, "Excluir cartão com
// gastos"): o app oferece arquivar.
func ErrEmUso() error {
	return errs.Conflict(contract.CodigoCartaoEmUso, "Este cartão tem gastos vinculados. Arquive-o em vez de excluir.")
}

// Contract converte para o formato do contrato público.
func (c Cartao) Contract() contract.Cartao {
	return contract.Cartao{
		ID: c.ID, Apelido: c.Apelido, Cor: c.Cor,
		DiaFechamento: c.DiaFechamento, DiaVencimento: c.DiaVencimento,
		LimiteCentavos: c.LimiteCentavos, Arquivado: c.Arquivado,
	}
}

// DadosDe converte o payload do contrato.
func DadosDe(in contract.CartaoIn) Dados {
	return Dados{
		Apelido: in.Apelido, Cor: in.Cor,
		DiaFechamento: in.DiaFechamento, DiaVencimento: in.DiaVencimento,
		LimiteCentavos: in.LimiteCentavos,
	}
}
