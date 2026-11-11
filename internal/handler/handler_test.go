package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/memstore"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
)

var ctx = context.Background()

// transporte entrega o envelope direto ao router, passando por JSON como os
// transportes reais.
type transporte struct{ r *invoke.Router }

func (t transporte) Send(ctx context.Context, req invoke.Request) (invoke.Response, error) {
	b, _ := json.Marshal(req)
	var dec invoke.Request
	_ = json.Unmarshal(b, &dec)
	resp := t.r.Dispatch(ctx, dec)
	b, _ = json.Marshal(resp)
	var out invoke.Response
	_ = json.Unmarshal(b, &out)
	return out, nil
}

func montar(t *testing.T) (*invoke.Client, *invoke.Router, *events.Router, *memstore.Store) {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	st := memstore.New()
	cursores, err := page.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	svc := app.New(st, cursores, log)
	r := Invoke(svc, log)
	return invoke.NewClient(transporte{r}), r, Events(svc, log), st
}

func almoco() contract.GastoIn {
	return contract.GastoIn{ValorCentavos: 4590, Data: "2026-11-03", CategoriaID: "alimentacao", Grupo: "CARTAO", Descricao: "Almoço"}
}

func TestCicloPeloContrato(t *testing.T) {
	c, _, _, _ := montar(t)

	var criado contract.Gasto
	if err := c.Call(ctx, contract.OpCriarGasto, "u-1", almoco(), &criado); err != nil {
		t.Fatal(err)
	}
	if criado.ID == "" || criado.ValorCentavos != 4590 || criado.Grupo != "CARTAO" || criado.Descricao != "Almoço" {
		t.Fatalf("criado = %+v", criado)
	}

	var lido contract.Gasto
	if err := c.Call(ctx, contract.OpObterGasto, "u-1", contract.ObterGastoIn{ID: criado.ID}, &lido); err != nil || lido != criado {
		t.Fatalf("obter = %+v, %v", lido, err)
	}

	in := contract.AtualizarGastoIn{ID: criado.ID, GastoIn: contract.GastoIn{ValorCentavos: 9990, Data: "2026-10-31", CategoriaID: "internet", Grupo: "FIXO", Recorrente: true}}
	var atualizado contract.Gasto
	if err := c.Call(ctx, contract.OpAtualizarGasto, "u-1", in, &atualizado); err != nil {
		t.Fatal(err)
	}
	if atualizado.ID == criado.ID || atualizado.Data != "2026-10-31" || atualizado.Grupo != "FIXO" || !atualizado.Recorrente || atualizado.Descricao != "" {
		t.Errorf("atualizado = %+v", atualizado)
	}

	var pg contract.PaginaGastos
	if err := c.Call(ctx, contract.OpListarGastos, "u-1", contract.ListarGastosIn{Mes: "2026-10", Grupo: "FIXO"}, &pg); err != nil {
		t.Fatal(err)
	}
	if len(pg.Items) != 1 || pg.Items[0] != atualizado || pg.NextCursor != "" {
		t.Errorf("página = %+v", pg)
	}

	var existe contract.ExisteGastoNaCategoriaOut
	if err := c.Call(ctx, contract.OpExisteGastoNaCategoria, "u-1", contract.ExisteGastoNaCategoriaIn{CategoriaID: "internet"}, &existe); err != nil || !existe.Existe {
		t.Errorf("existe = %+v, %v", existe, err)
	}

	if err := c.Call(ctx, contract.OpExcluirGasto, "u-1", contract.ExcluirGastoIn{ID: atualizado.ID}, nil); err != nil {
		t.Fatal(err)
	}
	err := c.Call(ctx, contract.OpObterGasto, "u-1", contract.ObterGastoIn{ID: atualizado.ID}, &lido)
	if e := errs.As(err); e == nil || e.Kind != errs.KindNotFound || e.Code != contract.CodigoNaoEncontrado {
		t.Errorf("depois de excluir = %v", err)
	}
}

