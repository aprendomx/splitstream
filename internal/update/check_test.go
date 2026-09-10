package update_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/update"
)

func TestParseVersion(t *testing.T) {
	casos := map[string]struct {
		v  [3]int
		ok bool
	}{
		"v0.10.0":       {[3]int{0, 10, 0}, true},
		"1.2.3":         {[3]int{1, 2, 3}, true},
		"v0.10.0-dirty": {[3]int{}, false},
		"v0.11.0-rc1":   {[3]int{}, false},
		"dev":           {[3]int{}, false},
		"docker":        {[3]int{}, false},
		"v1.2":          {[3]int{}, false},
		"":              {[3]int{}, false},
	}
	for in, want := range casos {
		v, ok := update.ParseVersion(in)
		if ok != want.ok || v != want.v {
			t.Errorf("ParseVersion(%q) = %v, %v; quería %v, %v", in, v, ok, want.v, want.ok)
		}
	}
}

// servidorDeReleases responde como api.github.com y cuenta las peticiones.
func servidorDeReleases(t *testing.T, cuerpo string, codigo int, llamadas *atomic.Int32, cabeceras *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		if cabeceras != nil {
			*cabeceras = r.Header.Clone()
		}
		w.WriteHeader(codigo)
		w.Write([]byte(cuerpo))
	}))
}

func TestCheckComparesAgainstTheLatestTag(t *testing.T) {
	casos := []struct {
		actual, tag string
		disponible  bool
	}{
		{"v0.9.0", "v0.10.0", true},
		{"v0.10.0", "v0.10.0", false},
		{"v0.11.0", "v0.10.0", false},
		{"v0.9.9", "v0.10.0", true},
		{"v0.9.0", "v0.10.0-rc1", false}, // una pre-release no cuenta
		{"v0.9.0", "raro", false},
	}
	for _, c := range casos {
		var n atomic.Int32
		srv := servidorDeReleases(t, `{"tag_name":"`+c.tag+`","html_url":"https://ejemplo/r/`+c.tag+`"}`, 200, &n, nil)
		chk := &update.Checker{Current: c.actual, URL: srv.URL, Client: srv.Client()}
		info, err := chk.Check(context.Background())
		srv.Close()
		if err != nil {
			t.Errorf("%s vs %s: %v", c.actual, c.tag, err)
			continue
		}
		if info.Available != c.disponible {
			t.Errorf("%s vs %s: available = %v, quería %v", c.actual, c.tag, info.Available, c.disponible)
		}
		if c.disponible && (info.Latest != c.tag || info.URL != "https://ejemplo/r/"+c.tag) {
			t.Errorf("%s vs %s: info = %+v", c.actual, c.tag, info)
		}
	}
}

func TestCheckSendsOnlyTheVersionAndNothingElse(t *testing.T) {
	var n atomic.Int32
	var h http.Header
	srv := servidorDeReleases(t, `{"tag_name":"v0.10.0","html_url":"u"}`, 200, &n, &h)
	defer srv.Close()
	chk := &update.Checker{Current: "v0.9.0", URL: srv.URL, Client: srv.Client()}
	if _, err := chk.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ua := h.Get("User-Agent"); ua != "splitstream/v0.9.0" {
		t.Errorf("User-Agent = %q", ua)
	}
	for k := range h {
		switch k {
		case "User-Agent", "Accept", "Accept-Encoding":
		default:
			t.Errorf("cabecera de más: %s (sin telemetría quiere decir sin identificadores)", k)
		}
	}
}

func TestCheckFailsSoftlyOnErrors(t *testing.T) {
	var n atomic.Int32
	for _, c := range []struct {
		cuerpo string
		codigo int
	}{
		{`{"message":"API rate limit exceeded"}`, 403},
		{`no es json`, 200},
		{`{"tag_name":123}`, 200},
	} {
		srv := servidorDeReleases(t, c.cuerpo, c.codigo, &n, nil)
		chk := &update.Checker{Current: "v0.9.0", URL: srv.URL, Client: srv.Client()}
		info, err := chk.Check(context.Background())
		srv.Close()
		if err == nil {
			t.Errorf("%d %q: Check no devolvió error", c.codigo, c.cuerpo)
		}
		if info.Available {
			t.Errorf("%d %q: un fallo no puede anunciar versión", c.codigo, c.cuerpo)
		}
	}
	// Timeout: el servidor no contesta nunca dentro del plazo.
	lento := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer lento.Close()
	chk := &update.Checker{Current: "v0.9.0", URL: lento.URL, Client: lento.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := chk.Check(ctx); err == nil {
		t.Error("un servidor colgado debería dar error, no bloquear")
	}
}

func TestRunDoesNothingForAnUnversionedBinary(t *testing.T) {
	var n atomic.Int32
	srv := servidorDeReleases(t, `{"tag_name":"v9.9.9","html_url":"u"}`, 200, &n, nil)
	defer srv.Close()
	chk := &update.Checker{Current: "dev", URL: srv.URL, Client: srv.Client(), Logger: slog.Default()}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	chk.Run(ctx, 0, time.Millisecond, func(update.Info) { t.Error("un binario dev no compara") })
	if n.Load() != 0 {
		t.Errorf("peticiones = %d, quería 0", n.Load())
	}
	if chk.Latest().Available {
		t.Error("Latest no puede anunciar nada")
	}
}

func TestRunNotifiesOncePerNewVersionAndRespectsTheContext(t *testing.T) {
	var n atomic.Int32
	srv := servidorDeReleases(t, `{"tag_name":"v0.10.0","html_url":"https://ejemplo/r/v0.10.0"}`, 200, &n, nil)
	defer srv.Close()
	chk := &update.Checker{Current: "v0.9.0", URL: srv.URL, Client: srv.Client(), Logger: slog.Default()}

	var avisos atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	hecho := make(chan struct{})
	go func() {
		chk.Run(ctx, 0, 20*time.Millisecond, func(i update.Info) {
			avisos.Add(1)
			if i.Latest != "v0.10.0" {
				t.Errorf("aviso con %+v", i)
			}
		})
		close(hecho)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for n.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-hecho:
	case <-time.After(2 * time.Second):
		t.Fatal("Run no volvió al cancelar el contexto")
	}
	if n.Load() < 3 {
		t.Fatalf("peticiones = %d, el intervalo no se respetó", n.Load())
	}
	if avisos.Load() != 1 {
		t.Errorf("avisos = %d, quería 1 (la misma versión no se anuncia dos veces)", avisos.Load())
	}
	if got := chk.Latest(); !got.Available || got.URL != "https://ejemplo/r/v0.10.0" {
		t.Errorf("Latest = %+v", got)
	}
}
