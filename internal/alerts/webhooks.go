package alerts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/store"
)

const (
	// busBuffer es cuántos eventos aguanta la cola antes de perder alguno. Un destino
	// aleteando produce uno cada pocos segundos; 256 son minutos de margen.
	busBuffer = 256
	// maxParalelo acota los envíos simultáneos. Con dos o tres webhooks configurados,
	// cuatro es de sobra; el tope existe para que un endpoint colgado no acumule
	// goroutines.
	maxParalelo = 4
	// defaultTimeout es el plazo de cada intento.
	defaultTimeout = 10 * time.Second
)

// defaultBackoff son las esperas entre reintentos. Solo con error de red o 5xx: un 4xx es
// configuración, y reintentarlo es martillear.
var defaultBackoff = []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}

// WebhookDispatcher escucha el bus y manda cada evento a los webhooks cuyo nivel mínimo
// alcance. Nunca bloquea al bus: la cola tiene tope y los envíos van en goroutines
// acotadas por un semáforo.
type WebhookDispatcher struct {
	db      *store.DB
	cipher  *crypto.Cipher
	log     *slog.Logger
	client  *http.Client
	ch      <-chan store.Event
	release func()
	version string

	// Timeout y Backoff son públicos para los tests; en producción se dejan por defecto.
	Timeout time.Duration
	Backoff []time.Duration

	sem    chan struct{}
	wg     sync.WaitGroup
	ok     atomic.Uint64
	failed atomic.Uint64
}

// NewWebhookDispatcher se suscribe al bus (si no es nil) y queda listo para Run.
func NewWebhookDispatcher(bus *events.Bus, db *store.DB, c *crypto.Cipher, log *slog.Logger, version string) *WebhookDispatcher {
	if log == nil {
		log = slog.Default()
	}
	d := &WebhookDispatcher{
		db: db, cipher: c, log: log, version: version,
		client:  &http.Client{},
		Timeout: defaultTimeout, Backoff: defaultBackoff,
		sem: make(chan struct{}, maxParalelo),
	}
	if bus != nil {
		d.ch, d.release = bus.Subscribe(busBuffer)
	}
	return d
}

// Run consume el bus hasta que el contexto termine. Espera a los envíos en vuelo al salir.
func (d *WebhookDispatcher) Run(ctx context.Context) {
	if d.ch == nil {
		return
	}
	defer d.release()
	for {
		select {
		case <-ctx.Done():
			d.wg.Wait()
			return
		case ev, ok := <-d.ch:
			if !ok {
				d.wg.Wait()
				return
			}
			d.dispatch(ctx, ev)
		}
	}
}

// Stats devuelve entregas buenas y fallidas (tras agotar reintentos), para /metrics.
func (d *WebhookDispatcher) Stats() (ok, failed uint64) {
	return d.ok.Load(), d.failed.Load()
}

func (d *WebhookDispatcher) dispatch(ctx context.Context, ev store.Event) {
	hooks, err := d.db.ListWebhooks(ctx)
	if err != nil {
		d.log.Error("no se pudieron leer los webhooks", "err", err)
		return
	}
	for _, h := range hooks {
		if !h.Enabled || rank(ev.Level) < rank(h.MinLevel) {
			continue
		}
		h := h
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			select {
			case d.sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-d.sem }()
			if err := d.Send(ctx, h, ev); err != nil {
				d.log.Warn("webhook sin entregar", "webhook", h.Name, "err", err)
			}
		}()
	}
}

// Send entrega un evento a un webhook con reintentos, y deja constancia del resultado
// en la fila del webhook. Lo usa también el botón «Probar» del panel.
func (d *WebhookDispatcher) Send(ctx context.Context, w store.Webhook, ev store.Event) error {
	var dest *store.Destination
	if ev.DestinationID != nil {
		if x, err := d.db.DestinationByID(ctx, *ev.DestinationID); err == nil {
			dest = x
		}
	}
	body, err := Payload(w.Format, ev, dest)
	if err != nil {
		return err
	}
	var firma string
	if w.Format == store.WebhookJSON && w.HasSecret {
		secret, err := d.db.WebhookSecret(ctx, d.cipher, w.ID)
		if err != nil {
			return err
		}
		if secret.Reveal() != "" {
			firma = Sign(secret, body)
		}
	}

	var (
		status  int
		lastErr error
	)
	for intento := 0; ; intento++ {
		status, lastErr = d.intento(ctx, w.URL, ev.Kind, firma, body)
		if lastErr == nil {
			d.ok.Add(1)
			_ = d.db.RecordWebhookDelivery(context.Background(), w.ID, status, "")
			return nil
		}
		// Solo se reintenta lo transitorio: red (status 0) o 5xx.
		if (status != 0 && status < 500) || intento >= len(d.Backoff) {
			break
		}
		// El temporizador se para si gana el contexto: con time.After, cada Send abortado
		// dejaba vivo hasta 16 s de temporizador que ya no le importaba a nadie.
		esperar := time.NewTimer(d.Backoff[intento])
		select {
		case <-ctx.Done():
			esperar.Stop()
			lastErr = ctx.Err()
		case <-esperar.C:
			continue
		}
		break
	}
	d.failed.Add(1)
	_ = d.db.RecordWebhookDelivery(context.Background(), w.ID, status, lastErr.Error())
	return lastErr
}

// intento hace un POST. Devuelve el código HTTP (0 si no hubo respuesta) y el error.
//
// El plazo NO cuelga del contexto que recibe: un POST ya lanzado sobrevive a que se cancele
// el del despachador. Justo el aviso que más le importa a quien opera esto —«el servicio se
// está apagando»— sale mientras el proceso se cierra, y heredar la cancelación lo perdía
// siempre, de forma determinista, a mitad de vuelo. Se sigue acotando por Timeout, y los
// REINTENTOS sí miran el contexto (en Send), así que tras cancelar termina un intento y
// nada más.
func (d *WebhookDispatcher) intento(ctx context.Context, destino, kind, firma string, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destino, bytes.NewReader(body))
	if err != nil {
		return 0, sinURL(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "splitstream/"+d.version)
	req.Header.Set("X-Splitstream-Event", kind)
	if firma != "" {
		req.Header.Set("X-Splitstream-Signature", firma)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, sinURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, fmt.Errorf("el servidor respondió %d", resp.StatusCode)
}

// sinURL quita la URL del error de red. Un *url.Error imprime la URL entera, y en Discord
// o Slack la URL ES el secreto: sin esto acabaría en el log de dispatch, en el `last_error`
// de la fila del webhook y en el cuerpo del 502 del botón «Probar» (spec §8).
func sinURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// rank ordena los niveles para comparar con min_level.
func rank(l store.Level) int {
	switch l {
	case store.LevelWarn:
		return 1
	case store.LevelError:
		return 2
	default:
		return 0
	}
}