func TestErrosChegamAoBFF(t *testing.T) {
	c, _, _, _ := montar(t)

	in := almoco()
	in.ValorCentavos = 0
	in.Grupo = "OUTRO"
	err := c.Call(ctx, contract.OpCriarGasto, "u-1", in, nil)
	e := errs.As(err)
	if e == nil || e.Kind != errs.KindValidation || len(e.Fields) != 2 || e.Fields[0].Field != "valorCentavos" || e.Fields[1].Field != "grupo" {
		t.Errorf("validação = %+v", e)
	}

	err = c.Call(ctx, contract.OpCriarGasto, "", almoco(), nil)
	if e := errs.As(err); e == nil || e.Kind != errs.KindBadRequest || e.Code != contract.CodigoUsuarioAusente {
		t.Errorf("sem usuário = %v", err)
	}

	err = c.Call(ctx, contract.OpListarGastos, "u-1", contract.ListarGastosIn{Mes: "2026-11", Cursor: "v1.x.y"}, nil)
	if e := errs.As(err); e == nil || e.Kind != errs.KindBadRequest || e.Code != contract.CodigoCursorInvalido {
		t.Errorf("cursor = %v", err)
	}

	err = c.Call(ctx, contract.OpListarGastos, "u-1", contract.ListarGastosIn{Mes: "novembro"}, nil)
	if errs.KindOf(err) != errs.KindValidation {
		t.Errorf("mês = %v", err)
	}
}

func TestListarFormatoJSON(t *testing.T) {
	// Mês vazio: items sai como [] e nextCursor some (openapi: TelaGastos).
	_, r, _, _ := montar(t)
	payload, _ := json.Marshal(contract.ListarGastosIn{Mes: "2026-11"})
	resp := r.Dispatch(ctx, invoke.Request{Operation: contract.OpListarGastos, UserID: "u-1", Payload: payload})
	if string(resp.Payload) != `{"items":[]}` {
		t.Errorf("payload = %s", resp.Payload)
	}
}

func TestGastoFormatoJSON(t *testing.T) {
	// Sem descrição, o campo some; recorrente sempre aparece.
	c, _, _, _ := montar(t)
	in := almoco()
	in.Descricao = ""
	var raw json.RawMessage
	if err := c.Call(ctx, contract.OpCriarGasto, "u-1", in, &raw); err != nil {
		t.Fatal(err)
	}
	var campos map[string]any
	_ = json.Unmarshal(raw, &campos)
	if _, ok := campos["descricao"]; ok {
		t.Errorf("descricao presente: %s", raw)
	}
	if campos["recorrente"] != false || campos["categoriaId"] != "alimentacao" || campos["valorCentavos"] != float64(4590) {
		t.Errorf("gasto = %s", raw)
	}
}

func TestPaginacaoPeloContrato(t *testing.T) {
	c, _, _, _ := montar(t)
	for range 5 {
		if err := c.Call(ctx, contract.OpCriarGasto, "u-1", almoco(), nil); err != nil {
			t.Fatal(err)
		}
	}
	vistos := map[string]bool{}
	cursor := ""
	for {
		var pg contract.PaginaGastos
		if err := c.Call(ctx, contract.OpListarGastos, "u-1", contract.ListarGastosIn{Mes: "2026-11", Cursor: cursor, Limit: 2}, &pg); err != nil {
			t.Fatal(err)
		}
		for _, g := range pg.Items {
			vistos[g.ID] = true
		}
		if pg.NextCursor == "" {
			break
		}
		cursor = pg.NextCursor
	}
	if len(vistos) != 5 {
		t.Errorf("vistos = %d", len(vistos))
	}
}

func TestUserDeleted(t *testing.T) {
	c, _, ev, st := montar(t)
	if err := c.Call(ctx, contract.OpCriarGasto, "u-1", almoco(), nil); err != nil {
		t.Fatal(err)
	}
	detail, _ := json.Marshal(events.UserDeletedDetail{UserID: "u-1"})
	e := events.Event{ID: "e1", DetailType: events.UserDeleted, Source: "teste", Detail: detail}
	for range 2 { // idempotente
		if err := ev.Dispatch(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if st.Quantos("u-1") != 0 {
		t.Errorf("sobraram %d gastos", st.Quantos("u-1"))
	}
}
