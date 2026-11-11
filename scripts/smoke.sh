#!/usr/bin/env bash
# Smoke test do expenses-service depois do deploy, rodado pelo go-service.yml na
# raiz do repositório com as credenciais da role gh-deploy-<repo>-dev e
# AMBIENTE=dev.
#
# A role de deploy não pode invocar a Lambda (por IAM, só o BFF pode, porque o
# invoke confia no userId do envelope), então este teste confere o que ela
# alcança: os parâmetros que o BFF vai ler do SSM. Que a função sobe e
# responde já foi verificado pelo canary do CodeDeploy durante o deploy. O
# teste de ponta a ponta (GET /telas/gastos com token real) fica na esteira do BFF.
set -euo pipefail

ambiente="${AMBIENTE:?defina AMBIENTE (ex.: dev)}"
regiao="${AWS_REGION:-us-east-1}"
prefixo="/gestor-gastos/$ambiente/expenses"

ler() {
  if ! aws ssm get-parameter --name "$1" --region "$regiao" --query Parameter.Value --output text; then
    echo "::error::Parâmetro $1 não encontrado no SSM. O deploy da stack gestor-gastos-$ambiente-expenses terminou?" >&2
    return 1
  fi
}

arn=$(ler "$prefixo/function-arn")
tabela=$(ler "$prefixo/table-name")

if [[ "$arn" != arn:aws:lambda:"$regiao":*:function:gestor-gastos-"$ambiente"-expenses:live ]]; then
  echo "::error::$prefixo/function-arn não aponta para o alias live: $arn" >&2
  exit 1
fi
# O segredo dos cursores é criado à mão (README); sem ele a função nem sobe.
# Confere só o tipo: sem --with-decryption o valor vem cifrado e não é usado.
tipo=$(aws ssm get-parameter --name "$prefixo/cursor-secret" --region "$regiao"   --query Parameter.Type --output text 2>/dev/null || true)
if [[ "$tipo" != "SecureString" ]]; then
  echo "::error::$prefixo/cursor-secret não existe como SecureString (README, \"Na AWS\")." >&2
  exit 1
fi
if [[ "$tabela" != "gestor-gastos-$ambiente-expenses" ]]; then
  echo "::error::$prefixo/table-name inesperado: $tabela" >&2
  exit 1
fi

echo "Smoke test ok: $arn, tabela $tabela"
