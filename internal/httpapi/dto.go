package httpapi

import (
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// Los DTO son el contrato público de la API. Existen aparte de los tipos del motor y del
// store a propósito (spec §15.2): así renombrar un campo interno no rompe al frontend, y el
// motor sigue sin saber que existe JSON, igual que hoy no sabe de go-rtmp ni de SQL.
//
// El precio es una copia, y lo cobra TestMetricsDTOCoversEveryEngineField, que falla si el
// motor gana un campo que nadie mapeó.

type metricsDTO struct {
	State          string `json:"state"`
	Degraded       bool   `json:"degraded"`
	BytesSent      uint64 `json:"bytes_sent"`
	BitrateBPS     uint64 `json:"bitrate_bps"`
	DroppedFrames  uint64 `json:"dropped_frames"`
	UptimeSeconds  int64  `json:"uptime_seconds"`
	Reconnections  uint64 `json:"reconnections"`
	LastError      string `json:"last_error"`
	QueuedBytes    int    `json:"queued_bytes"`
	QueuedMessages int    `json:"queued_messages"`
}

// newMetricsDTO copia las métricas del motor.
//
// Uptime pasa a segundos: un time.Duration serializa como nanosegundos en un int64, que
// para el frontend es un número enorme y sin unidad ninguna.
func newMetricsDTO(m relay.Metrics) metricsDTO {
	return metricsDTO{
		State:          m.State,
		Degraded:       m.Degraded,
		BytesSent:      m.BytesSent,
		BitrateBPS:     m.BitrateBPS,
		DroppedFrames:  m.DroppedFrames,
		UptimeSeconds:  int64(m.Uptime / time.Second),
		Reconnections:  m.Reconnections,
		LastError:      m.LastError,
		QueuedBytes:    m.QueuedBytes,
		QueuedMessages: m.QueuedMessages,
	}
}

// capabilitiesDTO es lo que el destino puede hacer a través de su plataforma (roadmap §2:
// capacidades por destino, no una lista uniforme). Todo false para custom, TikTok o X.
//
// Los dos últimos campos no son cosas que la plataforma sepa hacer sino condiciones para
// poder usarla: YouTube exige credenciales de una app propia (la cuota es por app) y Kick
// exige una URL pública por HTTPS donde recibir el webhook del chat. El panel los usa para
// explicar ANTES de intentarlo, en vez de dejar que el flujo falle a mitad.
type capabilitiesDTO struct {
	Title             bool `json:"title"`
	Category          bool `json:"category"`
	Chat              bool `json:"chat"`
	Schedule          bool `json:"schedule"`
	IngestKey         bool `json:"ingest_key"`
	RequiresOwnApp    bool `json:"requires_own_app"`
	RequiresPublicURL bool `json:"requires_public_url"`
}

// broadcastDTO es la emisión que la plataforma dio para un destino: la de YouTube (con sus
// ids) o solo la marca de que la clave vino por API (Kick). Ni la clave ni el token
// aparecen: la clave vive cifrada en el destino y solo sale enmascarada en key_mask.
type broadcastDTO struct {
	Platform     string `json:"platform"`
	BroadcastRef string `json:"broadcast_ref"`
	Status       string `json:"status"`
	LiveChatID   string `json:"live_chat_id"`
	KeyFromAPI   bool   `json:"key_from_api"`
	WatchURL     string `json:"watch_url"`
}

// newBroadcastDTO. La URL para ver la emisión se compone aquí y solo para YouTube: es la
// única de las tres plataformas donde la emisión tiene una página propia deducible del id.
func newBroadcastDTO(b store.Broadcast) broadcastDTO {
	dto := broadcastDTO{
		Platform: string(b.Platform), BroadcastRef: b.BroadcastRef, Status: b.Status,
		LiveChatID: b.LiveChatID, KeyFromAPI: b.KeyFromAPI,
	}
	if b.Platform == store.PlatformYouTube && b.BroadcastRef != "" {
		dto.WatchURL = "https://www.youtube.com/watch?v=" + b.BroadcastRef
	}
	return dto
}

// accountRefDTO es la cuenta vinculada tal como la ve la tarjeta del destino. Sin tokens.
type accountRefDTO struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Platform    string `json:"platform"`
	Status      string `json:"status"`
}

