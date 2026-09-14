// Package webtls construye el TLS que el propio binario termina: con Let's Encrypt para
// un dominio (autocert, que ya viene en el módulo x/crypto del proyecto) o con un
// certificado propio. Es un paquete aparte para que httpapi siga sin saber nada del
// transporte y para que main.go solo tenga que enchufar un *tls.Config y un handler.
package webtls

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/aprendomx/splitstream/internal/config"
)

// Setup es lo que main.go necesita: la configuración TLS del servidor del panel, el
// handler del listener HTTP (reto HTTP-01 más redirección) y la URL pública si se sabe.
type Setup struct {
	TLSConfig *tls.Config
	Redirect  http.Handler
	// PublicURL es "https://<dominio>" con Let's Encrypt; vacía con certificado propio,
	// porque el binario no sabe con qué nombre lo alcanzan.
	PublicURL string
}

// avisoCadaMax es la cadencia máxima de onError: un dominio mal apuntado recibe un
// ClientHello por cada intento del navegador y del propio ACME, y avisar de cada uno
// convertiría el log en un torrente.
const avisoCadaMax = 10 * time.Minute

// Build devuelve nil, nil cuando la configuración no pide TLS. onError puede ser nil.
func Build(cfg *config.Config, onError func(error)) (*Setup, error) {
	switch {
	case cfg.TLSDomain != "" && cfg.TLSCertFile != "":
		return nil, errors.New("webtls: dominio y certificado propio a la vez")
	case cfg.TLSDomain != "":
		return conLetsEncrypt(cfg, onError)
	case cfg.TLSCertFile != "":
		return conCertificadoPropio(cfg)
	default:
		return nil, nil
	}
}

func conLetsEncrypt(cfg *config.Config, onError func(error)) (*Setup, error) {
	// La caché se comprueba aquí, al arrancar, porque autocert la trata como opcional: si
	// no puede escribir en ella no falla, sirve el certificado y lo olvida. Cada arranque
	// —y cada renovación— pediría uno nuevo, y Let's Encrypt corta a los 5 por semana y
	// por dominio. Mejor no arrancar que quedarse sin cupo el día que haga falta.
	if err := os.MkdirAll(cfg.TLSCacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("SPLITSTREAM_TLS_CACHE_DIR %s no es escribible: %w", cfg.TLSCacheDir, err)
	}
	// MkdirAll pasa si el directorio ya existe aunque sea de otro dueño o de solo lectura,
	// así que además se escribe de verdad un archivo y se borra.
	sonda, err := os.CreateTemp(cfg.TLSCacheDir, ".sonda-*")
	if err != nil {
		return nil, fmt.Errorf("SPLITSTREAM_TLS_CACHE_DIR %s no es escribible: %w", cfg.TLSCacheDir, err)
	}
	nombre := sonda.Name()
	if err := sonda.Close(); err != nil {
		// Borrar la sonda es una cortesía: ya vamos a fallar el arranque por el Close, y
		// si tampoco se puede borrar, lo que queda es un .sonda-* que DirCache ignora.
		_ = os.Remove(nombre)
		return nil, fmt.Errorf("SPLITSTREAM_TLS_CACHE_DIR %s no es escribible: %w", cfg.TLSCacheDir, err)
	}
	if err := os.Remove(nombre); err != nil {
		return nil, fmt.Errorf("SPLITSTREAM_TLS_CACHE_DIR %s no es escribible: %w", cfg.TLSCacheDir, err)
	}

	m := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		// Solo este nombre: sin lista blanca, cualquiera que apunte un dominio a esta IP
		// haría que el servicio pidiera certificados en su nombre hasta agotar el cupo.
		HostPolicy: autocert.HostWhitelist(cfg.TLSDomain),
		Cache:      autocert.DirCache(cfg.TLSCacheDir),
	}
	tc := m.TLSConfig() // trae GetCertificate y acme-tls/1 en NextProtos
	tc.MinVersion = tls.VersionTLS12

	// OJO CON LOS TESTS: llamar a tc.GetCertificate con un ClientHello cuyo ServerName sea
	// el dominio configurado SALE A INTERNET de verdad —autocert registra la cuenta ACME y
	// pide el certificado a Let's Encrypt, contra el cupo de 5 por semana y por dominio—.
	// Ningún test debe hacerlo. Un nombre ajeno sí es seguro: HostWhitelist lo rechaza sin
	// tocar la red.

	if onError != nil {
		interno := tc.GetCertificate
		avisar := avisador(onError, time.Now)
		dominio := cfg.TLSDomain
		tc.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := interno(hello)
			// Solo los fallos del propio dominio interesan: los escáneres piden nombres
			// ajenos todo el día y HostWhitelist los rechaza sin salir a la red.
			if err != nil && hello.ServerName == dominio {
				avisar(err)
			}
			return cert, err
		}
	}

	return &Setup{
		TLSConfig: tc,
		// HTTPHandler atiende /.well-known/acme-challenge/ y delega el resto en nuestro
		// redirector, que sabe el puerto HTTPS (el de autocert supone 443).
		Redirect:  m.HTTPHandler(redirigirAHTTPS(puertoDe(cfg.HTTPAddr))),
		PublicURL: "https://" + cfg.TLSDomain,
	}, nil
}

func conCertificadoPropio(cfg *config.Config) (*Setup, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("cargar el certificado de SPLITSTREAM_TLS_CERT_FILE: %w", err)
	}
	return &Setup{
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		Redirect:  redirigirAHTTPS(puertoDe(cfg.HTTPAddr)),
	}, nil
}

// avisador devuelve una función que reenvía a fn como máximo una vez por avisoCadaMax.
//
// Recibe el reloj como parámetro (en vez de llamar a time.Now directamente) para que los
// tests puedan controlar la cadencia sin dormir de verdad diez minutos; en producción
// siempre se le pasa time.Now.
func avisador(fn func(error), now func() time.Time) func(error) {
	var mu sync.Mutex
	var ultimo time.Time
	return func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if ahora := now(); ahora.Sub(ultimo) >= avisoCadaMax {
			ultimo = ahora
			fn(err)
		}
	}
}

// redirigirAHTTPS manda un 301 a la misma ruta por HTTPS. Se conserva el puerto solo si
// no es el 443: "https://host:443" funciona pero queda feo en la barra del navegador.
func redirigirAHTTPS(puerto string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		// Un cliente que llega por el puerto por defecto manda Host sin puerto (RFC 7230),
		// y un literal IPv6 viene entonces entre corchetes: SplitHostPort falla y los
		// corchetes se quedan. Se quitan aquí para que JoinHostPort los ponga él y no
		// salga "[[::1]]:8443".
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if puerto != "" && puerto != "443" {
			host = net.JoinHostPort(host, puerto)
		} else if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

func puertoDe(addr string) string {
	_, puerto, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return puerto
}
