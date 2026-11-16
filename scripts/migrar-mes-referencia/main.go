// Command migrar-mes-referencia converte os gastos para o formato de chave
// com o mês de referência (change cartoes-credito, tarefa 2.5). Roda uma vez
// em dev, depois do deploy da versão com leitura dupla; é idempotente.
//
//	# simula (só conta)
//	AWS_PROFILE=rossete.inc go run ./scripts/migrar-mes-referencia -tabela gestor-gastos-dev-expenses -simular
//	# migra
//	AWS_PROFILE=rossete.inc go run ./scripts/migrar-mes-referencia -tabela gestor-gastos-dev-expenses
//
// Contra o DynamoDB Local: -endpoint http://localhost:8000.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/dynamotest"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/migracao"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func main() {
	tabela := flag.String("tabela", "", "nome da tabela de gastos (ex.: gestor-gastos-dev-expenses)")
	simular := flag.Bool("simular", false, "só conta os gastos a migrar, sem gravar")
	endpoint := flag.String("endpoint", "", "endpoint do DynamoDB Local (vazio = AWS)")
	regiao := flag.String("regiao", "us-east-1", "região da AWS")
	flag.Parse()
	if *tabela == "" {
		flag.Usage()
		os.Exit(2)
	}

	ctx := context.Background()
	var api migracao.API
	if *endpoint != "" {
		api = dynamotest.Client(*endpoint)
	} else {
		cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(*regiao))
		if err != nil {
			log.Fatalf("credenciais da AWS: %v", err)
		}
		api = dynamodb.NewFromConfig(cfg)
	}

	r, err := migracao.Migrar(ctx, api, *tabela, *simular, func(s string) { fmt.Println(s) })
	fmt.Printf("lidos %d, no formato antigo %d, migrados %d, já migrados por outra execução %d\n",
		r.Lidos, r.Legados, r.Migrados, r.JaMigrados)
	if err != nil {
		log.Fatal(err)
	}
}
