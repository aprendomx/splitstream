package youtube_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/youtube"
	"github.com/aprendomx/splitstream/internal/store"
)

// servidorEmisiones simula www.googleapis.com/youtube/v3 para las rutas de emisiones:
// insert/bind/transition/update/list. Cada campo *Res es inyectable, con un valor por
// defecto que basta para el caso feliz de la mayoría de los tests; los contadores dejan
// verificar que un error de validación (privacidad inválida, título vacío) no llega a
// tocar la red, y que SetBroadcastTitle se salta los list.
type servidorEmisiones struct {
	*httptest.Server

	insertBroadcastCalls atomic.Int32
	insertStreamCalls    atomic.Int32
	bindCalls            atomic.Int32
	transitionCalls      atomic.Int32
	updateCalls          atomic.Int32
	listCalls            atomic.Int32

	lastInsertBroadcastBody []byte
	lastBindQuery           string
	lastUpdateBody          []byte
	lastTransitionQuery     string

	// transitionRes decide código+cuerpo de POST /liveBroadcasts/transition; por defecto
	// siempre responde éxito en vivo.
	transitionRes func(n int32, r *http.Request) (int, []byte)
	// listRes decide código+cuerpo de GET /liveBroadcasts?mine=true&broadcastStatus=...;
	// por defecto no hay ninguna emisión ni en upcoming ni en active.
	listRes func(estado string) []byte
	// getSnippetRes decide el cuerpo de GET /liveBroadcasts?part=snippet&id=...
	// (SetBroadcastTitle releyendo antes de reemplazar el snippet); por defecto trae el
	// snippet de broadcast_insert.json (mismo scheduledStartTime del fixture).
	getSnippetRes func(id string) []byte
}

