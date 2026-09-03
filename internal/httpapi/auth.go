// Package httpapi sirve la API HTTP y el WebSocket del panel (spec §9).
//
// Depende de store, crypto y relay, y ninguno de los tres depende de él: la serialización
// a JSON vive aquí y no se filtra hacia el motor.
package httpapi

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/aprendomx/splitstream/internal/crypto"
)

const (
	// sessionCookieName es el nombre de la cookie de sesión.
	sessionCookieName = "splitstream_session"

	// sessionTTL es lo que dura una sesión. Treinta días: es un servicio personal de un
	// solo usuario, y tener que volver a escribir la contraseña a mitad de un directo es
	// peor que el riesgo de una sesión larga en un navegador que ya es de confianza.
	sessionTTL = 30 * 24 * time.Hour

	// sessionInfo es la etiqueta de HKDF. Aísla la clave de firma de la cookie del
	// material que cifra las claves de destino: un fallo en una no debe ayudar con la
	// otra. Si algún día cambia el formato de la cookie se sube el /v, y todas las
	// sesiones vivas quedan invalidadas de golpe.
	sessionInfo = "splitstream/session-cookie/v1"

	// cookieVersion prefija el valor para poder cambiar el formato sin ambigüedad.
	cookieVersion = "v1"
)

var (
	errCookieMalformed    = errors.New("cookie de sesión con formato inválido")
	errCookieBadSignature = errors.New("firma de la cookie de sesión inválida")
	errCookieExpired      = errors.New("sesión caducada")
)

// sessionSigner emite y verifica cookies de sesión firmadas.
//
// La sesión no tiene estado en el servidor: la cookie lleva su propia caducidad y va
// firmada, así que reiniciar el proceso no cierra la sesión de nadie —importante, porque
// reiniciar el servicio no debería echarte del panel a mitad de transmisión—. La
// contrapartida es que no se puede revocar UNA sesión: revocar es rotar la master key,
// que las tumba todas.
type sessionSigner struct{ key []byte }

func newSessionSigner(master [32]byte) (*sessionSigner, error) {
	key, err := hkdf.Key(sha256.New, master[:], nil, sessionInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("derivar la clave de sesión: %w", err)
	}
	return &sessionSigner{key: key}, nil
}

// issue emite el valor de una cookie válida hasta now+sessionTTL. El formato es
// "v1.<caducidad unix>.<hmac en base64url>".
func (s *sessionSigner) issue(now time.Time) string {
	payload := cookieVersion + "." + strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	return payload + "." + s.sign(payload)
}

// verify comprueba firma y caducidad, EN ESE ORDEN: la caducidad la escribe quien manda la
// cookie, así que sin una firma válida no significa nada.
func (s *sessionSigner) verify(value string, now time.Time) error {
	partes := strings.Split(value, ".")
	if len(partes) != 3 || partes[0] != cookieVersion {
		return errCookieMalformed
	}
	payload := partes[0] + "." + partes[1]

	// hmac.Equal compara en tiempo constante: con == se filtraría por temporización
	// cuántos bytes iniciales acertó quien lo está intentando.
	if !hmac.Equal([]byte(partes[2]), []byte(s.sign(payload))) {
		return errCookieBadSignature
	}

	exp, err := strconv.ParseInt(partes[1], 10, 64)
	if err != nil {
		return errCookieMalformed
	}
	if now.After(time.Unix(exp, 0)) {
		return errCookieExpired
	}
	return nil
}

func (s *sessionSigner) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// loginLimiter acota los intentos de login por IP.
//
// Sin esto, la contraseña del panel se puede probar a fuerza bruta tan rápido como aguante
// el argon2id, que es lento pero no infinitamente. El límite es generoso a propósito: debe
// molestar a un script, no a alguien que se equivoca dos veces al escribir.
type loginLimiter struct {
	mu  sync.Mutex
	por map[string]*rate.Limiter
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{por: make(map[string]*rate.Limiter)}
}

// allow consume un intento. Ráfaga de 5 y reposición de uno cada 10 s.
func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, ok := l.por[ip]
	if !ok {
		lim = rate.NewLimiter(rate.Every(10*time.Second), 5)
		l.por[ip] = lim
	}
	return lim.Allow()
}

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, codeRateLimited,
			"demasiados intentos; espera un momento")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidInput, "cuerpo JSON inválido")
		return
	}

	settings, err := s.db.Settings(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if settings.PasswordHash == "" {
		writeError(w, http.StatusConflict, codeConflict,
			"no hay contraseña configurada: ejecuta `splitstream -setpassword`")
		return
	}

	ok, err := crypto.VerifyPassword(settings.PasswordHash, req.Password)
	if err != nil {
		s.logger.Error("no se pudo verificar la contraseña", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	if !ok {
		// Se registra el intento pero NO la contraseña probada (spec §8).
		s.logger.Warn("intento de login fallido", "ip", clientIP(r))
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "contraseña incorrecta")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.signer.issue(time.Now()),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// requireSession exige una cookie de sesión válida.
//
// Todas las respuestas de fallo son el mismo 401 sin detalle: distinguir "no hay cookie"
// de "la firma no cuadra" de "caducó" solo le sirve a quien está probando.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "sesión requerida")
			return
		}
		if err := s.signer.verify(c.Value, time.Now()); err != nil {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "sesión requerida")
			return
		}
		next.ServeHTTP(w, r)
	})
}
