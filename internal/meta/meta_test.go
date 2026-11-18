package meta

import (
	"reflect"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

func TestEstadoDe(t *testing.T) {
	// spec metas-gastos, "Estados da meta"
	casos := []struct {
		nome         string
		total, valor int64
		want         Estado
	}{
		{"zero", 0, 100000, Dentro},
		{"abaixo de 80% (R$ 799,99)", 79999, 100000, Dentro},
		{"exatamente 80%", 80000, 100000, Aviso},
		{"entre 80% e 100%", 99999, 100000, Aviso},
		{"exatamente 100%", 100000, 100000, Estourada},
		{"acima de 100%", 105000, 100000, Estourada},
		{"80% sem divisão exata (R$ 720,00 de R$ 900,00)", 72000, 90000, Aviso},
		{"um centavo abaixo de 80% de R$ 900,00", 71999, 90000, Dentro},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := EstadoDe(c.total, c.valor); got != c.want {
				t.Fatalf("EstadoDe(%d, %d) = %s, want %s", c.total, c.valor, got, c.want)
			}
		})
	}
}

func TestLimiaresCruzados(t *testing.T) {
	casos := []struct {
		nome          string
		antes, depois int64
		jaNotificados []int
		want          []int
		avisar        int
	}{
		{"nada atingido", 50000, 70000, nil, nil, 0},
		// spec, "Cruzar 80%": R$ 700,00 + R$ 120,00
		{"cruza 80%", 70000, 82000, nil, []int{80}, 80},
		{"chega exatamente a 80%", 79999, 80000, nil, []int{80}, 80},
		// spec, "Cruzar 80% e 100% de uma vez": avisa só o de 100%
		{"cruza 80% e 100% de uma vez", 70000, 110000, nil, []int{80, 100}, 100},
		// spec, "Lançamento que não cruza limiar"
		{"já estava em aviso e não chega a 100%", 85000, 95000, []int{80}, nil, 0},
		{"já estava em aviso e estoura", 85000, 100000, []int{80}, []int{100}, 100},
		// meta criada com o mês já acima de 80%: um gasto a mais não avisa 80%
		{"já estava acima de 80% sem aviso", 85000, 90000, nil, nil, 0},
		// spec, "Limiar já notificado no mês": cair e subir não renotifica
		{"caiu e subiu de novo", 70000, 85000, []int{80}, nil, 0},
		{"total diminuiu", 120000, 90000, nil, nil, 0},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := LimiaresCruzados(c.antes, c.depois, 100000, c.jaNotificados)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("LimiaresCruzados = %v, want %v", got, c.want)
			}
			avisar, ok := ParaAvisar(got)
			if ok != (c.avisar != 0) || avisar != c.avisar {
				t.Fatalf("ParaAvisar = %d, %v; want %d", avisar, ok, c.avisar)
			}
		})
	}
}

func centavos(v int64) *int64 { return &v }

func TestVigencias(t *testing.T) {
	// spec, "Alterar o valor": R$ 900,00 desde outubro, R$ 1.100,00 em novembro
	m := Meta{CategoriaID: "combustivel"}.Definida("2026-10", 90000).Definida("2026-11", 110000)

	casos := []struct {
		mes   string
		valor int64
		tem   bool
	}{
		{"2026-09", 0, false},
		{"2026-10", 90000, true},
		{"2026-11", 110000, true},
		{"2027-03", 110000, true}, // "Novo mês": a meta se repete
	}
	for _, c := range casos {
		if v, ok := m.ValorNoMes(c.mes); v != c.valor || ok != c.tem {
			t.Errorf("ValorNoMes(%s) = %d, %v; want %d, %v", c.mes, v, ok, c.valor, c.tem)
		}
	}

	// spec, "Segunda meta para a mesma categoria": no mesmo mês, substitui
	m2 := m.Definida("2026-11", 120000)
	if len(m2.Vigencias) != 2 {
		t.Fatalf("vigências = %+v, want 2", m2.Vigencias)
	}
	if v, _ := m2.ValorNoMes("2026-11"); v != 120000 {
		t.Fatalf("valor substituído = %d", v)
	}
	if v, _ := m.ValorNoMes("2026-11"); v != 110000 {
		t.Fatalf("Definida mudou a meta original: %d", v)
	}

	// spec, "Remover a meta": some do mês em diante, o passado continua
	r := m.Removida("2026-12")
	if _, ok := r.ValorNoMes("2026-12"); ok {
		t.Fatal("meta removida ainda vale em dezembro")
	}
	if _, ok := r.ValorNoMes("2027-01"); ok {
		t.Fatal("meta removida voltou em janeiro")
	}
	if v, ok := r.ValorNoMes("2026-11"); !ok || v != 110000 {
		t.Fatalf("novembro depois da remoção = %d, %v", v, ok)
	}

	// definir de novo depois de remover volta a valer
	d := r.Definida("2027-02", 50000)
	if v, ok := d.ValorNoMes("2027-02"); !ok || v != 50000 {
		t.Fatalf("redefinida = %d, %v", v, ok)
	}
	if _, ok := d.ValorNoMes("2027-01"); ok {
		t.Fatal("janeiro deveria seguir sem meta")
	}

	// uma vigência futura é descartada ao definir num mês anterior
	f := Meta{Vigencias: []Vigencia{{Desde: "2026-10", ValorCentavos: centavos(1)}, {Desde: "2027-01", ValorCentavos: centavos(2)}}}.
		Definida("2026-12", 3)
	if !reflect.DeepEqual(f.Vigencias, []Vigencia{{Desde: "2026-10", ValorCentavos: centavos(1)}, {Desde: "2026-12", ValorCentavos: centavos(3)}}) {
		t.Fatalf("vigências = %+v", f.Vigencias)
	}
}

func TestValidar(t *testing.T) {
	// spec, "Valor inválido"
	for _, v := range []int64{0, -100, 100_000_001} {
		err := ValidarValor(v)
		if errs.KindOf(err) != errs.KindValidation {
			t.Errorf("ValidarValor(%d) = %v, want validação", v, err)
		}
	}
	if err := ValidarValor(100000); err != nil {
		t.Errorf("ValidarValor(100000) = %v", err)
	}
	if err := ValidarMes("mes", "2026-13"); errs.KindOf(err) != errs.KindValidation {
		t.Errorf("ValidarMes(2026-13) = %v", err)
	}
	if err := ValidarMes("mes", "2026-11"); err != nil {
		t.Errorf("ValidarMes(2026-11) = %v", err)
	}
}
