// Package serie é o domínio dos parcelamentos e das recorrências (spec
// parcelamentos-recorrencias): a série guarda o modelo dos gastos (valor,
// categoria, cartão, descrição e dia) e cada parcela ou ocorrência é um
// gasto comum com a posição na série.
//
// O parcelamento cria todas as parcelas no lançamento. A recorrência cria a
// ocorrência do mês do lançamento e as dos meses seguintes na primeira
// leitura de cada mês; MesesGerados guarda os meses já criados, para uma
// ocorrência excluída com "só esta" não voltar.
//
// Os meses da série são os meses da data dos gastos (calendário). O mês de
// referência de cada gasto (a fatura do cartão) é calculado pelo app.
package serie

import (
	"crypto/sha256"
	"slices"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

// Serie de um usuário.
type Serie struct {
	ID     string
	UserID string
	// Tipo é contract.SerieParcelamento ou contract.SerieRecorrencia.
	Tipo string
	// ValorCentavos de cada gasto da série. No parcelamento pelo total, a 1ª
	// parcela leva também o resto da divisão.
	ValorCentavos int64
	CategoriaID   string
	Grupo         gasto.Grupo
	CartaoID      string
	Descricao     string
	// Dia do mês dos gastos; nos meses mais curtos, o último dia.
	Dia int
	// MesInicial (AAAA-MM) é o mês da data do gasto PrimeiroNumero.
	MesInicial string
	// MesFinal (AAAA-MM) é o mês da última ocorrência da recorrência; vazio
	// = sem fim. No parcelamento, o mês da última parcela.
	MesFinal string
	// PrimeiroNumero é o número do primeiro gasto criado: a parcela atual
	// do parcelamento em andamento, ou 1.
	PrimeiroNumero int
	// TotalParcelas do parcelamento; 0 na recorrência.
	TotalParcelas int
	// MesesGerados são os meses em que a recorrência já criou a ocorrência.
	MesesGerados []string
	// PrimeiroGastoID é o gasto devolvido no lançamento (e no reenvio).
	PrimeiroGastoID string
	// Versao muda a cada gravação; o store só grava por cima da versão lida,
	// para uma edição não perder os meses criados por uma leitura ao mesmo
	// tempo.
	Versao       int
	CriadoEm     time.Time
	AtualizadoEm time.Time
}

// Mudancas são as gravações de uma edição ou exclusão em série: séries a
// gravar (Versao é a lida; 0 = nova) ou apagar, e gastos a gravar ou apagar.
// Um gasto em Gravar com a mesma chave de um em Excluir é só regravado.
type Mudancas struct {
	UserID          string
	Series          []Serie
	SeriesExcluidas []string
	Gravar          []gasto.Gasto
	Excluir         []gasto.Gasto
}

// Recorrente informa se a série é uma recorrência.
func (s Serie) Recorrente() bool { return s.Tipo == contract.SerieRecorrencia }

// Pedido é o parcelamento ou a recorrência pedidos no lançamento.
type Pedido struct {
	Tipo         string
	Modo         string
	Parcelas     int
	ParcelaAtual int
	MesFinal     string
}

// PedidoDe lê e confere o pedido do lançamento. Sem parcelamento nem
// recorrência, devolve nil: o gasto é avulso.
func PedidoDe(in contract.CriarGastoIn) (*Pedido, error) {
	var campos []errs.FieldError
	add := func(campo, msg string) { campos = append(campos, errs.FieldError{Field: campo, Message: msg}) }
	var p *Pedido
	switch {
	case in.Parcelamento != nil && in.Recorrencia != nil:
		add("recorrencia", "não pode ser usada junto com o parcelamento")
	case in.Parcelamento != nil:
		pa := in.Parcelamento
		p = &Pedido{Tipo: contract.SerieParcelamento, Modo: strings.TrimSpace(pa.Modo), Parcelas: pa.Parcelas,
			ParcelaAtual: pa.ParcelaAtual, MesFinal: strings.TrimSpace(pa.MesFinal)}
		switch p.Modo {
		case contract.ModoTotal:
			if p.Parcelas == 0 {
				add("parcelas", "obrigatório")
			}
			if p.ParcelaAtual > 1 || p.MesFinal != "" {
				add("modo", "parcela atual e mês final só valem no modo PARCELA")
			}
			p.ParcelaAtual = 1
		case contract.ModoParcela:
			if p.Parcelas == 0 && p.MesFinal == "" {
				add("parcelas", "informe o total de parcelas ou o mês final")
			}
			if p.ParcelaAtual == 0 {
				p.ParcelaAtual = 1
			}
			if p.ParcelaAtual < 1 || p.ParcelaAtual > contract.MaxParcelas {
				add("parcelaAtual", "deve ser de 1 a 72")
			}
		default:
			add("modo", "deve ser TOTAL ou PARCELA")
		}
		if p.Parcelas != 0 && (p.Parcelas < contract.MinParcelas || p.Parcelas > contract.MaxParcelas) {
			add("parcelas", "deve ser de 2 a 72")
		}
		if p.MesFinal != "" && !gasto.MesValido(p.MesFinal) {
			add("mesFinal", "deve ser um mês válido no formato AAAA-MM")
		}
	case in.Recorrencia != nil:
		p = &Pedido{Tipo: contract.SerieRecorrencia, MesFinal: strings.TrimSpace(in.Recorrencia.MesFinal)}
		if p.MesFinal != "" && !gasto.MesValido(p.MesFinal) {
			add("mesFinal", "deve ser um mês válido no formato AAAA-MM")
		}
	}
	if len(campos) > 0 {
		return nil, errs.Validation("Dados do gasto inválidos.", campos...)
	}
	return p, nil
}

// Item é um gasto planejado da série: a posição e os dados, com o mês de
// referência já calculado.
type Item struct {
	Numero int
	Dados  gasto.Dados
}

// MesDaFatura calcula o mês de referência de um gasto na data (o do cartão,
// ou "" para o mês da data).
type MesDaFatura func(data string) string

// Planejar monta a série e os gastos que o lançamento cria: todas as
// parcelas, ou a primeira ocorrência da recorrência. d já vem validado pelo
// domínio do gasto; o valor é o total (modo TOTAL) ou o de cada parcela.
func Planejar(p Pedido, id, userID string, d gasto.Dados, fatura MesDaFatura, agora time.Time) (Serie, []Item, error) {
	mesData := d.Data[:len(gasto.LayoutMes)]
	dia := diaDe(d.Data)
	s := Serie{
		ID: id, UserID: userID, Tipo: p.Tipo, ValorCentavos: d.ValorCentavos,
		CategoriaID: d.CategoriaID, Grupo: d.Grupo, CartaoID: d.CartaoID, Descricao: d.Descricao,
		Dia: dia, MesInicial: mesData, PrimeiroNumero: 1, CriadoEm: agora.UTC().Truncate(time.Millisecond),
	}
	invalido := func(campo, msg string) error {
		return errs.Validation("Dados do gasto inválidos.", errs.FieldError{Field: campo, Message: msg})
	}

	if s.Recorrente() {
		if p.MesFinal != "" && p.MesFinal < mesData {
			return Serie{}, nil, invalido("mesFinal", "não pode ser anterior ao mês do lançamento")
		}
		s.MesFinal = p.MesFinal
		s.MesesGerados = []string{mesData}
		return s, []Item{{Numero: 1, Dados: comMes(d, fatura)}}, nil
	}

	primeiro := p.ParcelaAtual
	total := p.Parcelas
	if total == 0 {
		mesRef := mesDe(d.Data, fatura)
		if p.MesFinal < mesRef {
			return Serie{}, nil, invalido("mesFinal", "não pode ser anterior ao mês da fatura da compra")
		}
		total = primeiro + MesesEntre(mesRef, p.MesFinal)
		if total > contract.MaxParcelas {
			return Serie{}, nil, invalido("mesFinal", "dá mais de 72 parcelas")
		}
		if total < contract.MinParcelas {
			return Serie{}, nil, invalido("mesFinal", "dá menos de 2 parcelas")
		}
	}
	if primeiro > total {
		return Serie{}, nil, invalido("parcelaAtual", "não pode ser maior que o total de parcelas")
	}
	valores := slices.Repeat([]int64{d.ValorCentavos}, total-primeiro+1)
	if p.Modo == contract.ModoTotal {
		if d.ValorCentavos < int64(total) {
			return Serie{}, nil, invalido("valorCentavos", "deve ser de pelo menos R$ 0,01 por parcela")
		}
		valores = DividirTotal(d.ValorCentavos, total)
		s.ValorCentavos = d.ValorCentavos / int64(total)
	}
	s.PrimeiroNumero, s.TotalParcelas = primeiro, total
	s.MesFinal = SomarMeses(mesData, total-primeiro)
	itens := make([]Item, 0, len(valores))
	for i, v := range valores {
		di := d
		di.ValorCentavos = v
		di.Data = DataNoMes(SomarMeses(mesData, i), dia)
		itens = append(itens, Item{Numero: primeiro + i, Dados: comMes(di, fatura)})
	}
	return s, itens, nil
}

// DividirTotal divide total em n parcelas iguais em centavos, com o resto na
// 1ª (cenário "Divisão com resto").
//
//	DividirTotal(10_000, 3) // => [3334 3333 3333]
func DividirTotal(total int64, n int) []int64 {
	base := total / int64(n)
	v := slices.Repeat([]int64{base}, n)
	v[0] += total % int64(n)
	return v
}

// MesesFaltando são os meses da recorrência até ate (AAAA-MM), inclusive,
// que ainda não têm ocorrência criada, em ordem.
func (s Serie) MesesFaltando(ate string) []string {
	if !s.Recorrente() || ate < s.MesInicial {
		return nil
	}
	if s.MesFinal != "" && s.MesFinal < ate {
		ate = s.MesFinal
	}
	var meses []string
	for m := s.MesInicial; m <= ate; m = SomarMeses(m, 1) {
		if !slices.Contains(s.MesesGerados, m) {
			meses = append(meses, m)
		}
	}
	return meses
}

// MesDoNumero é o mês da data do gasto de número n.
func (s Serie) MesDoNumero(n int) string { return SomarMeses(s.MesInicial, n-s.PrimeiroNumero) }

// NumeroDoMes é o número do gasto no mês (AAAA-MM).
func (s Serie) NumeroDoMes(mes string) int { return s.PrimeiroNumero + MesesEntre(s.MesInicial, mes) }

// Modelo são os dados do gasto da série no mês (AAAA-MM), sem o mês de
// referência.
func (s Serie) Modelo(mes string) gasto.Dados {
	return gasto.Dados{
		ValorCentavos: s.ValorCentavos, Data: DataNoMes(mes, s.Dia), CategoriaID: s.CategoriaID,
		Grupo: s.Grupo, CartaoID: s.CartaoID, Descricao: s.Descricao,
	}
}

// ComModelo devolve a série com o modelo dos dados editados: valor,
// categoria, grupo, cartão, descrição e o dia da data.
func (s Serie) ComModelo(d gasto.Dados, agora time.Time) Serie {
	s.ValorCentavos, s.CategoriaID, s.Grupo = d.ValorCentavos, d.CategoriaID, d.Grupo
	s.CartaoID, s.Descricao, s.Dia = d.CartaoID, d.Descricao, diaDe(d.Data)
	s.AtualizadoEm = agora.UTC().Truncate(time.Millisecond)
	return s
}

// Posicao é a posição do gasto de número n.
func (s Serie) Posicao(n int) gasto.Posicao {
	return gasto.Posicao{SerieID: s.ID, Tipo: s.Tipo, Numero: n, Total: s.TotalParcelas}
}

// IDDaChave deriva o ID da série da chave de idempotência do usuário: o
// mesmo lançamento reenviado cai na mesma série.
func IDDaChave(userID, chave string) string {
	h := sha256.Sum256([]byte(userID + "\x00" + chave))
	var b [16]byte
	copy(b[:], h[:16])
	return gasto.ULIDDeBytes(b)
}

// SomarMeses soma n meses a mes (AAAA-MM).
//
//	SomarMeses("2026-11", 3) // => "2027-02"
func SomarMeses(mes string, n int) string {
	t, err := time.Parse(gasto.LayoutMes, mes)
	if err != nil {
		return ""
	}
	return t.AddDate(0, n, 0).Format(gasto.LayoutMes)
}

// MesesEntre é quantos meses vão de de até ate (AAAA-MM); negativo se ate
// for antes.
//
//	MesesEntre("2026-12", "2027-07") // => 7
func MesesEntre(de, ate string) int {
	a, _ := time.Parse(gasto.LayoutMes, de)
	b, _ := time.Parse(gasto.LayoutMes, ate)
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

// DataNoMes é a data do dia no mês (AAAA-MM), ou o último dia do mês se ele
// não existir (cenário "Dia 31").
//
//	DataNoMes("2027-02", 31) // => "2027-02-28"
func DataNoMes(mes string, dia int) string {
	t, err := time.Parse(gasto.LayoutMes, mes)
	if err != nil {
		return ""
	}
	ultimo := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(t.Year(), t.Month(), min(dia, ultimo), 0, 0, 0, 0, time.UTC).Format(gasto.LayoutData)
}

func diaDe(data string) int {
	t, _ := time.Parse(gasto.LayoutData, data)
	return t.Day()
}

// mesDe é o mês de referência do gasto na data: o da fatura, ou o da data.
func mesDe(data string, fatura MesDaFatura) string {
	if m := fatura(data); m != "" {
		return m
	}
	return data[:len(gasto.LayoutMes)]
}

func comMes(d gasto.Dados, fatura MesDaFatura) gasto.Dados {
	d.MesReferencia = fatura(d.Data)
	return d
}

// ErrNaoEncontrada é o 404 de série inexistente ou de outro usuário.
func ErrNaoEncontrada() error {
	return errs.NotFound(contract.CodigoNaoEncontrado, "Gasto não encontrado.")
}
