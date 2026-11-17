package gasto

import (
	"strings"
	"testing"
	"time"
)

func TestNovoULID(t *testing.T) {
	// 2026-11-11T15:04:05.123Z em milissegundos: os 10 primeiros caracteres
	// codificam o instante, os 16 restantes são aleatórios.
	a := NovoULID(agora)
	b := NovoULID(agora)
	if len(a) != 26 || a == b || a[:10] != b[:10] {
		t.Errorf("ULIDs = %s, %s", a, b)
	}
	if p, ok := PartesDoID(IDDe("2026-12", "2026-11-11", a)); !ok || p.ULID != a || !ULIDValido(a) {
		t.Errorf("ULID %s fora do formato aceito", a)
	}
	// Um instante depois ordena depois (lexicograficamente).
	if c := NovoULID(agora.Add(time.Millisecond)); c[:10] <= a[:10] {
		t.Errorf("%s não ordena depois de %s", c, a)
	}
	// Época Unix: o prefixo de tempo é todo zero.
	if z := NovoULID(time.UnixMilli(0)); !strings.HasPrefix(z, "0000000000") {
		t.Errorf("ULID da época = %s", z)
	}
}

func TestCodificarULID(t *testing.T) {
	var tudo [16]byte
	for i := range tudo {
		tudo[i] = 0xFF
	}
	// O maior ULID possível (especificação do ULID).
	if got := codificarULID(tudo); got != "7ZZZZZZZZZZZZZZZZZZZZZZZZZ" {
		t.Errorf("máximo = %s", got)
	}
	var um [16]byte
	um[15] = 1
	if got := codificarULID(um); got != "00000000000000000000000001" {
		t.Errorf("um = %s", got)
	}
}

func TestPartesDoID(t *testing.T) {
	id := IDDe("2026-12", "2026-11-10", ulid)
	if id != "202612-20261110-"+ulid || len(id) != TamanhoID {
		t.Fatalf("IDDe = %s", id)
	}
	p, ok := PartesDoID(id)
	if !ok || p != (Partes{MesReferencia: "2026-12", Data: "2026-11-10", ULID: ulid}) {
		t.Errorf("partes = %+v, %v", p, ok)
	}

	invalidos := []string{
		"",
		ulid,
		"20261103" + ulid,
		"20261103-" + ulid, // formato anterior a cartoes-credito, sem o mês
		"2026110-" + ulid,
		"20261103-" + strings.ToLower(ulid),
		"20261103-8" + ulid[1:],            // acima do maior ULID
		"20261103-" + ulid[:25] + "U",      // U não é do alfabeto
		"20261103-" + ulid + "0",           // longo demais
		"20260230-" + ulid,                 // 30 de fevereiro
		"18991231-" + ulid,                 // antes de MinAno
		"20261103-" + ulid[:20] + "#EXP#1", // tentativa de forjar a SK
		"202613-20261103-" + ulid,          // mês 13
		"202612-20260230-" + ulid,          // data inexistente
		"189912-20261103-" + ulid,          // mês antes de MinAno
		"2026-12-20261103-" + ulid,         // mês com hífen
	}
	for _, id := range invalidos {
		if _, ok := PartesDoID(id); ok {
			t.Errorf("PartesDoID(%q) aceitou", id)
		}
	}
}
