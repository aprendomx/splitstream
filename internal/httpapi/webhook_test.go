package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// ingestaFalsa apunta lo que le llega del webhook. Con candado porque el handler escribe
// desde la goroutine de la petición.
type ingestaFalsa struct {
	mu       sync.Mutex
	llamadas int
	msgs     []platforms.ChatMessage
}

func (i *ingestaFalsa) Ingest(msgs []platforms.ChatMessage) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.llamadas++
	i.msgs = append(i.msgs, msgs...)
	return len(msgs)
}

func (i *ingestaFalsa) leer() (int, []platforms.ChatMessage) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.llamadas, append([]platforms.ChatMessage(nil), i.msgs...)
}

// servidorWebhook levanta el servidor con Kick por webhook y la ingesta de mentira.
func servidorWebhook(t *testing.T) (*Server, *store.DB, *fakeProvider, *ingestaFalsa) {
	t.Helper()
	p := &fakeProvider{configured: true, id: platforms.Kick, webhook: true}
	ing := &ingestaFalsa{}
	srv, db := newTestServer(t, func(c *Config) {
		c.Platforms = platforms.NewRegistry(envolverProveedor(p))
		c.Tokens = tokensFalsos{db: c.DB, c: c.Cipher}
		c.ChatIngest = ing.Ingest
		c.TLS, c.PublicURL = true, "https://relay.ejemplo.com"
	})
	return srv, db, p, ing
}

// webhook manda una entrega SIN cookie: quien llama es la plataforma, que no tiene sesión.
func webhook(t *testing.T, srv *Server, firma, cuerpo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/platforms/kick/webhook", strings.NewReader(cuerpo))
	r.Header.Set("Content-Type", "application/json")
	if firma != "" {
		r.Header.Set("X-Test-Signature", firma)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

// Un mensaje bien firmado entra en el bus con la cuenta resuelta por el id del canal: la
// plataforma no dice a qué cuenta nuestra pertenece, solo de qué canal es.
func TestWebhookIngestsChatForTheAccountOfTheBroadcaster(t *testing.T) {
	srv, db, _, ing := servidorWebhook(t)
	a := cuentaDePlataforma(t, srv, db, store.PlatformKick, "123", "kickdev")

	rec := webhook(t, srv, "ok", `{"evento":"chat"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook = %d %s", rec.Code, rec.Body)
	}
	llamadas, msgs := ing.leer()
	if llamadas != 1 || len(msgs) != 1 {
		t.Fatalf("ingesta = %d llamadas, %d mensajes", llamadas, len(msgs))
	}
	if msgs[0].AccountID != a.ID || msgs[0].Text != "hola" {
		t.Errorf("mensaje = %+v", msgs[0])
	}
	// La respuesta no cuenta nada de lo que pasó dentro.
	if strings.Contains(rec.Body.String(), "kickdev") || strings.Contains(rec.Body.String(), "tok-") {
		t.Errorf("la respuesta cuenta demasiado: %s", rec.Body)
	}
}

// Una firma que no vale es 401 —la plataforma lo necesita para dejar de reintentar— y deja
// constancia, pero como mucho una vez por minuto: si no, un bucle llenaría el registro.
func TestWebhookRejectsBadSignaturesAndLogsOnce(t *testing.T) {
	srv, db, _, ing := servidorWebhook(t)
	cuentaDePlataforma(t, srv, db, store.PlatformKick, "123", "kickdev")

	for i := 0; i < 2; i++ {
		if rec := webhook(t, srv, "bad", `{}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("entrega %d = %d, quería 401", i, rec.Code)
		}
	}
	if llamadas, _ := ing.leer(); llamadas != 0 {
		t.Errorf("se ingirió algo de una entrega rechazada: %d", llamadas)
	}
	if n := eventosPorTipo(t, db)["webhook_rejected"]; n != 1 {
		t.Errorf("eventos webhook_rejected = %d, quería 1", n)
	}
}

// Un evento que no es de chat, una entrega de un canal que no es nuestro y un cuerpo
// enorme: los tres se atienden sin contar nada y sin tocar el bus.
func TestWebhookIgnoresWhatItCannotUse(t *testing.T) {
	srv, db, _, ing := servidorWebhook(t)
	cuentaDePlataforma(t, srv, db, store.PlatformKick, "123", "kickdev")

	if rec := webhook(t, srv, "ignore", `{"evento":"seguidor"}`); rec.Code != http.StatusOK {
		t.Errorf("evento ignorado = %d, quería 200", rec.Code)
	}
	if llamadas, _ := ing.leer(); llamadas != 0 {
		t.Errorf("un evento que no es de chat llegó al bus: %d", llamadas)
	}

	// Cuerpo por encima del tope: 413 y ni se parsea.
	grande := strings.Repeat("a", maxWebhookBody+10)
	if rec := webhook(t, srv, "ok", grande); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("cuerpo enorme = %d, quería 413", rec.Code)
	}
	if llamadas, _ := ing.leer(); llamadas != 0 {
		t.Errorf("un cuerpo enorme llegó al bus: %d", llamadas)
	}

	// Un canal del que no tenemos cuenta: 200, y nada entra.
	srv2, _, _, ing2 := servidorWebhook(t)
	if rec := webhook(t, srv2, "ok", `{}`); rec.Code != http.StatusOK {
		t.Errorf("canal desconocido = %d, quería 200", rec.Code)
	}
	if llamadas, _ := ing2.leer(); llamadas != 0 {
		t.Errorf("un canal sin cuenta llegó al bus: %d", llamadas)
	}
}

// Sin proveedor de Kick no hay webhook que atender.
func TestWebhookWithoutProviderIsNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	if rec := webhook(t, srv, "ok", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("sin proveedor = %d, quería 404", rec.Code)
	}
}
