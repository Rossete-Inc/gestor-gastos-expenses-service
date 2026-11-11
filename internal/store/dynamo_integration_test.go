//go:build integration

// Testes contra o DynamoDB Local do infra-base (task local:up). Cada teste
// cria uma tabela própria, com o mesmo esquema do seed, e a apaga no fim,
// sem tocar na tabela gestor-gastos-expenses usada no ambiente local.
//
//	go test -tags integration ./internal/store
package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/dynamotest"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/page"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var ctx = context.Background()

func tabelaDeTeste(t *testing.T) (*Dynamo, *dynamodb.Client, string) {
	t.Helper()
	c, nome := dynamotest.ExpensesTable(t, TableName)
	return NewDynamo(c, nome), c, nome
}

var relogio = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// lancar cria e grava um gasto; cada chamada avança o relógio 1 ms, então
// gastos do mesmo dia ficam em ordem de lançamento.
func lancar(t *testing.T, d *Dynamo, userID, data, categoria string, grupo gasto.Grupo, centavos int64) gasto.Gasto {
	t.Helper()
	relogio = relogio.Add(time.Millisecond)
	g, err := gasto.Novo(gasto.NovoULID(relogio), userID, gasto.Dados{
		ValorCentavos: centavos, Data: data, CategoriaID: categoria, Grupo: grupo, Descricao: "teste " + data,
	}, relogio)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Criar(ctx, g); err != nil {
		t.Fatal(err)
	}
	return g
}

func naoEncontrado(t *testing.T, nome string, err error) {
	t.Helper()
	if e := errs.As(err); e == nil || e.Kind != errs.KindNotFound || e.Code != "gasto_nao_encontrado" {
		t.Errorf("%s = %v, quero 404 gasto_nao_encontrado", nome, err)
	}
}

func TestCriarObterEFormatoDoItem(t *testing.T) {
	d, c, tabela := tabelaDeTeste(t)
	g := lancar(t, d, "u-1", "2026-11-03", "alimentacao", gasto.Cartao, 4590)

	lido, err := d.Obter(ctx, "u-1", g.ID)
	if err != nil || lido != g {
		t.Fatalf("obter = %+v, %v\nquero %+v", lido, err, g)
	}

	// O item tem as chaves do contrato e o valor como número inteiro.
	out, err := c.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tabela),
		Key:       map[string]types.AttributeValue{"PK": s("USER#u-1"), "SK": s("EXP#2026-11-03#" + g.ULID())},
	})
	if err != nil || out.Item == nil {
		t.Fatalf("item não está na chave esperada: %v", err)
	}
	if n, ok := out.Item["valorCentavos"].(*types.AttributeValueMemberN); !ok || n.Value != "4590" {
		t.Errorf("valorCentavos = %#v", out.Item["valorCentavos"])
	}
	for _, attr := range []string{"gastoId", "data", "grupo", "categoriaId", "descricao", "recorrente", "criadoEm"} {
		if _, ok := out.Item[attr]; !ok {
			t.Errorf("sem o atributo %s", attr)
		}
	}
	if _, ok := out.Item["atualizadoEm"]; ok {
		t.Error("atualizadoEm gravado num gasto nunca editado")
	}

	// O mesmo ULID de novo não sobrescreve.
	if err := d.Criar(ctx, g); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("criar repetido = %v", err)
	}
}

func TestIsolamento(t *testing.T) {
	// Cenário "Acesso a gasto alheio": a partition key do usuário A não
	// alcança o gasto de B.
	d, _, _ := tabelaDeTeste(t)
	deB := lancar(t, d, "u-b", "2026-11-03", "alimentacao", gasto.Cartao, 100)

	_, err := d.Obter(ctx, "u-a", deB.ID)
	naoEncontrado(t, "obter", err)
	naoEncontrado(t, "excluir", d.Excluir(ctx, "u-a", deB.ID))
	falso := deB
	falso.UserID = "u-a"
	naoEncontrado(t, "substituir", d.Substituir(ctx, falso, falso))

	itens, _, err := d.Listar(ctx, "u-a", gasto.Filtro{Mes: "2026-11"}, nil, 20)
	if err != nil || len(itens) != 0 {
		t.Errorf("listar = %d, %v", len(itens), err)
	}
	if existe, _ := d.ExisteNaCategoria(ctx, "u-a", "alimentacao"); existe {
		t.Error("A vê a categoria de B em uso")
	}
	if lido, err := d.Obter(ctx, "u-b", deB.ID); err != nil || lido != deB {
		t.Errorf("o gasto de B mudou: %+v, %v", lido, err)
	}
}

