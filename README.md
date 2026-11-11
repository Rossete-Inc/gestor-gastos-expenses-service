# gestor-gastos-expenses-service

Serviço de gastos do Gestor de Gastos (Go, Lambda, DynamoDB). Guarda os gastos de cartão e fixos de cada usuário, com valor em centavos, data, categoria, grupo, descrição opcional e o flag informativo de recorrência (spec `lancamento-gastos`).

## Estrutura

| Pacote | O que faz |
| --- | --- |
| `contract` | Contrato público com o BFF: nomes das operações, códigos de erro e formatos de entrada e saída. É o único pacote fora de `internal/` |
| `internal/gasto` | Domínio: validação do lançamento, formato do ID, grupos e filtro da listagem |
| `internal/app` | Casos de uso: criar, obter, atualizar, excluir, listar com cursor assinado, checar categoria em uso e consumir `UserDeleted` |
| `internal/store` | Store DynamoDB (chaves abaixo) |
| `internal/handler` | Liga os casos de uso ao `invoke.Router` e ao `events.Router` da go-commons, usados pela Lambda e pelo `cmd/local` |
| `cmd/local` | Servidor HTTP local que chama os mesmos handlers da Lambda (ver "Rodar localmente") |
| `internal/memstore` | Store em memória com a mesma semântica do DynamoDB, para os testes unitários |
| `internal/dynamotest` | Cria tabelas temporárias no DynamoDB Local para os testes de integração |
| `cmd/lambda` | Função na AWS: o mesmo `handler.Lambda` atende o invoke do BFF e o evento `UserDeleted` |
| `infra` | App AWS CDK (Go), módulo separado: stack `gestor-gastos-<env>-expenses` (ver "Na AWS") |

## Contrato com o BFF

Toda operação age sobre o `userId` do envelope, que o BFF tira do token. Envelope sem `userId` responde 400 `usuario_ausente`. Um gasto de outro usuário nunca é encontrado: responde 404 `gasto_nao_encontrado`, como se não existisse. Erros chegam como problem+json dentro do envelope.

| Operação | Payload | Resposta | Erros |
| --- | --- | --- | --- |
| `CriarGasto` | `GastoIn` | `Gasto` | 422 `validacao` com `errors[]` (`valorCentavos`, `data`, `categoriaId`, `grupo`, `descricao`) |
| `ObterGasto` | `{"id"}` | `Gasto` | 404 `gasto_nao_encontrado` |
| `AtualizarGasto` | `{"id"}` + campos do `GastoIn` | `Gasto` (com ID novo se a data mudou) | 422 `validacao`; 404 `gasto_nao_encontrado` |
| `ExcluirGasto` | `{"id"}` | sem payload | 404 `gasto_nao_encontrado` |
| `ListarGastos` | `{"mes","grupo?","categoriaId?","cursor?","limit?"}` | `{"items":[Gasto],"nextCursor?"}`, do mais recente para o mais antigo | 422 `validacao` (`mes`, `grupo`, `categoriaId`); 400 `cursor_invalido` |
| `ExisteGastoNaCategoria` | `{"categoriaId"}` | `{"existe":bool}` | 422 `validacao` |
| `ResumoMensal` | `{"mes"}` | `ResumoMensalOut` (abaixo) | 422 `validacao` (`mes`) |

- `GastoIn` é `{"valorCentavos","data","categoriaId","grupo","descricao?","recorrente"}`: o `GastoIn` do `openapi.yaml` do BFF mais o `grupo` da categoria.
- `Gasto` é `{"id","valorCentavos","data","grupo","categoriaId","descricao?","recorrente"}`. O BFF troca o `categoriaId` pelo resumo da categoria (nome e cor) do schema `Gasto` do openapi.
- `ResumoMensalOut` é `{"mes","totalCentavos","totaisPorGrupo":[{"grupo","totalCentavos"}],"totaisPorCategoria":[{"categoriaId","grupo","totalCentavos"}],"recentes":[Gasto]}`. `totaisPorGrupo` traz sempre `CARTAO` e `FIXO`, nessa ordem, mesmo zerados. `totaisPorCategoria` só tem categorias com gasto no mês, do maior total para o menor. `recentes` são até 5 gastos, na ordem da listagem. As duas somas por grupo e por categoria fecham exatamente com `totalCentavos`. Mês sem gastos devolve totais zerados e listas `[]`. O nome e a cor das categorias e os percentuais (método do maior resto) ficam com o BFF, em `/telas/dashboard`.
- A existência e a visibilidade da categoria são checadas pelo BFF no categories-service antes de `CriarGasto` e `AtualizarGasto`. O BFF também chama `ExisteGastoNaCategoria` antes de excluir uma categoria (409 `categoria_em_uso`). Ver design.md, "Regras que atravessam serviços ficam no BFF".

