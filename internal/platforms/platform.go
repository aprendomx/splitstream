// Package platforms es la capa de plataformas del roadmap §8: una interfaz por
// CAPACIDAD, no por plataforma, para que el panel enseñe lo que cada destino puede hacer
// y para que Facebook, X o TikTok puedan no tener nada sin que nadie tenga que fingir.
// El motor no importa este paquete jamás: si Twitch cambia su API, el relay ni se entera.
package platforms

import (
	"context"
	"errors"
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
)

// AuthPrompt es lo que la interfaz enseña durante el flujo de dispositivo.
type AuthPrompt struct {
	State           string
	VerificationURI string
	UserCode        string
	// DeviceCode es secreto del flujo: no sale de la API.
	DeviceCode string
	ExpiresAt  time.Time
	Interval   time.Duration
}

// Identity es lo que la plataforma dice de un token válido.
type Identity struct {
	ExternalID  string
	DisplayName string
	Scopes      []string
	ExpiresIn   time.Duration
}

// Provider es lo mínimo que toda plataforma con cuentas implementa.
type Provider interface {
	ID() ID
	Capabilities() Capabilities
	// Configured dice si hay client_id; sin él el panel explica en vez de fallar.
	Configured() bool
	BeginAuth(ctx context.Context) (AuthPrompt, error)
	// PollAuth pregunta UNA vez. Quien lo llama respeta prompt.Interval.
	PollAuth(ctx context.Context, prompt AuthPrompt) (store.NewAccount, error)
	Refresh(ctx context.Context, refresh crypto.Secret) (store.Tokens, error)
	Validate(ctx context.Context, access crypto.Secret) (Identity, error)
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

// Declaradas para la v0.12; ningún proveedor las implementa todavía.
type BroadcastScheduler interface {
	CreateBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, title string, start time.Time) (string, error)
}
type IngestKeyProvider interface {
	IngestKey(ctx context.Context, acct store.Account, token crypto.Secret, broadcastRef string) (url string, key crypto.Secret, err error)
}
