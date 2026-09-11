// Package platforms es la capa de plataformas del roadmap §8: una interfaz por
// CAPACIDAD, no por plataforma, para que el panel enseñe lo que cada destino puede hacer
// y para que Facebook, X o TikTok puedan no tener nada sin que nadie tenga que fingir.
// El motor no importa este paquete jamás: si Twitch cambia su API, el relay ni se entera.
package platforms

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

// ID identifica una plataforma con proveedor. Coincide con store.Platform para las tres
// que lo tienen; las demás (facebook, x, tiktok, custom) no llegan aquí.
type ID string

const (
	Twitch  ID = "twitch"
	YouTube ID = "youtube"
	Kick    ID = "kick"
)

// Capabilities es lo que la plataforma sabe hacer a través de su proveedor.
type Capabilities struct {
	Title     bool `json:"title"`
	Category  bool `json:"category"`
	ChatRead  bool `json:"chat"`
	Schedule  bool `json:"schedule"`
	IngestKey bool `json:"ingest_key"`
	// RequiresOwnApp: la cuota es por app y hay que traer credenciales propias (YouTube).
	RequiresOwnApp bool `json:"requires_own_app"`
	// RequiresPublicURL: el chat llega por webhook y hace falta HTTPS público (Kick).
	RequiresPublicURL bool `json:"requires_public_url"`
}

var (
	// ErrAuthPending: la persona todavía no ha autorizado; volver a preguntar tras Interval.
	ErrAuthPending = errors.New("autorización pendiente")
	// ErrAuthExpired: el código venció; hay que empezar de nuevo.
	ErrAuthExpired = errors.New("el código de autorización venció")
	// ErrNoClientID: el proveedor no tiene client_id (ni incluido ni por entorno).
	ErrNoClientID = errors.New("la plataforma no tiene client_id configurado")
	// ErrUnauthorized: la plataforma rechazó el token (401): toca refrescar o reconectar.
	ErrUnauthorized = errors.New("la plataforma rechazó el token")
	// ErrRateLimited: 429; el mensaje dice cuándo reintentar.
	ErrRateLimited = errors.New("la plataforma pide esperar")
	// ErrChatRevoked: la persona quitó el permiso de leer el chat. No se reintenta: el
	// agregador para esa cuenta y avisa, en vez de reconectar para que se la rechacen otra vez.
	ErrChatRevoked = errors.New("la plataforma revocó la suscripción al chat")
	// ErrNoBroadcast: el destino no tiene una emisión creada (ni CreateBroadcast, ni clave
	// de ingesta que leer).
	ErrNoBroadcast = errors.New("el destino no tiene emisión")
	// ErrWebhookRejected: ParseWebhook no pudo verificar la firma o el payload del webhook.
	ErrWebhookRejected = errors.New("webhook rechazado")
)

// Credentials son las de la app propia del usuario; vacías con la app incluida (Twitch).
type Credentials = store.Credentials

// AuthPrompt es lo que la interfaz enseña durante el flujo de autorización, sea de
// dispositivo (Twitch, YouTube) o con redirect (Kick).
type AuthPrompt struct {
	State           string
	VerificationURI string
	UserCode        string
	// DeviceCode es secreto del flujo: no sale de la API.
	DeviceCode string
	// RedirectURL es lo que el panel abre para empezar un flujo con redirect: la URL de
	// autorización de la plataforma con nuestro redirect_uri y el state ya puestos.
	RedirectURL string
	// CodeVerifier es el secreto de PKCE del flujo con redirect: no sale de la API.
	CodeVerifier string
	// RedirectURI es la redirect_uri usada para BeginRedirect; el intercambio del código
	// por tokens tiene que mandar la misma, o la plataforma lo rechaza.
	RedirectURI string
	ExpiresAt   time.Time
	Interval    time.Duration
}

// Identity es lo que la plataforma dice de un token válido.
type Identity struct {
	ExternalID  string
	DisplayName string
	Scopes      []string
	ExpiresIn   time.Duration
}

// Provider es lo mínimo que toda plataforma con cuentas implementa. Las credenciales son
// las de la app propia del usuario (RequiresOwnApp); vacías con la app incluida.
type Provider interface {
	ID() ID
	Capabilities() Capabilities
	// Configured dice si hay client_id; sin él el panel explica en vez de fallar.
	Configured() bool
	BeginAuth(ctx context.Context, creds Credentials) (AuthPrompt, error)
	// PollAuth pregunta UNA vez. Quien lo llama respeta prompt.Interval.
	PollAuth(ctx context.Context, creds Credentials, prompt AuthPrompt) (store.NewAccount, error)
	Refresh(ctx context.Context, acct store.Account, creds Credentials, refresh crypto.Secret) (store.Tokens, error)
	Validate(ctx context.Context, access crypto.Secret) (Identity, error)
}