func nuevoServidorEmisiones(t *testing.T) *servidorEmisiones {
	t.Helper()
	s := &servidorEmisiones{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /liveBroadcasts", func(w http.ResponseWriter, r *http.Request) {
		s.insertBroadcastCalls.Add(1)
		if r.URL.Query().Get("part") != "snippet,status,contentDetails" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"falta part","errors":[{"reason":"invalidParameter"}]}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.lastInsertBroadcastBody = body
		w.Write(fixture(t, "broadcast_insert.json"))
	})

	mux.HandleFunc("POST /liveStreams", func(w http.ResponseWriter, r *http.Request) {
		s.insertStreamCalls.Add(1)
		if r.URL.Query().Get("part") != "snippet,cdn" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"falta part","errors":[{"reason":"invalidParameter"}]}}`))
			return
		}
		w.Write(fixture(t, "stream_insert.json"))
	})

	mux.HandleFunc("POST /liveBroadcasts/bind", func(w http.ResponseWriter, r *http.Request) {
		s.bindCalls.Add(1)
		s.lastBindQuery = r.URL.RawQuery
		q := r.URL.Query()
		if q.Get("id") != "bcast123" || q.Get("streamId") != "stream789" || q.Get("part") != "id,contentDetails" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"bind mal formado","errors":[{"reason":"invalidParameter"}]}}`))
			return
		}
		w.Write(fixture(t, "bind.json"))
	})

	mux.HandleFunc("POST /liveBroadcasts/transition", func(w http.ResponseWriter, r *http.Request) {
		n := s.transitionCalls.Add(1)
		s.lastTransitionQuery = r.URL.RawQuery
		if s.transitionRes != nil {
			code, body := s.transitionRes(n, r)
			w.WriteHeader(code)
			w.Write(body)
			return
		}
		w.Write([]byte(`{"id":"bcast123","status":{"lifeCycleStatus":"live"}}`))
	})

	mux.HandleFunc("PUT /liveBroadcasts", func(w http.ResponseWriter, r *http.Request) {
		s.updateCalls.Add(1)
		if r.URL.Query().Get("part") != "snippet" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"falta part","errors":[{"reason":"invalidParameter"}]}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.lastUpdateBody = body
		w.Write(fixture(t, "broadcast_update.json"))
	})

	mux.HandleFunc("GET /liveBroadcasts", func(w http.ResponseWriter, r *http.Request) {
		s.listCalls.Add(1)
		q := r.URL.Query()
		// Dos formas de la misma ruta: por id (SetBroadcastTitle releyendo el snippet) o
		// por mine+broadcastStatus (SetTitle buscando la próxima/activa). Se distinguen
		// por los parámetros, no por el path: la API real solo tiene un endpoint list.
		if id := q.Get("id"); id != "" {
			if s.getSnippetRes != nil {
				w.Write(s.getSnippetRes(id))
				return
			}
			w.Write([]byte(`{"items":[{"id":"` + id + `","snippet":{"title":"Prueba","description":"","scheduledStartTime":"2026-09-11T20:00:00Z"}}]}`))
			return
		}
		if q.Get("mine") != "true" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"message":"falta mine=true","errors":[{"reason":"invalidParameter"}]}}`))
			return
		}
		estado := q.Get("broadcastStatus")
		if s.listRes != nil {
			w.Write(s.listRes(estado))
			return
		}
		w.Write([]byte(`{"items":[]}`))
	})

	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func proveedorEmisiones(s *servidorEmisiones, opts youtube.Options) *youtube.Provider {
	opts.HTTPClient = s.Client()
	opts.APIBase = s.URL
	// OAuthBase también apunta al fake: ningún New de test debe quedarse con el host real
	// de Google, aunque estos tests no ejerciten el flujo de OAuth.
	opts.OAuthBase = s.URL
	return youtube.New(opts)
}

func cuentaCuota() (map[int64]int, func(int64) platforms.QuotaSink) {
	sinks := map[int64]int{}
	return sinks, func(id int64) platforms.QuotaSink {
		return func(units int) { sinks[id] += units }
	}
}

func cuentaYT() store.Account {
	return store.Account{ID: 7, Platform: store.PlatformYouTube, ExternalID: "UCabc123"}
}

func TestCreateBroadcastInsertsBindsAndReturnsTheIngestKey(t *testing.T) {
	s := nuevoServidorEmisiones(t)
	ahora := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	sinks, quota := cuentaCuota()
	p := proveedorEmisiones(s, youtube.Options{Now: func() time.Time { return ahora }, Quota: quota})
	ctx := context.Background()
	acct := cuentaYT()

	bcast, err := p.CreateBroadcast(ctx, acct, "tok", platforms.BroadcastRequest{Title: "Prueba", Privacy: "unlisted"})
	if err != nil {
		t.Fatal(err)
	}
	quiero := platforms.Broadcast{
		Ref: "bcast123", StreamRef: "stream789", LiveChatID: "chat456",
		IngestURL: "rtmp://a.rtmp.youtube.com/live2", Key: "abcd-efgh-ijkl-mnop",
		WatchURL: "https://www.youtube.com/watch?v=bcast123",
	}
	if bcast != quiero {
		t.Fatalf("broadcast = %+v, quería %+v", bcast, quiero)
	}

	var cuerpo struct {
		Snippet struct {
			Title              string `json:"title"`
			ScheduledStartTime string `json:"scheduledStartTime"`
		} `json:"snippet"`
		Status struct {
			PrivacyStatus           string `json:"privacyStatus"`
			SelfDeclaredMadeForKids bool   `json:"selfDeclaredMadeForKids"`
		} `json:"status"`
		ContentDetails struct {
			EnableAutoStart   bool   `json:"enableAutoStart"`
			EnableAutoStop    bool   `json:"enableAutoStop"`
			LatencyPreference string `json:"latencyPreference"`
		} `json:"contentDetails"`
	}
	if err := json.Unmarshal(s.lastInsertBroadcastBody, &cuerpo); err != nil {
		t.Fatal(err)
	}
	if cuerpo.Snippet.Title != "Prueba" || cuerpo.Snippet.ScheduledStartTime != "2026-09-11T20:00:00Z" ||
		cuerpo.Status.PrivacyStatus != "unlisted" || cuerpo.Status.SelfDeclaredMadeForKids ||
		!cuerpo.ContentDetails.EnableAutoStart || !cuerpo.ContentDetails.EnableAutoStop ||
		cuerpo.ContentDetails.LatencyPreference != "normal" {
		t.Errorf("cuerpo del insert = %+v", cuerpo)
	}
	if s.lastBindQuery != "id=bcast123&streamId=stream789&part=id,contentDetails" {
		t.Errorf("bind query = %q", s.lastBindQuery)
	}
	if sinks[acct.ID] != 150 {
		t.Errorf("cuota = %d, quería 150", sinks[acct.ID])
	}

	// Privacidad inválida y título vacío fallan antes de llamar a la red.
	antes := s.insertBroadcastCalls.Load()
	if _, err := p.CreateBroadcast(ctx, acct, "tok", platforms.BroadcastRequest{Title: "x", Privacy: "bogus"}); err == nil {
		t.Error("privacidad inválida: quería error")
	}
	if _, err := p.CreateBroadcast(ctx, acct, "tok", platforms.BroadcastRequest{Title: "", Privacy: "public"}); err == nil {
		t.Error("título vacío: quería error")
	}
	if s.insertBroadcastCalls.Load() != antes {
		t.Errorf("la validación debió fallar antes de llamar a la red: llamadas = %d, antes = %d", s.insertBroadcastCalls.Load(), antes)
	}
}

func TestStartRetriesWhileTheStreamIsInactive(t *testing.T) {
	s := nuevoServidorEmisiones(t)
	s.transitionRes = func(n int32, r *http.Request) (int, []byte) {
		if n < 3 {
			return 403, fixture(t, "transition_inactive.json")
		}
		return 200, []byte(`{"id":"bcast123","status":{"lifeCycleStatus":"live"}}`)
	}
	sinks, quota := cuentaCuota()
	p := proveedorEmisiones(s, youtube.Options{Quota: quota, TransitionRetry: time.Millisecond})
	ctx := context.Background()
	acct := cuentaYT()

	if err := p.StartBroadcast(ctx, acct, "tok", "bcast123"); err != nil {
		t.Fatal(err)
	}
	if s.transitionCalls.Load() != 3 {
		t.Errorf("transition calls = %d, quería 3", s.transitionCalls.Load())
	}
	if sinks[acct.ID] != 150 {
		t.Errorf("cuota = %d, quería 150", sinks[acct.ID])
	}

	s2 := nuevoServidorEmisiones(t)
	s2.transitionRes = func(int32, *http.Request) (int, []byte) { return 403, fixture(t, "transition_inactive.json") }
	p2 := proveedorEmisiones(s2, youtube.Options{TransitionRetry: time.Millisecond, TransitionDeadline: 5 * time.Millisecond})
	err := p2.StartBroadcast(ctx, acct, "tok", "bcast123")
	if err == nil || !strings.Contains(err.Error(), "no recibe la señal") {
		t.Fatalf("plazo agotado: err = %v, quería que contuviera «no recibe la señal»", err)
	}
}

func TestEndBroadcastCompletes(t *testing.T) {
	s := nuevoServidorEmisiones(t)
	s.transitionRes = func(n int32, r *http.Request) (int, []byte) {
		return 200, []byte(`{"id":"bcast123","status":{"lifeCycleStatus":"complete"}}`)
	}
	p := proveedorEmisiones(s, youtube.Options{})
	ctx := context.Background()
	acct := cuentaYT()

	if err := p.EndBroadcast(ctx, acct, "tok", "bcast123"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.lastTransitionQuery, "broadcastStatus=complete") {
		t.Errorf("transition query = %q", s.lastTransitionQuery)
	}

	// redundantTransition (ya estaba terminada) se trata como éxito, no como error.
	s2 := nuevoServidorEmisiones(t)
	s2.transitionRes = func(int32, *http.Request) (int, []byte) {
		return 403, []byte(`{"error":{"code":403,"message":"ya terminada","errors":[{"reason":"redundantTransition"}]}}`)
	}
	p2 := proveedorEmisiones(s2, youtube.Options{})
	if err := p2.EndBroadcast(ctx, acct, "tok", "bcast123"); err != nil {
		t.Errorf("redundantTransition debería tratarse como éxito: err = %v", err)
	}
}

func TestSetTitleUpdatesTheUpcomingOrActiveBroadcast(t *testing.T) {
	s := nuevoServidorEmisiones(t)
	// El list ya trae part=id,snippet: SetTitle reutiliza este snippet (con su
	// scheduledStartTime) para el update, sin un GET aparte.
	s.listRes = func(estado string) []byte {
		if estado == "upcoming" {
			return []byte(`{"items":[{"id":"bcast123","snippet":{"title":"Prueba","liveChatId":"chat456","scheduledStartTime":"2026-09-11T20:00:00Z"}}]}`)
		}
		return []byte(`{"items":[]}`)
	}
	sinks, quota := cuentaCuota()
	p := proveedorEmisiones(s, youtube.Options{Quota: quota})
	ctx := context.Background()
	acct := cuentaYT()

	if err := p.SetTitle(ctx, acct, "tok", "Nuevo"); err != nil {
		t.Fatal(err)
	}
	var cuerpo struct {
		ID      string `json:"id"`
		Snippet struct {
			Title              string `json:"title"`
			ScheduledStartTime string `json:"scheduledStartTime"`
		} `json:"snippet"`
	}
	if err := json.Unmarshal(s.lastUpdateBody, &cuerpo); err != nil {
		t.Fatal(err)
	}
	if cuerpo.ID != "bcast123" || cuerpo.Snippet.Title != "Nuevo" || cuerpo.Snippet.ScheduledStartTime != "2026-09-11T20:00:00Z" {
		t.Errorf("cuerpo del update = %+v, quería title=Nuevo y scheduledStartTime conservado", cuerpo)
	}
	// list (1, upcoming) + update (50): el reintento de SetTitle sobre la que ya
	// encontró en upcoming no vuelve a leer, así que la cuota queda en 51, no en 101.
	if sinks[acct.ID] != 51 {
		t.Errorf("cuota SetTitle = %d, quería 51", sinks[acct.ID])
	}

	// Sin nada en upcoming ni en active: ErrNoBroadcast.
	s2 := nuevoServidorEmisiones(t)
	p2 := proveedorEmisiones(s2, youtube.Options{})
	if err := p2.SetTitle(ctx, acct, "tok", "Nuevo"); !errors.Is(err, platforms.ErrNoBroadcast) {
		t.Fatalf("sin emisión: err = %v, quería ErrNoBroadcast", err)
	}

	// SetBroadcastTitle no adivina cuál emisión es (se salta el list por
	// broadcastStatus), pero sí relee su snippet por id antes de reemplazarlo: cuota
	// 51 (list 1 + update 50), no 50.
	s3 := nuevoServidorEmisiones(t)
	sinks3, quota3 := cuentaCuota()
	p3 := proveedorEmisiones(s3, youtube.Options{Quota: quota3})
	if err := p3.SetBroadcastTitle(ctx, acct, "tok", "bcast123", "Nuevo"); err != nil {
		t.Fatal(err)
	}
	var cuerpo3 struct {
		ID      string `json:"id"`
		Snippet struct {
			Title              string `json:"title"`
			ScheduledStartTime string `json:"scheduledStartTime"`
		} `json:"snippet"`
	}
	if err := json.Unmarshal(s3.lastUpdateBody, &cuerpo3); err != nil {
		t.Fatal(err)
	}
	if cuerpo3.Snippet.Title != "Nuevo" || cuerpo3.Snippet.ScheduledStartTime != "2026-09-11T20:00:00Z" {
		t.Errorf("cuerpo del update (SetBroadcastTitle) = %+v", cuerpo3)
	}
	if sinks3[acct.ID] != 51 {
		t.Errorf("cuota SetBroadcastTitle = %d, quería 51", sinks3[acct.ID])
	}
}

func TestIngestKeyWithoutBroadcastIsErrNoBroadcast(t *testing.T) {
	s := nuevoServidorEmisiones(t)
	p := proveedorEmisiones(s, youtube.Options{})
	ctx := context.Background()
	acct := cuentaYT()

	url, key, err := p.IngestKey(ctx, acct, "tok")
	if !errors.Is(err, platforms.ErrNoBroadcast) || url != "" || key != "" {
		t.Fatalf("IngestKey = (%q, %q, %v), quería (\"\", \"\", ErrNoBroadcast)", url, key, err)
	}
	if s.insertBroadcastCalls.Load()+s.insertStreamCalls.Load()+s.bindCalls.Load()+s.listCalls.Load() != 0 {
		t.Error("IngestKey sin emisión no debería tocar la red")
	}
}
