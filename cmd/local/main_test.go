package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/memstore"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadConfigPadroes(t *testing.T) {
	c, err := LoadConfig(env(map[string]string{"RUNTIME": "local"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "8084" || c.DynamoEndpoint != "http://localhost:8000" || c.Table != "gestor-gastos-expenses" ||
		c.Base.Env != "local" || c.CursorSecret != SegredoCursorLocal {
		t.Errorf("config = %+v", c)
	}
	if len(SegredoCursorLocal) < page.MinSecretBytes {
		t.Errorf("SegredoCursorLocal tem %d bytes", len(SegredoCursorLocal))
	}
	c, err = LoadConfig(env(map[string]string{"RUNTIME": "local", "EXPENSES_TABLE": "t", "PORT": "9000", "CURSOR_SECRET": strings.Repeat("s", 32)}))
	if err != nil || c.Table != "t" || c.Port != "9000" || c.CursorSecret != strings.Repeat("s", 32) {
		t.Errorf("config = %+v, %v", c, err)
	}
}

func TestLoadConfigRecusa(t *testing.T) {
	casos := map[string]map[string]string{
		"sem RUNTIME":          {},
		"RUNTIME=aws":          {"RUNTIME": "aws", "ENV": "dev"},
		"DynamoDB da AWS":      {"RUNTIME": "local", "DYNAMODB_ENDPOINT": "https://dynamodb.us-east-1.amazonaws.com"},
		"endpoint sem esquema": {"RUNTIME": "local", "DYNAMODB_ENDPOINT": "localhost:8000"},
		"endpoint malformado":  {"RUNTIME": "local", "DYNAMODB_ENDPOINT": "http://[::1"},
		"segredo curto":        {"RUNTIME": "local", "CURSOR_SECRET": "curto"},
	}
	for nome, m := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := LoadConfig(env(m)); err == nil {
				t.Error("esperava erro")
			}
		})
	}
	t.Run("endpoints locais aceitos", func(t *testing.T) {
		for _, e := range []string{"http://127.0.0.1:8000", "http://[::1]:8000", "http://dynamodb:8000", "http://host.docker.internal:8000"} {
			if _, err := LoadConfig(env(map[string]string{"RUNTIME": "local", "DYNAMODB_ENDPOINT": e})); err != nil {
				t.Errorf("%s: %v", e, err)
			}
		}
	})
}

func TestRunConfigInvalida(t *testing.T) {
	if err := run(context.Background(), env(map[string]string{})); err == nil {
		t.Error("esperava erro sem RUNTIME")
	}
}

func TestRunPortaOcupada(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	_, porta, _ := net.SplitHostPort(l.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := run(ctx, env(map[string]string{"RUNTIME": "local", "LOG_LEVEL": "error", "PORT": porta})); err == nil {
		t.Error("esperava erro com a porta ocupada")
	}
}

func TestRotas(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cursores, _ := page.NewCodec([]byte(SegredoCursorLocal))
	srv := httptest.NewServer(NewHandler(app.New(memstore.New(), cursores, log), log))
	t.Cleanup(srv.Close)

	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || strings.TrimSpace(string(b)) != "ok" {
			t.Errorf("healthz = %d %q", resp.StatusCode, b)
		}
	})

	t.Run("invoke", func(t *testing.T) {
		bff := invoke.NewClient(invoke.NewHTTP(srv.URL, nil))
		var g contract.Gasto
		in := contract.GastoIn{ValorCentavos: 4590, Data: "2026-11-03", CategoriaID: "alimentacao", Grupo: "CARTAO"}
		if err := bff.Call(context.Background(), contract.OpCriarGasto, "u-1", in, &g); err != nil || g.ID == "" {
			t.Fatalf("criar = %+v, %v", g, err)
		}
		var pg contract.PaginaGastos
		if err := bff.Call(context.Background(), contract.OpListarGastos, "u-1", contract.ListarGastosIn{Mes: "2026-11"}, &pg); err != nil || len(pg.Items) != 1 {
			t.Errorf("listar = %+v, %v", pg, err)
		}
	})

	t.Run("events", func(t *testing.T) {
		body := `{"id":"e1","detail-type":"UserDeleted","source":"t","detail":{"userId":"u-1"}}`
		resp, err := http.Post(srv.URL+"/events", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("status = %d", resp.StatusCode)
		}
	})

	t.Run("rota inexistente", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/gastos")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d", resp.StatusCode)
		}
	})
}
