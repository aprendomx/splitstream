# v0.12 «YouTube y Kick» — Plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Conectar YouTube (flujo de dispositivo) y Kick (redirect con PKCE) con credenciales propias del usuario, crear emisiones de YouTube desde el panel y escribir en el destino la URL y la clave que devuelve la API, leer la clave de Kick por API, cambiar títulos, y leer el chat de YouTube (sondeo con presupuesto de cuota) y de Kick (webhook firmado, solo con URL pública).

**Architecture:** Sobre la capa de la v0.11. `platforms` gana `Credentials` en `BeginAuth`/`PollAuth`/`Refresh`, la capacidad `RedirectAuth` (Kick), `ChatWebhook` (Kick), y las de emisión (`BroadcastScheduler`, `IngestKeyProvider`) con tipos concretos. Dos proveedores nuevos (`youtube`, `kick`) calcados del de Twitch (cliente HTTP con hosts inyectables, `do`, errores sin tokens). `quota.Counter` persiste unidades por cuenta y día; YouTube suma en cada llamada. El store guarda credenciales cifradas, `destination_broadcasts` (emisión vinculada al destino y «clave por API») y `quota_usage`. `httpapi` añade dos rutas públicas protegidas por criptografía (callback con `state` firmado; webhook con firma RSA de Kick) y los endpoints de emisión; `chat.Aggregator.Ingest` mete el webhook en el flujo de la sesión. El panel gana el asistente de credenciales, la rama de redirect, «Crear emisión y traer la clave», botones de emisión y la barra de cuota.

**Tech Stack:** Go 1.25 (`crypto/rsa`, `crypto/sha256`, `crypto/hmac`, `net/http`), SQLite (migración 0009), Vue 3 + Quasar + Pinia.

**Spec:** `docs/superpowers/specs/2026-09-11-youtube-kick-design.md` (sobre el spec de la v0.11 `docs/superpowers/specs/2026-09-11-twitch-design.md`, el spec base y el plan maestro §7).

## Global Constraints

- **El motor no se entera**: `internal/relay` e `internal/rtmpio` no importan `internal/platforms/...` ni `internal/chat`; estos no importan go-rtmp, `internal/relay` ni `internal/rtmpio`; `internal/httpapi` no importa go-rtmp, `internal/rtmpio`, `internal/webtls`, `platforms/twitch`, `platforms/youtube`, `platforms/kick` ni `platforms/tokens` (los proveedores entran por el registro; el manager por `TokenGetter`; la cuota por una interfaz). La CI lo vigila (Task 10).
- **Un solo mecanismo de secretos**: `client_secret`, tokens, `code_verifier`, `device_code` y claves de stream van cifrados o solo en memoria como `crypto.Secret`; **jamás** en logs, eventos, errores, DTOs, respuestas ni URLs (salvo la clave dentro del `rtmp_url`/`key` del destino, que ya existe y va enmascarada). El `client_id` de una app propia no sale por la API una vez guardado.
- **Ningún test habla con internet**: hosts inyectables; sin cuentas nada llama a nadie.
- **Cuota visible**: cada llamada a YouTube suma; el chat se pausa por presupuesto con evento.
- **Migraciones idempotentes**, sin `ALTER TABLE`; `SchemaVersion` 9; `TestOpenCreatesSchema` con 13 tablas.
- **`statusDTO` único**; DTOs nuevos en snake_case y en `TestDTOFieldNamesAreSnakeCase`.
- **Rutas públicas nuevas** (`callback`, `webhook`) fuera de `protegida`, protegidas por firma; todo lo demás con sesión.
- **Comentarios, commits, copys y errores en español** explicando el porqué. Tests con `-race`, estables con `GOMAXPROCS=2`. Cero dependencias nuevas (Go ni npm); `go mod tidy` no se ejecuta.
- **Rama `feat/youtube-kick` desde `main`; nada se fusiona con la CI en rojo.** Commits con los trailers de la sesión.
- **Patrón**: cada proveedor nuevo se escribe **calcando** `internal/platforms/twitch` (`client.go`: `Options` con hosts, `Provider`, `New`, `do` con 401→`ErrUnauthorized`/429→`ErrRateLimited`, `sinQuery`, `httpError`; tests con `httptest` y fixtures en `testdata/`). Donde este plan dice «como en twitch», el implementador copia esa forma.

---

### Task 1: Store — credenciales propias, emisiones por destino y cuota

**Files:**
- Create: `internal/store/migrations/0009_broadcasts_quota.sql`, `internal/store/broadcasts.go`, `internal/store/broadcasts_test.go`, `internal/store/quota.go`, `internal/store/quota_test.go`
- Modify: `internal/store/db.go` (`SchemaVersion = 9`), `internal/store/db_test.go` (13 tablas), `internal/store/accounts.go` (`NewAccount.OwnApp/ClientID/ClientSecret`, `AccountCredentials`, `UpsertAccount` escribe las tres columnas), `internal/store/accounts_test.go`, `internal/store/destinations.go` (`Destination.KeyFromAPI` por `LEFT JOIN destination_broadcasts`), `internal/store/retention.go` (`PruneQuota`)

**Interfaces:**
- Consumes: `crypto.Cipher`, `crypto.Secret`, `InTx`, `formatTime`, `notFound`/`invalidInput`.
- Produces: `store.Credentials{ClientID, ClientSecret crypto.Secret}`; `NewAccount.OwnApp bool`, `NewAccount.Credentials Credentials`; `AccountCredentials(ctx, c, id) (Credentials, error)` (vacías si `!OwnApp`); `store.Broadcast{DestinationID, AccountID int64; Platform Platform; BroadcastRef, StreamRef, LiveChatID string; KeyFromAPI bool; Status string; CreatedAt, UpdatedAt time.Time}`; `SetBroadcast(ctx, Broadcast) error` (upsert por destino), `BroadcastFor(ctx, destID) (*Broadcast, error)`, `SetBroadcastStatus(ctx, destID, status) error`, `ClearBroadcast(ctx, destID) error`; constantes `BroadcastCreated/Live/Complete`; `Destination.KeyFromAPI bool`; `AddQuota(ctx, accountID int64, day string, units int) error`, `QuotaUsed(ctx, accountID, day) (int, error)`, `PruneQuota(ctx, olderThanDay string) (int64, error)`.

- [ ] **Step 1: Migración**

`internal/store/migrations/0009_broadcasts_quota.sql`:

```sql
-- v0.12: la emisión (o la clave por API) vinculada a un destino, y la cuota de YouTube por
-- cuenta y día. Sin ALTER TABLE: idempotente como las demás.
CREATE TABLE IF NOT EXISTS destination_broadcasts (
    destination_id INTEGER PRIMARY KEY REFERENCES destinations (id) ON DELETE CASCADE,
    account_id     INTEGER NOT NULL REFERENCES platform_accounts (id) ON DELETE CASCADE,
    platform       TEXT    NOT NULL,
    broadcast_ref  TEXT    NOT NULL DEFAULT '',
    stream_ref     TEXT    NOT NULL DEFAULT '',
    live_chat_id   TEXT    NOT NULL DEFAULT '',
    key_from_api   INTEGER NOT NULL DEFAULT 1 CHECK (key_from_api IN (0,1)),
    status         TEXT    NOT NULL DEFAULT 'created' CHECK (status IN ('created','live','complete')),
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS quota_usage (
    account_id INTEGER NOT NULL REFERENCES platform_accounts (id) ON DELETE CASCADE,
    day        TEXT    NOT NULL,
    units      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, day)
);
```

`db.go`: `SchemaVersion = 9`. `db_test.go`: `want` gana `"destination_broadcasts"` y `"quota_usage"` (13 tablas, ordenadas).

- [ ] **Step 2: Tests**

En `accounts_test.go`:

```go
func TestOwnAppCredentialsAreEncryptedAndOnlyReadableThroughAccountCredentials(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a, err := db.UpsertAccount(ctx, c, store.NewAccount{
		Platform: store.PlatformYouTube, ExternalID: "UC123", DisplayName: "Mi canal",
		Scopes: []string{"https://www.googleapis.com/auth/youtube"},
		Tokens: store.Tokens{Access: "acc", Refresh: "ref", ExpiresAt: time.Now().Add(time.Hour)},
		OwnApp: true, Credentials: store.Credentials{ClientID: "cid.apps.googleusercontent.com", ClientSecret: "GOCSPX-secreto"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !a.OwnApp {
		t.Error("OwnApp debería ser true")
	}
	creds, err := db.AccountCredentials(ctx, c, a.ID)
	if err != nil || creds.ClientID.Reveal() != "cid.apps.googleusercontent.com" || creds.ClientSecret.Reveal() != "GOCSPX-secreto" {
		t.Fatalf("creds = %+v, %v", creds, err)
	}
	var blob []byte
	db.SQL().QueryRowContext(ctx, `SELECT client_secret_encrypted FROM platform_accounts WHERE id = ?`, a.ID).Scan(&blob)
	if strings.Contains(string(blob), "GOCSPX") {
		t.Error("el client_secret está en claro")
	}
	// Reconectar con credenciales nuevas las sustituye; sin OwnApp las deja en NULL.
	b, _ := db.UpsertAccount(ctx, c, store.NewAccount{Platform: store.PlatformYouTube, ExternalID: "UC123", DisplayName: "Mi canal",
		Tokens: store.Tokens{Access: "acc2"}, OwnApp: true, Credentials: store.Credentials{ClientID: "otro", ClientSecret: "s2"}})
	creds, _ = db.AccountCredentials(ctx, c, b.ID)
	if creds.ClientID.Reveal() != "otro" {
		t.Errorf("no se sustituyeron: %+v", creds)
	}
	tw := cuentaDePrueba(t, db, c, "42")
	creds, err = db.AccountCredentials(ctx, c, tw.ID)
	if err != nil || creds.ClientID.Reveal() != "" || creds.ClientSecret.Reveal() != "" {
		t.Errorf("sin app propia: creds = %+v, %v", creds, err)
	}
	if _, err := db.UpsertAccount(ctx, c, store.NewAccount{Platform: store.PlatformKick, ExternalID: "1", DisplayName: "k",
		Tokens: store.Tokens{Access: "a"}, OwnApp: true}); !errors.Is(err, store.ErrInvalidInput) {
		t.Errorf("OwnApp sin credenciales debería ser inválido: %v", err)
	}
}
```

`broadcasts_test.go`:

```go
package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestBroadcastIsUpsertedPerDestinationAndMarksTheKey(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a := cuentaDePrueba(t, db, c, "42")
	d, _ := db.CreateDestination(ctx, c, store.NewDestination{Name: "yt", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})

	if _, err := db.BroadcastFor(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sin emisión: err = %v", err)
	}
	if err := db.SetBroadcast(ctx, store.Broadcast{DestinationID: d.ID, AccountID: a.ID, Platform: store.PlatformTwitch,
		BroadcastRef: "b1", StreamRef: "s1", LiveChatID: "chat1", KeyFromAPI: true}); err != nil {
		t.Fatal(err)
	}
	b, err := db.BroadcastFor(ctx, d.ID)
	if err != nil || b.BroadcastRef != "b1" || b.Status != store.BroadcastCreated || !b.KeyFromAPI || b.LiveChatID != "chat1" {
		t.Fatalf("broadcast = %+v, %v", b, err)
	}
	dd, _ := db.DestinationByID(ctx, d.ID)
	if !dd.KeyFromAPI {
		t.Error("Destination.KeyFromAPI debería ser true")
	}
	if err := db.SetBroadcastStatus(ctx, d.ID, store.BroadcastLive); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBroadcastStatus(ctx, d.ID, "raro"); !errors.Is(err, store.ErrInvalidInput) {
		t.Errorf("status inválido: %v", err)
	}
	// Upsert: una emisión nueva sustituye la anterior y vuelve a created.
	db.SetBroadcast(ctx, store.Broadcast{DestinationID: d.ID, AccountID: a.ID, Platform: store.PlatformTwitch, BroadcastRef: "b2", KeyFromAPI: true})
	b, _ = db.BroadcastFor(ctx, d.ID)
	if b.BroadcastRef != "b2" || b.Status != store.BroadcastCreated {
		t.Errorf("tras upsert = %+v", b)
	}
	if err := db.ClearBroadcast(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	dd, _ = db.DestinationByID(ctx, d.ID)
	if dd.KeyFromAPI {
		t.Error("tras Clear, KeyFromAPI debería ser false")
	}
	// Cae con el destino y con la cuenta.
	db.SetBroadcast(ctx, store.Broadcast{DestinationID: d.ID, AccountID: a.ID, Platform: store.PlatformTwitch, KeyFromAPI: true})
	db.DeleteAccount(ctx, a.ID)
	if _, err := db.BroadcastFor(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("tras borrar la cuenta: %v", err)
	}
}
```

