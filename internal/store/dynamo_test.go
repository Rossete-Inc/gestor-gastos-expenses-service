package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// falso é um client DynamoDB que devolve respostas fixas, para os caminhos
// de erro que o DynamoDB Local não reproduz.
type falso struct {
	err        error
	getItem    map[string]types.AttributeValue
	queries    []*dynamodb.QueryOutput
	batch      []*dynamodb.BatchWriteItemOutput
	nQuery     int
	nBatch     int
	transacoes int
}

func (f *falso) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: f.getItem}, f.err
}

func (f *falso) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, f.err
}

func (f *falso) DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, f.err
}

func (f *falso) Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := f.queries[min(f.nQuery, len(f.queries)-1)]
	f.nQuery++
	return out, nil
}

func (f *falso) BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := f.batch[min(f.nBatch, len(f.batch)-1)]
	f.nBatch++
	return out, nil
}

func (f *falso) TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.transacoes++
	return &dynamodb.TransactWriteItemsOutput{}, f.err
}

func novoFalso(f *falso) *Dynamo {
	d := NewDynamo(f, "t")
	d.espera = func(int) {}
	return d
}

const ulidTeste = "01JC8Z6Q3N4W5X6Y7Z8A9B0C1D"

func gastoTeste(t *testing.T, data string) gasto.Gasto {
	t.Helper()
	g, err := gasto.Novo(ulidTeste, "u-1", gasto.Dados{ValorCentavos: 10, Data: data, CategoriaID: "c", Grupo: gasto.Cartao}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

var errRede = errors.New("rede fora")

func interno(t *testing.T, nome string, err error) {
	t.Helper()
	if errs.KindOf(err) != errs.KindInternal {
		t.Errorf("%s = %v, quero erro interno", nome, err)
	}
}

func TestErrosDoClientViramInternos(t *testing.T) {
	d := novoFalso(&falso{err: errRede})
	g := gastoTeste(t, "2026-11-03")
	interno(t, "criar", d.Criar(context.Background(), g))
	_, err := d.Obter(context.Background(), "u-1", g.ID)
	interno(t, "obter", err)
	interno(t, "substituir", d.Substituir(context.Background(), g, g))
	outro := g
	outro.ID = gasto.IDDe("2026-11-04", ulidTeste)
	interno(t, "substituir com data nova", d.Substituir(context.Background(), g, outro))
	interno(t, "excluir", d.Excluir(context.Background(), "u-1", g.ID))
	_, _, err = d.Listar(context.Background(), "u-1", gasto.Filtro{Mes: "2026-11"}, nil, 10)
	interno(t, "listar", err)
	_, err = d.ExisteNaCategoria(context.Background(), "u-1", "c")
	interno(t, "existe", err)
	_, err = d.ExcluirDoUsuario(context.Background(), "u-1")
	interno(t, "excluir do usuário", err)
}

func TestCondicaoFalhaViraNaoEncontrado(t *testing.T) {
	ccf := &types.ConditionalCheckFailedException{Message: aws.String("x")}
	d := novoFalso(&falso{err: ccf})
	g := gastoTeste(t, "2026-11-03")
	if err := d.Substituir(context.Background(), g, g); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("substituir = %v", err)
	}
	if err := d.Excluir(context.Background(), "u-1", g.ID); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("excluir = %v", err)
	}

	tce := &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}, {Code: aws.String("None")}}}
	d = novoFalso(&falso{err: tce})
	outro := g
	outro.ID = gasto.IDDe("2026-11-04", ulidTeste)
	if err := d.Substituir(context.Background(), g, outro); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("substituir com data nova = %v", err)
	}
	// A condição do Put (ULID repetido) não é "não encontrado".
	tce = &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("None")}, {Code: aws.String("ConditionalCheckFailed")}}}
	d = novoFalso(&falso{err: tce})
	interno(t, "substituir com colisão", d.Substituir(context.Background(), g, outro))
}

