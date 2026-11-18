//go:build integration

package store

import (
	"reflect"
	"sync"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/gasto"
	"github.com/Rossete-Inc/gestor-gastos-expenses-service/internal/meta"
)

func metaDeLazer(dono string) meta.Meta {
	return meta.Meta{DonoID: dono, CategoriaID: "lazer", NomeCategoria: "Lazer", Grupo: "CARTAO"}.
		Definida("2026-10", 90000)
}

func TestMetaGravarLerEListar(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)

	if _, ok, err := d.ObterMeta(ctx, "ana", "lazer"); err != nil || ok {
		t.Fatalf("meta antes de gravar: ok=%v, err=%v", ok, err)
	}

	m := metaDeLazer("ana").Definida("2026-11", 110000)
	if err := d.SalvarMeta(ctx, m); err != nil {
		t.Fatal(err)
	}
	lida, ok, err := d.ObterMeta(ctx, "ana", "lazer")
	if err != nil || !ok {
		t.Fatalf("obter = %v, %v", ok, err)
	}
	if lida.NomeCategoria != "Lazer" || lida.Grupo != "CARTAO" || lida.DonoID != "ana" {
		t.Fatalf("meta lida = %+v", lida)
	}
	if !reflect.DeepEqual(lida.Vigencias, m.Vigencias) {
		t.Fatalf("vigências = %+v, want %+v", lida.Vigencias, m.Vigencias)
	}

	// a vigência removida (NULL) volta como nil
	if err := d.SalvarMeta(ctx, lida.Removida("2026-12")); err != nil {
		t.Fatal(err)
	}
	lida, _, _ = d.ObterMeta(ctx, "ana", "lazer")
	if _, tem := lida.ValorNoMes("2026-12"); tem {
		t.Fatalf("meta removida ainda vale: %+v", lida.Vigencias)
	}
	if v, _ := lida.ValorNoMes("2026-11"); v != 110000 {
		t.Fatalf("novembro = %d", v)
	}

	outra := meta.Meta{DonoID: "ana", CategoriaID: "alimentacao", NomeCategoria: "Alimentação", Grupo: "CARTAO"}.Definida("2026-11", 50000)
	if err := d.SalvarMeta(ctx, outra); err != nil {
		t.Fatal(err)
	}
	if err := d.SalvarMeta(ctx, metaDeLazer("bruno")); err != nil {
		t.Fatal(err)
	}
	// gastos não aparecem entre as metas, nem metas entre os gastos do mês
	lancar(t, d, "ana", "2026-11-03", "lazer", gasto.Cartao, 100)
	metas, err := d.ListarMetas(ctx, "ana")
	if err != nil || len(metas) != 2 || metas[0].CategoriaID != "alimentacao" || metas[1].CategoriaID != "lazer" {
		t.Fatalf("listar = %+v, %v", metas, err)
	}
	if gs, _ := d.DoMes(ctx, "ana", "2026-11"); len(gs) != 1 {
		t.Fatalf("gastos do mês = %d", len(gs))
	}
}

func TestMetaMarcarAlerta(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)

	// sem meta, não marca nem falha
	if ok, err := d.MarcarAlerta(ctx, "ana", "lazer", "2026-11", 80); ok || err != nil {
		t.Fatalf("sem meta: %v, %v", ok, err)
	}
	if err := d.SalvarMeta(ctx, metaDeLazer("ana")); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.MarcarAlerta(ctx, "ana", "lazer", "2026-11", 80); !ok || err != nil {
		t.Fatalf("primeira marcação: %v, %v", ok, err)
	}
	if ok, _ := d.MarcarAlerta(ctx, "ana", "lazer", "2026-11", 80); ok {
		t.Fatal("80% marcado duas vezes no mesmo mês")
	}
	if ok, _ := d.MarcarAlerta(ctx, "ana", "lazer", "2026-11", 100); !ok {
		t.Fatal("100% não foi marcado")
	}
	if ok, _ := d.MarcarAlerta(ctx, "ana", "lazer", "2026-12", 80); !ok {
		t.Fatal("80% de outro mês não foi marcado")
	}

	// salvar a meta de novo preserva os alertas
	m, _, _ := d.ObterMeta(ctx, "ana", "lazer")
	if err := d.SalvarMeta(ctx, m.Definida("2026-11", 120000)); err != nil {
		t.Fatal(err)
	}
	m, _, _ = d.ObterMeta(ctx, "ana", "lazer")
	if !reflect.DeepEqual(m.Alertas, map[string][]int{"2026-11": {80, 100}, "2026-12": {80}}) {
		t.Fatalf("alertas = %+v", m.Alertas)
	}

	if err := d.ExcluirMeta(ctx, "ana", "lazer"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.ObterMeta(ctx, "ana", "lazer"); ok {
		t.Fatal("meta excluída ainda existe")
	}
	if err := d.ExcluirMeta(ctx, "ana", "lazer"); err != nil {
		t.Fatalf("excluir de novo: %v", err)
	}
}

// Duas escritas simultâneas que cruzam o mesmo limiar: só uma notifica.
func TestMetaMarcarAlertaConcorrente(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	if err := d.SalvarMeta(ctx, metaDeLazer("ana")); err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	marcou := make(chan bool, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := d.MarcarAlerta(ctx, "ana", "lazer", "2026-11", 80)
			if err != nil {
				t.Error(err)
			}
			marcou <- ok
		}()
	}
	wg.Wait()
	close(marcou)
	vezes := 0
	for ok := range marcou {
		if ok {
			vezes++
		}
	}
	if vezes != 1 {
		t.Fatalf("o limiar foi marcado %d vezes, want 1", vezes)
	}
}

func TestMetaSaiComOUsuario(t *testing.T) {
	d, _, _ := tabelaDeTeste(t)
	if err := d.SalvarMeta(ctx, metaDeLazer("ana")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExcluirDoUsuario(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	if metas, _ := d.ListarMetas(ctx, "ana"); len(metas) != 0 {
		t.Fatalf("metas depois de excluir o usuário = %+v", metas)
	}
}