`quota_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestQuotaAccumulatesPerAccountAndDayAndPrunes(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a := cuentaDePrueba(t, db, c, "1")
	b := cuentaDePrueba(t, db, c, "2")
	for _, u := range []int{50, 1, 5} {
		if err := db.AddQuota(ctx, a.ID, "2026-09-11", u); err != nil {
			t.Fatal(err)
		}
	}
	db.AddQuota(ctx, a.ID, "2026-09-10", 100)
	db.AddQuota(ctx, b.ID, "2026-09-11", 7)
	if n, _ := db.QuotaUsed(ctx, a.ID, "2026-09-11"); n != 56 {
		t.Errorf("hoy = %d, quería 56", n)
	}
	if n, _ := db.QuotaUsed(ctx, a.ID, "2026-09-12"); n != 0 {
		t.Errorf("día sin filas = %d", n)
	}
	if err := db.AddQuota(ctx, a.ID, "2026-09-11", 0); err == nil {
		t.Error("0 unidades debería ser inválido")
	}
	if n, err := db.PruneQuota(ctx, "2026-09-11"); err != nil || n != 1 {
		t.Errorf("PruneQuota = %d, %v; quería 1 (el día 10)", n, err)
	}
	if n, _ := db.QuotaUsed(ctx, a.ID, "2026-09-11"); n != 56 {
		t.Errorf("la poda tocó el día vigente: %d", n)
	}
}
```

Run: `go test ./internal/store/ -run 'OwnApp|Broadcast|Quota|OpenCreatesSchema' -count=1` → FAIL.

- [ ] **Step 3: Implementar cuentas**

`accounts.go`: tipos y cambios:

```go
// Credentials son las de una app propia del usuario (YouTube, Kick). Van cifradas como
// los tokens y solo las leen el manager de tokens y los proveedores, por AccountCredentials.
type Credentials struct {
	ClientID     crypto.Secret
	ClientSecret crypto.Secret
}
```

`NewAccount` gana `OwnApp bool` y `Credentials Credentials`. En `UpsertAccount`, tras validar tokens: `if in.OwnApp && (in.Credentials.ClientID.Reveal() == "" || in.Credentials.ClientSecret.Reveal() == "") { return nil, invalidInput("una app propia necesita client_id y client_secret") }`; cifrar `cid, csec []byte` con `c.Encrypt` cuando `OwnApp` (nil si no); el `INSERT` añade `own_app, client_id_encrypted, client_secret_encrypted` con `boolToInt(in.OwnApp), cid, csec` y el `DO UPDATE SET` añade `own_app = excluded.own_app, client_id_encrypted = excluded.client_id_encrypted, client_secret_encrypted = excluded.client_secret_encrypted`.

```go
// AccountCredentials descifra las credenciales de la app propia; vacías si la cuenta usa
// la app incluida. No audita, como AccountTokens.
func (d *DB) AccountCredentials(ctx context.Context, c *crypto.Cipher, id int64) (Credentials, error) {
	var cid, csec []byte
	err := d.ex.QueryRowContext(ctx, `SELECT client_id_encrypted, client_secret_encrypted FROM platform_accounts WHERE id = ?`, id).Scan(&cid, &csec)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, notFound("cuenta no encontrada")
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("leer las credenciales: %w", err)
	}
	var out Credentials
	if len(cid) > 0 {
		p, err := c.Decrypt(cid)
		if err != nil {
			return Credentials{}, fmt.Errorf("descifrar el client_id: %w", err)
		}
		out.ClientID = crypto.Secret(p)
	}
	if len(csec) > 0 {
		p, err := c.Decrypt(csec)
		if err != nil {
			return Credentials{}, fmt.Errorf("descifrar el client_secret: %w", err)
		}
		out.ClientSecret = crypto.Secret(p)
	}
	return out, nil
}
```

- [ ] **Step 4: Implementar `broadcasts.go` y `quota.go`**

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	BroadcastCreated  = "created"
	BroadcastLive     = "live"
	BroadcastComplete = "complete"
)

// Broadcast es lo que la plataforma dio para un destino: la emisión de YouTube (ids de
// emisión, stream y chat) o solo la clave (Kick). KeyFromAPI hace que «probar destino»
// se salte: si la clave la trajo la API, no hay clave inválida que probar.
type Broadcast struct {
	DestinationID int64
	AccountID     int64
	Platform      Platform
	BroadcastRef  string
	StreamRef     string
	LiveChatID    string
	KeyFromAPI    bool
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SetBroadcast crea o sustituye la emisión del destino; siempre vuelve a `created`.
func (d *DB) SetBroadcast(ctx context.Context, b Broadcast) error {
	if b.DestinationID == 0 || b.AccountID == 0 || b.Platform == "" {
		return invalidInput("la emisión necesita destino, cuenta y plataforma")
	}
	ahora := nowRFC3339()
	_, err := d.ex.ExecContext(ctx,
		`INSERT INTO destination_broadcasts (destination_id, account_id, platform, broadcast_ref, stream_ref, live_chat_id, key_from_api, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'created', ?, ?)
		 ON CONFLICT (destination_id) DO UPDATE SET account_id = excluded.account_id, platform = excluded.platform,
		    broadcast_ref = excluded.broadcast_ref, stream_ref = excluded.stream_ref, live_chat_id = excluded.live_chat_id,
		    key_from_api = excluded.key_from_api, status = 'created', updated_at = excluded.updated_at`,
		b.DestinationID, b.AccountID, string(b.Platform), b.BroadcastRef, b.StreamRef, b.LiveChatID, boolToInt(b.KeyFromAPI), ahora, ahora)
	if err != nil {
		return fmt.Errorf("guardar la emisión: %w", err)
	}
	return nil
}

func (d *DB) BroadcastFor(ctx context.Context, destID int64) (*Broadcast, error) {
	var (
		b                    Broadcast
		platform             string
		key                  int
		createdAt, updatedAt string
	)
	err := d.ex.QueryRowContext(ctx,
		`SELECT destination_id, account_id, platform, broadcast_ref, stream_ref, live_chat_id, key_from_api, status, created_at, updated_at
		   FROM destination_broadcasts WHERE destination_id = ?`, destID).
		Scan(&b.DestinationID, &b.AccountID, &platform, &b.BroadcastRef, &b.StreamRef, &b.LiveChatID, &key, &b.Status, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("el destino no tiene emisión")
	}
	if err != nil {
		return nil, fmt.Errorf("leer la emisión: %w", err)
	}
	b.Platform, b.KeyFromAPI = Platform(platform), key == 1
	if b.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return nil, fmt.Errorf("created_at inválido: %w", err)
	}
	if b.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at inválido: %w", err)
	}
	return &b, nil
}

