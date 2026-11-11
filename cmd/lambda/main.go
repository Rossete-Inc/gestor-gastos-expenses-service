// Command lambda é a função do expenses-service na AWS (stack
// gestor-gastos-<env>-expenses, em infra/). Uma função só atende o invoke do
// BFF e o evento UserDeleted, pelo handler.Lambda.
//
// Variáveis de ambiente (definidas pelo CDK):
//
//	RUNTIME=aws                 só roda na AWS; localmente use o cmd/local
//	ENV=dev                     ambiente
//	EXPENSES_TABLE=...          tabela DynamoDB dos gastos
//	CURSOR_SECRET_PARAM=...     caminho do SecureString com o segredo dos cursores
//	LOG_LEVEL=info              opcional
//
// Build (o mesmo do go-service.yml):
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -trimpath -ldflags="-s -w" \
//	  -o dist/lambda/bootstrap ./cmd/lambda
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/handler"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/store"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/config"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/logging"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const servico = "expenses-service"

// Config é a configuração da Lambda.
type Config struct {
	Base              config.Base
	Table             string
	CursorSecretParam string
}

// LoadConfig lê o ambiente. Só aceita RUNTIME=aws e informa de uma vez todas
// as variáveis que faltam.
func LoadConfig(lookup config.LookupFunc) (Config, error) {
	base, err := config.LoadFrom(lookup, servico)
	if err != nil {
		return Config{}, err
	}
	if base.IsLocal() {
		return Config{}, errors.New("cmd/lambda exige RUNTIME=aws; localmente use o cmd/local")
	}
	r := config.NewReader(lookup)
	c := Config{Base: base, Table: r.Require("EXPENSES_TABLE"), CursorSecretParam: r.Require("CURSOR_SECRET_PARAM")}
	if err := r.Err(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// SSMAPI é o subconjunto do client SSM usado aqui.
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opt ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// lerSegredo lê o SecureString dos cursores, descriptografado. O valor nunca
// vai para o log.
func lerSegredo(ctx context.Context, api SSMAPI, nome string) (*page.Codec, error) {
	out, err := api.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(nome), WithDecryption: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("ler o segredo dos cursores em %s (criado à mão, ver README): %w", nome, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return nil, fmt.Errorf("segredo dos cursores vazio em %s", nome)
	}
	codec, err := page.NewCodec([]byte(*out.Parameter.Value))
	if err != nil {
		return nil, fmt.Errorf("segredo dos cursores em %s: %w", nome, err)
	}
	return codec, nil
}

// clientes são as dependências da AWS, trocadas nos testes.
type clientes struct {
	ssm    SSMAPI
	dynamo store.API
}

func clientesAWS(ctx context.Context, region string) (clientes, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return clientes{}, fmt.Errorf("carregar configuração da AWS: %w", err)
	}
	return clientes{ssm: ssm.NewFromConfig(cfg), dynamo: dynamodb.NewFromConfig(cfg)}, nil
}

// novoHandler monta o handler da função.
func novoHandler(ctx context.Context, lookup config.LookupFunc, novosClientes func(context.Context, string) (clientes, error)) (handler.LambdaFunc, error) {
	cfg, err := LoadConfig(lookup)
	if err != nil {
		return nil, err
	}
	c, err := novosClientes(ctx, cfg.Base.Region)
	if err != nil {
		return nil, err
	}
	cursores, err := lerSegredo(ctx, c.ssm, cfg.CursorSecretParam)
	if err != nil {
		return nil, err
	}
	log := logging.New(logging.Options{Service: servico, Env: cfg.Base.Env, Level: cfg.Base.LogLevel})
	svc := app.New(store.NewDynamo(c.dynamo, cfg.Table), cursores, log)
	return handler.Lambda(svc, log), nil
}

// executar monta o handler e o entrega a iniciar (lambda.Start). Devolve o
// código de saída do processo.
func executar(ctx context.Context, lookup config.LookupFunc, novosClientes func(context.Context, string) (clientes, error), iniciar func(any)) int {
	h, err := novoHandler(ctx, lookup, novosClientes)
	if err != nil {
		slog.Error("inicialização falhou", "erro", err.Error())
		return 1
	}
	iniciar(h)
	return 0
}

func main() {
	os.Exit(executar(context.Background(), os.LookupEnv, clientesAWS, lambda.Start))
}