func TestSubstituirOutroGasto(t *testing.T) {
	f := &falso{}
	d := novoFalso(f)
	g := gastoTeste(t, "2026-11-03")
	outroUsuario := g
	outroUsuario.UserID = "u-2"
	interno(t, "outro usuário", d.Substituir(context.Background(), g, outroUsuario))
	outroULID := g
	outroULID.ID = gasto.IDDe(g.Data, "01JC8Z6Q3N4W5X6Y7Z8A9B0C1E")
	interno(t, "outro ULID", d.Substituir(context.Background(), g, outroULID))
	if f.transacoes != 0 {
		t.Error("gravou")
	}
}

func TestGastoMalformadoNaoGrava(t *testing.T) {
	d := novoFalso(&falso{})
	g := gastoTeste(t, "2026-11-03")
	semID := g
	semID.ID = "x"
	interno(t, "sem ID", d.Criar(context.Background(), semID))
	semDono := g
	semDono.UserID = ""
	interno(t, "sem dono", d.Criar(context.Background(), semDono))
	interno(t, "substituir sem dono", d.Substituir(context.Background(), semDono, semDono))
	if err := d.Excluir(context.Background(), "u-1", "x"); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("excluir ID ruim = %v", err)
	}
	if _, err := d.Obter(context.Background(), "u-1", "x"); errs.KindOf(err) != errs.KindNotFound {
		t.Errorf("obter ID ruim = %v", err)
	}
	interno(t, "substituir ID ruim", d.Substituir(context.Background(), semID, semID))
	_, err := d.ExcluirDoUsuario(context.Background(), " ")
	interno(t, "excluir do usuário vazio", err)
}

func TestItemCorrompido(t *testing.T) {
	ruins := map[string]map[string]types.AttributeValue{
		"criadoEm": {"PK": s("USER#u-1"), "SK": s("EXP#x"), "gastoId": s("x"), "criadoEm": s("ontem")},
		"atualizadoEm": {"PK": s("USER#u-1"), "SK": s("EXP#x"), "gastoId": s("x"),
			"criadoEm": s("2026-11-11T12:00:00.000Z"), "atualizadoEm": s("ontem")},
		"tipo do valor": {"PK": s("USER#u-1"), "valorCentavos": s("dez")},
	}
	for nome, raw := range ruins {
		t.Run(nome, func(t *testing.T) {
			d := novoFalso(&falso{getItem: raw, queries: []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{raw}}}})
			_, err := d.Obter(context.Background(), "u-1", gastoTeste(t, "2026-11-03").ID)
			interno(t, "obter", err)
			_, _, err = d.Listar(context.Background(), "u-1", gasto.Filtro{Mes: "2026-11"}, nil, 10)
			interno(t, "listar", err)
		})
	}
}

func TestLotesComRetentativa(t *testing.T) {
	chaves := func(n int) []map[string]types.AttributeValue {
		var out []map[string]types.AttributeValue
		for range n {
			out = append(out, map[string]types.AttributeValue{"PK": s("USER#u-1"), "SK": s("EXP#x")})
		}
		return out
	}
	sobra := map[string][]types.WriteRequest{"t": {{DeleteRequest: &types.DeleteRequest{}}}}

	// 30 itens: dois lotes; o primeiro devolve um item não processado uma vez.
	f := &falso{
		queries: []*dynamodb.QueryOutput{{Items: chaves(30)}},
		batch:   []*dynamodb.BatchWriteItemOutput{{UnprocessedItems: sobra}, {}, {}},
	}
	n, err := novoFalso(f).ExcluirDoUsuario(context.Background(), "u-1")
	if err != nil || n != 30 || f.nBatch != 3 {
		t.Errorf("n = %d, lotes = %d, %v", n, f.nBatch, err)
	}

	// Sempre sobra: desiste depois de maxTentativas.
	f = &falso{queries: []*dynamodb.QueryOutput{{Items: chaves(1)}}, batch: []*dynamodb.BatchWriteItemOutput{{UnprocessedItems: sobra}}}
	_, err = novoFalso(f).ExcluirDoUsuario(context.Background(), "u-1")
	if errs.KindOf(err) != errs.KindInternal || f.nBatch != maxTentativas {
		t.Errorf("tentativas = %d, %v", f.nBatch, err)
	}
}

func TestEsperaPadrao(t *testing.T) {
	d := NewDynamo(&falso{}, "t")
	inicio := time.Now()
	d.espera(1)
	if time.Since(inicio) < 20*time.Millisecond {
		t.Error("a espera padrão não esperou")
	}
}