func (d *DB) SetBroadcastStatus(ctx context.Context, destID int64, status string) error {
	switch status {
	case BroadcastCreated, BroadcastLive, BroadcastComplete:
	default:
		return invalidInput("estado de emisión inválido: " + status)
	}
	res, err := d.ex.ExecContext(ctx, `UPDATE destination_broadcasts SET status = ?, updated_at = ? WHERE destination_id = ?`, status, nowRFC3339(), destID)
	if err != nil {
		return fmt.Errorf("cambiar el estado de la emisión: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("el destino no tiene emisión")
	}
	return nil
}

func (d *DB) ClearBroadcast(ctx context.Context, destID int64) error {
	if _, err := d.ex.ExecContext(ctx, `DELETE FROM destination_broadcasts WHERE destination_id = ?`, destID); err != nil {
		return fmt.Errorf("quitar la emisión: %w", err)
	}
	return nil
}
```

`quota.go`:

```go
package store

import (
	"context"
	"fmt"
)

// AddQuota suma unidades de cuota de una cuenta en un día (formato YYYY-MM-DD, en la zona
// que decida quien llama: para YouTube, la del Pacífico, que es donde Google reinicia).
func (d *DB) AddQuota(ctx context.Context, accountID int64, day string, units int) error {
	if units <= 0 || len(day) != 10 {
		return invalidInput("cuota: unidades y día inválidos")
	}
	_, err := d.ex.ExecContext(ctx,
		`INSERT INTO quota_usage (account_id, day, units) VALUES (?, ?, ?)
		 ON CONFLICT (account_id, day) DO UPDATE SET units = units + excluded.units`, accountID, day, units)
	if err != nil {
		return fmt.Errorf("sumar cuota: %w", err)
	}
	return nil
}

func (d *DB) QuotaUsed(ctx context.Context, accountID int64, day string) (int, error) {
	var n int
	err := d.ex.QueryRowContext(ctx, `SELECT COALESCE(SUM(units), 0) FROM quota_usage WHERE account_id = ? AND day = ?`, accountID, day).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("leer la cuota: %w", err)
	}
	return n, nil
}
```

`retention.go`:

```go
// PruneQuota borra los días anteriores a olderThanDay (YYYY-MM-DD, exclusivo).
func (d *DB) PruneQuota(ctx context.Context, olderThanDay string) (int64, error) {
	res, err := d.ex.ExecContext(ctx, `DELETE FROM quota_usage WHERE day < ?`, olderThanDay)
	if err != nil {
		return 0, fmt.Errorf("podar la cuota: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
```

`destinations.go`: `Destination` gana `KeyFromAPI bool`; las dos consultas de `scanDestination` añaden `LEFT JOIN destination_broadcasts db ON db.destination_id = d.id` y la columna `COALESCE(db.key_from_api, 0)`; `scanDestination` lee un `int` más.

- [ ] **Step 5: Correr y commit**

Run: `go vet ./internal/store/ && go test ./internal/store/ -race -count=1` → PASS.

```bash
git add internal/store
git commit -m "feat(store): credenciales de app propia, emisión por destino con clave por API y cuota diaria"
```

---

### Task 2: `platforms` con credenciales, redirect y emisiones; gestor de tokens; `quota.Counter`

**Files:**
- Modify: `internal/platforms/platform.go`, `internal/platforms/registry_test.go`, `internal/platforms/tokens/manager.go`, `internal/platforms/tokens/manager_test.go`, `internal/platforms/twitch/auth.go`, `internal/platforms/twitch/twitch_test.go`, `internal/chat/aggregator_test.go`, `internal/httpapi/platforms_test.go` (los `fakeProvider` cambian de firma), `internal/httpapi/platforms.go` (`BeginAuth(ctx, platforms.Credentials{})`, `PollAuth(ctx, creds, prompt)`)
- Create: `internal/platforms/quota/counter.go`, `internal/platforms/quota/counter_test.go`

**Interfaces:**
- Consumes: `store.Credentials`, `store.AccountCredentials`, `store.AddQuota`, `store.QuotaUsed`.
- Produces: `platforms.Credentials = store.Credentials` (alias), `Provider.BeginAuth(ctx, creds)`, `PollAuth(ctx, creds, prompt)`, `Refresh(ctx, acct, creds, refresh)`; `AuthPrompt.RedirectURL, CodeVerifier string`; `RedirectAuth{BeginRedirect(ctx, creds, redirectURI, state string) (AuthPrompt, error); CompleteRedirect(ctx, creds, prompt, code string) (store.NewAccount, error)}`; `BroadcastRequest{Title, Privacy string; ScheduledAt time.Time}`, `Broadcast{Ref, StreamRef, LiveChatID, IngestURL string; Key crypto.Secret; WatchURL string}`, `BroadcastScheduler{CreateBroadcast(ctx, acct, token, BroadcastRequest) (Broadcast, error); StartBroadcast(ctx, acct, token, ref) error; EndBroadcast(ctx, acct, token, ref) error}`, `IngestKeyProvider{IngestKey(ctx, acct, token) (url string, key crypto.Secret, err error)}`, `ChatWebhook{SubscribeChat(ctx, acct, token, webhookURL) error; UnsubscribeChat(ctx, acct, token) error; ParseWebhook(hdr http.Header, body []byte) ([]ChatMessage, error)}`, `ErrNoBroadcast`, `ErrWebhookRejected`; `QuotaSink func(units int)`; `quota.Counter{Add(ctx, accountID, units); UsedToday(ctx, accountID) (int, error); Day() string; Sink(accountID) QuotaSink}` con `Now` inyectable y zona `America/Los_Angeles`.

- [ ] **Step 1: `platform.go`**

Añadir/cambiar:

```go
// Credentials son las de la app propia del usuario; vacías con la app incluida (Twitch).
type Credentials = store.Credentials

type Provider interface {
	ID() ID
	Capabilities() Capabilities
	Configured() bool
	BeginAuth(ctx context.Context, creds Credentials) (AuthPrompt, error)
	PollAuth(ctx context.Context, creds Credentials, prompt AuthPrompt) (store.NewAccount, error)
	Refresh(ctx context.Context, acct store.Account, creds Credentials, refresh crypto.Secret) (store.Tokens, error)
	Validate(ctx context.Context, access crypto.Secret) (Identity, error)
}

// AuthPrompt gana lo del redirect: RedirectURL es lo que el panel abre; CodeVerifier es
// secreto del flujo y no sale de la API.
type AuthPrompt struct {
	State, VerificationURI, UserCode, DeviceCode string
	RedirectURL, CodeVerifier                   string
	ExpiresAt                                   time.Time
	Interval                                    time.Duration
}

// RedirectAuth es el flujo con redirect (Kick): el navegador va a la plataforma y vuelve
// al panel por /api/platforms/{p}/callback con un código. El state lo firma httpapi.
type RedirectAuth interface {
	BeginRedirect(ctx context.Context, creds Credentials, redirectURI, state string) (AuthPrompt, error)
	CompleteRedirect(ctx context.Context, creds Credentials, prompt AuthPrompt, code string) (store.NewAccount, error)
}

var (
	ErrNoBroadcast     = errors.New("el destino no tiene emisión")
	ErrWebhookRejected = errors.New("webhook rechazado")
)

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

type BroadcastScheduler interface {
	CreateBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, req BroadcastRequest) (Broadcast, error)
	StartBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, ref string) error
	EndBroadcast(ctx context.Context, acct store.Account, token crypto.Secret, ref string) error
}

// IngestKeyProvider trae la URL y la clave de ingesta del canal (Kick).
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

// QuotaSink recibe las unidades de cuota que gasta cada llamada (YouTube).
type QuotaSink func(units int)
```

Quitar las declaraciones antiguas de `BroadcastScheduler`/`IngestKeyProvider`. Importar `net/http`.

- [ ] **Step 2: Adaptar Twitch, manager, tests y `httpapi`**

`twitch/auth.go`: `BeginAuth(ctx, _ platforms.Credentials)`, `PollAuth(ctx, _ platforms.Credentials, prompt)`, `Refresh(ctx, _ store.Account, _ platforms.Credentials, refresh)`; cuerpos sin cambios. Tests de twitch: pasar `platforms.Credentials{}` y `cuenta()`.

`tokens/manager.go`, `doRefresh`: antes de `p.Refresh`:

```go
	var creds platforms.Credentials
	if acct.OwnApp {
		// Con app propia, refrescar exige el client_id y el client_secret del usuario.
		var err error
		if creds, err = m.db.AccountCredentials(ctx, m.cipher, acct.ID); err != nil {
			return "", err
		}
	}
	nuevo, err := p.Refresh(ctx, acct, creds, t.Refresh)
```

Test nuevo en `manager_test.go`: cuenta con `OwnApp` y credenciales; el `fakeProvider.refreshFn` pasa a recibir `(acct, creds, refresh)` y el test comprueba que `creds.ClientSecret.Reveal() == "s"`; para la cuenta de Twitch (sin app) recibe vacías. Adaptar `fakeProvider` en `manager_test.go`, `chat/aggregator_test.go` (`lectorFalso`) y `httpapi/platforms_test.go` (`fakeProvider`) a las firmas nuevas. En `httpapi/platforms.go`, de momento `p.BeginAuth(r.Context(), platforms.Credentials{})` y `p.PollAuth(ctx, platforms.Credentials{}, prompt)` (la Task 9 cambia esto).

- [ ] **Step 3: `quota.Counter`**

`internal/platforms/quota/counter.go`:

```go
// Package quota cuenta las unidades de cuota de YouTube por cuenta y día. El día es el
// del Pacífico, que es donde Google reinicia la cuota. El contador es lo que el panel
// enseña y lo que decide cuándo pausar el chat: cuota visible, nunca silenciosa.
package quota

import (
	"context"
	"log/slog"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

type Counter struct {
	db     *store.DB
	Now    func() time.Time
	Logger *slog.Logger
	zona   *time.Location
}

func NewCounter(db *store.DB) *Counter {
	zona, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		// Sin tzdata (imagen scratch sin zoneinfo): UTC es peor que nada pero no rompe.
		zona = time.UTC
	}
	return &Counter{db: db, Now: time.Now, Logger: slog.Default(), zona: zona}
}

// Day es el día de cuota actual, YYYY-MM-DD en hora del Pacífico.
func (c *Counter) Day() string { return c.Now().In(c.zona).Format("2006-01-02") }

// Add suma unidades. Un fallo se loguea y no se propaga: contar la cuota nunca puede
// impedir la llamada que la gasta.
func (c *Counter) Add(ctx context.Context, accountID int64, units int) {
	if units <= 0 {
		return
	}
	if err := c.db.AddQuota(context.WithoutCancel(ctx), accountID, c.Day(), units); err != nil {
		c.Logger.Debug("no se pudo sumar la cuota", "cuenta", accountID, "err", err)
	}
}

func (c *Counter) UsedToday(ctx context.Context, accountID int64) (int, error) {
	return c.db.QuotaUsed(ctx, accountID, c.Day())
}

// Sink adapta Add a lo que el proveedor de YouTube recibe por cuenta.
func (c *Counter) Sink(accountID int64) platforms.QuotaSink {
	return func(units int) { c.Add(context.Background(), accountID, units) }
}
```

`counter_test.go`: con `Now` fijo a `2026-09-11T06:30:00Z` (= 2026-09-10 23:30 PT) `Day()` es `2026-09-10`; `Sink(id)(50)` + `Add(ctx, id, 1)` → `UsedToday` 51; `Add` con 0 no toca; base cerrada → `Add` no entra en pánico. (La imagen Docker copia `zoneinfo`: `time.LoadLocation` funciona en `scratch`.)

- [ ] **Step 4: Correr y commit**

Run: `go vet ./... && go test ./internal/platforms/... ./internal/chat/ ./internal/httpapi/ -race -count=1` (httpapi ≈2–3 min).

```bash
git add internal/platforms internal/chat internal/httpapi
git commit -m "feat(platforms): credenciales de app propia, flujo con redirect, emisiones y contador de cuota"
```

---
### Task 3: YouTube — cliente, flujo de dispositivo, identidad, refresco y validación

**Files:**
- Create: `internal/platforms/youtube/client.go`, `internal/platforms/youtube/auth.go`, `internal/platforms/youtube/youtube_test.go`, `internal/platforms/youtube/testdata/{device.json,pending.json,slow_down.json,denied.json,token.json,refresh.json,channels_mine.json,tokeninfo.json,error_quota.json}`

**Interfaces:**
- Consumes: `platforms.*` (Task 2), `store.NewAccount`, `store.Tokens`, `store.Credentials`.
- Produces: `youtube.New(Options) *Provider` con `Options{HTTPClient; OAuthBase (https://oauth2.googleapis.com); APIBase (https://www.googleapis.com/youtube/v3); Now; Logger; Quota func(accountID int64) platforms.QuotaSink}`; `Provider` implementa `platforms.Provider` con `Capabilities{Title, Schedule, IngestKey, ChatRead, RequiresOwnApp: true}` y `Configured() = true` (las credenciales van por cuenta); `youtube.Scope = "https://www.googleapis.com/auth/youtube"`; `costos` (mapa fijo de unidades por método).

- [ ] **Step 1: Fixtures**

`device.json`: `{"device_code":"4/4-GMMhmHCXhWEzkobqIHGG_EnNYYsAkukHspeYUk9E8","user_code":"GQVQ-JKEC","verification_url":"https://www.google.com/device","expires_in":1800,"interval":5}`. `pending.json`: `{"error":"authorization_pending","error_description":"Precondition Required"}`. `slow_down.json`: `{"error":"slow_down","error_description":"Forbidden"}`. `denied.json`: `{"error":"access_denied","error_description":"Forbidden"}`. `token.json`: `{"access_token":"ya29.acceso-fixture","expires_in":3599,"refresh_token":"1//refresco-fixture","scope":"https://www.googleapis.com/auth/youtube","token_type":"Bearer"}`. `refresh.json`: igual sin `refresh_token` (Google no lo rota). `channels_mine.json`: `{"kind":"youtube#channelListResponse","items":[{"kind":"youtube#channel","id":"UCabc123","snippet":{"title":"Mi canal","customUrl":"@micanal"}}]}`. `tokeninfo.json`: `{"azp":"cid","aud":"cid","scope":"https://www.googleapis.com/auth/youtube","exp":"1757600000","expires_in":"3400","access_type":"offline"}`. `error_quota.json`: `{"error":{"code":403,"message":"The request cannot be completed because you have exceeded your quota.","errors":[{"message":"The request cannot be completed because you have exceeded your quota.","domain":"youtube.quota","reason":"quotaExceeded"}]}}`.

- [ ] **Step 2: Tests (`youtube_test.go`)**

Calcar `twitch_test.go`: un `servidorGoogle` con `httptest` y rutas `POST /device/code`, `POST /token`, `GET /tokeninfo` (en `OAuthBase`) y `GET /channels` (en `APIBase`), con `tokenRes func(r) (int, []byte)` inyectable y contadores. `creds := platforms.Credentials{ClientID: "cid", ClientSecret: "csec"}`. Tests:

- `TestDeviceFlowRequiresOwnCredentialsAndUsesTheYouTubeScope`: `BeginAuth(ctx, Credentials{})` → `ErrNoClientID`; con `creds`, el servidor recibe `client_id=cid` y `scope=https://www.googleapis.com/auth/youtube` **y nada de force-ssl**; el prompt trae `UserCode "GQVQ-JKEC"`, `VerificationURI "https://www.google.com/device"`, `Interval 5s`, `State` no vacío, `DeviceCode` guardado.
- `TestDeviceFlowPendingSlowDownDeniedAndDone`: sondeos que devuelven pending → `ErrAuthPending`; `slow_down` → `ErrAuthPending` (y el prompt no cambia); `access_denied` → error que **no** es `ErrAuthPending` y contiene «rechaz»; `token.json` → `NewAccount{Platform youtube, ExternalID "UCabc123", DisplayName "Mi canal", Scopes [youtube], OwnApp true, Credentials creds, Tokens{Access ya29…, Refresh 1//…, ExpiresAt ≈ +3599s}}`; el sondeo manda `client_secret=csec` y `grant_type=urn:ietf:params:oauth:grant-type:device_code`; `channels.list` se llamó con `mine=true` y `Authorization: Bearer ya29.acceso-fixture`, y sumó **1** unidad al `QuotaSink` inyectado (`Options.Quota` devuelve un sink que acumula en un mapa por cuenta; en `PollAuth` la cuenta aún no tiene id: se suma a la cuenta 0 y el test lo acepta, o el sink se llama con `accountID` 0 → documentar).
- `TestPollExpires`: `Now` inyectado más allá de `ExpiresAt` → `ErrAuthExpired`; `{"error":"invalid_grant"}` → `ErrAuthExpired`.
- `TestRefreshKeepsTheOldRefreshTokenWhenGoogleOmitsIt`: `Refresh(ctx, acct, creds, "1//viejo")` manda `client_id`, `client_secret`, `refresh_token`, `grant_type=refresh_token`; con `refresh.json` (sin `refresh_token`) el `Tokens.Refresh` devuelto es `"1//viejo"`; con `{"error":"invalid_grant"}` (400) → `ErrUnauthorized`; sin credenciales → `ErrNoClientID`.
- `TestValidateMapsTokeninfo`: 200 → `Identity{ExpiresIn 3400s}` (sin nombre: `tokeninfo` no lo da; `ExternalID` vacío es aceptable y el manager no lo usa); 400 → `ErrUnauthorized`.
- `TestErrorsCarryTheReasonNeverTheToken`: `error_quota.json` con 403 → error que contiene `quotaExceeded` y no `ya29`.
- `TestCapabilities`: `{Title, Schedule, IngestKey, ChatRead, RequiresOwnApp}` true, `Category` false, `Configured()` true.

- [ ] **Step 3: Implementar `client.go`**

Como `twitch/client.go`, con: `Scope`, `costos = map[string]int{"list": 1, "insert": 50, "update": 50, "bind": 50, "transition": 50, "chat_list": 5}`; `Options{HTTPClient, OAuthBase, APIBase, Now, Logger, Quota func(int64) platforms.QuotaSink}`; `do(req, esperado)` que parsea el sobre de error de Google (`{"error":{"code","message","errors":[{"reason"}]}}` **y también** `{"error":"slow_down"}` plano del endpoint de tokens) a `*apiError{code, reason, msg}`; 401 → `ErrUnauthorized`; 403 con `reason` `quotaExceeded` → `fmt.Errorf("%w: cuota de YouTube agotada", platforms.ErrRateLimited)`; 403 con `liveStreamingNotEnabled` → error legible «este canal no tiene habilitadas las emisiones en directo (youtube.com/features)»; `api(ctx, method, path, token, body)` con `Authorization: Bearer`; `gastar(acct store.Account, metodo string)` que llama al sink si `Quota != nil`. `Capabilities()` y `Configured()` fijos.

- [ ] **Step 4: Implementar `auth.go`**

Como `twitch/auth.go` con las URL y parámetros de §4.1 del spec: `BeginAuth` exige credenciales (`ErrNoClientID` si vacías), `POST {OAuthBase}/device/code` (`client_id`, `scope`), `State` aleatorio de 16 bytes; `PollAuth` comprueba `ExpiresAt`, `POST {OAuthBase}/token` (`client_id`, `client_secret`, `device_code`, `grant_type`), errores planos: `authorization_pending`/`slow_down` → `ErrAuthPending`, `access_denied` → `errors.New("la persona rechazó la autorización")`, `expired_token`/`invalid_grant` → `ErrAuthExpired`; con token, `GET {APIBase}/channels?part=snippet&mine=true` (suma `list`), `items[0].id`/`snippet.title`; devuelve `NewAccount{…, OwnApp: true, Credentials: creds}`. `Refresh` como en el spec (conserva el refresh anterior si Google no manda uno). `Validate` con `GET {OAuthBase}/tokeninfo?access_token=` — el token va en la query **solo en este endpoint** porque Google así lo define; `sinQuery` cubre el `*url.Error`.

- [ ] **Step 5: Correr y commit**

Run: `go vet ./internal/platforms/youtube/ && go test ./internal/platforms/youtube/ -race -count=1`; `go list -deps ./internal/platforms/youtube | grep -E "go-rtmp|internal/relay|internal/rtmpio"` vacío.

```bash
git add internal/platforms/youtube
git commit -m "feat(youtube): flujo de dispositivo con credenciales propias, identidad, refresco y validación"
```

---

### Task 4: YouTube — emisiones, clave por API, salir al aire, terminar y título

**Files:**
- Create: `internal/platforms/youtube/broadcasts.go`, `internal/platforms/youtube/broadcasts_test.go`, `testdata/{broadcast_insert.json,stream_insert.json,bind.json,transition_inactive.json,broadcast_update.json}`

**Interfaces:**
- Consumes: Task 3.
- Produces: `Provider` implementa `platforms.BroadcastScheduler`, `platforms.IngestKeyProvider` (devuelve `ErrNoBroadcast`: YouTube no tiene clave sin emisión; el handler usa `CreateBroadcast`), `platforms.TitleSetter` (`SetTitle` exige `ref` por `acct`… ver nota).

**Nota de diseño**: `TitleSetter.SetTitle(ctx, acct, token, title)` no recibe la emisión. Para YouTube, `SetTitle` busca la emisión activa o próxima del canal (`liveBroadcasts.list?mine=true&broadcastStatus=upcoming`, luego `active`; 1 unidad cada una) y actualiza la primera; sin ninguna, `ErrNoBroadcast`. La Task 9 hace que `aplicarEnDestino` prefiera `SetBroadcastTitle(ctx, acct, token, ref, title)` cuando el destino tiene `BroadcastRef` (interfaz opcional `BroadcastTitleSetter`), evitando los `list`.

- [ ] **Step 1: Fixtures**

`broadcast_insert.json`: `{"kind":"youtube#liveBroadcast","id":"bcast123","snippet":{"title":"Prueba","liveChatId":"chat456","scheduledStartTime":"2026-09-11T20:00:00Z"},"status":{"lifeCycleStatus":"ready","privacyStatus":"unlisted"},"contentDetails":{"enableAutoStart":true,"enableAutoStop":true}}`. `stream_insert.json`: `{"kind":"youtube#liveStream","id":"stream789","cdn":{"ingestionType":"rtmp","resolution":"variable","frameRate":"variable","ingestionInfo":{"streamName":"abcd-efgh-ijkl-mnop","ingestionAddress":"rtmp://a.rtmp.youtube.com/live2","backupIngestionAddress":"rtmp://b.rtmp.youtube.com/live2?backup=1"}},"status":{"streamStatus":"ready"}}`. `bind.json`: el de `broadcast_insert` con `contentDetails.boundStreamId: "stream789"`. `transition_inactive.json`: `{"error":{"code":403,"message":"Stream is inactive","errors":[{"reason":"errorStreamInactive","domain":"youtube.liveBroadcast"}]}}`. `broadcast_update.json`: el de insert con `snippet.title: "Nuevo"`.

- [ ] **Step 2: Tests (`broadcasts_test.go`)**

Servidor con rutas `POST /liveBroadcasts` (guarda el cuerpo; exige `part=snippet,status,contentDetails`), `POST /liveStreams`, `POST /liveBroadcasts/bind` (exige `id=bcast123&streamId=stream789&part=id,contentDetails`), `POST /liveBroadcasts/transition` (contador: las dos primeras veces `transition_inactive.json` 403, luego 200 con `status.lifeCycleStatus: live`), `PUT /liveBroadcasts` (update), `GET /liveBroadcasts` (list). Sink de cuota que acumula por cuenta.

- `TestCreateBroadcastInsertsBindsAndReturnsTheIngestKey`: `CreateBroadcast(ctx, acct, tok, BroadcastRequest{Title: "Prueba", Privacy: "unlisted"})` → `Broadcast{Ref "bcast123", StreamRef "stream789", LiveChatID "chat456", IngestURL "rtmp://a.rtmp.youtube.com/live2", Key "abcd-efgh-ijkl-mnop", WatchURL "https://www.youtube.com/watch?v=bcast123"}`; el cuerpo del insert lleva `snippet.title`, `status.privacyStatus unlisted`, `status.selfDeclaredMadeForKids false`, `contentDetails.enableAutoStart/enableAutoStop true`, `latencyPreference normal`, y `scheduledStartTime` ≈ ahora (con `Now` inyectado) cuando no se da; el stream `cdn.resolution/frameRate variable`, `ingestionType rtmp`; cuota sumada **150** (insert 50 + insert 50 + bind 50). Privacidad inválida → error antes de llamar; título vacío → error.
- `TestStartRetriesWhileTheStreamIsInactive`: con `Options.Now` y un `retryEvery` inyectable (campo `Options.TransitionRetry`, por defecto 5 s, en el test 1 ms), `StartBroadcast` reintenta ante `errorStreamInactive` hasta que la tercera llamada responde 200; cuota 150 (3 transiciones); con un servidor que siempre da `errorStreamInactive` y `TransitionDeadline` de 5 ms → error que contiene «no llega la señal».
- `TestEndBroadcastCompletes`: `transition?broadcastStatus=complete`; `redundantTransition` (403 con ese `reason`) se trata como éxito.
- `TestSetTitleUpdatesTheUpcomingOrActiveBroadcast`: `GET /liveBroadcasts?mine=true&broadcastStatus=upcoming` devuelve uno → `PUT` con `id` y `snippet.title`; sin ninguno en `upcoming` ni `active` → `ErrNoBroadcast`. `SetBroadcastTitle(ctx, acct, tok, "bcast123", "Nuevo")` va directo al `PUT` (cuota 50).
- `TestIngestKeyWithoutBroadcastIsErrNoBroadcast`.

- [ ] **Step 3: Implementar `broadcasts.go`**

Según el spec §5: `CreateBroadcast` (tres llamadas, `WatchURL` `https://www.youtube.com/watch?v=` + id), `StartBroadcast` (bucle con `TransitionRetry`/`TransitionDeadline`, por defecto 5 s / 60 s, `errorStreamInactive` → esperar, otro error → devolver; al agotar: `errors.New("YouTube no recibe la señal todavía: arranca OBS y vuelve a intentarlo")`), `EndBroadcast` (`complete`; `redundantTransition` → nil), `SetTitle` (list upcoming → active → `ErrNoBroadcast`), `SetBroadcastTitle`, `IngestKey` → `ErrNoBroadcast`. Interfaz opcional en `platforms`: `BroadcastTitleSetter{SetBroadcastTitle(ctx, acct, token, ref, title) error}` (añadir en `platform.go`; una línea). Cada llamada `gastar`.

- [ ] **Step 4: Correr y commit**

Run: `go vet ./internal/platforms/... && go test ./internal/platforms/youtube/ -race -count=1`.

```bash
git add internal/platforms
git commit -m "feat(youtube): crear emisión con clave por API, salir al aire, terminar y título"
```

---

### Task 5: YouTube — chat por sondeo con presupuesto de cuota

**Files:**
- Create: `internal/platforms/youtube/chat.go`, `internal/platforms/youtube/chat_test.go`, `testdata/{broadcasts_active.json,chat_page1.json,chat_page2.json}`

**Interfaces:**
- Consumes: Task 3 (`gastar`), `platforms.ChatReader`, `platforms.TokenSource`.
- Produces: `Provider` implementa `platforms.ChatReader`; `Options.ChatBudget func(accountID int64) (used, budget int, ok bool)` (nil: sin presupuesto), `Options.ChatNoBroadcastWait` (30 s; en tests ms) y `Options.ChatNoBroadcastGiveUp` (10 min); `Options.OnChatPaused func(acct store.Account, used, budget int)`.

- [ ] **Step 1: Fixtures**

`broadcasts_active.json`: `{"items":[{"id":"bcast123","snippet":{"title":"Prueba","liveChatId":"chat456"},"status":{"lifeCycleStatus":"live"}}]}`. `chat_page1.json`: `{"kind":"youtube#liveChatMessageListResponse","nextPageToken":"tok2","pollingIntervalMillis":4000,"items":[{"id":"m1","snippet":{"type":"textMessageEvent","liveChatId":"chat456","authorChannelId":"UCviewer","publishedAt":"2026-09-11T20:01:00Z","displayMessage":"Hola desde YouTube"},"authorDetails":{"channelId":"UCviewer","displayName":"Espectadora","isChatOwner":false,"isChatModerator":true}}]}`. `chat_page2.json`: `nextPageToken "tok3"`, `pollingIntervalMillis 3000`, un mensaje `m2` de `isChatOwner: true`.

- [ ] **Step 2: Tests (`chat_test.go`)**

- `TestReadChatFindsTheActiveBroadcastAndRespectsPollingInterval`: `GET /liveBroadcasts?mine=true&broadcastStatus=active` → activo; `GET /liveChatMessages` con `liveChatId=chat456&part=snippet,authorDetails`: primera sin `pageToken` → page1, segunda con `pageToken=tok2` → page2; los dos mensajes llegan a `out` con `Author`, `AuthorID`, `Text`, badges `moderator` y `owner`, `MessageID`; entre la primera y la segunda llamada pasan ≥ `pollingIntervalMillis` (con `Options.MinPoll` inyectable a 20 ms y el fixture a 4000 ms: el test mide que el segundo sondeo no llega antes de… usar un fixture con `pollingIntervalMillis: 60` y comprobar ≥ 60 ms); cuota: 1 (list) + 5 por sondeo.
- `TestReadChatPausesWhenTheBudgetIsReached`: `ChatBudget` devuelve `used 5996, budget 6000` → tras el primer sondeo (que cabe: 5996+5 > 6000 → NO cabe) → no sondea, llama a `OnChatPaused` una vez y devuelve un error que contiene «presupuesto».
- `TestReadChatWaitsForABroadcastThenGivesUp`: sin emisión activa, con `ChatNoBroadcastWait` 5 ms y `GiveUp` 30 ms → reintenta varias veces (contador ≥ 3) y devuelve error «no hay emisión activa».
- `TestReadChatStopsOnContext`: cancelar durante el sondeo → vuelve sin error.
- `TestReadChatNeverPollsBelowTwoSeconds`: fixture con `pollingIntervalMillis: 500` y `MinPoll` por defecto → el segundo sondeo espera ≥ 2 s (usar `Now`/`sleep` inyectable: `Options.Sleep func(ctx, d) error`, en tests registra la duración pedida en vez de dormir).

- [ ] **Step 3: Implementar `chat.go`**

`ReadChat`: bucle de búsqueda de emisión (`broadcastStatus=active`; `gastar list`), luego sondeo: antes de cada `liveChatMessages.list`, si `ChatBudget != nil` y `used+5 > budget` → `OnChatPaused` y `return fmt.Errorf("el chat de YouTube se pausó al llegar al presupuesto de %d unidades", budget)`; llamada (`gastar chat_list`), convertir `items` (badges: `isChatOwner` → `owner`, `isChatModerator` → `moderator`; `At` de `publishedAt`), enviar a `out` con `select`+`default` (descartar si no leen, como Twitch), esperar `max(pollingIntervalMillis, MinPoll 2 s)` con `Sleep(ctx, d)`; `pageToken` encadenado; 401 → `ErrUnauthorized` (el manager marcará `reauth` al siguiente `Token`); 403 `quotaExceeded` → `OnChatPaused` con `used=budget` y error; otros errores → reintento con backoff 5 s → 60 s. Twitch no cambia.

- [ ] **Step 4: Correr y commit**

Run: `go vet ./internal/platforms/youtube/ && GOMAXPROCS=2 go test ./internal/platforms/youtube/ -race -count=3`.

```bash
git add internal/platforms/youtube
git commit -m "feat(youtube): chat por sondeo con presupuesto de cuota y pausa avisada"
```

---
### Task 6: Kick — cliente, redirect con PKCE, identidad, refresco, título, categoría y clave por API

**Files:**
- Create: `internal/platforms/kick/client.go`, `internal/platforms/kick/auth.go`, `internal/platforms/kick/channel.go`, `internal/platforms/kick/kick_test.go`, `testdata/{token.json,refresh.json,users.json,channels.json,categories.json}`

**Interfaces:**
- Consumes: `platforms.*` (Task 2), `store.NewAccount`.
- Produces: `kick.New(Options) *Provider` con `Options{HTTPClient; AuthBase (https://id.kick.com); APIBase (https://api.kick.com); Now; Logger}`; implementa `platforms.Provider` (`BeginAuth`/`PollAuth` devuelven `errors.New("kick no admite el flujo de dispositivo; usa el redirect")` envuelto en un error exportado `ErrUseRedirect`), `platforms.RedirectAuth`, `TitleSetter`, `CategorySetter`, `IngestKeyProvider`; `Capabilities{Title, Category, ChatRead, IngestKey, RequiresOwnApp, RequiresPublicURL}`; `kick.Scopes = "user:read channel:read channel:write streamkey:read events:subscribe"`; `kick.PKCE()` (verifier/challenge S256).

- [ ] **Step 1: Fixtures**

`token.json`: `{"access_token":"kick-acceso-fixture","token_type":"Bearer","expires_in":7200,"refresh_token":"kick-refresco-fixture","refresh_expires_in":2592000,"scope":"user:read channel:read channel:write streamkey:read events:subscribe"}`. `refresh.json`: igual con `kick-acceso-2`/`kick-refresco-2`. `users.json`: `{"message":"","data":[{"user_id":123,"name":"John Doe","email":"","profile_picture":"https://kick.com/img/default-profile-pictures/default-avatar-2.webp"}]}`. `channels.json`: el del spike (§3 del spike de Kick) con `stream.url "rtmps://stream.kick.com/1234567890"` y `stream.key "super-secret-stream-key"`, `broadcaster_user_id 123`, `stream_title`, `category{id 101, name}`. `categories.json`: `{"message":"","data":[{"id":101,"name":"Old School Runescape","thumbnail":"https://kick.com/img/categories/old-school-runescape.jpeg"}]}`.

- [ ] **Step 2: Tests (`kick_test.go`)**

Servidor con `POST /oauth/token` (exige `client_id`, `client_secret`, `grant_type`, y en `authorization_code`: `code`, `code_verifier`, `redirect_uri`; verifica que `code_verifier` cumple `S256(verifier) == challenge` guardado por el test desde la URL de authorize), `GET /public/v1/users`, `GET /public/v1/channels`, `PATCH /public/v1/channels` (204, guarda el cuerpo), `GET /public/v1/categories?q=`. `creds := Credentials{ClientID: "kid", ClientSecret: "ksec"}`.

- `TestBeginRedirectBuildsThePKCEAuthorizeURL`: `BeginRedirect(ctx, creds, "http://localhost:8080/api/platforms/kick/callback", "estado.firmado")` → `AuthPrompt.RedirectURL` con host `AuthBase`, path `/oauth/authorize`, query `response_type=code`, `client_id=kid`, `redirect_uri` exacta, `scope` = `Scopes`, `code_challenge_method=S256`, `code_challenge` = base64url(SHA256(`CodeVerifier`)) sin padding, `state=estado.firmado`; `CodeVerifier` de 43–128 caracteres; `ExpiresAt` ≈ +10 min. Sin credenciales → `ErrNoClientID`. `BeginAuth`/`PollAuth` → `ErrUseRedirect`.
- `TestCompleteRedirectExchangesTheCodeAndBuildsTheAccount`: `CompleteRedirect(ctx, creds, prompt, "codigo")` → el servidor recibe `client_secret=ksec`, `code_verifier` correcto, `redirect_uri`; `NewAccount{Platform kick, ExternalID "123", DisplayName "John Doe", Scopes (5), OwnApp true, Credentials creds, Tokens{Access, Refresh, ExpiresAt ≈ +7200s}}`; `GET /users` con `Bearer kick-acceso-fixture`. Código rechazado (400) → error legible; sin `client_secret` → el test del servidor lo rechaza y el proveedor devuelve el error.
- `TestRefreshRotates`: `Refresh(ctx, acct, creds, "kick-refresco-fixture")` manda `client_secret`; devuelve el par nuevo; 400/401 → `ErrUnauthorized`.
- `TestValidateUsesUsers`: 200 → `Identity{ExternalID "123", DisplayName "John Doe"}`; 401 → `ErrUnauthorized`.
- `TestSetTitleAndCategoryPatchChannels`: `SetTitle` → `PATCH` con `{"stream_title": "…"}`; `SetCategory(ctx, acct, tok, "101")` → `{"category_id": 101}` (entero); id vacío → error «Kick no permite quitar la categoría»; `SearchCategories(ctx, tok, "rune")` → `[{ID "101", Name, BoxArtURL thumbnail}]`; q vacía → `[]` sin llamar.
- `TestIngestKeyReadsTheChannel`: `IngestKey(ctx, acct, tok)` → `GET /public/v1/channels?broadcaster_user_id=123` → `("rtmps://stream.kick.com/1234567890", "super-secret-stream-key")`; sin `stream.key` en la respuesta → error «Kick no devolvió la clave: ¿falta el permiso streamkey:read? Reconecta la cuenta»; ningún error contiene la clave.
- `TestCapabilities`: `{Title, Category, ChatRead, IngestKey, RequiresOwnApp, RequiresPublicURL}`; `Schedule` false.

- [ ] **Step 3: Implementar**

`client.go` como Twitch (`do`, `api`, `sinQuery`, `httpError`; sobre de error `{"message": …}`); `auth.go`: `PKCE()` (`crypto/rand` 32 bytes → verifier base64url; challenge `sha256` base64url sin padding), `BeginRedirect` (URL de authorize; `ExpiresAt` +10 min), `CompleteRedirect` (`POST /oauth/token` form con `grant_type=authorization_code`, `client_id`, `client_secret`, `code`, `code_verifier`, `redirect_uri` (la del prompt: guardar `RedirectURI` en un campo no exportado del prompt… `AuthPrompt` es un struct público: añadir `RedirectURI string` en Task 2 si no está; añádelo ahora en `platform.go` con una línea), luego `GET /public/v1/users` para la identidad), `Refresh` (`grant_type=refresh_token` con `client_id`, `client_secret`, `refresh_token`), `Validate` (`GET /public/v1/users`), `BeginAuth`/`PollAuth` → `ErrUseRedirect`. `channel.go`: `SetTitle`, `SetCategory` (id numérico), `SearchCategories` (`GET /public/v1/categories?q=`, aislado en una función para cambiar a v2 cuando la v1 desaparezca), `IngestKey`.

- [ ] **Step 4: Correr y commit**

Run: `go vet ./internal/platforms/kick/ && go test ./internal/platforms/kick/ -race -count=1`; guard de frontera vacío.

```bash
git add internal/platforms
git commit -m "feat(kick): OAuth 2.1 con redirect y PKCE, identidad, refresco, título, categoría y clave por API"
```

---

### Task 7: Kick — webhook firmado y suscripción al chat

**Files:**
- Create: `internal/platforms/kick/webhook.go`, `internal/platforms/kick/webhook_test.go`, `testdata/{chat_message.json,public_key_response.json (generada en el test),subscriptions.json}`

**Interfaces:**
- Consumes: Task 6 (`api`, `do`), `platforms.ChatWebhook`, `platforms.ChatMessage`, `platforms.ErrWebhookRejected`.
- Produces: `Provider` implementa `platforms.ChatWebhook`; `Options.PublicKeyTTL` (24 h), `Options.ReplayWindow` (5 min), `Options.SeenTTL` (10 min); `Provider.ParseWebhook(hdr, body)`; `SubscribeChat(ctx, acct, token, webhookURL)` (el `webhookURL` solo se loguea: Kick lo toma del portal), `UnsubscribeChat`.

- [ ] **Step 1: Tests (`webhook_test.go`)**

Generar en el test un par RSA 2048 (`rsa.GenerateKey`), servir la pública en `GET /public/v1/public-key` como `{"data":{"public_key":"-----BEGIN PUBLIC KEY-----…"}}` (PKIX PEM), y una función `firmar(id, ts, body)` que hace `rsa.SignPKCS1v15(sha256(id+"."+ts+"."+body))` en base64. `chat_message.json`: el del spike (§4) con `content "Hello [emote:4148074:HYPERCLAP]"`, `sender.username "sender_name"`, `sender.user_id 987654321`, `sender.identity.username_color "#FF5733"`, badges moderator/sub_gifter/subscriber, `created_at`.

- `TestParseWebhookAcceptsAValidSignature`: cabeceras `Kick-Event-Message-Id`, `Kick-Event-Message-Timestamp` (ahora, RFC 3339), `Kick-Event-Type: chat.message.sent`, `Kick-Event-Version: 1`, `Kick-Event-Signature` válida → un `ChatMessage{Platform kick, AuthorID "987654321", Author "sender_name", Text (content tal cual), Color "#FF5733", Badges ["moderator","sub_gifter","subscriber"], MessageID, At}`; **`AccountID` queda 0**: el handler lo rellena por `broadcaster.user_id` → cuenta (Task 9). Devolver también el `broadcaster_user_id` como `ChatMessage.AuthorID`? No: añadir a `platforms.ChatMessage` el campo `BroadcasterID string` (Task 2 no lo tenía: añadirlo aquí en `platform.go`, una línea, tag no aplica).
- `TestParseWebhookRejectsBadSignatureStaleTimestampAndReplay`: firma alterada → `ErrWebhookRejected` («firma»); marca de tiempo de hace 6 min → «antigüedad»; el mismo `message_id` dos veces → la segunda `ErrWebhookRejected` («repetido»); tipo `channel.followed` → `nil, nil` (ignorado, no rechazado); cuerpo > 256 KiB → rechazado; sin cabeceras → rechazado.
- `TestPublicKeyIsCachedAndRefreshedOnce`: el servidor cuenta las peticiones a `/public-key`; dos webhooks válidos → 1 petición; rotar la clave en el servidor y firmar con la nueva → el proveedor falla con la cacheada, refresca **una vez** y acepta (2 peticiones); firmar con una clave que no es la del servidor → rechazo y 3 peticiones como máximo (no bucle).
- `TestSubscribeAndUnsubscribeChat`: `POST /public/v1/events/subscriptions` con `{"events":[{"name":"chat.message.sent","version":1}],"method":"webhook"}` y `Bearer`; respuesta 200 `{"data":[{"name":"chat.message.sent","version":1,"subscription_id":"01HZ…","error":""}]}`; `UnsubscribeChat` lista (`GET /public/v1/events/subscriptions` → `subscriptions.json`) y borra (`DELETE /public/v1/events/subscriptions?id=01HZ…`).

- [ ] **Step 2: Implementar `webhook.go`**

`ParseWebhook`: límite de cuerpo por el llamante (Task 9) y aquí `if len(body) > 256<<10 → rechazo`; leer cabeceras; `Kick-Event-Type != "chat.message.sent"` o versión ≠ "1" → `nil, nil`; parsear `Kick-Event-Message-Timestamp` RFC 3339 y comparar con `Now()` (`|Δ| > ReplayWindow` → rechazo); `seen` (`map[string]time.Time` con mutex y purga de más de `SeenTTL` en cada llamada) → repetido → rechazo; firma: `base64.StdEncoding.DecodeString`, `rsa.VerifyPKCS1v15(pub, crypto.SHA256, sha256.Sum256([]byte(id+"."+ts+string(body))), sig)`; si falla y la clave cacheada tiene más de 1 min, refrescarla una vez (`GET /public/v1/public-key`, parsear PEM PKIX) y reintentar; JSON → `ChatMessage` (badges: `identity.badges[].type`; `BroadcasterID` de `broadcaster.user_id`); errores con `fmt.Errorf("%w: %s", platforms.ErrWebhookRejected, motivo)` sin el cuerpo. `SubscribeChat`/`UnsubscribeChat` como en los tests. El `Now` inyectable ya existe en `Options`.

- [ ] **Step 3: Correr y commit**

Run: `go vet ./internal/platforms/kick/ && go test ./internal/platforms/kick/ -race -count=1`.

```bash
git add internal/platforms
git commit -m "feat(kick): webhook de chat firmado con clave pública cacheada, anti-repetición y suscripción"
```

---

### Task 8: `chat.Aggregator.Ingest` para mensajes que llegan por webhook

**Files:**
- Modify: `internal/chat/aggregator.go`, `internal/chat/aggregator_test.go`

**Interfaces:**
- Produces: `func (a *Aggregator) Ingest(msgs []platforms.ChatMessage) int` (aceptados; sin sesión viva, 0 y `dropped += len`).

- [ ] **Step 1: Test**

`TestIngestFeedsTheSessionOrDropsWithoutOne`: sin sesión → `Ingest` devuelve 0 y `Stats().dropped == n`; con sesión arrancada (evento `publisher_connected`, cuenta con proveedor que **no** es `ChatReader`: un `webhookFalso` que implementa `Provider` y `ChatWebhook`) → el agregador no lanza goroutine para esa cuenta pero **sí** crea el canal `in` y la escritora; `Ingest` de 3 mensajes → llegan al bus con `SessionID` y se persisten; `Stats().messages[kick] == 3`.

- [ ] **Step 2: Implementar**

`arrancar` deja de retornar cuando `len(cuentas) == 0` **si** alguna cuenta `ok` con destino habilitado tiene proveedor `ChatWebhook` (la escritora hace falta igual): calcular `cuentas` con `ChatRead` (ya lo hace); lanzar `escribir` siempre que haya alguna; solo lanzar `ReadChat` para las `ChatReader`. Guardar `a.in` (canal) bajo `a.mu` mientras la sesión vive:

```go
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
```

`parar()` pone `a.in = nil` bajo el mutex antes de cancelar.

- [ ] **Step 3: Correr y commit**

Run: `go vet ./internal/chat/ && GOMAXPROCS=2 go test ./internal/chat/ -race -count=3`.

```bash
git add internal/chat
git commit -m "feat(chat): Ingest para el chat que llega por webhook"
```

---
### Task 9: API HTTP — credenciales en el flujo, callback y webhook públicos, emisiones, clave por API, capacidades completas

**Files:**
- Modify: `internal/httpapi/platforms.go`, `internal/httpapi/platforms_test.go`, `internal/httpapi/dto.go`, `internal/httpapi/dto_test.go`, `internal/httpapi/server.go` (`Config.Quota`, `Config.WebhookURL func() string`, rutas), `internal/httpapi/test_destination.go`, `internal/httpapi/live.go` (preferir `BroadcastTitleSetter`), `internal/httpapi/metrics.go`
- Create: `internal/httpapi/broadcasts.go`, `internal/httpapi/broadcasts_test.go`, `internal/httpapi/webhook.go`, `internal/httpapi/webhook_test.go`

**Interfaces:**
- Consumes: Tasks 1–8.
- Produces: rutas de spec §8.2; `httpapi.Config.Quota QuotaReader` (`UsedToday(ctx, accountID) (int, error)`; nil → sin cuota), `Config.ChatIngest func([]platforms.ChatMessage) int`, `Config.ChatBudget int`; DTOs: `capabilitiesDTO` con `title, category, chat, schedule, ingest_key, requires_own_app, requires_public_url`; `platformDTO.public_url_ok bool`; `accountDTO.own_app bool`, `quota_used_today *int`; `broadcastDTO{platform, broadcast_ref, status, live_chat_id, key_from_api, watch_url}`; `destinationDTO.key_from_api bool`, `destinationDTO.broadcast *broadcastDTO`; `authStartDTO.redirect_url`; `authStartRequest{client_id, client_secret, origin}`; `fromAccountRequest{account_id, name, title, privacy, scheduled_at}`.

- [ ] **Step 1: DTOs y `Config`**

`capabilitiesDTO` con los siete campos y `capsDTO` los mapea todos. `platformDTO.PublicURLOK bool `json:"public_url_ok"`` = `!caps.RequiresPublicURL || (s.tls && s.publicURL != "")`. `accountDTO` gana `OwnApp bool `json:"own_app"`` y `QuotaUsedToday *int `json:"quota_used_today"`` (solo YouTube, vía `s.quota`). `broadcastDTO` nuevo. `destinationDTO` gana `KeyFromAPI bool `json:"key_from_api"`` (de `d.KeyFromAPI`) y `Broadcast *broadcastDTO `json:"broadcast"`` (lo rellena `decorar` con `BroadcastFor` en un mapa por lista: añadir `store.BroadcastsByDestination(ctx) (map[int64]Broadcast, error)` en la Task 1 si no está — añádelo aquí en `store/broadcasts.go` con su test). `authStartDTO` gana `RedirectURL string `json:"redirect_url"``. `TestDTOFieldNamesAreSnakeCase` recibe `broadcastDTO{}`. `Config`: `Quota QuotaReader`, `ChatIngest func([]platforms.ChatMessage) int`, `ChatBudget int`.

- [ ] **Step 2: Flujo de autorización con credenciales y redirect (`platforms.go`)**

`handleStartAuth` lee cuerpo opcional `authStartRequest{ClientID, ClientSecret, Origin string}` (con `decodeBody` solo si `r.ContentLength > 0`). Si `p.Capabilities().RequiresOwnApp` y faltan `client_id`/`client_secret` → 400 «esta plataforma necesita las credenciales de tu propia app». `creds := platforms.Credentials{ClientID: crypto.Secret(in.ClientID), ClientSecret: crypto.Secret(in.ClientSecret)}`. Dos caminos:

- Si `p` implementa `platforms.RedirectAuth`: `redirectURI := origenPermitido(in.Origin) + "/api/platforms/" + p.ID() + "/callback"`; `state := s.firmarState(flowID)` con `flowID` 16 bytes hex y el `sessionSigner` (`"v1." + flowID + "." + exp + "." + hmac`, 10 min; función `firmarState`/`verificarState` en `platforms.go`); `prompt, err := ra.BeginRedirect(ctx, creds, redirectURI, state)`; se guarda `authFlow{status: pending, platform, creds, prompt, redirect: true}` bajo la clave `flowID` (no `state`); **no** se lanza sondeo; respuesta `{state: flowID, redirect_url: prompt.RedirectURL, expires_in}`. El panel sondea `GET …/auth/{flowID}` como siempre.
- Si no: como hoy, `BeginAuth(ctx, creds)`, `sondear` con `PollAuth(ctx, creds, prompt)`; `creds` vive en el `authFlow` mientras dura.

`origenPermitido(origin)`: si hay TLS integrado (`s.tls && s.publicURL != ""`) → solo `s.publicURL` o un origen `http://localhost[:p]`/`http://127.0.0.1[:p]`; sin TLS integrado → cualquier `http(s)://host[:port]` bien formado sin path. Inválido → 400.

`handleAuthCallback` (**pública**, `GET /api/platforms/{p}/callback`): `state`, `code`, `error`, `error_description` de la query; `flowID, err := s.verificarState(state)` (firma y caducidad; error → 400 texto plano «solicitud inválida», sin detalle); flujo no encontrado o no `redirect` o no `pending` → 400; `error != ""` → `terminar(f, "error", nil, error_description)` y página «No se pudo conectar: …»; si no, `ra.CompleteRedirect(ctx, f.creds, f.prompt, code)` con `context.WithTimeout(s.baseCtx, 30 s)` → `UpsertAccount` (con `OwnApp` y `Credentials` desde `NewAccount`), evento `account_connected`, y **si** `p` implementa `ChatWebhook` y `s.tls && s.publicURL != ""` → `SubscribeChat(ctx, acct, token, s.publicURL+"/api/platforms/kick/webhook")` (token vía `s.tokens.Token`; un fallo se registra como evento warn `chat_disconnected` «no se pudo suscribir el chat de Kick: …» sin romper la conexión); `terminar(f, "done", acct, "")`; respuesta `text/html` mínima (sin scripts): «Cuenta conectada. Ya puedes cerrar esta pestaña y volver al panel.» Nunca se registra `code` ni `state` en logs.

`handleDeleteAccount`: si el proveedor es `ChatWebhook` y la cuenta está `ok`, `UnsubscribeChat` best-effort antes de borrar.

Tests (`platforms_test.go`): `fakeProvider` gana `redirect bool` (implementa `RedirectAuth` cuando true: `BeginRedirect` guarda `redirectURI`/`state` y devuelve `RedirectURL "https://kick.test/authorize?state="+state`; `CompleteRedirect` acepta `code == "ok"`), `requiresOwnApp bool`, `webhook bool` (implementa `ChatWebhook`: `SubscribeChat` cuenta y guarda `webhookURL`). Casos: sin credenciales con `requiresOwnApp` → 400; redirect: `POST` devuelve `redirect_url` y `state` (flowID), `GET …/callback?state=<state firmado>&code=ok` → 200 HTML, luego `GET …/auth/{flowID}` → `done` con cuenta `own_app: true`; callback con `state` alterado → 400 y el flujo sigue `pending`; con `error=access_denied` → flujo `error`; `state` caducado (firmar con `now` −11 min) → 400; sin sesión (sin cookie) el callback **funciona** (es pública) y el resto de rutas de plataformas siguen exigiendo cookie; con `Config.TLS: true, PublicURL: "https://relay.ejemplo.com"` y `webhook: true`, el callback suscribe con `webhookURL == "https://relay.ejemplo.com/api/platforms/kick/webhook"`; sin TLS no suscribe; `origin` no permitido con TLS → 400; ningún cuerpo de respuesta contiene `client_secret`, `code_verifier` ni el token.

- [ ] **Step 3: Webhook público (`webhook.go`)**

`POST /api/platforms/kick/webhook` (**pública**): `p, _ := s.platforms.Get(platforms.Kick)`; `cw, ok := p.(platforms.ChatWebhook)` (sin proveedor → 404); cuerpo con `io.LimitReader(r.Body, 256<<10+1)` (si supera → 413); `msgs, err := cw.ParseWebhook(r.Header, body)`; `ErrWebhookRejected` → 401 y evento `webhook_rejected` (warn, «webhook de Kick rechazado: <motivo>», acotado a uno por minuto con un `time.Time` bajo mutex); `nil, nil` → 200 (tipo ignorado); mensajes → resolver `AccountID` por `BroadcasterID`: `s.cuentaPorExternalID(ctx, kick, id)` (nuevo `store.AccountByExternalID(ctx, platform, externalID)`: añadir en `store/accounts.go` con test) → si no hay cuenta, 200 y se ignoran; si hay, `s.chatIngest(msgs)` y **200 siempre**, antes de cualquier trabajo lento (el `Ingest` es no bloqueante).

Tests (`webhook_test.go`): con `fakeProvider{webhook: true}` cuyo `ParseWebhook` devuelve mensajes con `BroadcasterID "123"` cuando `X-Test-Signature: ok`, error `ErrWebhookRejected` con `bad`, y `nil, nil` con `ignore`: 200 + `ChatIngest` llamado con `AccountID` de la cuenta cuyo `ExternalID` es "123"; 401 con `bad` y evento `webhook_rejected` (solo uno aunque lleguen dos); 200 sin ingest con `ignore`; 413 con cuerpo grande; sin cookie funciona; cuenta inexistente → 200 sin ingest.

- [ ] **Step 4: Emisiones y clave por API (`broadcasts.go`)**

- `POST /api/destinations/from-account` `{account_id, name, title?, privacy?, scheduled_at?}`: cuenta `ok` (si no, 409), proveedor de su plataforma; si implementa `BroadcastScheduler` → `CreateBroadcast(ctx, acct, tok, BroadcastRequest{Title (por defecto `name`), Privacy (por defecto `unlisted`), ScheduledAt})`; si no, `IngestKeyProvider.IngestKey`; sin ninguna de las dos → 409 «esta plataforma no da la clave por API». Con la URL y la clave: `CreateDestination(NewDestination{Name, Platform: acct.Platform, RTMPURL: url, Key: key, Enabled: true})`, `LinkDestination`, `SetBroadcast(Broadcast{…, KeyFromAPI: true})`, eventos `broadcast_created` (si hubo emisión) y `destination_key_from_api`; respuesta 201 con el `destinationDTO` decorado (con `broadcast`). Errores de plataforma → 502 con `mensajePlataforma`.
- `POST /api/destinations/{id}/broadcast`: igual sobre un destino con cuenta vinculada (`AccountForDestination`); actualiza `RTMPURL`/`Key` con `UpdateDestination` y `SetBroadcast`; 200.
- `GET /api/destinations/{id}/broadcast`: `BroadcastFor` → `broadcastDTO` (con `watch_url` para YouTube: `https://www.youtube.com/watch?v=` + ref) o 404.
- `POST /api/destinations/{id}/broadcast/start` y `/end`: solo con `BroadcastRef != ""` y proveedor `BroadcastScheduler` (si no, 409 «esta plataforma sale al aire sola»); `StartBroadcast`/`EndBroadcast` con `context.WithTimeout(r.Context(), 90 s)` (el `Start` reintenta hasta 60 s); éxito → `SetBroadcastStatus(live|complete)` y evento `broadcast_started`/`broadcast_ended`; error → 502 con `mensajePlataforma`.
- `handleTestDestination`: si `d.KeyFromAPI` → 200 `{"skipped": true, "message": "la clave vino por API: no hay clave inválida que probar"}` (DTO `testSkippedDTO`; añadir al test de snake_case) antes de cualquier probe.
- `live.go`, `aplicarEnDestino`: si el destino tiene `BroadcastFor` con `BroadcastRef` y `p` implementa `platforms.BroadcastTitleSetter` → `SetBroadcastTitle`; si no, `SetTitle` como hoy; `ErrNoBroadcast` → mensaje «crea la emisión primero».
- `handleListAccounts`/`accountDTO`: `OwnApp` y, si `s.quota != nil` y la plataforma es `youtube`, `QuotaUsedToday`.
- `metrics.go`: `splitstream_youtube_quota_units{account="<display_name>"}` por cuenta de YouTube (gauge, `UsedToday`).

Tests (`broadcasts_test.go`): `fakeProvider` gana `schedule bool` (implementa `BroadcastScheduler`: `CreateBroadcast` devuelve `Broadcast{Ref "b1", StreamRef "s1", LiveChatID "c1", IngestURL "rtmp://a.rtmp.youtube.com/live2", Key "clave-api", WatchURL "https://www.youtube.com/watch?v=b1"}` y registra la `BroadcastRequest`; `StartBroadcast`/`EndBroadcast` cuentan) e `ingest bool` (implementa `IngestKeyProvider`: `("rtmps://stream.kick.com/1", "kick-key")`). Casos: `from-account` con YouTube → 201, destino con `rtmp_url` de la respuesta, `key_mask` de `clave-api`, `key_from_api true`, `broadcast.broadcast_ref "b1"`, `account` vinculada, eventos `broadcast_created` + `destination_key_from_api`, `Privacy` por defecto `unlisted` y `Title` = `name`; con Kick (`ingest`) → 201 sin `broadcast_ref` y con la clave de Kick; cuenta `reauth` → 409; proveedor sin capacidad → 409; `{id}/broadcast` sobre un destino existente sustituye URL y clave; `start` → `StartBroadcast` llamado y `status live`; `end` → `complete`; `start` en Kick → 409; `test` con `key_from_api` → `{skipped: true}` sin llamar a `tester`; ningún cuerpo contiene `clave-api` ni `kick-key` en claro (solo la máscara `••••-api`… comprobar con la máscara real de `crypto.Secret.Mask()`: `••••` + últimos 4).

- [ ] **Step 5: Correr y commit**

Run: `go vet ./internal/httpapi/ && go test ./internal/httpapi/ -race -count=1` (≈3 min); `go list -deps ./internal/httpapi | grep -E "go-rtmp|internal/rtmpio|internal/webtls|platforms/twitch|platforms/youtube|platforms/kick|platforms/tokens"` vacío.

```bash
git add internal/httpapi internal/store
git commit -m "feat(httpapi): credenciales propias, callback y webhook públicos, emisiones y clave por API, capacidades completas"
```

---
### Task 10: Cableado en `main.go`, configuración, mantenimiento y guards de CI

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go` (`YouTubeChatBudget`, `YouTubeQuota`), `cmd/splitstream/main.go`, `cmd/splitstream/main_test.go`, `.github/workflows/ci.yml`, `deploy/env.example`

**Interfaces:**
- Produces: `config.Config.YouTubeChatBudget int` (`SPLITSTREAM_YOUTUBE_CHAT_BUDGET`, 6000), `config.Config.YouTubeQuota int` (`SPLITSTREAM_YOUTUBE_QUOTA`, 10000, informativo); registro con `twitch`, `youtube` y `kick`; `quota.Counter` en `fondo` no (no tiene bucle); jobs `cuota` (`PruneQuota` de 7 días) y `webhooks_kick` (re-suscribir cuentas de Kick con URL pública); `httpapi.Config` con `Quota`, `ChatIngest: agregador.Ingest`, `ChatBudget`.

- [ ] **Step 1: Config**

Tests como en la v0.11 (`TestTwitchClientIDAndChatRetention` sirve de patrón): defaults 6000/10000, override, negativo → error, `LogValue` con `youtube_chat_budget`. Implementación con `parseNonNegative`.

- [ ] **Step 2: `main.go`**

En el bloque de plataformas:

```go
	cuota := quota.NewCounter(db)
	cuota.Logger = logger
	registro := platforms.NewRegistry(
		twitch.New(twitch.Options{ClientID: twitch.ResolveClientID(cfg.TwitchClientID), Logger: logger}),
		youtube.New(youtube.Options{Logger: logger, Quota: cuota.Sink,
			ChatBudget: func(accountID int64) (int, int, bool) {
				used, err := cuota.UsedToday(context.Background(), accountID)
				return used, cfg.YouTubeChatBudget, err == nil
			},
			OnChatPaused: func(acct store.Account, used, budget int) {
				db.LogEvent(context.Background(), store.Event{Level: store.LevelWarn, Kind: "chat_paused_quota",
					Message: fmt.Sprintf("el chat de YouTube de %s se pausó al llegar a %d de %d unidades; se reanuda mañana o si subes SPLITSTREAM_YOUTUBE_CHAT_BUDGET", acct.DisplayName, used, budget)})
			}}),
		kick.New(kick.Options{Logger: logger}),
	)
```

(`youtube.Options.ChatBudget` con esa firma: ajustar la Task 5 si difiere.) `httpapi.Config`: `Quota: cuota, ChatIngest: agregador.Ingest, ChatBudget: cfg.YouTubeChatBudget`. Jobs: `{Name: "cuota", Run: PruneQuota(cuota.Day() − 7 días)}` y `{Name: "webhooks_kick", Run: …}`: si `cfg.TLS()` y `publicURL != ""`, para cada cuenta `ok` de Kick con destino habilitado vinculado, `SubscribeChat` best-effort (Kick des-suscribe tras un día de fallos) — necesita `gestorTokens.Token`; devuelve «kick: N suscripciones renovadas». El job corre en el planificador diario existente. Mover la construcción de `webtls.Build`/`publicURL` **antes** del bloque de plataformas si aún no lo está (hoy va después: reordenar; `webtls.Build` no depende de nada del bloque).

`main_test.go`: `TestRunExposesPlatforms` pasa a esperar tres plataformas (`kick`, `twitch`, `youtube` en ese orden) con `configured` true en YouTube y Kick (credenciales por cuenta) y `requires_own_app` true en ambas.

- [ ] **Step 3: CI y `env.example`**

`ci.yml`: el guard de `internal/platforms/... ./internal/chat` ya cubre `youtube` y `kick`; el de `httpapi` añade `|internal/platforms/youtube|internal/platforms/kick`. `env.example`: `#SPLITSTREAM_YOUTUBE_CHAT_BUDGET=6000` y `#SPLITSTREAM_YOUTUBE_QUOTA=10000` con comentarios.

- [ ] **Step 4: Correr y commit**

Run: `go vet ./... && go test ./internal/config/ ./cmd/splitstream/ -race -count=1`; guards en local; YAML válido.

```bash
git add internal/config cmd/splitstream .github/workflows/ci.yml deploy/env.example
git commit -m "feat: cablear YouTube, Kick y la cuota; presupuesto del chat; jobs de cuota y webhooks"
```

---

### Task 11: Panel — asistente de credenciales, redirect, clave por API, emisiones y cuota

**Files:**
- Create: `web/src/components/AsistenteCredenciales.vue`
- Modify: `web/src/api.js`, `web/src/components/ConectarCuenta.vue`, `web/src/components/DialogoDestino.vue`, `web/src/components/TarjetaDestino.vue`, `web/src/components/Chat.vue`, `web/src/pages/Ajustes.vue`, `web/src/pages/Panel.vue` (props/eventos nuevos de la tarjeta), `web/src/stores/panel.js`, `web/src/iconos.js`

**Interfaces:**
- Consumes: Task 9.
- Produces: `api.iniciarAuth(p, body)`, `api.crearDestinoDesdeCuenta(body)`, `api.crearEmision(id, body)`, `api.emision(id)`, `api.salirAlAire(id)`, `api.terminarEmision(id)`; componentes.

- [ ] **Step 1: `api.js` e iconos**

```js
  iniciarAuth: (p, body) => pedir('POST', `/api/platforms/${p}/auth`, body),
  crearDestinoDesdeCuenta: (body) => pedir('POST', '/api/destinations/from-account', body),
  crearEmision: (id, body) => pedir('POST', `/api/destinations/${id}/broadcast`, body),
  emision: (id) => pedir('GET', `/api/destinations/${id}/broadcast`),
  salirAlAire: (id) => pedir('POST', `/api/destinations/${id}/broadcast/start`),
  terminarEmision: (id) => pedir('POST', `/api/destinations/${id}/broadcast/end`),
```

(`iniciarAuth` con `body` `undefined` sigue funcionando para Twitch.) Iconos: `mdiOpenInNew as iAbrir`, `mdiPlayCircle as iAlAire`, `mdiStopCircle as iTerminar`, `mdiKeyLink as iClaveApi` (comprobar en `@quasar/extras/mdi-v7`; elegir equivalentes y anotar).

- [ ] **Step 2: `AsistenteCredenciales.vue`**

Props `plataforma` (`youtube|kick`), `origin` (`location.origin`); emite `listo({client_id, client_secret})`. Un `q-stepper` vertical con los pasos de §3.3 del spec (texto en español; en YouTube el último paso lleva el aviso de los 7 días en «Testing» y el enlace a `docs/youtube-credenciales.md`; en Kick el paso de «Redirect URL» enseña `${origin}/api/platforms/kick/callback` con botón de copiar, y el de webhooks `${panel.estado.panel.public_url}/api/platforms/kick/webhook` solo si `panel.estado.panel.tls`, con la nota «sin URL pública el chat de Kick no está disponible»), y al final dos `q-input` (`client_id`, `client_secret` tipo password con «mostrar») y el botón «Continuar». Enlace «¿Por qué me piden esto?» que despliega el párrafo honesto (cuota por proyecto en YouTube; Kick no admite apps públicas).

- [ ] **Step 3: `ConectarCuenta.vue`**

Nuevas props `requiereApp` y `redirect` (del `platformDTO`: `requires_own_app`, y redirect = plataforma `kick`; mejor: el `POST` devuelve `redirect_url` y el componente decide por eso). Flujo: si `requiereApp` y no hay credenciales, muestra `AsistenteCredenciales` y, al `listo`, llama `api.iniciarAuth(plataforma, {client_id, client_secret, origin: location.origin})`; si la respuesta trae `redirect_url`, enseña «Abrir Kick para autorizar» (`<a :href target="_blank" rel="noopener">`) y sondea `estadoAuth` cada 3 s; si trae `user_code`, el código como hoy. Las credenciales no se guardan en el componente más allá de la petición (`ref` que se limpia tras enviar).

- [ ] **Step 4: `DialogoDestino.vue`**

Con `cuentaId` de una plataforma cuyas `capabilities` tienen `ingest_key` (YouTube/Kick): en el **alta**, el campo de clave y el servidor se sustituyen por un bloque «Clave por API»: para YouTube, campos «Título de la emisión» (por defecto el nombre), «Privacidad» (`unlisted` por defecto: `q-select` público/no listado/privado) y «Hora» (`q-input type=datetime-local`, opcional); botón «Crear emisión y traer la clave» = `api.crearDestinoDesdeCuenta({account_id, name, title, privacy, scheduled_at})` y cierre con `guardado`; para Kick, «Traer la clave de Kick» = `crearDestinoDesdeCuenta({account_id, name})`. Un enlace «pegar la clave a mano» vuelve al formulario normal. En **edición** de un destino con cuenta y `ingest_key`, botón «Nueva emisión / releer la clave» = `api.crearEmision(id, {…})`. Chips «Clave por API» y «Programar» cuando aplican.

- [ ] **Step 5: `TarjetaDestino.vue`, `Chat.vue`, `Ajustes.vue`, `Panel.vue`, store**

- Tarjeta: en el pie, si `destino.key_from_api` → `clave por API` en vez de la máscara (con tooltip «la trajo la plataforma; no hace falta probarla»); si `destino.broadcast?.broadcast_ref`: botón «Salir al aire» (`status !== 'live'`, emite `alAire`) o «Terminar» (`status === 'live'`, emite `terminar`), y enlace «ver en YouTube» (`watch_url`); el ítem «Probar» del menú se deshabilita con `key_from_api` (caption «no hace falta»). `Panel.vue` maneja `alAire`/`terminar` con `api.salirAlAire`/`terminarEmision` y notifica.
- `Chat.vue`: si alguna cuenta de YouTube (`panel.cuentas` → cargar `api.cuentas()` al abrir el chat) tiene `quota_used_today`, barra `q-linear-progress` con «{used} / {budget} unidades hoy · el chat se pausará a {budget}» (`budget` viene de `GET /api/platforms`? no: añadir `chat_budget` a `platformDTO` de YouTube en la Task 9, o al `statusDTO.panel`; elegir `panelDTO.youtube_chat_budget int` y añadirlo en la Task 9 (`Config.ChatBudget`) — anotarlo).
- `Ajustes.vue` «Cuentas conectadas»: badge «app propia» con `own_app`, «{quota_used_today} unidades hoy» en YouTube; el texto fijo pasa a «Cuentas conectadas por código (Twitch, YouTube) o por redirect (Kick)».
- Store: `cuentas` y `cargarCuentas()` si aún no existen (Ajustes ya llama a `api.cuentas()` localmente: reutilizar).

- [ ] **Step 6: Compilar y commit**

Run: `cd web && npm run build` → limpio.

```bash
git add web/src
git commit -m "feat(panel): asistente de credenciales, conexión por redirect, clave por API, emisiones y cuota"
```

---
### Task 12: Documentación — credenciales de YouTube y Kick, manual, README y spec base

**Files:**
- Create: `docs/youtube-credenciales.md`, `docs/kick-credenciales.md`
- Modify: `docs/manual-de-usuario.md` (§2: «Conectar YouTube» y «Conectar Kick»; §6 tabla de capacidades; sección «Título en vivo y chat»: cuota y pausa, chat de Kick con URL pública; sección nueva «Crear la emisión desde el panel»), `README.md` («Alcance»: chat hoy Twitch, YouTube y Kick; tabla de configuración con las dos variables; «Cómo se usa»), `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§1, §4, §7, §9, §12), `docs/lanzamiento.md` (cabecera v0.12.0 y el párrafo de «se acaba el copiar y pegar claves»), `deploy/env.example` (si la Task 10 no lo cerró)

- [ ] **Step 1: `docs/youtube-credenciales.md`**

Los cinco pasos de §3.3 del spec, cada uno con un título, qué pulsar, y un marcador `![captura](img/youtube-paso-N.png)` que el usuario rellena en la puerta (crear `docs/img/` con un `.gitkeep`). Encabezado con el párrafo honesto: por qué se pide (la cuota de la YouTube Data API es por proyecto de Google Cloud: 10 000 unidades al día; con una app compartida, doscientos cambios de título entre todos los usuarios del mundo la agotarían), cuánto tarda (unos 10 minutos), y el aviso de «Testing»: la autorización caduca a los 7 días y hay que reconectar; publicar la app («In production») lo evita a cambio de una pantalla de «app no verificada» que solo ves tú. Al final, «Qué gasta cuota»: tabla con crear emisión ≈ 150, cambiar título 50, cada sondeo del chat 5 (cada 4 s ≈ 4 500 por hora), y por qué el chat se pausa a 6 000 (`SPLITSTREAM_YOUTUBE_CHAT_BUDGET`).

- [ ] **Step 2: `docs/kick-credenciales.md`**

Pasos: Ajustes → Developer (2FA obligatoria) → «Create app» → nombre → **Redirect URL exactamente** la que el panel enseña (`<origen del panel>/api/platforms/kick/callback`; ejemplos `http://localhost:8080/...` y `https://relay.ejemplo.com/...`) → si quieres chat: «Enable Webhooks» con `<URL pública>/api/platforms/kick/webhook` (solo con TLS integrado o proxy con HTTPS público; en un PC sin URL pública el chat de Kick no está disponible y el panel lo dice) → copiar `client_id` y `client_secret`. Por qué se pide: Kick no admite apps públicas y exige el secret en cada intercambio; no lo incluimos en el binario.

- [ ] **Step 3: Manual**

§2 «Vincula tus canales»: subsecciones «Conectar tu cuenta de YouTube» (asistente, código en `google.com/device`, 7 días en Testing) y «Conectar tu cuenta de Kick» (asistente, pestaña nueva de Kick, volver al panel). Sección nueva «Crear la emisión desde el panel» (antes de «Título en vivo y chat»): con una cuenta de YouTube, «Crear emisión y traer la clave» crea la emisión (no listada por defecto), el *stream* y escribe la URL y la clave en el canal; YouTube sale al aire sola cuando llega la señal de OBS y termina sola cuando se va; «Salir al aire» y «Terminar» por si acaso; con Kick, «Traer la clave de Kick» lee la clave del canal; «Probar» dice que no hace falta. §6: tabla «Qué puede hacer desde el panel» con las tres plataformas (Twitch: título, categoría, chat; YouTube: título, emisión + clave por API, chat con cuota; Kick: título, categoría, clave por API, chat solo con URL pública) y Facebook/X/TikTok «no» con la razón. «Título en vivo y chat»: párrafo de cuota y pausa; párrafo de Kick y URL pública.

- [ ] **Step 4: README, spec base y lanzamiento**

README «Alcance» y «Cómo se usa» (una línea por plataforma nueva; enlaces a los dos docs de credenciales); tabla de configuración con `SPLITSTREAM_YOUTUBE_CHAT_BUDGET` y `SPLITSTREAM_YOUTUBE_QUOTA`. Spec base: §1 (chat de lectura en Twitch, YouTube y Kick), §4 (`internal/platforms/youtube`, `kick`, `quota`), §7 (`destination_broadcasts`, `quota_usage`, `SchemaVersion` 9), §9 (rutas nuevas, dos públicas), §12 (variables y eventos `broadcast_created/started/ended`, `destination_key_from_api`, `chat_paused_quota`, `webhook_rejected`). `docs/lanzamiento.md`: cabecera v0.12.0 y el párrafo «se acaba el copiar y pegar claves» (roadmap §5) acotado a YouTube y Kick.

- [ ] **Step 5: Comprobar y commit**

Run: `grep -rn "SPLITSTREAM_YOUTUBE" README.md deploy/env.example internal/config/config.go`; `cd web && npm run build`; `go vet ./... && go test ./... -race -count=1` (≈6 min).

```bash
git add README.md docs deploy/env.example
git commit -m "docs: credenciales de YouTube y Kick, emisión desde el panel, cuota y chat por webhook"
```

---

## Autorrevisión

**Cobertura del spec.** §3 credenciales: Task 1 (store), Task 2 (`Credentials` en `Provider`, manager), Task 9 (cuerpo del `POST /auth`), Task 11 (asistente), Task 12 (docs). §4.1 YouTube dispositivo: Task 3. §4.2 Kick redirect: Task 6 (proveedor) y Task 9 (`state` firmado, callback pública, `origenPermitido`). §5 emisiones/título: Task 4 y Task 9 (`from-account`, `{id}/broadcast`, `start`/`end`, `BroadcastTitleSetter`). §6 cuota: Task 2 (`quota.Counter`), Task 1 (tabla), Task 9 (`accountDTO`, métrica), Task 10 (job `cuota`). §7.1 chat YouTube: Task 5 (+ presupuesto en Task 10). §7.2 Kick webhook: Task 7 (firma, ventana, repetición, suscripción) y Task 9 (ruta pública, `Ingest`, `webhook_rejected`), Task 10 (job `webhooks_kick`). §7.3: Task 8. §8.1: Task 1. §8.2: Task 9 (incluye `test` saltado y capacidades completas). §8.3: Task 11. §9 pruebas: cada tarea; CI en Task 10. §10/§11: nada lo contradice.

**Marcadores.** Sin «TBD». Los proveedores se «calcan» de Twitch por instrucción explícita, con los tests especificados caso por caso y los fixtures con sus valores. Dos decisiones anotadas para el implementador: `AuthPrompt.RedirectURI` y `ChatMessage.BroadcasterID` se añaden en `platform.go` en las Tasks 6 y 7 (una línea cada una); `panelDTO.youtube_chat_budget` en la Task 9.

**Consistencia de tipos.** `store.Credentials` ↔ `platforms.Credentials` (alias). `Provider.BeginAuth(ctx, creds)`/`PollAuth(ctx, creds, prompt)`/`Refresh(ctx, acct, creds, refresh)` en Twitch (T2), YouTube (T3), Kick (T6), `fakeProvider` de httpapi/chat/tokens (T2) y el manager (T2). `RedirectAuth.BeginRedirect(ctx, creds, redirectURI, state)` (T2) ↔ Kick (T6) ↔ `handleStartAuth` (T9). `BroadcastScheduler`/`IngestKeyProvider`/`BroadcastTitleSetter` (T2/T4) ↔ YouTube (T4), Kick (T6) ↔ `broadcasts.go`/`live.go` (T9). `ChatWebhook.ParseWebhook(hdr, body)` (T2) ↔ Kick (T7) ↔ `webhook.go` (T9). `Aggregator.Ingest` (T8) ↔ `Config.ChatIngest` (T9/T10). `quota.Counter.Sink/UsedToday` (T2) ↔ `youtube.Options.Quota` (T3) ↔ `Config.Quota` (T9/T10). `store.Broadcast`/`SetBroadcast`/`BroadcastFor`/`BroadcastsByDestination` (T1/T9) ↔ `broadcastDTO` (T9) ↔ `TarjetaDestino` (T11).

**Riesgos anotados.** (1) Costes de los métodos `live*` no confirmados en la tabla pública: la tabla fija es conservadora y está documentada como estimación. (2) `cdn.resolution/frameRate = variable` sin cita literal: si YouTube los rechaza en la puerta, se cambia a `1080p`/`30fps` (una constante). (3) Kick: categorías por la v1 deprecada, aislada en una función. (4) El test de `PollAuth` de YouTube suma cuota a una cuenta que aún no tiene id: se documenta que la primera consulta de identidad (1 unidad) no se contabiliza. (5) La puerta necesita las apps propias del usuario en Google Cloud y Kick, y un VPS con TLS integrado para el chat de Kick.
