package store

import (
	"context"
	"fmt"
	"time"
)

// RecordingSettings es la fila única de recording_settings.
type RecordingSettings struct {
	Enabled    bool
	SegmentMin int
	MaxGB      float64
	KeepDays   int
	UpdatedAt  time.Time
}

// MaxBytes es el tope en bytes. Vive aquí y no en cada llamante porque la conversión
// —GB decimales a bytes binarios— estaba copiada en tres sitios, y basta con que una
// copia use 1e9 para que la cuota y lo que enseña el panel dejen de cuadrar.
func (s RecordingSettings) MaxBytes() int64 { return int64(s.MaxGB * float64(1<<30)) }

// RecordingSettingsPatch es una modificación parcial: los campos nil no se tocan.
type RecordingSettingsPatch struct {
	Enabled    *bool
	SegmentMin *int
	MaxGB      *float64
	KeepDays   *int
}

// Límites. Duplican los CHECK del esquema para que el error llegue antes y legible.
const (
	maxSegmentMinutes = 240
)

func (d *DB) RecordingSettings(ctx context.Context) (*RecordingSettings, error) {
	var (
		s         RecordingSettings
		enabled   int
		updatedAt string
	)
	err := d.ex.QueryRowContext(ctx,
		`SELECT enabled, segment_min, max_gb, keep_days, updated_at FROM recording_settings WHERE id = 1`).
		Scan(&enabled, &s.SegmentMin, &s.MaxGB, &s.KeepDays, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("leer los ajustes de grabación: %w", err)
	}
	s.Enabled = enabled == 1
	if s.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &s, nil
}

// UpdateRecordingSettings aplica el patch tras validarlo entero: un valor inválido no
// modifica nada.
func (d *DB) UpdateRecordingSettings(ctx context.Context, p RecordingSettingsPatch) (*RecordingSettings, error) {
	actual, err := d.RecordingSettings(ctx)
	if err != nil {
		return nil, err
	}
	if p.Enabled != nil {
		actual.Enabled = *p.Enabled
	}
	if p.SegmentMin != nil {
		if *p.SegmentMin < 0 || *p.SegmentMin > maxSegmentMinutes {
			return nil, invalidInput(fmt.Sprintf("los minutos por segmento deben estar entre 0 y %d", maxSegmentMinutes))
		}
		actual.SegmentMin = *p.SegmentMin
	}
	if p.MaxGB != nil {
		if *p.MaxGB <= 0 {
			return nil, invalidInput("el tope en GB debe ser mayor que 0")
		}
		actual.MaxGB = *p.MaxGB
	}
	if p.KeepDays != nil {
		if *p.KeepDays < 0 {
			return nil, invalidInput("los días de retención no pueden ser negativos")
		}
		actual.KeepDays = *p.KeepDays
	}
	if _, err := d.ex.ExecContext(ctx,
		`UPDATE recording_settings SET enabled = ?, segment_min = ?, max_gb = ?, keep_days = ?, updated_at = ? WHERE id = 1`,
		boolToInt(actual.Enabled), actual.SegmentMin, actual.MaxGB, actual.KeepDays, nowRFC3339()); err != nil {
		return nil, fmt.Errorf("guardar los ajustes de grabación: %w", err)
	}
	return d.RecordingSettings(ctx)
}
