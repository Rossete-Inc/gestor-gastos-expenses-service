// Command local roda o expenses-service na máquina do desenvolvedor, contra
// o DynamoDB Local do infra-base (task local:up).
//
// É um servidor HTTP que chama os mesmos handlers da Lambda:
//
//	POST /invoke  envelope do invoke (chamadas do BFF)
//	POST /events  eventos (UserDeleted)
//	GET  /healthz
//
// Escuta só em 127.0.0.1: o /invoke confia no userId do envelope, e na AWS
// quem garante isso é o IAM, que deixa só o BFF invocar a Lambda.
//
//	RUNTIME=local go run ./cmd/local
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/handler"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/store"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/config"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/logging"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

const servico = "expenses-service"

// SegredoCursorLocal assina os cursores no ambiente local quando
// CURSOR_SECRET não é informado. Não é segredo de verdade: na AWS o segredo
// vem do SSM (tarefa 8.5).
const SegredoCursorLocal = "gestor-gastos-local-cursor-nao-e-segredo"

// Config é a configuração do cmd/local.
type Config struct {
	Base           config.Base
	Port           string
	DynamoEndpoint string
	Table          string
	CursorSecret   string
}

// LoadConfig lê o ambiente. Só aceita RUNTIME=local e um DynamoDB local, para
// nunca gravar numa tabela da AWS por engano.
func LoadConfig(lookup config.LookupFunc) (Config, error) {
	base, err := config.LoadFrom(lookup, servico)
	if err != nil {
		return Config{}, err
	}
	if !base.IsLocal() {
		return Config{}, errors.New("cmd/local exige RUNTIME=local")
	}
	r := config.NewReader(lookup)
	c := Config{
		Base:           base,
		Port:           r.Get("PORT", "8084"),
		DynamoEndpoint: r.Get("DYNAMODB_ENDPOINT", "http://localhost:8000"),
		Table:          r.Get("EXPENSES_TABLE", store.TableName),
		CursorSecret:   r.Get("CURSOR_SECRET", SegredoCursorLocal),
	}
	if err := exigirLocal(c.DynamoEndpoint); err != nil {
		return Config{}, err
	}
	if len(c.CursorSecret) < page.MinSecretBytes {
		return Config{}, fmt.Errorf("CURSOR_SECRET precisa de pelo menos %d bytes", page.MinSecretBytes)
	}
	return c, nil
}

// exigirLocal aceita só a própria máquina ou um container do compose, como o
// seed do infra-base.
func exigirLocal(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("DYNAMODB_ENDPOINT inválido: %q", endpoint)
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "dynamodb", "host.docker.internal":
		return nil
	}
	return fmt.Errorf("DYNAMODB_ENDPOINT %q não é local; o cmd/local só roda contra o ambiente local", endpoint)
}

// NewHandler monta as rotas sobre os mesmos routers usados pela Lambda.
func NewHandler(svc *app.Service, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(invoke.Path, handler.Invoke(svc, log).HTTPHandler())
	mux.Handle(events.Path, handler.Events(svc, log).HTTPHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func main() {
	if err := run(context.Background(), os.LookupEnv); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, lookup config.LookupFunc) error {
	cfg, err := LoadConfig(lookup)
	if err != nil {
		return err
	}
	log := logging.New(logging.Options{Service: servico, Env: cfg.Base.Env, Level: cfg.Base.LogLevel})

	cursores, err := page.NewCodec([]byte(cfg.CursorSecret))
	if err != nil {
		return err
	}
	db := dynamodb.New(dynamodb.Options{
		Region:       cfg.Base.Region,
		BaseEndpoint: aws.String(cfg.DynamoEndpoint),
		Credentials:  credentials.NewStaticCredentialsProvider("local", "local", ""),
	})
	svc := app.New(store.NewDynamo(db, cfg.Table), cursores, log)

	srv := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", cfg.Port),
		Handler:           NewHandler(svc, log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	erro := make(chan error, 1)
	go func() { erro <- srv.ListenAndServe() }()
	log.Info("expenses-service local no ar", "addr", "http://"+srv.Addr, "dynamodb", cfg.DynamoEndpoint, "table", cfg.Table)

	select {
	case err := <-erro:
		return err
	case <-ctx.Done():
		log.Info("encerrando")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
