package chat

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// TokenSourcer entrega una fuente de tokens por cuenta: es tokens.Manager sin importarlo.
type TokenSourcer interface {
	Source(accountID int64) platforms.TokenSource
}

type Config struct {
	DB       *store.DB
	Events   *events.Bus
	Chat     *Bus
	Registry *platforms.Registry
	Tokens   TokenSourcer
	Logger   *slog.Logger
	// BatchEvery y BatchSize acotan la escritura: un lote cada 100 ms o cada 50 mensajes.
	BatchEvery time.Duration
	BatchSize  int
}

type Aggregator struct {
	cfg Config

	mu        sync.Mutex
	sessionID int64
	cancel    context.CancelFunc
	in        chan platforms.ChatMessage
	lectores  sync.WaitGroup

	mensajes  map[platforms.ID]*atomic.Uint64
	conectado map[platforms.ID]*atomic.Bool
	dropped   atomic.Uint64

	// eventos y soltar son la suscripción al bus, tomada en el constructor y no en Run:
	// así la garantía de no perder el publisher_connected de la primera sesión no
	// depende de que la goroutine de Run arranque antes que la ingesta.
	eventos <-chan store.Event
	soltar  func()
}

func NewAggregator(cfg Config) *Aggregator {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.BatchEvery <= 0 {
		cfg.BatchEvery = 100 * time.Millisecond
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	a := &Aggregator{cfg: cfg, mensajes: map[platforms.ID]*atomic.Uint64{}, conectado: map[platforms.ID]*atomic.Bool{}}
	a.eventos, a.soltar = cfg.Events.Subscribe(64)
	for id := range cfg.Registry.AllCapabilities() {
		a.mensajes[id] = &atomic.Uint64{}
		a.conectado[id] = &atomic.Bool{}
	}
	return a
}

// Run escucha el bus de eventos: publisher_connected arranca los lectores de la sesión y
// publisher_disconnected los para. Vuelve con ctx. Se engancha al bus y no al motor para
// que el motor siga sin conocer este paquete.
func (a *Aggregator) Run(ctx context.Context) {
	ch := a.eventos
	defer a.soltar()
	defer a.parar()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			switch ev.Kind {
			case "publisher_connected":
				if ev.SessionID != nil {
					a.arrancar(ctx, *ev.SessionID)
				}
			case "publisher_disconnected":
				a.parar()
			}
		}
	}
}

func (a *Aggregator) arrancar(ctx context.Context, sessionID int64) {
	a.parar()
	a.cfg.Chat.Reset()
	cuentas, err := a.cuentasConChat(ctx)
	if err != nil {
		// dropped cuenta MENSAJES perdidos, no intentos de arranque: si no se pudo ni
		// listar cuentas, ningún mensaje llegó a leerse, así que no hay nada que sumar
		// aquí. El log es la señal de este fallo.
		a.cfg.Logger.Error("no se pudieron listar las cuentas para el chat", "err", err)
		return
	}
	if len(cuentas) == 0 {
		return
	}
	sctx, cancel := context.WithCancel(ctx)
	in := make(chan platforms.ChatMessage, 512)
	a.mu.Lock()
	a.sessionID, a.cancel, a.in = sessionID, cancel, in
	a.mu.Unlock()

	a.lectores.Add(1)
	go func() {
		defer a.lectores.Done()
		a.escribir(sctx, sessionID, in)
	}()
	for _, c := range cuentas {
		p, _ := a.cfg.Registry.Get(platforms.ID(c.Platform))
		lector, ok := p.(platforms.ChatReader)
		if !ok {
			continue
		}
		acct := c
		// El evento de conexión se registra ANTES de lanzar el lector: si ReadChat falla al
		// instante (token revocado), su chat_disconnected no puede adelantarse a este.
		a.registrar(sctx, sessionID, store.LevelInfo, "chat_connected", "leyendo el chat de "+acct.DisplayName)
		a.lectores.Add(1)
		go func() {
			defer a.lectores.Done()
			a.conectado[platforms.ID(acct.Platform)].Store(true)
			defer a.conectado[platforms.ID(acct.Platform)].Store(false)
			if err := lector.ReadChat(sctx, acct, a.cfg.Tokens.Source(acct.ID), in); err != nil && sctx.Err() == nil {
				a.cfg.Logger.Warn("el chat se detuvo", "plataforma", acct.Platform, "cuenta", acct.DisplayName, "err", err)
				msg := "el chat de " + acct.DisplayName + " se detuvo: " + err.Error()
				if errors.Is(err, platforms.ErrChatRevoked) {
					msg = "Twitch revocó el permiso de chat; reconecta la cuenta"
				}
				a.registrar(sctx, sessionID, store.LevelWarn, "chat_disconnected", msg)
			}
		}()
	}
}

