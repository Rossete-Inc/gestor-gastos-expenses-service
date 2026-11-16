package migracao

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const ulid = "01JC8Z6Q3N4W5X6Y7Z8A9B0C1D"

func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

func TestConverter(t *testing.T) {
	antigo := map[string]types.AttributeValue{
		"PK": s("USER#u-1"), "SK": s("EXP#2026-11-03#" + ulid), "gastoId": s("20261103-" + ulid),
		"data": s("2026-11-03"), "grupo": s("CARTAO"), "valorCentavos": &types.AttributeValueMemberN{Value: "4590"},
	}
	novo, ok := Converter(antigo)
	if !ok {
		t.Fatal("não converteu um item legado")
	}
	if texto(novo, "SK") != "EXP#2026-11#2026-11-03#"+ulid || texto(novo, "gastoId") != "202611-20261103-"+ulid ||
		texto(novo, "mesReferencia") != "2026-11" || texto(novo, "PK") != "USER#u-1" || texto(novo, "grupo") != "CARTAO" {
		t.Errorf("convertido = %v", novo)
	}
	if n, _ := novo["valorCentavos"].(*types.AttributeValueMemberN); n == nil || n.Value != "4590" {
		t.Errorf("valor = %v", novo["valorCentavos"])
	}
	// O original não é alterado.
	if texto(antigo, "SK") != "EXP#2026-11-03#"+ulid {
		t.Error("o item original mudou")
	}

	for _, sk := range []string{
		"EXP#2026-11#2026-11-03#" + ulid, // já migrado
		"CARD#" + ulid,                   // cartão
		"EXP#2026-02-30#" + ulid,         // data inexistente
		"EXP#2026-11-03#curto",
	} {
		if _, ok := Converter(map[string]types.AttributeValue{"PK": s("USER#u-1"), "SK": s(sk)}); ok {
			t.Errorf("converteu %s", sk)
		}
	}
}
