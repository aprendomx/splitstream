package httpapi

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/relay"
)

// parseMetrics lee la exposición de Prometheus lo justo para afirmar sobre ella: una
// muestra por línea, `nombre{etiquetas} valor`.
func parseMetrics(t *testing.T, body string) map[string]string {
	t.Helper()
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		linea := sc.Text()
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}
		i := strings.LastIndex(linea, " ")
		if i < 0 {
			t.Fatalf("línea sin valor: %q", linea)
		}
		out[linea[:i]] = linea[i+1:]
	}
	return out
}

func getMetrics(t *testing.T, srv *Server, cookies []*http.Cookie, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

func TestMetricsRequiresSessionOrToken(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.metricsToken = "secreto"

	if rec := getMetrics(t, srv, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("sin nada: %d, quería 401", rec.Code)
	}
	if rec := getMetrics(t, srv, nil, "otro"); rec.Code != http.StatusUnauthorized {
		t.Errorf("token malo: %d, quería 401", rec.Code)
	}
	if rec := getMetrics(t, srv, nil, "secreto"); rec.Code != http.StatusOK {
		t.Errorf("token bueno: %d, quería 200", rec.Code)
	}
	if rec := getMetrics(t, srv, cookies, ""); rec.Code != http.StatusOK {
		t.Errorf("cookie: %d, quería 200", rec.Code)
	}
}

// Sin token configurado, un Bearer cualquiera no vale: solo la cookie.
func TestMetricsWithoutATokenOnlyAcceptsTheCookie(t *testing.T) {
	srv, _, _, _, _ := newDestServer(t)
	srv.metricsToken = ""
	if rec := getMetrics(t, srv, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("Bearer vacío sin token configurado: %d, quería 401", rec.Code)
	}
}

func TestMetricsExposeSessionAndDestinations(t *testing.T) {
	srv, db, eng, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, `canal "raro"`, "k", true)
	eng.setSesion(relay.LiveSession{ID: 7, BitrateBPS: 3_000_000})
	eng.setMetrics(map[int64]relay.Metrics{d.ID: {
		State: "live", Degraded: true, BytesSent: 1234, BitrateBPS: 2_900_000,
		DroppedFrames: 5, Reconnections: 2, QueuedBytes: 99,
	}})

	rec := getMetrics(t, srv, cookies, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	m := parseMetrics(t, rec.Body.String())

	if m["splitstream_session_live"] != "1" {
		t.Errorf("session_live = %q", m["splitstream_session_live"])
	}
	if m["splitstream_session_bitrate_bps"] != "3000000" {
		t.Errorf("session_bitrate = %q", m["splitstream_session_bitrate_bps"])
	}
	// Las etiquetas con comillas se escapan; el nombre lo escribe el usuario.
	base := `destination="` + itoa(d.ID) + `",name="canal \"raro\"",platform="custom"`
	if m[`splitstream_destination_state{`+base+`,state="live"}`] != "1" {
		t.Errorf("falta la serie live=1; claves: %v", claves(m))
	}
	if m[`splitstream_destination_state{`+base+`,state="idle"}`] != "0" {
		t.Error("falta la serie idle=0")
	}
	if m[`splitstream_destination_bytes_sent_total{`+base+`}`] != "1234" {
		t.Error("bytes_sent_total mal")
	}
	if m[`splitstream_destination_degraded{`+base+`}`] != "1" {
		t.Error("degraded mal")
	}
}

func TestMetricsIncludeExtras(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)
	srv.extra = []ExtraMetrics{func() []Metric {
		return []Metric{{Name: "splitstream_events_bus_dropped_total", Help: "x", Type: "counter", Value: 3}}
	}}
	m := parseMetrics(t, getMetrics(t, srv, cookies, "").Body.String())
	if m["splitstream_events_bus_dropped_total"] != "3" {
		t.Errorf("extra = %q", m["splitstream_events_bus_dropped_total"])
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func claves(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