func TestSubstituir(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	g := lancar(t, d, "u-1", "2026-11-03", "alimentacao", gasto.Cartao, 4590)

	// Mesma data: atualiza no lugar.
	novo, err := g.Atualizado(gasto.Dados{ValorCentavos: 9990, Data: "2026-11-03", CategoriaID: "internet", Grupo: gasto.Fixo, Recorrente: true}, relogio.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Substituir(ctx, g, novo); err != nil {
		t.Fatal(err)
	}
	if lido, _ := d.Obter(ctx, "u-1", g.ID); lido != novo {
		t.Errorf("lido = %+v\nquero %+v", lido, novo)
	}

	// Data nova: muda de chave; o antigo some.
	movido, _ := novo.Atualizado(gasto.Dados{ValorCentavos: 9990, Data: "2026-12-01", CategoriaID: "internet", Grupo: gasto.Fixo}, relogio.Add(2*time.Hour))
	if err := d.Substituir(ctx, novo, movido); err != nil {
		t.Fatal(err)
	}
	_, err = d.Obter(ctx, "u-1", g.ID)
	naoEncontrado(t, "ID antigo", err)
	if lido, err := d.Obter(ctx, "u-1", movido.ID); err != nil || lido != movido {
		t.Errorf("movido = %+v, %v", lido, err)
	}

	// O antigo já não existe: nada é gravado.
	outraVez, _ := novo.Atualizado(gasto.Dados{ValorCentavos: 1, Data: "2026-10-01", CategoriaID: "x", Grupo: gasto.Cartao}, relogio)
	naoEncontrado(t, "substituir o que já mudou de data", d.Substituir(ctx, novo, outraVez))
	_, err = d.Obter(ctx, "u-1", outraVez.ID)
	naoEncontrado(t, "gravou mesmo assim", err)
}

func TestExcluir(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	g := lancar(t, d, "u-1", "2026-11-03", "alimentacao", gasto.Cartao, 4590)
	if err := d.Excluir(ctx, "u-1", g.ID); err != nil {
		t.Fatal(err)
	}
	naoEncontrado(t, "excluir de novo", d.Excluir(ctx, "u-1", g.ID))
	_, err := d.Obter(ctx, "u-1", g.ID)
	naoEncontrado(t, "obter", err)
}

func idsDe(gs []gasto.Gasto) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.ID)
	}
	return out
}

// listarTudo percorre todas as páginas.
func listarTudo(t *testing.T, d *Dynamo, userID string, f gasto.Filtro, limite int) ([]gasto.Gasto, int) {
	t.Helper()
	var todos []gasto.Gasto
	var inicio page.Key
	paginas := 0
	for {
		itens, prox, err := d.Listar(ctx, userID, f, inicio, limite)
		if err != nil {
			t.Fatal(err)
		}
		paginas++
		if len(itens) > limite {
			t.Fatalf("página com %d itens, limite %d", len(itens), limite)
		}
		todos = append(todos, itens...)
		if prox == nil {
			return todos, paginas
		}
		if paginas > 100 {
			t.Fatal("paginação não termina")
		}
		inicio = prox
	}
}

func TestListarOrdemMesEPaginacao(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	lancar(t, d, "u-1", "2026-10-31", "lazer", gasto.Cartao, 1) // outro mês
	lancar(t, d, "u-1", "2026-12-01", "lazer", gasto.Cartao, 1) // outro mês
	var nov []gasto.Gasto
	for _, data := range []string{"2026-11-05", "2026-11-01", "2026-11-30", "2026-11-05", "2026-11-15", "2026-11-05", "2026-11-01"} {
		nov = append(nov, lancar(t, d, "u-1", data, "lazer", gasto.Cartao, 1))
	}
	// Mais recente primeiro: data decrescente e, no mesmo dia, o último
	// lançado primeiro (o ULID cresce com o tempo).
	slices.SortFunc(nov, func(a, b gasto.Gasto) int {
		if c := strings.Compare(b.Data, a.Data); c != 0 {
			return c
		}
		return strings.Compare(b.ULID(), a.ULID())
	})

	todos, paginas := listarTudo(t, d, "u-1", gasto.Filtro{Mes: "2026-11"}, 3)
	if !slices.Equal(idsDe(todos), idsDe(nov)) {
		t.Errorf("ordem:\n%v\nquero\n%v", idsDe(todos), idsDe(nov))
	}
	if paginas != 3 {
		t.Errorf("páginas = %d", paginas)
	}

	// Página exata: 7 itens com limite 7 não deixam cursor.
	itens, prox, err := d.Listar(ctx, "u-1", gasto.Filtro{Mes: "2026-11"}, nil, 7)
	if err != nil || len(itens) != 7 || prox != nil {
		t.Errorf("página exata = %d itens, próximo %v, %v", len(itens), prox, err)
	}
}

func TestListarMesVazio(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	lancar(t, d, "u-1", "2026-10-31", "lazer", gasto.Cartao, 1)
	itens, prox, err := d.Listar(ctx, "u-1", gasto.Filtro{Mes: "2026-11"}, nil, 20)
	if err != nil || itens == nil || len(itens) != 0 || prox != nil {
		t.Errorf("mês vazio = %v, %v, %v", itens, prox, err)
	}
}

