package store_test

import (
	"context"
	"testing"
)

func TestQuotaAccumulatesPerAccountAndDayAndPrunes(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a := cuentaDePrueba(t, db, c, "1")
	b := cuentaDePrueba(t, db, c, "2")
	for _, u := range []int{50, 1, 5} {
		if err := db.AddQuota(ctx, a.ID, "2026-09-11", u); err != nil {
			t.Fatal(err)
		}
	}
	db.AddQuota(ctx, a.ID, "2026-09-10", 100)
	db.AddQuota(ctx, b.ID, "2026-09-11", 7)
	if n, _ := db.QuotaUsed(ctx, a.ID, "2026-09-11"); n != 56 {
		t.Errorf("hoy = %d, quería 56", n)
	}
	if n, _ := db.QuotaUsed(ctx, a.ID, "2026-09-12"); n != 0 {
		t.Errorf("día sin filas = %d", n)
	}
	if err := db.AddQuota(ctx, a.ID, "2026-09-11", 0); err == nil {
		t.Error("0 unidades debería ser inválido")
	}
	if n, err := db.PruneQuota(ctx, "2026-09-11"); err != nil || n != 1 {
		t.Errorf("PruneQuota = %d, %v; quería 1 (el día 10)", n, err)
	}
	if n, _ := db.QuotaUsed(ctx, a.ID, "2026-09-11"); n != 56 {
		t.Errorf("la poda tocó el día vigente: %d", n)
	}
}
