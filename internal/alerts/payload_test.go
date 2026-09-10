package alerts_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/alerts"
	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func evento() store.Event {
	dest, ses := int64(3), int64(7)
	return store.Event{
		ID: 42, SessionID: &ses, DestinationID: &dest, Level: store.LevelError,
		Kind: "destination_suspended", Message: "el destino queda suspendido",
		CreatedAt: time.Date(2026, 9, 9, 20, 15, 3, 0, time.UTC),
	}
}

func TestJSONPayloadShape(t *testing.T) {
	body, err := alerts.Payload(store.WebhookJSON, evento(), &store.Destination{ID: 3, Name: "YouTube", Platform: "youtube"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != float64(42) || got["kind"] != "destination_suspended" || got["level"] != "error" {
		t.Errorf("cuerpo = %s", body)
	}
	if got["session_id"] != float64(7) {
		t.Errorf("session_id = %v", got["session_id"])
	}
	d := got["destination"].(map[string]any)
	if d["name"] != "YouTube" || d["platform"] != "youtube" {
		t.Errorf("destination = %v", d)
	}
	if got["at"] != "2026-09-09T20:15:03Z" {
		t.Errorf("at = %v", got["at"])
	}
}

func TestJSONPayloadWithoutDestinationIsNull(t *testing.T) {
	ev := evento()
	ev.DestinationID, ev.SessionID = nil, nil
	body, _ := alerts.Payload(store.WebhookJSON, ev, nil)
	if !strings.Contains(string(body), `"destination":null`) || !strings.Contains(string(body), `"session_id":null`) {
		t.Errorf("cuerpo = %s", body)
	}
}

func TestDiscordAndSlackPayloads(t *testing.T) {
	dest := &store.Destination{Name: "YouTube"}
	body, _ := alerts.Payload(store.WebhookDiscord, evento(), dest)
	var d map[string]string
	_ = json.Unmarshal(body, &d)
	if !strings.HasPrefix(d["content"], "**[error]** YouTube · ") {
		t.Errorf("discord = %s", body)
	}
	body, _ = alerts.Payload(store.WebhookSlack, evento(), dest)
	var s map[string]string
	_ = json.Unmarshal(body, &s)
	if !strings.HasPrefix(s["text"], "[error] YouTube · ") {
		t.Errorf("slack = %s", body)
	}
}

func TestSignIsHMACSHA256Hex(t *testing.T) {
	body := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := alerts.Sign(crypto.Secret("s3cr3t"), body); got != want {
		t.Errorf("Sign = %q, quería %q", got, want)
	}
}