// RedirectAuth es el flujo con redirect (Kick): el navegador va a la plataforma y vuelve
// al panel por /api/platforms/{p}/callback con un código. El state lo firma httpapi.
type RedirectAuth interface {
	BeginRedirect(ctx context.Context, creds Credentials, redirectURI, state string) (AuthPrompt, error)
	CompleteRedirect(ctx context.Context, creds Credentials, prompt AuthPrompt, code string) (store.NewAccount, error)
}

// TokenSource entrega un token vigente cada vez que se llama: el lector de chat lo usa al
// conectar y al reconectar, así un token renovado a mitad de sesión se usa sin reiniciar.
type TokenSource func(ctx context.Context) (crypto.Secret, error)

type Category struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BoxArtURL string `json:"box_art_url"`
}

// ChatMessage es un mensaje de chat tal como lo entrega un proveedor.
type ChatMessage struct {
	Platform  ID
	AccountID int64
	AuthorID  string
	Author    string
	Text      string
	Color     string
	Badges    []string
	MessageID string
	At        time.Time
	// BroadcasterID identifica el canal dueño del mensaje. Lo usan los proveedores por
	// webhook (Kick): la plataforma no dice a qué cuenta nuestra pertenece el mensaje, así
	// que el webhook resuelve la cuenta por este id antes de meterlo en el bus de chat.
	BroadcasterID string
}

type TitleSetter interface {
	SetTitle(ctx context.Context, acct store.Account, token crypto.Secret, title string) error
}

type CategorySetter interface {
	SearchCategories(ctx context.Context, token crypto.Secret, q string) ([]Category, error)
	SetCategory(ctx context.Context, acct store.Account, token crypto.Secret, id string) error
}

// ChatReader bloquea hasta ctx.Done(), reconectando por su cuenta; solo devuelve error si
// no puede ni empezar (sin token, sin client_id).
type ChatReader interface {
	ReadChat(ctx context.Context, acct store.Account, token TokenSource, out chan<- ChatMessage) error
}

// BroadcastRequest es lo que se pide al crear una emisión.
type BroadcastRequest struct {
	Title       string
	Privacy     string // public | unlisted | private
	ScheduledAt time.Time
}

// Broadcast es lo que devuelve la plataforma al crear una emisión (o al leer la clave).
type Broadcast struct {
	Ref, StreamRef, LiveChatID string
	IngestURL                  string
	Key                        crypto.Secret
	WatchURL                   string
}

// BroadcastScheduler crea y controla el ciclo de vida de una emisión (YouTube: hay que
// crear el broadcast antes de emitir, y arrancarlo/pararlo aparte de RTMP).
type BroadcastScheduler interface {
	CreateBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, req BroadcastRequest) (Broadcast, error)
	StartBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, ref string) error
	EndBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, ref string) error
}

// IngestKeyProvider trae la URL y la clave de ingesta del canal (Kick: la clave es del
// canal, no de una emisión creada aparte).
type IngestKeyProvider interface {
	IngestKey(ctx context.Context, acct store.Account, token crypto.Secret) (url string, key crypto.Secret, err error)
}

// ChatWebhook es el chat que llega por webhook entrante (Kick): la plataforma llama a
// nosotros. ParseWebhook verifica la firma y devuelve los mensajes; ErrWebhookRejected
// con el motivo cuando no pasa.
type ChatWebhook interface {
	SubscribeChat(ctx context.Context, acct store.Account, token crypto.Secret, webhookURL string) error
	UnsubscribeChat(ctx context.Context, acct store.Account, token crypto.Secret) error
	ParseWebhook(hdr http.Header, body []byte) ([]ChatMessage, error)
}

// BroadcastTitleSetter pone el título de una emisión concreta, no del canal (YouTube: el
// título vive en el broadcast, no en el canal como en Twitch/Kick).
type BroadcastTitleSetter interface {
	SetBroadcastTitle(ctx context.Context, acct store.Account, token crypto.Secret, ref, title string) error
}

// QuotaSink recibe las unidades de cuota que gasta cada llamada (YouTube).
type QuotaSink func(units int)
