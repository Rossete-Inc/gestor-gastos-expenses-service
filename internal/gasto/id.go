package gasto

import (
	"crypto/rand"
	"encoding/binary"
	"regexp"
	"strings"
	"time"
)

// O ID de um gasto é o mês de referência compacto (AAAAMM), a data compacta
// (AAAAMMDD) e um ULID, separados por hífen:
//
//	202612-20261110-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D
//
// O mês e a data no ID permitem chegar à chave do DynamoDB
// (SK=EXP#AAAA-MM#AAAA-MM-DD#<ULID>) sem índice secundário. O ULID ordena os
// gastos do mesmo dia pela hora do lançamento e não muda na edição; o mês de
// referência e a data mudam, e com eles o ID.
//
// Antes da change cartoes-credito o ID não tinha o mês (formato legado,
// AAAAMMDD-ULID, SK=EXP#AAAA-MM-DD#<ULID>). Os dois formatos são lidos até a
// migração terminar (scripts/migrar-mes-referencia).
//
// Um ULID tem 128 bits: 48 de milissegundos desde a época Unix e 80
// aleatórios, em 26 caracteres do base32 de Crockford (sem I, L, O e U).

const alfabeto = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const padraoULID = `([0-7][0-9A-HJKMNP-TV-Z]{25})`

var (
	padraoID       = regexp.MustCompile(`^([0-9]{6})-([0-9]{8})-` + padraoULID + `$`)
	padraoIDLegado = regexp.MustCompile(`^([0-9]{8})-` + padraoULID + `$`)
	padraoSoULID   = regexp.MustCompile(`^` + padraoULID + `$`)
)

// TamanhoID é o tamanho de todo ID de gasto no formato atual.
const TamanhoID = 6 + 1 + 8 + 1 + 26

// Partes são os componentes de um ID de gasto.
type Partes struct {
	// MesReferencia no formato AAAA-MM. No formato legado, o mês da data.
	MesReferencia string
	// Data no formato AAAA-MM-DD.
	Data string
	ULID string
	// Legado indica um ID sem o mês de referência (antes da migração).
	Legado bool
}

// NovoULID gera um ULID com o instante t.
func NovoULID(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	_, _ = rand.Read(b[6:])
	return codificarULID(b)
}

// ULIDValido informa se s é um ULID.
func ULIDValido(s string) bool { return padraoSoULID.MatchString(s) }

func codificarULID(b [16]byte) string {
	hi, lo := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = alfabeto[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}

// IDDe monta o ID a partir do mês de referência (AAAA-MM), da data
// (AAAA-MM-DD) e do ULID.
func IDDe(mesReferencia, data, ulid string) string {
	return strings.ReplaceAll(mesReferencia, "-", "") + "-" + strings.ReplaceAll(data, "-", "") + "-" + ulid
}

// IDLegado monta o ID no formato anterior à change cartoes-credito. Só é
// usado pela migração e pelos testes da leitura dupla.
func IDLegado(data, ulid string) string {
	return strings.ReplaceAll(data, "-", "") + "-" + ulid
}

// PartesDoID decompõe um ID nos dois formatos. ok é false para qualquer ID
// fora do formato ou com mês ou data inexistentes; esses IDs nunca existem e
// nem chegam ao banco.
func PartesDoID(id string) (p Partes, ok bool) {
	if m := padraoID.FindStringSubmatch(id); m != nil {
		p = Partes{MesReferencia: m[1][:4] + "-" + m[1][4:], Data: dataDe(m[2]), ULID: m[3]}
		if !MesValido(p.MesReferencia) || !DataValida(p.Data) {
			return Partes{}, false
		}
		return p, true
	}
	if m := padraoIDLegado.FindStringSubmatch(id); m != nil {
		p = Partes{Data: dataDe(m[1]), ULID: m[2], Legado: true}
		if !DataValida(p.Data) {
			return Partes{}, false
		}
		p.MesReferencia = p.Data[:len(LayoutMes)]
		return p, true
	}
	return Partes{}, false
}

func dataDe(compacta string) string {
	return compacta[:4] + "-" + compacta[4:6] + "-" + compacta[6:]
}
