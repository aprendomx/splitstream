package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

// TestSentinelsCarryTheirClass fija el contrato: cada centinela concreto pertenece a una
// de las tres clases transversales, y la API puede preguntar por la clase sin conocer el
// centinela.
func TestSentinelsCarryTheirClass(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
		clase  error
	}{
		{"destino no encontrado", store.ErrDestinationNotFound, store.ErrNotFound},
		{"sesión no encontrada", store.ErrSessionNotFound, store.ErrNotFound},
		{"URL inválida", store.ErrInvalidDestinationURL, store.ErrInvalidInput},
		{"settings sin inicializar", store.ErrSettingsNotInitialized, store.ErrConflict},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if !errors.Is(c.err, c.clase) {
				t.Errorf("%v no pertenece a la clase %v", c.err, c.clase)
			}
		})
	}
}

// TestClassesAreDistinct evita el error tonto de que los tres centinelas acaben siendo el
// mismo valor por copiar y pegar.
func TestClassesAreDistinct(t *testing.T) {
	if errors.Is(store.ErrNotFound, store.ErrInvalidInput) ||
		errors.Is(store.ErrInvalidInput, store.ErrConflict) ||
		errors.Is(store.ErrConflict, store.ErrNotFound) {
		t.Error("las clases se confunden entre sí")
	}
}

// TestWrappedErrorsStillMatchTheirOwnSentinel comprueba que envolver no rompe a quien ya
// preguntaba por el centinela concreto: es código que existe hoy y no debe cambiar.
func TestWrappedErrorsStillMatchTheirOwnSentinel(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)

	_, err := db.UpdateDestination(ctx, testCipher(t, 1), 9999, store.DestinationPatch{})
	if !errors.Is(err, store.ErrDestinationNotFound) {
		t.Errorf("err = %v, quería ErrDestinationNotFound", err)
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, quería que también fuera ErrNotFound", err)
	}
}

// TestCreateDestinationValidatesNameAndPlatform cierra el hueco que la API destaparía:
// hoy un nombre vacío o una plataforma inventada solo los para el CHECK de SQLite, que
// devuelve un error de driver indistinguible de un fallo de disco.
func TestCreateDestinationValidatesNameAndPlatform(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	c := testCipher(t, 1)

	casos := []struct {
		nombre string
		in     store.NewDestination
	}{
		{"nombre vacío", store.NewDestination{Name: "", Platform: store.PlatformCustom, RTMPURL: "rtmp://x/live", Key: crypto.Secret("k")}},
		{"nombre solo espacios", store.NewDestination{Name: "   ", Platform: store.PlatformCustom, RTMPURL: "rtmp://x/live", Key: crypto.Secret("k")}},
		{"plataforma inventada", store.NewDestination{Name: "n", Platform: store.Platform("myspace"), RTMPURL: "rtmp://x/live", Key: crypto.Secret("k")}},
		{"clave vacía", store.NewDestination{Name: "n", Platform: store.PlatformCustom, RTMPURL: "rtmp://x/live", Key: crypto.Secret("")}},
	}

	for _, cas := range casos {
		t.Run(cas.nombre, func(t *testing.T) {
			_, err := db.CreateDestination(ctx, c, cas.in)
			if !errors.Is(err, store.ErrInvalidInput) {
				t.Errorf("err = %v, quería que fuera ErrInvalidInput", err)
			}
		})
	}
}

// TestUpdateDestinationValidatesOnlyWhatItTouches: en un patch, los campos nil no se
// tocan, así que tampoco se validan. Validarlos daría un error por un campo que el
// llamante ni siquiera mandó.
func TestUpdateDestinationValidatesOnlyWhatItTouches(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	c := testCipher(t, 1)

	d, err := db.CreateDestination(ctx, c, store.NewDestination{
		Name: "bueno", Platform: store.PlatformCustom,
		RTMPURL: "rtmp://x/live", Key: crypto.Secret("clave"), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateDestination: %v", err)
	}

	// Un patch que solo toca enabled no debe validar nombre, plataforma ni clave.
	enabled := false
	if _, err := db.UpdateDestination(ctx, c, d.ID, store.DestinationPatch{Enabled: &enabled}); err != nil {
		t.Errorf("un patch de solo enabled falló: %v", err)
	}

	// Pero lo que sí se manda, se valida.
	vacio := "  "
	if _, err := db.UpdateDestination(ctx, c, d.ID, store.DestinationPatch{Name: &vacio}); !errors.Is(err, store.ErrInvalidInput) {
		t.Errorf("err = %v, quería ErrInvalidInput al mandar un nombre vacío", err)
	}

	mala := store.Platform("myspace")
	if _, err := db.UpdateDestination(ctx, c, d.ID, store.DestinationPatch{Platform: &mala}); !errors.Is(err, store.ErrInvalidInput) {
		t.Errorf("err = %v, quería ErrInvalidInput al mandar una plataforma inventada", err)
	}

	sinClave := crypto.Secret("")
	if _, err := db.UpdateDestination(ctx, c, d.ID, store.DestinationPatch{Key: &sinClave}); !errors.Is(err, store.ErrInvalidInput) {
		t.Errorf("err = %v, quería ErrInvalidInput al mandar una clave vacía", err)
	}
}
