package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/memstore"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
)

func lambdaTeste(t *testing.T) (LambdaFunc, *memstore.Store) {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	st := memstore.New()
	cursores, _ := page.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	return Lambda(app.New(st, cursores, log), log), st
}

func TestLambdaInvoke(t *testing.T) {
	h, st := lambdaTeste(t)
	payload, _ := json.Marshal(contract.GastoIn{ValorCentavos: 100, Data: "2026-11-03", CategoriaID: "lazer", Grupo: "CARTAO"})
	raw, _ := json.Marshal(invoke.Request{Operation: contract.OpCriarGasto, UserID: "u-1", Payload: payload})

	out, err := h(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := out.(invoke.Response)
	if !ok || resp.Error != nil || st.Quantos("u-1") != 1 {
		t.Errorf("resposta = %+v", out)
	}

	// Erro de domínio volta no envelope, sem falhar a função.
	raw, _ = json.Marshal(invoke.Request{Operation: contract.OpObterGasto, UserID: "u-1", Payload: json.RawMessage(`{"id":"x"}`)})
	out, err = h(ctx, raw)
	if err != nil || out.(invoke.Response).Error == nil {
		t.Errorf("obter inexistente = %+v, %v", out, err)
	}
}

func TestLambdaEvento(t *testing.T) {
	h, st := lambdaTeste(t)
	payload, _ := json.Marshal(contract.GastoIn{ValorCentavos: 100, Data: "2026-11-03", CategoriaID: "lazer", Grupo: "CARTAO"})
	raw, _ := json.Marshal(invoke.Request{Operation: contract.OpCriarGasto, UserID: "u-1", Payload: payload})
	if _, err := h(ctx, raw); err != nil {
		t.Fatal(err)
	}

	evento := []byte(`{"id":"e1","detail-type":"UserDeleted","source":"gestor-gastos.users","time":"2026-11-11T12:00:00Z","detail":{"userId":"u-1"}}`)
	if _, err := h(ctx, evento); err != nil || st.Quantos("u-1") != 0 {
		t.Errorf("UserDeleted = %v, sobraram %d", err, st.Quantos("u-1"))
	}

	// Evento sem userId falha, para a AWS tentar de novo e mandar à DLQ.
	if _, err := h(ctx, []byte(`{"id":"e2","detail-type":"UserDeleted","source":"s","detail":{}}`)); err == nil {
		t.Error("esperava erro no evento sem userId")
	}
}

func TestLambdaPayloadInvalido(t *testing.T) {
	h, _ := lambdaTeste(t)
	for _, raw := range []string{`[]`, `"x"`, `{"detail-type":1}`, `{"operation":1}`} {
		if _, err := h(ctx, []byte(raw)); err == nil {
			t.Errorf("%s: esperava erro", raw)
		}
	}
}
