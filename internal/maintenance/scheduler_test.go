package maintenance_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/maintenance"
)

func TestRunOnceRunsEveryJobAndSummarises(t *testing.T) {
	var orden []string
	s := &maintenance.Scheduler{
		Jobs: []maintenance.Job{
			{Name: "a", Run: func(context.Context) (string, error) { orden = append(orden, "a"); return "a: 3 filas", nil }},
			{Name: "b", Run: func(context.Context) (string, error) { orden = append(orden, "b"); return "b: 0 filas", nil }},
		},
	}
	resumen, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(orden) != 2 || orden[0] != "a" {
		t.Errorf("orden = %v", orden)
	}
	if !strings.Contains(resumen, "a: 3 filas") || !strings.Contains(resumen, "b: 0 filas") {
		t.Errorf("resumen = %q", resumen)
	}
}

// Un job que falla no impide a los demás, y el error se devuelve.
func TestRunOnceContinuesAfterAFailingJob(t *testing.T) {
	corrido := false
	s := &maintenance.Scheduler{Jobs: []maintenance.Job{
		{Name: "rompe", Run: func(context.Context) (string, error) { return "", errors.New("disco") }},
		{Name: "sigue", Run: func(context.Context) (string, error) { corrido = true; return "ok", nil }},
	}}
	if _, err := s.RunOnce(context.Background()); err == nil {
		t.Error("quería el error del job")
	}
	if !corrido {
		t.Error("el segundo job no corrió")
	}
}

// Nunca se poda con una transmisión en curso: el DELETE compite con los sinks por la
// única conexión a la base.
func TestRunOnceRefusesWhileBusy(t *testing.T) {
	corrido := false
	s := &maintenance.Scheduler{
		Busy: func() bool { return true },
		Jobs: []maintenance.Job{{Name: "x", Run: func(context.Context) (string, error) { corrido = true; return "", nil }}},
	}
	if _, err := s.RunOnce(context.Background()); !errors.Is(err, maintenance.ErrBusy) {
		t.Errorf("err = %v, quería ErrBusy", err)
	}
	if corrido {
		t.Error("corrió un job con el servicio ocupado")
	}
}

func TestNextRunIsTheNextOccurrenceOfTheHour(t *testing.T) {
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.Local)
	if got := maintenance.NextRun(base, 4); !got.Equal(time.Date(2026, 9, 10, 4, 0, 0, 0, time.Local)) {
		t.Errorf("a las 10 → %v, quería mañana a las 4", got)
	}
	antes := time.Date(2026, 9, 9, 3, 0, 0, 0, time.Local)
	if got := maintenance.NextRun(antes, 4); !got.Equal(time.Date(2026, 9, 9, 4, 0, 0, 0, time.Local)) {
		t.Errorf("a las 3 → %v, quería hoy a las 4", got)
	}
}

// Run hace una pasada inicial y avisa por OnDone; el contexto la para.
func TestRunDoesAnInitialPass(t *testing.T) {
	hecho := make(chan string, 1)
	s := &maintenance.Scheduler{
		InitialDelay: 10 * time.Millisecond,
		Jobs:         []maintenance.Job{{Name: "x", Run: func(context.Context) (string, error) { return "x: ok", nil }}},
		OnDone:       func(resumen string, err error) { hecho <- resumen },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	select {
	case r := <-hecho:
		if !strings.Contains(r, "x: ok") {
			t.Errorf("resumen = %q", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no hubo pasada inicial")
	}
}
