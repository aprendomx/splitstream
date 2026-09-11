package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/aprendomx/splitstream/internal/chat"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

func TestChatWSSendsTheSnapshotThenLiveMessages(t *testing.T) {
	cb := chat.NewBus()
	srv, db := newTestServer(t, func(c *Config) { c.Chat = cb })
	ck := login(t, srv)
	sid, _ := db.StartSession(context.Background())
	for i := 0; i < 3; i++ {
		cb.Publish(chat.Message{SessionID: sid, ChatMessage: platforms.ChatMessage{Platform: platforms.Twitch, Author: "a", Text: "previo"}})
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	hdr := http.Header{}
	for _, c := range ck {
		hdr.Add("Cookie", c.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/chat/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var m chatMessageDTO
	for i := 0; i < 3; i++ {
		if err := wsjson.Read(ctx, conn, &m); err != nil || m.Text != "previo" {
			t.Fatalf("snapshot %d: %v %+v", i, err, m)
		}
	}
	cb.Publish(chat.Message{SessionID: sid, ChatMessage: platforms.ChatMessage{Platform: platforms.Twitch, Author: "b", Text: "nuevo", Badges: []string{"moderator/1"}}})
	if err := wsjson.Read(ctx, conn, &m); err != nil || m.Text != "nuevo" || m.SessionID != sid || len(m.Badges) != 1 {
		t.Fatalf("live: %v %+v", err, m)
	}
}

func TestChatWSRequiresSessionAndBus(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// Sin cookie: 401 en el handshake.
	resp, err := http.Get(ts.URL + "/api/chat/ws")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("sin sesión = %d", resp.StatusCode)
	}
	// Con sesión pero sin bus configurado: 404.
	ck := login(t, srv)
	if rec := do(t, srv, ck, http.MethodGet, "/api/chat/ws", ""); rec.Code != 404 {
		t.Errorf("sin bus = %d", rec.Code)
	}
}

func TestSessionChatPaginates(t *testing.T) {
	srv, db := newTestServer(t)
	ck := login(t, srv)
	sid, _ := db.StartSession(context.Background())
	var ms []store.NewChatMessage
	for i := 0; i < 5; i++ {
		ms = append(ms, store.NewChatMessage{SessionID: sid, Platform: "twitch", AuthorID: "u", Author: "v", Text: "m", At: time.Now()})
	}
	db.InsertChatMessages(context.Background(), ms)
	rec := do(t, srv, ck, http.MethodGet, "/api/sessions/"+itoa(sid)+"/chat?limit=2", "")
	var out []chatMessageDTO
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || len(out) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = do(t, srv, ck, http.MethodGet, "/api/sessions/"+itoa(sid)+"/chat?after="+itoa(out[1].ID)+"&limit=10", "")
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out) != 3 {
		t.Errorf("after = %d", len(out))
	}
	if rec := do(t, srv, ck, http.MethodGet, "/api/sessions/"+itoa(sid)+"/chat?limit=0", ""); rec.Code != 400 {
		t.Errorf("limit 0 = %d", rec.Code)
	}
}
