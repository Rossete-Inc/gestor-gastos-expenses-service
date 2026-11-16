package gasto

import (
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// SemCartao é o filtro de cartão que seleciona os gastos sem cartão (spec
// lancamento-gastos, "Filtro sem cartão").
const SemCartao = contract.SemCartao

// Filtro da listagem: o mês de referência é obrigatório; grupo, categoria e
// cartão são opcionais e se somam (cenários "Filtro por grupo" e "Filtro
// por cartão").
type Filtro struct {
	// Mes de referência no formato AAAA-MM.
	Mes         string
	Grupo       Grupo  // vazio = todos
	CategoriaID string // vazio = todas
	// CartaoID vazio = todos; SemCartao = só os gastos sem cartão.
	CartaoID string
}

// NovoFiltro valida os parâmetros da listagem.
func NovoFiltro(mes, grupo, categoriaID, cartaoID string) (Filtro, error) {
	f := Filtro{
		Mes:         strings.TrimSpace(mes),
		Grupo:       Grupo(strings.TrimSpace(grupo)),
		CategoriaID: strings.TrimSpace(categoriaID),
		CartaoID:    strings.TrimSpace(cartaoID),
	}
	var campos []errs.FieldError
	if !MesValido(f.Mes) {
		campos = append(campos, errs.FieldError{Field: "mes", Message: "deve ser um mês válido no formato AAAA-MM"})
	}
	if f.Grupo != "" && !f.Grupo.Valido() {
		campos = append(campos, errs.FieldError{Field: "grupo", Message: "deve ser CARTAO ou FIXO"})
	}
	if f.CategoriaID != "" {
		if msg := validarCategoriaID(f.CategoriaID); msg != "" {
			campos = append(campos, errs.FieldError{Field: "categoriaId", Message: msg})
		}
	}
	if msg := ValidarCartaoID(f.CartaoID); msg != "" {
		campos = append(campos, errs.FieldError{Field: "cartaoId", Message: msg})
	}
	if len(campos) > 0 {
		return Filtro{}, errs.Validation("Filtros da listagem inválidos.", campos...)
	}
	return f, nil
}

// Aceita informa se o gasto entra na listagem.
func (f Filtro) Aceita(g Gasto) bool {
	return g.Mes() == f.Mes &&
		(f.Grupo == "" || g.Grupo == f.Grupo) &&
		(f.CategoriaID == "" || g.CategoriaID == f.CategoriaID) &&
		f.aceitaCartao(g.CartaoID)
}

func (f Filtro) aceitaCartao(cartaoID string) bool {
	switch f.CartaoID {
	case "":
		return true
	case SemCartao:
		return cartaoID == ""
	default:
		return cartaoID == f.CartaoID
	}
}

// Escopo identifica a listagem para assinar o cursor: um cursor só vale para
// o mesmo usuário, mês e filtros.
func (f Filtro) Escopo(userID string) string {
	return "USER#" + userID + "|mes=" + f.Mes + "|grupo=" + string(f.Grupo) + "|categoria=" + f.CategoriaID + "|cartao=" + f.CartaoID
}
