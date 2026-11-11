package gasto

import (
	"crypto/rand"
	"encoding/binary"
	"regexp"
	"strings"
	"time"
)

// O ID de um gasto é a data compacta (AAAAMMDD), um hífen e um ULID:
//
//	20261103-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D
//
// A data no ID permite chegar à chave do DynamoDB (SK=EXP#AAAA-MM-DD#<ULID>)
// sem índice secundário. O ULID ordena os gastos do mesmo dia pela hora do
// lançamento e não muda na edição; a data muda, e com ela o ID.
//
// Um ULID tem 128 bits: 48 de milissegundos desde a época Unix e 80
// aleatórios, em 26 caracteres do base32 de Crockford (sem I, L, O e U).

const alfabeto = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var padraoID = regexp.MustCompile(`^([0-9]{8})-([0-7][0-9A-HJKMNP-TV-Z]{25})$`)

// TamanhoID é o tamanho de todo ID de gasto.
const TamanhoID = 8 + 1 + 26

// NovoULID gera um ULID com o instante t.
func NovoULID(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	_, _ = rand.Read(b[6:])
	return codificarULID(b)
}

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

// IDDe monta o ID a partir da data (AAAA-MM-DD) e do ULID.
func IDDe(data, ulid string) string {
	return strings.ReplaceAll(data, "-", "") + "-" + ulid
}

// PartesDoID devolve a data (AAAA-MM-DD) e o ULID de um ID. ok é false para
// qualquer ID fora do formato ou com data inexistente; esses IDs nunca
// existem e nem chegam ao banco.
func PartesDoID(id string) (data, ulid string, ok bool) {
	m := padraoID.FindStringSubmatch(id)
	if m == nil {
		return "", "", false
	}
	data = m[1][:4] + "-" + m[1][4:6] + "-" + m[1][6:]
	if !DataValida(data) {
		return "", "", false
	}
	return data, m[2], true
}
