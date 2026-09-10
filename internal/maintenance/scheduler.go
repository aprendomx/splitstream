// Package maintenance ejecuta tareas periódicas de limpieza —hoy la poda de eventos y
// sesiones; en la v0.9, la de grabaciones— sin pisar una transmisión en curso.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ErrBusy: había una sesión viva y no se corrió nada. Se reintenta en el siguiente tick.
var ErrBusy = errors.New("mantenimiento pospuesto: hay una transmisión en curso")

// Job es una tarea. Run devuelve un resumen legible ("eventos: 120 borrados").
type Job struct {
	Name string
	Run  func(ctx context.Context) (string, error)
}

// Scheduler corre los jobs una vez al arrancar y después cada día a la hora Hour.
type Scheduler struct {
	Jobs []Job
	// Busy dice si hay una transmisión en curso. Con true no se corre nada: un DELETE
	// grande compite con los sinks por la única conexión a la base.
	Busy func() bool
	// Hour es la hora local de la pasada diaria. 4 por defecto: madrugada.
	Hour int
	// InitialDelay es la espera antes de la primera pasada. Un minuto por defecto, para
	// no competir con el arranque.
	InitialDelay time.Duration
	// RetryDelay es cuánto se espera si Busy devolvió true. Diez minutos por defecto.
	RetryDelay time.Duration
	// OnDone recibe el resumen de cada pasada, o su error. Es donde main deja el evento.
	OnDone func(resumen string, err error)
	Logger *slog.Logger
	Now    func() time.Time
}

// NextRun devuelve la próxima ocurrencia de la hora `hour` después de `now`.
func NextRun(now time.Time, hour int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// RunOnce ejecuta todos los jobs en orden. Un job que falla no detiene a los demás; el
// primer error se devuelve al final junto al resumen de los que sí corrieron.
func (s *Scheduler) RunOnce(ctx context.Context) (string, error) {
	if s.Busy != nil && s.Busy() {
		return "", ErrBusy
	}
	var partes []string
	var primero error
	for _, j := range s.Jobs {
		r, err := j.Run(ctx)
		if err != nil {
			partes = append(partes, fmt.Sprintf("%s: error (%v)", j.Name, err))
			if primero == nil {
				primero = fmt.Errorf("%s: %w", j.Name, err)
			}
			continue
		}
		partes = append(partes, r)
	}
	return strings.Join(partes, "; "), primero
}

// Run bloquea hasta que el contexto termine.
func (s *Scheduler) Run(ctx context.Context) {
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}
	hour := s.Hour
	if hour == 0 {
		hour = 4
	}
	initial := s.InitialDelay
	if initial == 0 {
		initial = time.Minute
	}
	retry := s.RetryDelay
	if retry == 0 {
		retry = 10 * time.Minute
	}

	espera := initial
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(espera):
		}

		resumen, err := s.RunOnce(ctx)
		if errors.Is(err, ErrBusy) {
			log.Info("mantenimiento pospuesto: hay una transmisión en curso", "reintento", retry)
			espera = retry
			continue
		}
		if err != nil {
			log.Error("mantenimiento con errores", "err", err, "resumen", resumen)
		} else {
			log.Info("mantenimiento hecho", "resumen", resumen)
		}
		if s.OnDone != nil {
			s.OnDone(resumen, err)
		}
		espera = time.Until(NextRun(now(), hour))
	}
}
