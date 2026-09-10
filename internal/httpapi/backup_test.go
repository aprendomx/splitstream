package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestBackupDownloadsAnOpenableDatabaseAndAudits(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "yt", "k", true)

	rec := do(t, srv, cookies, http.MethodPost, "/api/backup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="splitstream-`) || !strings.HasSuffix(cd, `.db"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}

	ruta := filepath.Join(t.TempDir(), "descargado.db")
	if err := os.WriteFile(ruta, rec.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db2, err := store.Open(ctx, ruta)
	if err != nil {
		t.Fatalf("el archivo descargado no abre: %v", err)
	}
	defer db2.Close()
	if err := db2.Bootstrap(ctx, srv.cipher); err != nil {
		t.Errorf("no pasa Bootstrap con la misma clave: %v", err)
	}
	dests, _ := db2.ListDestinations(ctx)
	if len(dests) != 1 {
		t.Errorf("destinos = %d, quería 1", len(dests))
	}

	eventos, _ := db.RecentEvents(ctx, 5)
	var visto bool
	for _, e := range eventos {
		if e.Kind == "backup_downloaded" && e.Level == store.LevelWarn {
			visto = true
		}
	}
	if !visto {
		t.Error("descargar el respaldo no dejó evento")
	}
}

// Con emisión en curso el respaldo retiene la base y frena a los destinos: se rechaza.
func TestBackupRefusesWhileASessionIsLive(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	eng.setLive(7)

	rec := do(t, srv, cookies, http.MethodPost, "/api/backup", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("código = %d, quería 409: %s", rec.Code, rec.Body.String())
	}
	if got := errorCodeDe(t, rec); got != codeConflict {
		t.Errorf("code = %q, quería %q", got, codeConflict)
	}

	eventos, err := db.RecentEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eventos {
		if e.Kind == "backup_downloaded" {
			t.Error("un respaldo rechazado dejó evento backup_downloaded")
		}
	}
}

func TestBackupRequiresASession(t *testing.T) {
	srv, _, _, _, _ := newDestServer(t)
	if rec := do(t, srv, nil, http.MethodPost, "/api/backup", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", rec.Code)
	}
}
