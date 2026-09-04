package store_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

// pngFalso no es un PNG: al store le da igual, porque validar y normalizar la imagen es
// trabajo de la capa que la recibe. Aquí solo se comprueba que los bytes van y vuelven.
var pngFalso = []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4, 5}

func TestSetYLeerLogo(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()
	d, err := db.CreateDestination(ctx, c, newDest("Canal"))
	if err != nil {
		t.Fatal(err)
	}

	etag, err := db.SetDestinationLogo(ctx, d.ID, pngFalso)
	if err != nil {
		t.Fatalf("SetDestinationLogo: %v", err)
	}
	if etag == "" {
		t.Fatal("el etag salió vacío")
	}

	logo, err := db.DestinationLogo(ctx, d.ID)
	if err != nil {
		t.Fatalf("DestinationLogo: %v", err)
	}
	if !bytes.Equal(logo.Image, pngFalso) {
		t.Errorf("los bytes no coinciden: %v", logo.Image)
	}
	if logo.ETag != etag {
		t.Errorf("etag = %q, quería %q", logo.ETag, etag)
	}
}

// TestETagCortoYDerivadoDeLosBytes: el etag viaja en cada push del WebSocket, así que se
// acorta a 16 hex (64 bits del SHA-256). Y depende solo del contenido: la misma imagen da
// el mismo etag, otra imagen da otro.
func TestETagCortoYDerivadoDeLosBytes(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()
	a, _ := db.CreateDestination(ctx, c, newDest("A"))
	b, _ := db.CreateDestination(ctx, c, newDest("B"))

	e1, err := db.SetDestinationLogo(ctx, a.ID, pngFalso)
	if err != nil {
		t.Fatal(err)
	}
	if len(e1) != 16 {
		t.Errorf("el etag mide %d, quería 16", len(e1))
	}

	e2, _ := db.SetDestinationLogo(ctx, b.ID, pngFalso)
	if e1 != e2 {
		t.Errorf("la misma imagen dio dos etags: %q y %q", e1, e2)
	}

	e3, _ := db.SetDestinationLogo(ctx, a.ID, append(pngFalso, 9))
	if e3 == e1 {
		t.Error("una imagen distinta dio el mismo etag")
	}
}

func TestSetLogoReemplazaElAnterior(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()
	d, _ := db.CreateDestination(ctx, c, newDest("Canal"))

	if _, err := db.SetDestinationLogo(ctx, d.ID, pngFalso); err != nil {
		t.Fatal(err)
	}
	nuevo := append(pngFalso, 'X')
	if _, err := db.SetDestinationLogo(ctx, d.ID, nuevo); err != nil {
		t.Fatal(err)
	}

	logo, err := db.DestinationLogo(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(logo.Image, nuevo) {
		t.Error("quedaron los bytes viejos")
	}
}

func TestLeerLogoQueNoExiste(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()
	d, _ := db.CreateDestination(ctx, c, newDest("Canal"))

	if _, err := db.DestinationLogo(ctx, d.ID); !errors.Is(err, store.ErrLogoNotFound) {
		t.Errorf("error = %v, quería ErrLogoNotFound", err)
	}
}

func TestSetLogoEnDestinoInexistente(t *testing.T) {
	db, _ := bootstrapped(t)
	if _, err := db.SetDestinationLogo(context.Background(), 999, pngFalso); !errors.Is(err, store.ErrDestinationNotFound) {
		t.Errorf("error = %v, quería ErrDestinationNotFound", err)
	}
}

func TestBorrarLogo(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()
	d, _ := db.CreateDestination(ctx, c, newDest("Canal"))
	db.SetDestinationLogo(ctx, d.ID, pngFalso)

	if err := db.DeleteDestinationLogo(ctx, d.ID); err != nil {
		t.Fatalf("DeleteDestinationLogo: %v", err)
	}
	if _, err := db.DestinationLogo(ctx, d.ID); !errors.Is(err, store.ErrLogoNotFound) {
		t.Errorf("sigue habiendo logo: %v", err)
	}
}

// TestBorrarLogoQueNoExisteEsIdempotente: quitar un logo que ya no está no es un error.
// Lo que el usuario pidió —que no haya logo— ya se cumple.
func TestBorrarLogoQueNoExisteEsIdempotente(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()
	d, _ := db.CreateDestination(ctx, c, newDest("Canal"))

	if err := db.DeleteDestinationLogo(ctx, d.ID); err != nil {
		t.Errorf("borrar dos veces debería ser inocuo: %v", err)
	}
}

// TestBorrarElDestinoSeLlevaSuLogo cubre el ON DELETE CASCADE, y con él que nadie apague
// las claves ajenas en el DSN sin que salte algo.
func TestBorrarElDestinoSeLlevaSuLogo(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()
	d, _ := db.CreateDestination(ctx, c, newDest("Canal"))
	db.SetDestinationLogo(ctx, d.ID, pngFalso)

	if err := db.DeleteDestination(ctx, d.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.SQL().QueryRowContext(ctx, `SELECT count(*) FROM destination_logos`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("quedaron %d logos huérfanos", n)
	}
}

// TestETagsDeTodos es lo que usa el listado: un mapa id -> etag, sin bytes de imagen.
func TestETagsDeTodos(t *testing.T) {
	db, c := bootstrapped(t)
	ctx := context.Background()
	conLogo, _ := db.CreateDestination(ctx, c, newDest("Con"))
	sinLogo, _ := db.CreateDestination(ctx, c, newDest("Sin"))
	etag, _ := db.SetDestinationLogo(ctx, conLogo.ID, pngFalso)

	m, err := db.DestinationLogoETags(ctx)
	if err != nil {
		t.Fatalf("DestinationLogoETags: %v", err)
	}
	if m[conLogo.ID] != etag {
		t.Errorf("etag de %d = %q, quería %q", conLogo.ID, m[conLogo.ID], etag)
	}
	if _, hay := m[sinLogo.ID]; hay {
		t.Errorf("un destino sin logo apareció en el mapa")
	}
}
