// Package alerts convierte eventos en avisos: hoy webhooks salientes; el panel recibe los
// suyos por el WebSocket de estado.
package alerts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

type jsonDestination struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type jsonPayload struct {
	ID          int64            `json:"id"`
	Kind        string           `json:"kind"`
	Level       string           `json:"level"`
	Message     string           `json:"message"`
	SessionID   *int64           `json:"session_id"`
	Destination *jsonDestination `json:"destination"`
	At          time.Time        `json:"at"`
}

// Payload compone el cuerpo del aviso según el formato (spec v0.8 §5.2). dest puede ser
// nil: un evento del sistema no tiene destino.
func Payload(format store.WebhookFormat, ev store.Event, dest *store.Destination) ([]byte, error) {
	switch format {
	case store.WebhookJSON:
		p := jsonPayload{
			ID: ev.ID, Kind: ev.Kind, Level: string(ev.Level), Message: ev.Message,
			SessionID: ev.SessionID, At: ev.CreatedAt.UTC(),
		}
		if dest != nil {
			p.Destination = &jsonDestination{ID: dest.ID, Name: dest.Name, Platform: string(dest.Platform)}
		}
		return json.Marshal(p)
	case store.WebhookDiscord:
		return json.Marshal(map[string]string{"content": "**[" + string(ev.Level) + "]** " + linea(ev, dest)})
	case store.WebhookSlack:
		return json.Marshal(map[string]string{"text": "[" + string(ev.Level) + "] " + linea(ev, dest)})
	default:
		return nil, fmt.Errorf("formato de webhook desconocido %q", format)
	}
}

// linea es el texto de una sola línea para los chats: "YouTube · el destino …" o solo el
// mensaje si no hay destino.
func linea(ev store.Event, dest *store.Destination) string {
	if dest != nil {
		return dest.Name + " · " + ev.Message
	}
	return ev.Message
}

// Sign devuelve "sha256=<hex>" del HMAC-SHA256 del cuerpo con el secreto.
func Sign(secret crypto.Secret, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret.Reveal()))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
