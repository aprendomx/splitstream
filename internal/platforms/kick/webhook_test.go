package kick_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/platforms/kick"
)

// reloj es el tiempo inyectado: los tests de la clave pública necesitan envejecer la
// copia cacheada sin dormir.
type reloj struct {
	mu sync.Mutex
	t  time.Time
}

func (r *reloj) ahora() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.t
}

func (r *reloj) avanzar(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.t = r.t.Add(d)
}

// nuevaClave genera un par RSA 2048 como el que Kick usa para firmar los webhooks.
func nuevaClave(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// pemPKIX serializa la clave pública como la publica Kick: PKIX en PEM.
func pemPKIX(t *testing.T, k *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// firmar reproduce la firma de Kick: RSA-SHA256 PKCS#1 v1.5 sobre
// "message_id.timestamp.cuerpo", en base64 estándar.
func firmar(t *testing.T, k *rsa.PrivateKey, id, ts string, body []byte) string {
	t.Helper()
	sum := sha256.Sum256([]byte(id + "." + ts + "." + string(body)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

// cabeceras arma las cinco cabeceras de un webhook de Kick firmado con k.
func cabeceras(t *testing.T, k *rsa.PrivateKey, id string, ts time.Time, body []byte) http.Header {
	t.Helper()
	marca := ts.UTC().Format(time.RFC3339)
	h := http.Header{}
	h.Set("Kick-Event-Message-Id", id)
	h.Set("Kick-Event-Message-Timestamp", marca)
	h.Set("Kick-Event-Type", "chat.message.sent")
	h.Set("Kick-Event-Version", "1")
	h.Set("Kick-Event-Signature", firmar(t, k, id, marca, body))
	return h
}

// servidorWebhook simula api.kick.com para el webhook: la clave pública (contando las
// peticiones), el alta, la lista y la baja de suscripciones.
type servidorWebhook struct {
	*httptest.Server
	mu       sync.Mutex
	priv     *rsa.PrivateKey
	pub      string
	altas    []map[string]any
	auth     []string
	borrados []string
	// altaError, si no está vacío, es el error que Kick devuelve dentro del data[] del
	// alta (la petición sale 200 igual).
	altaError    string
	clavePedidas atomic.Int32
}

func nuevoServidorWebhook(t *testing.T) *servidorWebhook {
	t.Helper()
	s := &servidorWebhook{priv: nuevaClave(t)}
	s.pub = pemPKIX(t, s.priv)
	subs := fixture(t, "subscriptions.json")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /public/v1/public-key", func(w http.ResponseWriter, r *http.Request) {
		s.clavePedidas.Add(1)
		s.mu.Lock()
		pub := s.pub
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"public_key": pub}})
	})
	mux.HandleFunc("POST /public/v1/events/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		var cuerpo map[string]any
		if err := json.NewDecoder(r.Body).Decode(&cuerpo); err != nil {
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"cuerpo ilegible"}`))
			return
		}
		s.mu.Lock()
		s.altas = append(s.altas, cuerpo)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		fallo := s.altaError
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"name": "chat.message.sent", "version": 1,
			"subscription_id": "01HZQ3T5V0SUBSCRIPTION001", "error": fallo,
		}}})
	})
	mux.HandleFunc("GET /public/v1/events/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		w.Write(subs)
	})
	mux.HandleFunc("DELETE /public/v1/events/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.borrados = append(s.borrados, r.URL.Query()["id"]...)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// rotar cambia la clave que el servidor publica, como haría Kick.
func (s *servidorWebhook) rotar(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k := nuevaClave(t)
	pub := pemPKIX(t, k)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.priv, s.pub = k, pub
	return k
}

func (s *servidorWebhook) clave() *rsa.PrivateKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.priv
}

// proveedorWebhook arma el proveedor contra el servidor falso con el reloj inyectado.
func proveedorWebhook(s *servidorWebhook, r *reloj) *kick.Provider {
	return kick.New(kick.Options{HTTPClient: s.Client(), AuthBase: s.URL, APIBase: s.URL, Now: r.ahora})
}

func nuevoReloj() *reloj {
	return &reloj{t: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)}
}

func TestParseWebhookAcceptsAValidSignature(t *testing.T) {
	s := nuevoServidorWebhook(t)
	r := nuevoReloj()
	p := proveedorWebhook(s, r)
	body := fixture(t, "chat_message.json")

	msgs, err := p.ParseWebhook(cabeceras(t, s.clave(), "01HZEVENTO0000000000000001", r.ahora(), body), body)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("mensajes = %d, quería 1", len(msgs))
	}
	m := msgs[0]
	if m.Platform != platforms.Kick {
		t.Errorf("platform = %q", m.Platform)
	}
	// AccountID queda en cero a propósito: la cuenta la resuelve quien llama, por
	// BroadcasterID.
	if m.AccountID != 0 {
		t.Errorf("account id = %d, quería 0", m.AccountID)
	}
	if m.AuthorID != "987654321" || m.Author != "sender_name" {
		t.Errorf("autor = %s / %s", m.AuthorID, m.Author)
	}
	if m.Text != "Hello [emote:4148074:HYPERCLAP]" {
		t.Errorf("texto = %q, quería el emote tal cual", m.Text)
	}
	if m.Color != "#FF5733" {
		t.Errorf("color = %q", m.Color)
	}
	if quiero := []string{"moderator", "sub_gifter", "subscriber"}; !slices.Equal(m.Badges, quiero) {
		t.Errorf("badges = %v, quería %v", m.Badges, quiero)
	}
	if m.MessageID != "01HZQ3T5V0MESSAGE00000001" {
		t.Errorf("message id = %q", m.MessageID)
	}
	if quiero := time.Date(2026, 9, 11, 11, 59, 40, 0, time.UTC); !m.At.Equal(quiero) {
		t.Errorf("at = %s, quería %s", m.At, quiero)
	}
	if m.BroadcasterID != "123" {
		t.Errorf("broadcaster = %q, quería el canal del evento", m.BroadcasterID)
	}
}

func TestParseWebhookRejectsBadSignatureStaleTimestampAndReplay(t *testing.T) {
	s := nuevoServidorWebhook(t)
	r := nuevoReloj()
	p := proveedorWebhook(s, r)
	body := fixture(t, "chat_message.json")

	// Firma alterada: la de otro message_id, válida en formato pero no para este evento.
	h := cabeceras(t, s.clave(), "01HZEVENTO0000000000000002", r.ahora(), body)
	firmaAjena := firmar(t, s.clave(), "otro-id", h.Get("Kick-Event-Message-Timestamp"), body)
	h.Set("Kick-Event-Signature", firmaAjena)
	msgs, err := p.ParseWebhook(h, body)
	exigirRechazo(t, msgs, err, "firma")
	if strings.Contains(err.Error(), firmaAjena) || strings.Contains(err.Error(), "sender_name") {
		t.Errorf("el error filtra la firma o el cuerpo: %v", err)
	}

	// Marca de tiempo de hace 6 minutos: fuera de la ventana de 5.
	vieja := cabeceras(t, s.clave(), "01HZEVENTO0000000000000003", r.ahora().Add(-6*time.Minute), body)
	msgs, err = p.ParseWebhook(vieja, body)
	exigirRechazo(t, msgs, err, "antigüedad")

	// El mismo message_id dos veces: la segunda es repetición.
	repetida := cabeceras(t, s.clave(), "01HZEVENTO0000000000000004", r.ahora(), body)
	if _, err := p.ParseWebhook(repetida, body); err != nil {
		t.Fatalf("el primero tenía que pasar: %v", err)
	}
	msgs, err = p.ParseWebhook(repetida, body)
	exigirRechazo(t, msgs, err, "repetido")

	// Otro tipo de evento: se ignora, no se rechaza.
	otro := cabeceras(t, s.clave(), "01HZEVENTO0000000000000005", r.ahora(), body)
	otro.Set("Kick-Event-Type", "channel.followed")
	msgs, err = p.ParseWebhook(otro, body)
	if err != nil || msgs != nil {
		t.Errorf("un evento ajeno = (%v, %v), quería (nil, nil)", msgs, err)
	}
	// Y una versión que no conocemos, igual.
	v2 := cabeceras(t, s.clave(), "01HZEVENTO0000000000000006", r.ahora(), body)
	v2.Set("Kick-Event-Version", "2")
	msgs, err = p.ParseWebhook(v2, body)
	if err != nil || msgs != nil {
		t.Errorf("una versión ajena = (%v, %v), quería (nil, nil)", msgs, err)
	}

	// Cuerpo de más de 256 KiB: se rechaza sin mirar nada más.
	grande := make([]byte, 256<<10+1)
	msgs, err = p.ParseWebhook(cabeceras(t, s.clave(), "01HZEVENTO0000000000000007", r.ahora(), grande), grande)
	exigirRechazo(t, msgs, err, "tamaño")

	// Sin cabeceras no hay nada que verificar.
	msgs, err = p.ParseWebhook(http.Header{}, body)
	exigirRechazo(t, msgs, err, "cabeceras")
}

// exigirRechazo comprueba que el error es un ErrWebhookRejected con el motivo esperado y
// que no se devolvió ningún mensaje.
func exigirRechazo(t *testing.T, msgs []platforms.ChatMessage, err error, motivo string) {
	t.Helper()
	if !errors.Is(err, platforms.ErrWebhookRejected) {
		t.Fatalf("err = %v, quería ErrWebhookRejected (%s)", err, motivo)
	}
	if !strings.Contains(err.Error(), motivo) {
		t.Errorf("err = %q, quería el motivo %q", err, motivo)
	}
	if msgs != nil {
		t.Errorf("un rechazo devolvió %d mensajes", len(msgs))
	}
}

func TestPublicKeyIsCachedAndRefreshedOnce(t *testing.T) {
	s := nuevoServidorWebhook(t)
	r := nuevoReloj()
	p := proveedorWebhook(s, r)
	body := fixture(t, "chat_message.json")

	// Dos webhooks válidos: la clave se pide una sola vez.
	for _, id := range []string{"01HZCACHE000000000000001", "01HZCACHE000000000000002"} {
		if _, err := p.ParseWebhook(cabeceras(t, s.clave(), id, r.ahora(), body), body); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if n := s.clavePedidas.Load(); n != 1 {
		t.Fatalf("peticiones de clave = %d, quería 1", n)
	}

	// Kick rota la clave: la cacheada ya no verifica, se refresca una vez y el webhook
	// acaba aceptándose.
	nueva := s.rotar(t)
	r.avanzar(2 * time.Minute)
	h := cabeceras(t, nueva, "01HZCACHE000000000000003", r.ahora(), body)
	if _, err := p.ParseWebhook(h, body); err != nil {
		t.Fatalf("tras rotar la clave: %v", err)
	}
	if n := s.clavePedidas.Load(); n != 2 {
		t.Fatalf("peticiones de clave = %d, quería 2 (un solo refresco)", n)
	}

	// Una firma de una clave que no es la de Kick: se rechaza y, como mucho, se refresca
	// una vez más. Nunca un bucle.
	r.avanzar(2 * time.Minute)
	ajena := nuevaClave(t)
	h = cabeceras(t, ajena, "01HZCACHE000000000000004", r.ahora(), body)
	msgs, err := p.ParseWebhook(h, body)
	exigirRechazo(t, msgs, err, "firma")
	if n := s.clavePedidas.Load(); n > 3 {
		t.Fatalf("peticiones de clave = %d, quería 3 como máximo", n)
	}

	// Y otra firma ajena seguida, con la clave recién refrescada, no vuelve a pedirla.
	antes := s.clavePedidas.Load()
	h = cabeceras(t, ajena, "01HZCACHE000000000000005", r.ahora(), body)
	msgs, err = p.ParseWebhook(h, body)
	exigirRechazo(t, msgs, err, "firma")
	if n := s.clavePedidas.Load(); n != antes {
		t.Errorf("peticiones de clave = %d, quería seguir en %d: la cacheada es reciente", n, antes)
	}
}

func TestParseWebhookIsSafeUnderConcurrency(t *testing.T) {
	s := nuevoServidorWebhook(t)
	r := nuevoReloj()
	p := proveedorWebhook(s, r)
	body := fixture(t, "chat_message.json")

	// Las cabeceras se firman aquí, en la goroutine del test: firmar puede fallar el
	// test y eso no se puede hacer desde otra goroutine.
	const n = 8
	hdrs := make([]http.Header, n)
	for i := range n {
		hdrs[i] = cabeceras(t, s.clave(), "01HZPARALELO000000000"+string(rune('a'+i)), r.ahora(), body)
	}

	var aceptados atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msgs, err := p.ParseWebhook(hdrs[i], body)
			if err != nil {
				t.Errorf("webhook %d: %v", i, err)
				return
			}
			aceptados.Add(int32(len(msgs)))
		}()
	}
	wg.Wait()
	if aceptados.Load() != n {
		t.Errorf("aceptados = %d, quería %d", aceptados.Load(), n)
	}
	if c := s.clavePedidas.Load(); c != 1 {
		t.Errorf("peticiones de clave = %d, quería 1: la cacheada se comparte", c)
	}
}

func TestSubscribeAndUnsubscribeChat(t *testing.T) {
	s := nuevoServidorWebhook(t)
	r := nuevoReloj()
	p := proveedorWebhook(s, r)
	ctx := context.Background()

	if err := p.SubscribeChat(ctx, cuenta(), "kick-acceso-fixture", "https://panel.example/api/platforms/kick/webhook"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	altas, auth := slices.Clone(s.altas), slices.Clone(s.auth)
	s.mu.Unlock()
	if len(altas) != 1 {
		t.Fatalf("altas = %d, quería 1", len(altas))
	}
	if auth[0] != "Bearer kick-acceso-fixture" {
		t.Errorf("authorization = %q", auth[0])
	}
	if altas[0]["method"] != "webhook" {
		t.Errorf("method = %v, quería webhook", altas[0]["method"])
	}
	// broadcaster_user_id: sin él Kick no sabe a qué canal suscribir.
	if n, ok := altas[0]["broadcaster_user_id"].(float64); !ok || int64(n) != 123 {
		t.Errorf("broadcaster_user_id = %v, quería 123", altas[0]["broadcaster_user_id"])
	}
	eventos, _ := altas[0]["events"].([]any)
	if len(eventos) != 1 {
		t.Fatalf("events = %v, quería uno", altas[0]["events"])
	}
	ev, _ := eventos[0].(map[string]any)
	if ev["name"] != "chat.message.sent" || ev["version"] != float64(1) {
		t.Errorf("evento = %v, quería chat.message.sent v1", ev)
	}

	if err := p.UnsubscribeChat(ctx, cuenta(), "kick-acceso-fixture"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	borrados := slices.Clone(s.borrados)
	s.mu.Unlock()
	// Solo la del chat de ESTE canal: ni la del canal 456 ni la de channel.followed.
	if quiero := []string{"01HZQ3T5V0SUBSCRIPTION001"}; !slices.Equal(borrados, quiero) {
		t.Errorf("borrados = %v, quería %v", borrados, quiero)
	}
}

func TestSubscribeChatReportsTheErrorOfKick(t *testing.T) {
	s := nuevoServidorWebhook(t)
	s.mu.Lock()
	s.altaError = "el webhook de la app no está configurado"
	s.mu.Unlock()
	p := proveedorWebhook(s, nuevoReloj())

	err := p.SubscribeChat(context.Background(), cuenta(), "kick-acceso-fixture", "https://panel.example/hook")
	if err == nil || !strings.Contains(err.Error(), "el webhook de la app no está configurado") {
		t.Fatalf("err = %v, quería el error que dio Kick", err)
	}
}
