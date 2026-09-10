package webtls_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/config"
	"github.com/aprendomx/splitstream/internal/webtls"
)

// certificadoDePrueba escribe un certificado autofirmado para 127.0.0.1 y devuelve las
// rutas y el pool con el que un cliente puede validarlo.
func certificadoDePrueba(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "splitstream-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "relay.crt")
	keyFile = filepath.Join(dir, "relay.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

func TestBuildReturnsNothingWithoutTLS(t *testing.T) {
	s, err := webtls.Build(&config.Config{HTTPAddr: ":8080"}, nil)
	if err != nil || s != nil {
		t.Fatalf("Build = %v, %v; sin TLS quería nil, nil", s, err)
	}
}

func TestOwnCertificateServesHTTPS(t *testing.T) {
	certFile, keyFile, pool := certificadoDePrueba(t)
	s, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSCertFile: certFile, TLSKeyFile: keyFile}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.PublicURL != "" {
		t.Errorf("PublicURL = %q; con certificado propio no se conoce el nombre público", s.PublicURL)
	}
	if s.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, quería TLS 1.2", s.TLSConfig.MinVersion)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hola"))
	}))
	srv.TLS = s.TLSConfig
	srv.StartTLS()
	defer srv.Close()

	cliente := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := cliente.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET por HTTPS con nuestro certificado: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("código = %d", resp.StatusCode)
	}
}

func TestOwnCertificateRejectsABrokenPair(t *testing.T) {
	certFile, _, _ := certificadoDePrueba(t)
	_, keyFile, _ := certificadoDePrueba(t)
	if _, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSCertFile: certFile, TLSKeyFile: keyFile}, nil); err == nil {
		t.Fatal("Build aceptó un certificado con la clave de otro")
	}
}

func TestRedirectSendsToHTTPSKeepingPathAndQuery(t *testing.T) {
	certFile, keyFile, _ := certificadoDePrueba(t)
	casos := []struct{ addr, host, want string }{
		{":443", "relay.ejemplo.com", "https://relay.ejemplo.com/api/status?x=1"},
		{":443", "relay.ejemplo.com:80", "https://relay.ejemplo.com/api/status?x=1"},
		{":8443", "relay.ejemplo.com", "https://relay.ejemplo.com:8443/api/status?x=1"},
		// IPv6 sin puerto: los corchetes no se duplican y se conservan sin puerto.
		{":8443", "[::1]", "https://[::1]:8443/api/status?x=1"},
		{":443", "[::1]", "https://[::1]/api/status?x=1"},
		{":443", "[::1]:80", "https://[::1]/api/status?x=1"},
	}
	for _, c := range casos {
		s, err := webtls.Build(&config.Config{HTTPAddr: c.addr, TLSCertFile: certFile, TLSKeyFile: keyFile}, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		r := httptest.NewRequest(http.MethodGet, "http://"+c.host+"/api/status?x=1", nil)
		r.Host = c.host
		w := httptest.NewRecorder()
		s.Redirect.ServeHTTP(w, r)
		if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != c.want {
			t.Errorf("addr %s host %s: %d %q, quería 301 %q", c.addr, c.host, w.Code, w.Header().Get("Location"), c.want)
		}
	}
}

// Con dominio no se habla con Let's Encrypt en el test: se comprueba la forma del
// TLSConfig (GetCertificate, ALPN del reto) y que el listener de redirección sigue
// redirigiendo lo que no es un reto.
func TestDomainConfiguresAutocertWithoutTalkingToAnyone(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "tls-cache")
	s, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSDomain: "relay.ejemplo.com", TLSCacheDir: cache}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if s.PublicURL != "https://relay.ejemplo.com" {
		t.Errorf("PublicURL = %q", s.PublicURL)
	}
	if s.TLSConfig.GetCertificate == nil {
		t.Fatal("con dominio, GetCertificate debe venir de autocert")
	}
	if !slices.Contains(s.TLSConfig.NextProtos, "acme-tls/1") {
		t.Errorf("NextProtos = %v, falta acme-tls/1 (reto TLS-ALPN-01)", s.TLSConfig.NextProtos)
	}
	r := httptest.NewRequest(http.MethodGet, "http://relay.ejemplo.com/panel", nil)
	r.Host = "relay.ejemplo.com"
	w := httptest.NewRecorder()
	s.Redirect.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "https://relay.ejemplo.com/panel" {
		t.Errorf("redirección: %d %q", w.Code, w.Header().Get("Location"))
	}
}