func TestListarFiltrosEsparsos(t *testing.T) {
	// 230 gastos de cartão e 5 fixos espalhados: a página de fixos precisa de
	// várias Queries (cada uma avalia até 100 itens) e não pode perder
	// nenhum nem repetir.
	d, _, _ := tabelaDeTeste(t)
	var fixos []gasto.Gasto
	for i := range 235 {
		data := fmt.Sprintf("2026-11-%02d", i%28+1)
		if i%47 == 0 {
			fixos = append(fixos, lancar(t, d, "u-1", data, "aluguel", gasto.Fixo, int64(i+1)))
			continue
		}
		lancar(t, d, "u-1", data, "lazer", gasto.Cartao, int64(i+1))
	}

	todos, _ := listarTudo(t, d, "u-1", gasto.Filtro{Mes: "2026-11", Grupo: gasto.Fixo}, 2)
	if len(todos) != len(fixos) {
		t.Fatalf("fixos = %d, quero %d", len(todos), len(fixos))
	}
	for _, g := range todos {
		if g.Grupo != gasto.Fixo || g.CategoriaID != "aluguel" {
			t.Errorf("fora do filtro: %+v", g)
		}
	}
	vistos := map[string]bool{}
	for _, g := range todos {
		if vistos[g.ID] {
			t.Errorf("repetido: %s", g.ID)
		}
		vistos[g.ID] = true
	}

	porCategoria, _ := listarTudo(t, d, "u-1", gasto.Filtro{Mes: "2026-11", CategoriaID: "lazer"}, 100)
	if len(porCategoria) != 230 {
		t.Errorf("lazer = %d", len(porCategoria))
	}
	ambos, _ := listarTudo(t, d, "u-1", gasto.Filtro{Mes: "2026-11", Grupo: gasto.Fixo, CategoriaID: "lazer"}, 20)
	if len(ambos) != 0 {
		t.Errorf("FIXO + lazer = %d", len(ambos))
	}
}

func TestListarRecusaChaveForaDoEscopo(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	lancar(t, d, "u-b", "2026-11-03", "lazer", gasto.Cartao, 1)
	ruins := []page.Key{
		{"PK": "USER#u-b", "SK": "EXP#2026-11-30#Z"}, // partição de outro usuário
		{"PK": "USER#u-a", "SK": "EXP#2026-12-01#Z"}, // outro mês
		{"PK": "USER#u-a", "SK": "EXP#2026-11-30#Z", "x": "y"},
	}
	for _, k := range ruins {
		_, _, err := d.Listar(ctx, "u-a", gasto.Filtro{Mes: "2026-11"}, k, 20)
		if e := errs.As(err); e == nil || e.Code != "cursor_invalido" {
			t.Errorf("chave %v = %v", k, err)
		}
	}
}

func TestExisteNaCategoria(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	for i := range 150 {
		lancar(t, d, "u-1", fmt.Sprintf("2026-%02d-01", i%12+1), "lazer", gasto.Cartao, 1)
	}
	lancar(t, d, "u-1", "2025-01-01", "pet", gasto.Cartao, 1) // o mais antigo de todos

	for cat, quero := range map[string]bool{"lazer": true, "pet": true, "aluguel": false} {
		existe, err := d.ExisteNaCategoria(ctx, "u-1", cat)
		if err != nil || existe != quero {
			t.Errorf("%s = %v, %v", cat, existe, err)
		}
	}
}

func TestExcluirDoUsuario(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	for i := range 60 { // mais de dois lotes de 25
		lancar(t, d, "u-1", fmt.Sprintf("2026-%02d-10", i%12+1), "lazer", gasto.Cartao, 1)
	}
	deOutro := lancar(t, d, "u-2", "2026-11-03", "lazer", gasto.Cartao, 1)

	n, err := d.ExcluirDoUsuario(ctx, "u-1")
	if err != nil || n != 60 {
		t.Fatalf("apagados = %d, %v", n, err)
	}
	// Idempotente: o mesmo evento de novo não encontra nada.
	if n, err := d.ExcluirDoUsuario(ctx, "u-1"); err != nil || n != 0 {
		t.Errorf("de novo = %d, %v", n, err)
	}
	for _, mes := range []string{"2026-01", "2026-11"} {
		if itens, _, _ := d.Listar(ctx, "u-1", gasto.Filtro{Mes: mes}, nil, 100); len(itens) != 0 {
			t.Errorf("sobrou em %s: %d", mes, len(itens))
		}
	}
	if lido, err := d.Obter(ctx, "u-2", deOutro.ID); err != nil || lido != deOutro {
		t.Errorf("apagou o de outro usuário: %v", err)
	}
}
