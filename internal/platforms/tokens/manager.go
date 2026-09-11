// Package tokens mantiene vivos los tokens de las cuentas: refresca los que caducan,
// valida cada hora (Twitch lo exige) y marca `reauth` cuando ya no hay nada que hacer.
// Es el ÚNICO sitio que lee tokens de la base.
package tokens

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// ErrReauth: la cuenta necesita que la persona la vuelva a conectar.
var ErrReauth = errors.New("la cuenta necesita reconectarse")

// refreshBefore es cuánto antes de la caducidad se refresca: con margen para que una
// llamada larga (una sesión de chat que arranca) no salga con un token a segundos de morir.
const refreshBefore = 5 * time.Minute

type Manager struct {
	db        *store.DB
	cipher    *crypto.Cipher
	providers func(platforms.ID) (platforms.Provider, bool)

	// OnReauth se llama, fuera de todo lock, cuando una cuenta pasa a `reauth`: main.go
	// registra el evento. Nil en los tests que no lo miran.
	OnReauth func(store.Account)
	// Interval es cada cuánto Run valida (una hora, lo que pide Twitch). Now para tests.
	Interval time.Duration
	Now      func() time.Time
	Logger   *slog.Logger

	mu       sync.Mutex
	inflight map[int64]*llamada
}

// llamada es un refresco en curso: single-flight por cuenta.
type llamada struct {
	hecho chan struct{}
	tok   crypto.Secret
	err   error
}

func NewManager(db *store.DB, c *crypto.Cipher, providers func(platforms.ID) (platforms.Provider, bool)) *Manager {
	return &Manager{db: db, cipher: c, providers: providers, Interval: time.Hour, Now: time.Now,
		Logger: slog.Default(), inflight: map[int64]*llamada{}}
}

// Token devuelve un token vigente de la cuenta, refrescándolo si caduca en menos de
// refreshBefore. Dos llamadas concurrentes comparten UN refresco: con Twitch el refresh
// token rota y el usado deja de valer, así que dos refrescos en paralelo romperían la
// cuenta.
func (m *Manager) Token(ctx context.Context, accountID int64) (crypto.Secret, error) {
	acct, err := m.db.AccountByID(ctx, accountID)
	if err != nil {
		return "", err
	}
	if acct.Status == store.AccountStatusReauth {
		return "", ErrReauth
	}
	t, err := m.db.AccountTokens(ctx, m.cipher, accountID)
	if err != nil {
		return "", err
	}
	if t.ExpiresAt.IsZero() || m.Now().Add(refreshBefore).Before(t.ExpiresAt) {
		return t.Access, nil
	}
	return m.refresh(ctx, *acct, t)
}

// Source adapta Token a platforms.TokenSource para el lector de chat.
func (m *Manager) Source(accountID int64) platforms.TokenSource {
	return func(ctx context.Context) (crypto.Secret, error) { return m.Token(ctx, accountID) }
}

func (m *Manager) refresh(ctx context.Context, acct store.Account, t store.Tokens) (crypto.Secret, error) {
	m.mu.Lock()
	if l, ok := m.inflight[acct.ID]; ok {
		m.mu.Unlock()
		select {
		case <-l.hecho:
			return l.tok, l.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	l := &llamada{hecho: make(chan struct{})}
	m.inflight[acct.ID] = l
	m.mu.Unlock()

	l.tok, l.err = m.doRefresh(ctx, acct, t)
	m.mu.Lock()
	delete(m.inflight, acct.ID)
	m.mu.Unlock()
	close(l.hecho)
	return l.tok, l.err
}

// refreshTimeout acota la rotación desligada del contexto de quien llama: sin plazo
// propio, un proveedor colgado dejaría el single-flight ocupado para siempre.
const refreshTimeout = 30 * time.Second

func (m *Manager) doRefresh(ctx context.Context, acct store.Account, t store.Tokens) (crypto.Secret, error) {
	// La rotación no se aborta a medias: quien llama puede rendirse (el `select` de
	// refresh devuelve ctx.Err() en cuanto su contexto muere), pero si Twitch ya rotó el
	// refresh token y cancelásemos antes del SaveTokens, el par de la base quedaría
	// inservible y la cuenta muerta. Por eso se sigue con un contexto desligado y con
	// plazo propio, para que la petición y el guardado terminen pase lo que pase.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()

	p, ok := m.providers(platforms.ID(acct.Platform))
	if !ok {
		return "", errors.New("plataforma sin proveedor: " + string(acct.Platform))
	}
	if t.Refresh.Reveal() == "" {
		return "", m.marcarReauth(ctx, acct)
	}
	nuevo, err := p.Refresh(ctx, t.Refresh)
	if err != nil {
		if errors.Is(err, platforms.ErrUnauthorized) {
			return "", m.marcarReauth(ctx, acct)
		}
		// Red caída o 5xx: no se castiga a la cuenta; se devuelve el error y el que
		// llama decide (el chat reintenta, la API responde 502).
		return "", err
	}
	// Se guarda ANTES de devolver: si muriéramos entre medias, el refresh token viejo ya
	// no vale y el nuevo no está en ningún sitio.
	if err := m.db.SaveTokens(ctx, m.cipher, acct.ID, nuevo); err != nil {
		return "", err
	}
	return nuevo.Access, nil
}

func (m *Manager) marcarReauth(ctx context.Context, acct store.Account) error {
	if err := m.db.SetAccountStatus(ctx, acct.ID, store.AccountStatusReauth); err != nil {
		return err
	}
	m.Logger.Warn("la cuenta necesita reconectarse", "plataforma", acct.Platform, "cuenta", acct.DisplayName)
	if m.OnReauth != nil {
		acct.Status = store.AccountStatusReauth
		m.OnReauth(acct)
	}
	return ErrReauth
}

// Run valida cada cuenta cada Interval, como exige Twitch, y refresca la que responda 401.
// Vuelve en cuanto ctx termina; los fallos de red se loguean a debug y se reintentan en la
// siguiente vuelta.
func (m *Manager) Run(ctx context.Context) {
	// Twitch pide validar «al arrancar y cada hora después»: la primera pasada va ya, sin
	// esperar el primer tick. Sin cuentas en la base no hace ninguna petición.
	m.validarTodas(ctx)
	t := time.NewTimer(m.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.validarTodas(ctx)
		t.Reset(m.Interval)
	}
}

func (m *Manager) validarTodas(ctx context.Context) {
	cuentas, err := m.db.Accounts(ctx)
	if err != nil {
		m.Logger.Debug("no se pudieron listar las cuentas", "err", err)
		return
	}
	for _, acct := range cuentas {
		if acct.Status != store.AccountStatusOK {
			continue
		}
		p, ok := m.providers(platforms.ID(acct.Platform))
		if !ok {
			continue
		}
		t, err := m.db.AccountTokens(ctx, m.cipher, acct.ID)
		if err != nil {
			continue
		}
		if _, err := p.Validate(ctx, t.Access); err == nil {
			continue
		} else if !errors.Is(err, platforms.ErrUnauthorized) {
			m.Logger.Debug("no se pudo validar el token", "cuenta", acct.DisplayName, "err", err)
			continue
		}
		if _, err := m.refresh(ctx, acct, t); err != nil && !errors.Is(err, ErrReauth) {
			m.Logger.Debug("no se pudo refrescar el token", "cuenta", acct.DisplayName, "err", err)
		}
	}
}
