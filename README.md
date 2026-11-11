# gestor-gastos-expenses-service

Serviço de gastos do Gestor de Gastos (Go, Lambda, DynamoDB). Guarda os gastos de cartão e fixos de cada usuário, com valor em centavos, data, categoria, grupo, descrição opcional e o flag informativo de recorrência (spec `lancamento-gastos`).

## Estrutura

| Pacote | O que faz |
| --- | --- |
| `contract` | Contrato público com o BFF: nomes das operações, códigos de erro e formatos de entrada e saída. É o único pacote fora de `internal/` |
| `internal/gasto` | Domínio: validação do lançamento, formato do ID, grupos e filtro da listagem |
| `internal/app` | Casos de uso: criar, obter, atualizar, excluir, listar com cursor assinado, checar categoria em uso e consumir `UserDeleted` |
| `internal/store` | Store DynamoDB (chaves abaixo) |
| `internal/handler` | Liga os casos de uso ao `invoke.Router` e ao `events.Router` da go-commons, usados pelo `cmd/local` e, na tarefa 8.5, pela Lambda |
| `cmd/local` | Servidor HTTP local que chama os mesmos handlers da Lambda (ver "Rodar localmente") |
| `internal/memstore` | Store em memória com a mesma semântica do DynamoDB, para os testes unitários |
| `internal/dynamotest` | Cria tabelas temporárias no DynamoDB Local para os testes de integração |

A Lambda (`cmd/lambda`), o CDK (`infra/`), o deploy em `dev` e o smoke test entram na tarefa 8.5, no padrão do categories-service.

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

- `GastoIn` é `{"valorCentavos","data","categoriaId","grupo","descricao?","recorrente"}`: o `GastoIn` do `openapi.yaml` do BFF mais o `grupo` da categoria.
- `Gasto` é `{"id","valorCentavos","data","grupo","categoriaId","descricao?","recorrente"}`. O BFF troca o `categoriaId` pelo resumo da categoria (nome e cor) do schema `Gasto` do openapi.
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
| `CURSOR_SECRET` | um valor fixo só para o ambiente local; precisa de pelo menos 32 bytes. Na AWS vem do SSM (tarefa 8.5) |
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

A esteira (`.github/workflows/ci.yml`, sobre o `go-service.yml` do infra-base) roda vet, lint e os testes unitários e de integração em todo PR, com um DynamoDB Local como serviço e cobertura mínima de 80% por pacote.
