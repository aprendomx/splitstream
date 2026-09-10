package rtmpio

import (
	"context"
	"errors"
	"time"

	"github.com/aprendomx/splitstream/internal/probe"
)

// Probe comprueba un destino SIN emitir: conecta, hace connect, createStream y publish,
// espera `grace` y cierra con FCUnpublish (spec v0.8 §3).
//
// Lo que no puede prometer: Stream.Publish de go-rtmp no espera el onStatus, así que una
// clave mala solo se ve si la plataforma cierra el socket dentro de la gracia. Por eso el
// resultado bueno es "plausible" y no "correcta".
//
// Ningún error reproduce la URL ni la clave: se heredan las reglas de parseTarget.
func Probe(ctx context.Context, cfg PublisherConfig, grace time.Duration) probe.Result {
	inicio := time.Now()
	done := func(o probe.Outcome, stage string, err error) probe.Result {
		return probe.Result{Outcome: o, Stage: stage, Elapsed: time.Since(inicio), Err: err}
	}

	p, err := NewPublisher(cfg)
	if err != nil {
		return done(probe.Rejected, "url", err)
	}
	defer p.Close()

	if err := p.Connect(ctx); err != nil {
		stage := "connect"
		var se *stageError
		if errors.As(err, &se) {
			stage = se.stage
		}
		switch stage {
		case "dns", "tcp", "tls":
			return done(probe.Unreachable, stage, err)
		default:
			return done(probe.Rejected, stage, err)
		}
	}

	select {
	case <-ctx.Done():
		return done(probe.Rejected, "grace", ctx.Err())
	case <-time.After(grace):
	}

	if err := p.lastError(); err != nil {
		return done(probe.ClosedEarly, "grace", err)
	}
	return done(probe.Plausible, "grace", nil)
}
