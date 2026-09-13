package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/store"
)

func TestSessionsListNewestFirst(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	ctx := context.Background()
	var ultimo int64
	for i := 0; i < 3; i++ {
		ultimo, _ = db.StartSession(ctx)
	}
	if _, err := db.LogEvent(ctx, store.Event{SessionID: &ultimo, Level: store.LevelError, Kind: "k", Message: "x"}); err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, cookies, http.MethodGet, "/api/sessions?limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	var got []sessionSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != ultimo || got[0].Events.Error != 1 {
		t.Errorf("got = %+v", got)
	}

	rec = do(t, srv, cookies, http.MethodGet, "/api/sessions?before="+itoa(got[1].ID), "")
	var resto []sessionSummaryDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &resto)
	if len(resto) != 1 {
		t.Errorf("página 2 = %d sesiones, quería 1", len(resto))
	}
}

func TestSessionsRejectsNonNumericParams(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	for _, q := range []string{"before=ayer", "before=-1", "limit=-3"} {
		rec := do(t, srv, cookies, http.MethodGet, "/api/sessions?"+q, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: código = %d, quería 400", q, rec.Code)
			continue
		}
		if got := errorCodeDe(t, rec); got != codeInvalidInput {
			t.Errorf("%s: code = %q, quería %q", q, got, codeInvalidInput)
		}
	}
}

