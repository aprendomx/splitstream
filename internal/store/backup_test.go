package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func cipherDePrueba(t *testing.T) *crypto.Cipher {
	t.Helper()
	var k [32]byte
	for i := range k {
		k[i] = byte(i)
	}
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// El respaldo tiene que abrir con store.Open y pasar Bootstrap con la MISMA clave: es
// exactamente lo que hará quien lo restaure.
func TestBackupToProducesAnOpenableConsistentCopy(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	c := cipherDePrueba(t)
	if err := db.Bootstrap(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateDestination(ctx, c, store.NewDestination{
		Name: "YouTube", Platform: store.PlatformYouTube, RTMPURL: "rtmp://a/b",
		Key: crypto.Secret("k"), Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	copia := filepath.Join(t.TempDir(), "respaldo.db")
	if err := db.BackupTo(ctx, copia); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	db2, err := store.Open(ctx, copia)
	if err != nil {
		t.Fatalf("abrir el respaldo: %v", err)
	}
	defer db2.Close()
	if err := db2.Bootstrap(ctx, c); err != nil {
		t.Fatalf("el respaldo no pasa Bootstrap con la misma clave: %v", err)
	}
	dests, err := db2.ListDestinations(ctx)
	if err != nil || len(dests) != 1 {
		t.Errorf("destinos en el respaldo = %d (%v), quería 1", len(dests), err)
	}
}

func TestBackupToRefusesToRunInsideATransaction(t *testing.T) {
	db := openTemp(t)
	err := db.InTx(context.Background(), func(tx *store.DB) error {
		return tx.BackupTo(context.Background(), filepath.Join(t.TempDir(), "x.db"))
	})
	if err == nil {
		t.Error("VACUUM INTO dentro de una transacción debería fallar")
	}
}