type accountDTO struct {
	ID           int64      `json:"id"`
	Platform     string     `json:"platform"`
	DisplayName  string     `json:"display_name"`
	Status       string     `json:"status"`
	Scopes       []string   `json:"scopes"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Destinations []int64    `json:"destinations"`
	// OwnApp dice que la cuenta trajo credenciales propias (YouTube, Kick). Es un booleano:
	// ni el client_id ni el client_secret salen nunca de la base.
	OwnApp bool `json:"own_app"`
	// QuotaUsedToday son las unidades de cuota gastadas hoy por la cuenta. Solo YouTube la
	// tiene, y solo si el servidor arrancó con contador: null significa «no aplica» o «no
	// se sabe», que el panel distingue de un cero.
	QuotaUsedToday *int      `json:"quota_used_today"`
	CreatedAt      time.Time `json:"created_at"`
}

// newAccountDTO. Ni el token de acceso ni el de refresco tienen campo en Account, así que
// no hay forma de que se filtren aquí por descuido.
func newAccountDTO(a store.Account, dests []int64) accountDTO {
	if dests == nil {
		dests = []int64{}
	}
	scopes := a.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return accountDTO{ID: a.ID, Platform: string(a.Platform), DisplayName: a.DisplayName, Status: a.Status,
		Scopes: scopes, ExpiresAt: a.ExpiresAt, Destinations: dests, OwnApp: a.OwnApp, CreatedAt: a.CreatedAt}
}

type platformDTO struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Capabilities capabilitiesDTO `json:"capabilities"`
	Configured   bool            `json:"configured"`
	// PublicURLOK dice si esta instalación cumple lo que la plataforma exige de URL
	// pública: true siempre que no la pida, y solo con TLS integrado y PublicURL cuando sí
	// (Kick). Con un proxy delante es false aunque el navegador vea HTTPS, porque el
	// binario no sabe por qué nombre lo alcanzan.
	PublicURLOK bool `json:"public_url_ok"`
}

type chatMessageDTO struct {
	ID        int64     `json:"id"`
	SessionID int64     `json:"session_id"`
	Platform  string    `json:"platform"`
	AccountID *int64    `json:"account_id"`
	AuthorID  string    `json:"author_id"`
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	Color     string    `json:"color"`
	Badges    []string  `json:"badges"`
	MessageID string    `json:"message_id"`
	At        time.Time `json:"at"`
}

type destinationDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	RTMPURL   string `json:"rtmp_url"`
	KeyMask   string `json:"key_mask"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sort_order"`
	// LogoETag identifica la versión del logo del canal; vacío significa que no tiene.
	// Con él, el panel decide si pinta un avatar y construye una URL que cambia cuando la
	// imagen cambia, para que la caché del navegador no sirva la anterior.
	//
	// Va en este DTO —y por tanto también en el push del WebSocket, que empuja este mismo
	// tipo cada segundo— porque el spec §10 exige que el snapshot REST y el push tengan la
	// misma forma. Por eso el etag son 16 caracteres y no 64.
	LogoETag  string      `json:"logo_etag"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Metrics   *metricsDTO `json:"metrics"`
	// KeyFromAPI: la clave la trajo la plataforma, no la pegó nadie. El panel lo usa para
	// no ofrecer «probar destino» (no hay clave inválida que probar) ni invitar a editarla.
	KeyFromAPI bool `json:"key_from_api"`
	// Broadcast es la emisión vinculada al destino; nil si no tiene. Lo rellena decorar.
	Broadcast *broadcastDTO `json:"broadcast"`
	// Account es la cuenta vinculada, sin tokens; nil sin cuenta. Capabilities son las de
	// la plataforma del destino, ceros si no hay proveedor (custom, TikTok, X). Los llena
	// decorar, no newDestinationDTO: así este constructor sigue sin depender de la base ni
	// del registro de plataformas.
	Account      *accountRefDTO  `json:"account"`
	Capabilities capabilitiesDTO `json:"capabilities"`
}

// newDestinationDTO. m es nil cuando no hay sesión viva o el destino está apagado: el
// frontend distingue "sin métricas" de "métricas en cero" por el null, y sin eso enseñaría
// "0 kbps" para un destino apagado.
//
// La clave NO aparece, ni cifrada ni en claro: solo la máscara que el store ya guarda
// desnormalizada (spec §8).
func newDestinationDTO(d store.Destination, m *relay.Metrics, logoETag string) destinationDTO {
	dto := destinationDTO{
		ID: d.ID, Name: d.Name, Platform: string(d.Platform),
		RTMPURL: d.RTMPURL, KeyMask: d.KeyMask, Enabled: d.Enabled,
		SortOrder: d.SortOrder, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		LogoETag: logoETag, KeyFromAPI: d.KeyFromAPI,
	}
	if m != nil {
		x := newMetricsDTO(*m)
		dto.Metrics = &x
	}
	return dto
}

type eventDTO struct {
	ID            int64     `json:"id"`
	SessionID     *int64    `json:"session_id"`
	DestinationID *int64    `json:"destination_id"`
	Level         string    `json:"level"`
	Kind          string    `json:"kind"`
	Message       string    `json:"message"`
	CreatedAt     time.Time `json:"created_at"`
}

func newEventDTO(e store.Event) eventDTO {
	return eventDTO{
		ID: e.ID, SessionID: e.SessionID, DestinationID: e.DestinationID,
		Level: string(e.Level), Kind: e.Kind, Message: e.Message, CreatedAt: e.CreatedAt,
	}
}

// sessionDTO describe la sesión de ingesta en curso. Live en false significa que no hay
// nadie publicando, y entonces el resto de los campos no significan nada.
type sessionDTO struct {
	Live       bool       `json:"live"`
	ID         int64      `json:"id"`
	StartedAt  *time.Time `json:"started_at"`
	Width      *int       `json:"width"`
	Height     *int       `json:"height"`
	BitrateBPS *int       `json:"bitrate_bps"`
}

// ingestDTO es la tarjeta de ingesta del panel: dónde publicar y con qué app. La clave va
// enmascarada; para verla en claro hay que rotarla (spec §8).
type ingestDTO struct {
	URL     string `json:"url"`
	App     string `json:"app"`
	KeyMask string `json:"key_mask"`
}

// statusDTO es lo que devuelve GET /api/status y lo que el WebSocket empuja cada segundo.
//
// Es el MISMO tipo a propósito: el spec §10 dice que el snapshot inicial de la UI viene del
// GET para no depender de que el WS conecte primero, y eso solo funciona si las dos fuentes
// tienen exactamente la misma forma.
type statusDTO struct {
	// Version del binario. Va en el estado y no en un endpoint público aparte para no
	// anunciar a cualquiera qué versión corre este servicio.
	Version      string           `json:"version"`
	Ingest       ingestDTO        `json:"ingest"`
	Session      sessionDTO       `json:"session"`
	Destinations []destinationDTO `json:"destinations"`
	// RecentEvents son los últimos 20 eventos, para el registro del panel y sus avisos.
	RecentEvents []eventDTO `json:"recent_events"`
	// Recording es el estado de la grabación de la sesión (spec v0.9 §6).
	Recording recordingStatusDTO `json:"recording"`
	// Panel es cómo se sirve el panel (ver panelDTO).
	Panel panelDTO `json:"panel"`
	// Update es el aviso de versión nueva (ver updateDTO).
	Update updateDTO `json:"update"`
}

// updateDTO es el aviso de versión nueva (spec v0.10 §6). Solo el aviso: el panel enseña
// un enlace y nada se actualiza solo.
type updateDTO struct {
	Available bool   `json:"available"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
}