func (a *Aggregator) parar() {
	a.mu.Lock()
	cancel := a.cancel
	a.cancel, a.sessionID, a.in = nil, 0, nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		a.lectores.Wait()
	}
}

// Ingest mete en la sesión viva mensajes que no vienen de un ReadChat (el webhook de
// Kick). Sin sesión se descartan y se cuentan: el chat pertenece a la sesión.
func (a *Aggregator) Ingest(msgs []platforms.ChatMessage) int {
	a.mu.Lock()
	in := a.in
	a.mu.Unlock()
	if in == nil {
		a.dropped.Add(uint64(len(msgs)))
		return 0
	}
	n := 0
	for _, m := range msgs {
		select {
		case in <- m:
			n++
		default:
			a.dropped.Add(1)
		}
	}
	return n
}

// cuentasConChat: cuentas en `ok` cuya plataforma lee chat y con al menos un destino
// habilitado vinculado. Un destino que se vincula en caliente entra en la siguiente sesión.
func (a *Aggregator) cuentasConChat(ctx context.Context) ([]store.Account, error) {
	cuentas, err := a.cfg.DB.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	dests, err := a.cfg.DB.ListDestinations(ctx)
	if err != nil {
		return nil, err
	}
	habilitadas := map[int64]bool{}
	for _, d := range dests {
		if d.Enabled && d.AccountID != nil {
			habilitadas[*d.AccountID] = true
		}
	}
	var out []store.Account
	for _, c := range cuentas {
		if c.Status != store.AccountStatusOK || !habilitadas[c.ID] {
			continue
		}
		if caps, ok := a.cfg.Registry.AllCapabilities()[platforms.ID(c.Platform)]; !ok || !caps.ChatRead {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// escribir persiste por lotes y reparte por el bus. Si la base falla, el lote se cuenta
// como descartado y se sigue: el chat jamás bloquea a nadie.
func (a *Aggregator) escribir(ctx context.Context, sessionID int64, in <-chan platforms.ChatMessage) {
	t := time.NewTicker(a.cfg.BatchEvery)
	defer t.Stop()
	lote := make([]store.NewChatMessage, 0, a.cfg.BatchSize)
	vaciar := func() {
		if len(lote) == 0 {
			return
		}
		if err := a.cfg.DB.InsertChatMessages(context.WithoutCancel(ctx), lote); err != nil {
			a.dropped.Add(uint64(len(lote)))
			a.cfg.Logger.Debug("no se pudo guardar un lote de chat", "n", len(lote), "err", err)
		}
		lote = lote[:0]
	}
	defer vaciar()
	acumular := func(m platforms.ChatMessage) {
		// Guard: una plataforma que el registro no conoce (proveedor mal configurado,
		// o test con un doble) no debe hacer que esto entre en pánico por indexar un
		// mapa que no la tiene.
		if c := a.mensajes[m.Platform]; c != nil {
			c.Add(1)
		}
		a.cfg.Chat.Publish(Message{SessionID: sessionID, ChatMessage: m})
		acct := m.AccountID
		lote = append(lote, store.NewChatMessage{
			SessionID: sessionID, AccountID: &acct, Platform: string(m.Platform), AuthorID: m.AuthorID,
			Author: m.Author, Text: m.Text, Color: m.Color, Badges: m.Badges, MessageID: m.MessageID, At: m.At,
		})
		if len(lote) >= a.cfg.BatchSize {
			vaciar()
		}
	}
	for {
		select {
		case <-ctx.Done():
			// Lo que quedó en el canal al terminar la sesión se guarda también: si el
			// select eligió ctx.Done con mensajes ya en cola, no se pierden en silencio.
			for {
				select {
				case m := <-in:
					acumular(m)
				default:
					return
				}
			}
		case <-t.C:
			vaciar()
		case m := <-in:
			acumular(m)
		}
	}
}

func (a *Aggregator) registrar(ctx context.Context, sessionID int64, level store.Level, kind, msg string) {
	sid := sessionID
	if _, err := a.cfg.DB.LogEvent(context.WithoutCancel(ctx), store.Event{SessionID: &sid, Level: level, Kind: kind, Message: msg}); err != nil {
		a.cfg.Logger.Debug("no se pudo registrar el evento del chat", "err", err)
	}
}

// Stats para /metrics.
func (a *Aggregator) Stats() (messages map[platforms.ID]uint64, connected map[platforms.ID]bool, dropped uint64) {
	messages, connected = map[platforms.ID]uint64{}, map[platforms.ID]bool{}
	for id, c := range a.mensajes {
		messages[id] = c.Load()
	}
	for id, c := range a.conectado {
		connected[id] = c.Load()
	}
	return messages, connected, a.dropped.Load()
}