// Una caché no escribible tiene que abortar el arranque: autocert la trata como opcional
// y sin ella pediría certificado en cada arranque, con el cupo de 5 por semana de por
// medio. Se apunta a un archivo regular, que no es un directorio y nunca lo será.
func TestUnwritableCacheDirFailsTheBuild(t *testing.T) {
	archivo := filepath.Join(t.TempDir(), "no-soy-un-directorio")
	if err := os.WriteFile(archivo, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSDomain: "relay.ejemplo.com",
		TLSCacheDir: archivo}, nil)
	if err == nil {
		t.Fatal("Build con una caché no escribible = nil, quería error")
	}
	if !strings.Contains(err.Error(), "SPLITSTREAM_TLS_CACHE_DIR") {
		t.Errorf("el error no dice qué variable revisar: %v", err)
	}
}

// El caso normal: el directorio no existe todavía y Build lo crea.
func TestCacheDirIsCreatedWhenMissing(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "nuevo", "tls-cache")
	if _, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSDomain: "relay.ejemplo.com",
		TLSCacheDir: cache}, nil); err != nil {
		t.Fatalf("Build: %v", err)
	}
	info, err := os.Stat(cache)
	if err != nil {
		t.Fatalf("la caché no se creó: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%s no es un directorio", cache)
	}
	// La sonda no deja rastro.
	entradas, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entradas) != 0 {
		t.Errorf("la sonda dejó archivos en la caché: %v", entradas)
	}
}

// Un ClientHello para otro nombre no debe avisar (los escáneres lo hacen todo el día); un
// fallo del propio dominio sí, y no más de una vez cada diez minutos.
//
// DESVIACIÓN respecto al brief: no se llama a GetCertificate con el propio dominio desde
// este test. autocert.Manager.GetCertificate, al recibir un ClientHello para el dominio
// permitido, sale a la red real de Let's Encrypt (intenta obtener o renovar el
// certificado, incluido el registro de cuenta ACME) y eso está prohibido en un test. En
// su lugar: (a) aquí solo se comprueba que un nombre ajeno falla sin avisar —
// HostWhitelist lo rechaza sin tocar la red—, y (b) la cadencia del aviso (una vez cada
// diez minutos) se prueba por separado, en TestAvisadorLimitaLaCadencia, contra el
// avisador exportado en export_test.go con un reloj falso.
func TestCertificateErrorsAreReportedOncePerTenMinutesForOurDomainOnly(t *testing.T) {
	var avisos []error
	s, err := webtls.Build(&config.Config{HTTPAddr: ":443", TLSDomain: "relay.ejemplo.com",
		TLSCacheDir: filepath.Join(t.TempDir(), "c")}, func(err error) { avisos = append(avisos, err) })
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// autocert.HostWhitelist rechaza cualquier otro nombre sin salir a la red.
	if _, err := s.TLSConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: "otro.ejemplo.com"}); err == nil {
		t.Fatal("otro nombre debería fallar")
	}
	if len(avisos) != 0 {
		t.Fatalf("un nombre ajeno avisó: %v", avisos)
	}
}

// Prueba la cadencia del avisador en aislamiento, con un reloj falso, sin pasar por
// autocert ni por la red. Ver la nota de desviación en el test anterior.
func TestAvisadorLimitaLaCadencia(t *testing.T) {
	ahora := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	reloj := func() time.Time { return ahora }

	var avisos []error
	avisar := webtls.Avisador(func(err error) { avisos = append(avisos, err) }, reloj)

	errACME := errors.New("acme: fallo simulado")
	avisar(errACME)
	avisar(errACME)
	if len(avisos) != 1 {
		t.Fatalf("avisos tras dos fallos seguidos = %d, quería 1", len(avisos))
	}
	if errors.Is(avisos[0], nil) || avisos[0].Error() == "" {
		t.Error("el aviso debería llevar el error de ACME")
	}

	ahora = ahora.Add(webtls.AvisoCadaMax)
	avisar(errACME)
	if len(avisos) != 2 {
		t.Fatalf("avisos tras 10 minutos = %d, quería 2", len(avisos))
	}
}