// panelDTO dice cómo se sirve el panel: si el propio binario termina TLS y con qué URL
// pública. Lo necesita el chat de Kick (v0.12) para saber si hay dirección HTTPS que dar
// a un webhook. Con un proxy delante `tls` es false aunque el navegador vea HTTPS: es el
// TLS del binario, no el del proxy.
type panelDTO struct {
	TLS       bool   `json:"tls"`
	PublicURL string `json:"public_url"`
	// YouTubeChatBudget son las unidades de cuota diarias que el lector de chat de YouTube
	// puede gastar. Va aquí, con el resto de lo que describe este arranque, para que el
	// panel pueda contarlo junto a `quota_used_today` de cada cuenta: sin el presupuesto,
	// un número de unidades gastadas no dice si queda mucho o poco. 0: sin presupuesto
	// configurado, y el panel no lo enseña.
	YouTubeChatBudget int `json:"youtube_chat_budget"`
	// YouTubeQuota es la cuota diaria que Google asigna al proyecto de la app propia
	// (SPLITSTREAM_YOUTUBE_QUOTA). Aquí no se aplica nada: quien la aplica es Google. Está
	// para que el panel la enseñe junto al presupuesto del chat, que es solo una parte de
	// ella, y se entienda cuánto margen queda para crear emisiones y leer el canal. 0: sin
	// cuota declarada, y el panel no la enseña.
	YouTubeQuota int `json:"youtube_quota"`
}

// sessionSummaryDTO es una fila del historial: la sesión y cuántos eventos dejó por nivel.
type sessionSummaryDTO struct {
	ID           int64      `json:"id"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at"`
	Width        *int       `json:"width"`
	Height       *int       `json:"height"`
	BitrateBPS   *int       `json:"bitrate_bps"`
	HasRecording bool       `json:"has_recording"`
	Events       struct {
		Info  int `json:"info"`
		Warn  int `json:"warn"`
		Error int `json:"error"`
	} `json:"events"`
}

func newSessionSummaryDTO(s store.SessionSummary) sessionSummaryDTO {
	dto := sessionSummaryDTO{
		ID: s.ID, StartedAt: s.StartedAt.UTC(),
		Width: s.Width, Height: s.Height, BitrateBPS: s.BitrateBPS,
		HasRecording: s.HasRecording,
	}
	if s.EndedAt != nil {
		e := s.EndedAt.UTC()
		dto.EndedAt = &e
	}
	dto.Events.Info, dto.Events.Warn, dto.Events.Error = s.Info, s.Warn, s.Error
	return dto
}

