package cartao

import "time"

const layoutData = "2006-01-02"
const layoutMes = "2006-01"

// MesDaFatura devolve o mês de referência (AAAA-MM) da fatura em que cai uma
// compra feita em data (AAAA-MM-DD), num cartão que fecha no dia
// diaFechamento e vence no dia diaVencimento (spec cartoes-credito, "Mês de
// referência da fatura" e "Mês de vencimento e dias inexistentes"):
//
//  1. o fechamento do mês da compra é o dia diaFechamento, ou o último dia do
//     mês se ele não existir (fechamento 31 em fevereiro = dia 28 ou 29);
//  2. compra até o fechamento, inclusive, entra na fatura que fecha naquele
//     mês; depois dele, na que fecha no mês seguinte;
//  3. a fatura vence no mês do fechamento se diaVencimento > diaFechamento,
//     senão no mês seguinte; o mês do vencimento é o mês de referência.
//
// A data e os dias já vêm validados; uma data inválida devolve "".
//
//	MesDaFatura("2026-11-10", 25, 5) // => "2026-12"
//	MesDaFatura("2026-11-28", 25, 5) // => "2027-01"
//	MesDaFatura("2026-11-02", 3, 10) // => "2026-11"
//	MesDaFatura("2027-02-28", 31, 10) // => "2027-03"
func MesDaFatura(data string, diaFechamento, diaVencimento int) string {
	t, err := time.Parse(layoutData, data)
	if err != nil {
		return ""
	}
	fechamento := min(diaFechamento, ultimoDia(t.Year(), t.Month()))
	mes := primeiroDia(t)
	if t.Day() > fechamento {
		mes = mes.AddDate(0, 1, 0)
	}
	if diaVencimento <= diaFechamento {
		mes = mes.AddDate(0, 1, 0)
	}
	return mes.Format(layoutMes)
}

// Vencimento devolve a data de vencimento da fatura do mês de referência
// (AAAA-MM): o dia diaVencimento, ou o último dia do mês se ele não existir.
// Mês inválido devolve o zero de time.Time.
func Vencimento(mesReferencia string, diaVencimento int) time.Time {
	t, err := time.Parse(layoutMes, mesReferencia)
	if err != nil {
		return time.Time{}
	}
	return time.Date(t.Year(), t.Month(), min(diaVencimento, ultimoDia(t.Year(), t.Month())), 0, 0, 0, 0, time.UTC)
}

func primeiroDia(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// ultimoDia é o último dia do mês: o dia 0 do mês seguinte.
func ultimoDia(ano int, mes time.Month) int {
	return time.Date(ano, mes+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
