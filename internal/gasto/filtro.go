package gasto

import (
	"strings"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Filtro da listagem: o mês é obrigatório; grupo e categoria são opcionais e
// se somam (cenário "Filtro por grupo").
type Filtro struct {
	// Mes no formato AAAA-MM.
	Mes         string
	Grupo       Grupo  // vazio = todos
	CategoriaID string // vazio = todas
}

// NovoFiltro valida os parâmetros da listagem.
func NovoFiltro(mes, grupo, categoriaID string) (Filtro, error) {
	f := Filtro{
		Mes:         strings.TrimSpace(mes),
		Grupo:       Grupo(strings.TrimSpace(grupo)),
		CategoriaID: strings.TrimSpace(categoriaID),
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
	if len(campos) > 0 {
		return Filtro{}, errs.Validation("Filtros da listagem inválidos.", campos...)
	}
	return f, nil
}

// Aceita informa se o gasto entra na listagem.
func (f Filtro) Aceita(g Gasto) bool {
	return g.Mes() == f.Mes &&
		(f.Grupo == "" || g.Grupo == f.Grupo) &&
		(f.CategoriaID == "" || g.CategoriaID == f.CategoriaID)
}

// Escopo identifica a listagem para assinar o cursor: um cursor só vale para
// o mesmo usuário, mês e filtros.
func (f Filtro) Escopo(userID string) string {
	return "USER#" + userID + "|mes=" + f.Mes + "|grupo=" + string(f.Grupo) + "|categoria=" + f.CategoriaID
}
