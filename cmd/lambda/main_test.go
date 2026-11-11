package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/handler"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

var envValido = map[string]string{
	"RUNTIME": "aws", "ENV": "dev",
	"EXPENSES_TABLE": "gestor-gastos-dev-expenses", "CURSOR_SECRET_PARAM": "/gestor-gastos/dev/expenses/cursor-secret",
}

// ssmFalso devolve o valor configurado e guarda o pedido.
type ssmFalso struct {
	valor  *string
	err    error
	pedido *ssm.GetParameterInput
}

func (s *ssmFalso) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	s.pedido = in
	if s.err != nil {
		return nil, s.err
	}
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{Value: s.valor}}, nil
}

func com(s *ssmFalso) func(context.Context, string) (clientes, error) {
	return func(context.Context, string) (clientes, error) {
		return clientes{ssm: s, dynamo: dynamodb.New(dynamodb.Options{Region: "us-east-1"})}, nil
	}
}

func TestLoadConfig(t *testing.T) {
	c, err := LoadConfig(env(envValido))
	if err != nil {
		t.Fatal(err)
	}
	if c.Table != "gestor-gastos-dev-expenses" || c.CursorSecretParam != "/gestor-gastos/dev/expenses/cursor-secret" || c.Base.Region != "us-east-1" {
		t.Errorf("config = %+v", c)
	}
}

func TestLoadConfigRecusa(t *testing.T) {
	casos := map[string]map[string]string{
		"sem RUNTIME":   {"ENV": "dev", "EXPENSES_TABLE": "t", "CURSOR_SECRET_PARAM": "p"},
		"RUNTIME=local": {"RUNTIME": "local", "EXPENSES_TABLE": "t", "CURSOR_SECRET_PARAM": "p"},
		"sem tabela":    {"RUNTIME": "aws", "ENV": "dev", "CURSOR_SECRET_PARAM": "p"},
		"sem segredo":   {"RUNTIME": "aws", "ENV": "dev", "EXPENSES_TABLE": "t"},
		"sem ENV":       {"RUNTIME": "aws", "EXPENSES_TABLE": "t", "CURSOR_SECRET_PARAM": "p"},
	}
	for nome, m := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := LoadConfig(env(m)); err == nil {
				t.Error("esperava erro")
			}
		})
	}
}

func TestExecutarLeOSegredoDescriptografadoEEntregaOHandler(t *testing.T) {
	s := &ssmFalso{valor: aws.String(strings.Repeat("s", 48))}
	var recebido any
	if codigo := executar(context.Background(), env(envValido), com(s), func(h any) { recebido = h }); codigo != 0 {
		t.Fatalf("código = %d", codigo)
	}
	if _, ok := recebido.(handler.LambdaFunc); !ok {
		t.Errorf("handler = %T", recebido)
	}
	if aws.ToString(s.pedido.Name) != "/gestor-gastos/dev/expenses/cursor-secret" || !aws.ToBool(s.pedido.WithDecryption) {
		t.Errorf("pedido = %+v", s.pedido)
	}
}

func TestExecutarFalhaSemSegredoValido(t *testing.T) {
	casos := map[string]*ssmFalso{
		"SSM fora":     {err: errors.New("AccessDenied")},
		"vazio":        {},
		"curto demais": {valor: aws.String("curto")},
	}
	for nome, s := range casos {
		t.Run(nome, func(t *testing.T) {
			chamou := false
			if codigo := executar(context.Background(), env(envValido), com(s), func(any) { chamou = true }); codigo != 1 || chamou {
				t.Errorf("código = %d, iniciou = %v", codigo, chamou)
			}
		})
	}
}

func TestExecutarFalhaSemConfigOuSemAWS(t *testing.T) {
	if codigo := executar(context.Background(), env(map[string]string{}), com(&ssmFalso{}), func(any) {}); codigo != 1 {
		t.Errorf("sem config: código = %d", codigo)
	}
	semAWS := func(context.Context, string) (clientes, error) { return clientes{}, errors.New("sem credenciais") }
	if codigo := executar(context.Background(), env(envValido), semAWS, func(any) {}); codigo != 1 {
		t.Errorf("sem AWS: código = %d", codigo)
	}
}

func TestClientesAWS(t *testing.T) {
	c, err := clientesAWS(context.Background(), "us-east-1")
	if err != nil || c.ssm == nil || c.dynamo == nil {
		t.Errorf("clientes = %+v, %v", c, err)
	}
}