// TestSessionDetailCarriesEventsRecordingsAndChat cubre la ficha completa de una sesión:
// eventos en orden ascendente, grabaciones con sus bytes/duración y los contadores de
// chat por plataforma.
func TestSessionDetailCarriesEventsRecordingsAndChat(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	ctx := context.Background()

	sid, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}

	dest, err := db.CreateDestination(ctx, srv.cipher, store.NewDestination{
		Name: "Canal", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Dos eventos: uno sin destino, otro con destino, para comprobar que el DTO
	// conserva ambos casos y el orden ascendente por id.
	e1, err := db.LogEvent(ctx, store.Event{SessionID: &sid, Level: store.LevelInfo, Kind: "k1", Message: "primero"})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := db.LogEvent(ctx, store.Event{SessionID: &sid, DestinationID: &dest.ID, Level: store.LevelWarn, Kind: "k2", Message: "segundo"})
	if err != nil {
		t.Fatal(err)
	}

	// Una grabación cerrada, con bytes y duración conocidos.
	if _, err := db.OpenRecording(ctx, sid, "seg-0.mp4", 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRecording(ctx, "seg-0.mp4", time.Now(), 12345, 60000); err != nil {
		t.Fatal(err)
	}

	// Tres mensajes de chat: dos de twitch, uno de kick.
	msgs := []store.NewChatMessage{
		{SessionID: sid, Platform: "twitch", AuthorID: "a1", Author: "Ana", Text: "hola", MessageID: "m1", At: time.Now()},
		{SessionID: sid, Platform: "twitch", AuthorID: "a2", Author: "Beto", Text: "hola2", MessageID: "m2", At: time.Now()},
		{SessionID: sid, Platform: "kick", AuthorID: "a3", Author: "Cova", Text: "hola3", MessageID: "m3", At: time.Now()},
	}
	if err := db.InsertChatMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}

	if err := db.FinishSession(ctx, sid, 1920, 1080, 6_000_000); err != nil {
		t.Fatal(err)
	}
	stored, err := db.SessionByID(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	wantDuration := 0
	if stored.EndedAt != nil {
		wantDuration = int(stored.EndedAt.Sub(stored.StartedAt).Seconds())
	}

	rec := do(t, srv, cookies, http.MethodGet, "/api/sessions/"+itoa(sid), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	var got sessionDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if got.ID != sid {
		t.Errorf("id = %d, quería %d", got.ID, sid)
	}
	if got.DurationS != wantDuration {
		t.Errorf("duration_s = %d, quería %d", got.DurationS, wantDuration)
	}
	if len(got.Events) != 2 || got.Events[0].ID != e1 || got.Events[1].ID != e2 {
		t.Fatalf("events = %+v, quería [%d,%d] en orden ascendente", got.Events, e1, e2)
	}
	if got.Events[0].DestinationID != nil {
		t.Errorf("events[0].destination_id = %v, quería nil", got.Events[0].DestinationID)
	}
	if got.Events[1].DestinationID == nil || *got.Events[1].DestinationID != dest.ID {
		t.Errorf("events[1].destination_id = %v, quería %d", got.Events[1].DestinationID, dest.ID)
	}
	if len(got.Recordings) != 1 || got.Recordings[0].Bytes != 12345 || got.Recordings[0].DurationMS != 60000 {
		t.Errorf("recordings = %+v", got.Recordings)
	}
	if got.ChatCount != 3 {
		t.Errorf("chat_count = %d, quería 3", got.ChatCount)
	}
	if got.ChatByPlatform["twitch"] != 2 || got.ChatByPlatform["kick"] != 1 {
		t.Errorf("chat_by_platform = %+v", got.ChatByPlatform)
	}
	if got.EventsTruncated || got.RecordingsTruncated {
		t.Errorf("una sesión de dos eventos y una grabación no está truncada: events_truncated=%v recordings_truncated=%v",
			got.EventsTruncated, got.RecordingsTruncated)
	}
}

// TestSessionDetailAvisaCuandoTrunca: traer justo el tope es la única señal de que faltan
// filas, y la ficha tiene que decirlo. Se prueba sobre el constructor del DTO porque
// sembrar 2000 eventos en la base solo para contarlos no aporta nada.
func TestSessionDetailAvisaCuandoTrunca(t *testing.T) {
	ses := store.Session{ID: 1, StartedAt: time.Now()}
	eventos := make([]store.Event, store.EventsBySessionLimit)
	grabaciones := make([]store.Recording, recordingsPorSesionLimit)

	lleno := newSessionDetailDTO(ses, eventos, grabaciones, 0, nil)
	if !lleno.EventsTruncated {
		t.Errorf("con %d eventos (el tope) events_truncated debería ser true", len(eventos))
	}
	if !lleno.RecordingsTruncated {
		t.Errorf("con %d grabaciones (el tope) recordings_truncated debería ser true", len(grabaciones))
	}

	corto := newSessionDetailDTO(ses, eventos[:len(eventos)-1], grabaciones[:len(grabaciones)-1], 0, nil)
	if corto.EventsTruncated || corto.RecordingsTruncated {
		t.Errorf("por debajo del tope no hay truncado: events=%v recordings=%v",
			corto.EventsTruncated, corto.RecordingsTruncated)
	}
}

// TestSessionDetailNotFound cubre las dos formas de "no vale": un id que no existe (404)
// y un id que ni siquiera es un número (400).
func TestSessionDetailNotFound(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := do(t, srv, cookies, http.MethodGet, "/api/sessions/999999", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("id inexistente: código = %d, quería 404", rec.Code)
	}
	if got := errorCodeDe(t, rec); got != codeNotFound {
		t.Errorf("id inexistente: code = %q, quería %q", got, codeNotFound)
	}

	rec = do(t, srv, cookies, http.MethodGet, "/api/sessions/no-numerico", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("id no numérico: código = %d, quería 400", rec.Code)
	}
	if got := errorCodeDe(t, rec); got != codeInvalidInput {
		t.Errorf("id no numérico: code = %q, quería %q", got, codeInvalidInput)
	}
}

// TestSessionsListCarriesHasRecording comprueba que el listado distingue una sesión con
// grabación de una sin ella.
func TestSessionsListCarriesHasRecording(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	ctx := context.Background()

	conGrabacion, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenRecording(ctx, conGrabacion, "seg-0.mp4", 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	sinGrabacion, err := db.StartSession(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, srv, cookies, http.MethodGet, "/api/sessions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	var got []sessionSummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	porID := map[int64]bool{}
	for _, s := range got {
		porID[s.ID] = s.HasRecording
	}
	if !porID[conGrabacion] {
		t.Errorf("sesión con grabación: has_recording = false, quería true")
	}
	if porID[sinGrabacion] {
		t.Errorf("sesión sin grabación: has_recording = true, quería false")
	}
}
