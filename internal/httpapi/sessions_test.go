package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

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
