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

type destinationDTO struct {
	ID        int64       `json:"id"`
	Name      string      `json:"name"`
	Platform  string      `json:"platform"`
	RTMPURL   string      `json:"rtmp_url"`
	KeyMask   string      `json:"key_mask"`
	Enabled   bool        `json:"enabled"`
	SortOrder int         `json:"sort_order"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Metrics   *metricsDTO `json:"metrics"`
}

// newDestinationDTO. m es nil cuando no hay sesión viva o el destino está apagado: el
// frontend distingue "sin métricas" de "métricas en cero" por el null, y sin eso enseñaría
// "0 kbps" para un destino apagado.
//
// La clave NO aparece, ni cifrada ni en claro: solo la máscara que el store ya guarda
// desnormalizada (spec §8).
func newDestinationDTO(d store.Destination, m *relay.Metrics) destinationDTO {
	dto := destinationDTO{
		ID: d.ID, Name: d.Name, Platform: string(d.Platform),
		RTMPURL: d.RTMPURL, KeyMask: d.KeyMask, Enabled: d.Enabled,
		SortOrder: d.SortOrder, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
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
}