// sessionDetailDTO es la ficha completa de una sesión: sus eventos, sus grabaciones y los
// contadores de su chat (spec v0.13 §3.4). Se llama "Detail" y no "DTO" a secas porque
// sessionDTO ya existe y significa otra cosa: la sesión VIVA del estado (spec base §10).
type sessionDetailDTO struct {
	ID             int64          `json:"id"`
	StartedAt      time.Time      `json:"started_at"`
	EndedAt        *time.Time     `json:"ended_at"`
	Width          *int           `json:"width"`
	Height         *int           `json:"height"`
	BitrateBPS     *int           `json:"bitrate_bps"`
	DurationS      int            `json:"duration_s"`
	Events         []eventDTO     `json:"events"`
	Recordings     []recordingDTO `json:"recordings"`
	ChatCount      int            `json:"chat_count"`
	ChatByPlatform map[string]int `json:"chat_by_platform"`
}

// newSessionDetailDTO arma la ficha a partir de la sesión y de lo que ya se leyó del
// store. duration_s es 0 mientras la sesión sigue viva (ended_at nil): no hay fin con el
// que restar.
func newSessionDetailDTO(s store.Session, events []store.Event, recordings []store.Recording, chatCount int, chatByPlatform map[string]int) sessionDetailDTO {
	dto := sessionDetailDTO{
		ID: s.ID, StartedAt: s.StartedAt.UTC(),
		Width: s.Width, Height: s.Height, BitrateBPS: s.BitrateBPS,
		ChatCount: chatCount, ChatByPlatform: chatByPlatform,
	}
	if s.EndedAt != nil {
		e := s.EndedAt.UTC()
		dto.EndedAt = &e
		dto.DurationS = int(e.Sub(dto.StartedAt).Seconds())
	}
	dto.Events = make([]eventDTO, 0, len(events))
	for _, ev := range events {
		dto.Events = append(dto.Events, newEventDTO(ev))
	}
	dto.Recordings = make([]recordingDTO, 0, len(recordings))
	for _, r := range recordings {
		dto.Recordings = append(dto.Recordings, newRecordingDTO(r))
	}
	return dto
}

type webhookDTO struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Format     string    `json:"format"`
	HasSecret  bool      `json:"has_secret"`
	MinLevel   string    `json:"min_level"`
	Enabled    bool      `json:"enabled"`
	LastStatus *int      `json:"last_status"`
	LastError  string    `json:"last_error"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// newWebhookDTO. El secreto no aparece: el store ni siquiera lo pone en Webhook.
func newWebhookDTO(w store.Webhook) webhookDTO {
	return webhookDTO{
		ID: w.ID, Name: w.Name, URL: w.URL, Format: string(w.Format), HasSecret: w.HasSecret,
		MinLevel: string(w.MinLevel), Enabled: w.Enabled, LastStatus: w.LastStatus,
		LastError: w.LastError, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}
}

type recordingStatusDTO struct {
	Enabled       bool   `json:"enabled"`
	Active        bool   `json:"active"`
	State         string `json:"state"`
	Degraded      bool   `json:"degraded"`
	Bytes         uint64 `json:"bytes"`
	DroppedFrames uint64 `json:"dropped_frames"`
	Segments      int    `json:"segments"`
	UsedBytes     int64  `json:"used_bytes"`
	MaxBytes      int64  `json:"max_bytes"`
	FreeBytes     int64  `json:"free_bytes"`
	Dir           string `json:"dir"`
}

type recordingSettingsDTO struct {
	Enabled    bool    `json:"enabled"`
	SegmentMin int     `json:"segment_min"`
	MaxGB      float64 `json:"max_gb"`
	KeepDays   int     `json:"keep_days"`
	Dir        string  `json:"dir"`
	UsedBytes  int64   `json:"used_bytes"`
	FreeBytes  int64   `json:"free_bytes"`
}

type recordingDTO struct {
	ID         int64      `json:"id"`
	SessionID  *int64     `json:"session_id"`
	Segment    int        `json:"segment"`
	Path       string     `json:"path"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at"`
	Bytes      int64      `json:"bytes"`
	DurationMS int        `json:"duration_ms"`
	InProgress bool       `json:"in_progress"`
}

func newRecordingDTO(r store.Recording) recordingDTO {
	dto := recordingDTO{
		ID: r.ID, SessionID: r.SessionID, Segment: r.Segment, Path: r.Path,
		StartedAt: r.StartedAt.UTC(), Bytes: r.Bytes, DurationMS: r.DurationMS, InProgress: r.EndedAt == nil,
	}
	if r.EndedAt != nil {
		e := r.EndedAt.UTC()
		dto.EndedAt = &e
	}
	return dto
}