Evento consumido: `UserDeleted` (`{"userId"}`), que apaga todos os gastos do usuário em lotes. É idempotente.

## Regras

- **Valor:** inteiro em centavos, de 1 (R$ 0,01) a 100.000.000 (R$ 1.000.000,00). Nunca passa por float; somas usam o pacote `money` da go-commons.
- **Data:** `AAAA-MM-DD`, uma data que existe no calendário, a partir de 1900. `2026-02-30` e `2026-2-3` são recusadas.
- **Descrição:** opcional, até 140 caracteres, gravada em NFC e sem espaços nas pontas. Quebras de linha e outros caracteres de controle são recusados.
- **Grupo:** `CARTAO` ou `FIXO`, o grupo da categoria, gravado no gasto para filtrar e somar sem consultar o categories-service.
- **Recorrente:** só informativo no MVP.
- **ID:** a data compacta, um hífen e um [ULID](https://github.com/ulid/spec), por exemplo `20261103-01JC8Z6Q3N4W5X6Y7Z8A9B0C1D`. A data no ID leva à chave do DynamoDB sem índice secundário. O ULID ordena os gastos do mesmo dia pela hora do lançamento e não muda na edição. **Se a data muda, o ID muda**: o gasto troca de chave numa transação e o antigo deixa de existir.
- **Listagem:** 20 itens por página por padrão, no máximo 100. O cursor é a chave do último gasto devolvido, em base64url e assinada com HMAC sobre o escopo da listagem (usuário, mês, grupo e categoria). Um cursor adulterado, de outra listagem ou de outro segredo responde 400 `cursor_invalido`.

## DynamoDB

Tabela `gestor-gastos-expenses`, com as chaves do contrato com o seed do infra-base e o CDK (`docs/local.md` do infra-base):

| Item | PK | SK |
| --- | --- | --- |
| Gasto | `USER#<userId>` | `EXP#<AAAA-MM-DD>#<ULID>` |

Atributos: `gastoId`, `valorCentavos` (número inteiro), `data`, `grupo`, `categoriaId`, `descricao` (só se houver), `recorrente`, `criadoEm` e `atualizadoEm` (só depois da primeira edição).

- **Isolamento:** tudo do usuário fica na partição dele. Ler, editar ou excluir o gasto de outro usuário não encontra nada.
- **Listagem do mês:** uma Query com `begins_with(SK, "EXP#AAAA-MM-")` em ordem decrescente. Os filtros por grupo e categoria usam `FilterExpression`. Como o `Limit` do DynamoDB conta os itens antes do filtro, o store monta a página com Queries de até 100 itens até juntar o tamanho pedido. Se a página enche exatamente no fim de uma Query que ainda tinha continuação, o cursor é devolvido mesmo sem haver mais gastos, e a página seguinte pode vir vazia, sem cursor.
- **Resumo do mês:** uma Query com `begins_with(SK, "EXP#AAAA-MM-")`, seguindo as continuações até o fim do mês, e as somas em memória. O volume pessoal (dezenas a centenas de gastos por mês) cabe nisso; o gatilho para materializar os totais via Streams está no ADR 0002.
- **Categoria em uso:** uma Query na partição do usuário só com as chaves, filtrando por `categoriaId` e parando no primeiro gasto encontrado.
- Leituras consistentes em todas as operações, para um gasto recém-lançado aparecer na hora.

## Rodar localmente

Com o ambiente local do infra-base no ar (`task local:up` e `task local:seed`, que cria a tabela):

```sh
RUNTIME=local go run ./cmd/local
```

O servidor sobe em http://127.0.0.1:8084 e chama os mesmos handlers da Lambda:

| Rota | Uso |
| --- | --- |
| `POST /invoke` | Envelope do `invoke`, chamado pelo BFF |
| `POST /events` | Eventos, como `UserDeleted` |
| `GET /healthz` | Responde `ok` |

| Variável | Padrão |
| --- | --- |
| `RUNTIME` | obrigatória; só aceita `local` |
| `PORT` | `8084` |
| `DYNAMODB_ENDPOINT` | `http://localhost:8000`; endpoints fora da máquina são recusados |
| `EXPENSES_TABLE` | `gestor-gastos-expenses` |
| `CURSOR_SECRET` | um valor fixo só para o ambiente local; precisa de pelo menos 32 bytes. Na AWS vem do SSM (ver "Na AWS") |
| `LOG_LEVEL` | `info` |

**Segurança local:** o `/invoke` confia no `userId` do envelope. Na AWS, só o BFF consegue invocar a Lambda, por IAM. Localmente, o servidor escuta **só em 127.0.0.1**.

Exemplos com `curl`:

```sh
# lançar um gasto de cartão; a resposta traz o id gerado
curl -s -X POST localhost:8084/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"CriarGasto","userId":"u-demo","payload":{"valorCentavos":4590,"data":"2026-11-03","categoriaId":"alimentacao","grupo":"CARTAO","descricao":"Almoço"}}'

# valor zero: 422 validacao no campo valorCentavos
curl -s -X POST localhost:8084/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"CriarGasto","userId":"u-demo","payload":{"valorCentavos":0,"data":"2026-11-03","categoriaId":"alimentacao","grupo":"CARTAO"}}'

# listar novembro, só os fixos, 10 por página (repita com "cursor" para a próxima)
curl -s -X POST localhost:8084/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"ListarGastos","userId":"u-demo","payload":{"mes":"2026-11","grupo":"FIXO","limit":10}}'

# editar (troque <id>); mudar a data devolve um id novo
curl -s -X POST localhost:8084/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"AtualizarGasto","userId":"u-demo","payload":{"id":"<id>","valorCentavos":9990,"data":"2026-11-04","categoriaId":"internet","grupo":"FIXO"}}'

# resumo de novembro para o painel
curl -s -X POST localhost:8084/invoke -H 'Content-Type: application/json'   -d '{"operation":"ResumoMensal","userId":"u-demo","payload":{"mes":"2026-11"}}'

# a categoria está em uso?
curl -s -X POST localhost:8084/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"ExisteGastoNaCategoria","userId":"u-demo","payload":{"categoriaId":"internet"}}'

# excluir
curl -s -X POST localhost:8084/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"ExcluirGasto","userId":"u-demo","payload":{"id":"<id>"}}'

# evento de usuário excluído: apaga todos os gastos dele e responde 204
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8084/events -H 'Content-Type: application/json' \
  -d '{"id":"e1","detail-type":"UserDeleted","source":"curl","time":"2026-11-11T18:30:00Z","detail":{"userId":"u-demo"}}'
```

Para inspecionar a tabela, abra o dynamodb-admin em http://localhost:8001.

## Na AWS

A esteira (`.github/workflows/ci.yml`, sobre o `go-service.yml` do infra-base) implanta a stack `gestor-gastos-dev-expenses` a cada merge na `main`, no padrão do categories-service:

| Recurso | Detalhe |
| --- | --- |
| Tabela `gestor-gastos-dev-expenses` | On-demand, `RETAIN`, chaves da seção DynamoDB, sem índices |
| Lambda `gestor-gastos-dev-expenses` | arm64, 256 MB, 10 s, alias `live` com canary do CodeDeploy (10% por 5 min) e rollback pelo alarme de erros; logs por 7 dias |
| Regra `gestor-gastos-dev-expenses-user-deleted` | `source` `gestor-gastos.users`, `detail-type` `UserDeleted`, com retentativas e a DLQ do barramento |
| SSM publicado | `/gestor-gastos/dev/expenses/function-arn` (lido pelo BFF) e `/gestor-gastos/dev/expenses/table-name` |
| SSM lido | `events/bus-name` e `events/dlq-arn` (stack `events` do infra-base) e o segredo dos cursores (abaixo) |

**Segredo dos cursores.** A função lê, ao iniciar, o SecureString `/gestor-gastos/<env>/expenses/cursor-secret` (pelo menos 32 bytes), descriptografado, e só tem `ssm:GetParameter` nele. O CloudFormation não cria SecureString, então ele é criado uma vez à mão, antes do primeiro deploy (em `dev`, criado em 2026-11-11):

```sh
aws ssm put-parameter --name /gestor-gastos/dev/expenses/cursor-secret --type SecureString   --value "$(openssl rand -base64 48)" --region us-east-1
```

Trocar o valor (`--overwrite`) invalida os cursores em uso: quem estiver paginando recebe 400 `cursor_invalido` e volta à primeira página. Não há outro efeito.

O smoke test confere os parâmetros publicados e o tipo do segredo, sem ler o valor. A role de deploy não invoca a Lambda; o teste de ponta a ponta fica na esteira do BFF. Os testes de assertion do CDK rodam com `cd infra && go test ./...`, sem AWS.

## Testes

```sh
go test ./... -cover
```

Os testes de integração (store e `cmd/local` de ponta a ponta) rodam contra o DynamoDB Local do `gestor-gastos-infra-base`. Cada teste cria e apaga uma tabela própria, sem tocar na `gestor-gastos-expenses`:

```sh
# no infra-base
task local:up
# aqui
go test -tags integration ./... -v
```

A esteira roda vet, lint e os testes unitários e de integração em todo PR, com um DynamoDB Local como serviço e cobertura mínima de 80% por pacote, além do `cdk diff` contra `dev`.
