package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestRecordingSettingsDefaults(t *testing.T) {
	db := openTemp(t)
	s, err := db.RecordingSettings(context.Background())
	if err != nil {
		t.Fatalf("RecordingSettings: %v", err)
	}
	if s.Enabled || s.SegmentMin != 10 || s.MaxGB != 20 || s.KeepDays != 30 {
		t.Errorf("defaults = %+v; quería apagada, 10 min, 20 GB, 30 días", s)
	}
}

func TestUpdateRecordingSettingsPatchesAndValidates(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	on, seg, gb := true, 5, 2.5
	s, err := db.UpdateRecordingSettings(ctx, store.RecordingSettingsPatch{Enabled: &on, SegmentMin: &seg, MaxGB: &gb})
	if err != nil {
		t.Fatalf("UpdateRecordingSettings: %v", err)
	}
	if !s.Enabled || s.SegmentMin != 5 || s.MaxGB != 2.5 || s.KeepDays != 30 {
		t.Errorf("patch mal aplicado: %+v", s)
	}
	if s.UpdatedAt.IsZero() || s.UpdatedAt.Year() < 2026 {
		t.Errorf("updated_at no se fijó: %v", s.UpdatedAt)
	}

	malos := []store.RecordingSettingsPatch{
		{SegmentMin: ptr(-1)},
		{SegmentMin: ptr(241)},
		{MaxGB: ptrF(0)},
		{KeepDays: ptr(-1)},
	}
	for i, p := range malos {
		if _, err := db.UpdateRecordingSettings(ctx, p); !errors.Is(err, store.ErrInvalidInput) {
			t.Errorf("patch %d aceptado (err = %v)", i, err)
		}
	}
	// Los rechazos no tocaron nada.
	s, _ = db.RecordingSettings(ctx)
	if s.SegmentMin != 5 || s.MaxGB != 2.5 {
		t.Errorf("un patch inválido modificó la fila: %+v", s)
	}
}

func ptr(n int) *int          { return &n }
func ptrF(f float64) *float64 { return &f }
