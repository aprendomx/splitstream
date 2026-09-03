package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

// minPasswordLen es el mínimo aceptable, el mismo que exige `splitstream -setpassword`.
// No es una política seria, es un filtro contra el descuido: el panel puede quedar expuesto
// a internet y una contraseña de tres letras no es una contraseña.
const minPasswordLen = 8

// ahora se puede sustituir en los tests. El resto del paquete usa time.Now directamente.
var ahora = time.Now

// GenerateSetupCode produce el código de un solo uso del primer arranque.
//
// Formato agrupado en tríos —A7K2-9QMX-4RTZ— porque hay que leerlo de una consola y
// teclearlo en otra pantalla, a veces desde el móvil. Sin caracteres que se confundan:
// fuera I, L, O, 0 y 1.
func GenerateSetupCode() (string, error) {
	const alfabeto = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generar el código de configuración: %w", err)
	}
	var sb strings.Builder
	for i, b := range buf {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alfabeto[int(b)%len(alfabeto)])
	}
	return sb.String(), nil
}

// esLocal dice si la petición viene de la propia máquina.
//
// Es la señal que decide si el primer arranque pide código: quien está en el teclado del
// equipo ya lo controla, así que exigirle un código sería fricción sin ganancia. Desde
// fuera —un VPS con el puerto abierto, o la red de casa— sí hace falta, o el primero que
// cargue la página se queda con el servicio.
//
// No se mira X-Forwarded-For a propósito: quien llega de fuera puede inventárselo, y
// entonces el control no controlaría nada.
func esLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type setupEstadoDTO struct {
	// Necesario es true mientras no haya contraseña configurada.
	Necesario bool `json:"necesario"`
	// PideCodigo es true cuando la petición no viene de la propia máquina.
	PideCodigo bool `json:"pide_codigo"`
	// Local dice desde dónde se está viendo, para que la interfaz pueda avisar de la
	// exposición al terminar.
	Local bool `json:"local"`
}

// handleSetupEstado es público: se consulta antes de tener sesión, que es justo lo que el
// primer arranque necesita saber. No revela nada que un atacante no pueda deducir pidiendo
// el login y viendo si responde 409.
func (s *Server) handleSetupEstado(w http.ResponseWriter, r *http.Request) {
	settings, err := s.db.Settings(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	local := esLocal(r)
	writeJSON(w, http.StatusOK, setupEstadoDTO{
		Necesario:  settings.PasswordHash == "",
		PideCodigo: !local,
		Local:      local,
	})
}

type setupRequest struct {
	Password string `json:"password"`
	Codigo   string `json:"codigo"`
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	// El mismo limitador que el login: sin él, el código de doce caracteres se puede
	// probar a fuerza bruta.
	if !s.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, codeRateLimited,
			"demasiados intentos; espera un momento")
		return
	}

	var in setupRequest
	if !decodeBody(w, r, &in) {
		return
	}

	settings, err := s.db.Settings(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	// Ya configurado: este endpoint deja de existir para siempre. Es lo que impide que
	// alguien reclame un servicio que ya tiene dueño.
	if settings.PasswordHash != "" {
		writeError(w, http.StatusConflict, codeConflict,
			"este servicio ya está configurado; entra con tu contraseña")
		return
	}

	if !esLocal(r) {
		if s.setupCode == "" {
			writeError(w, http.StatusConflict, codeConflict,
				"la configuración inicial desde fuera de esta máquina necesita el código "+
					"que el servicio imprime al arrancar, y este no tiene ninguno")
			return
		}
		// Comparación en tiempo constante: con == se filtraría por temporización cuántos
		// caracteres iniciales acertó quien lo intenta.
		dado := strings.ToUpper(strings.TrimSpace(in.Codigo))
		if subtle.ConstantTimeCompare([]byte(dado), []byte(s.setupCode)) != 1 {
			s.logger.Warn("código de configuración incorrecto", "ip", clientIP(r))
			writeError(w, http.StatusUnauthorized, codeUnauthorized,
				"el código no es correcto; míralo en la consola donde arrancaste splitstream")
			return
		}
	}

	if len(in.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, codeInvalidInput,
			fmt.Sprintf("la contraseña necesita al menos %d caracteres", minPasswordLen))
		return
	}

	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		s.logger.Error("no se pudo hashear la contraseña de configuración", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	if err := s.db.SetPasswordHash(r.Context(), hash); err != nil {
		s.writeStoreError(w, err)
		return
	}

	if _, err := s.db.LogEvent(r.Context(), store.Event{
		Level:   store.LevelWarn,
		Kind:    "setup_completed",
		Message: "se completó la configuración inicial",
	}); err != nil {
		s.logger.Error("no se pudo registrar la configuración inicial", "err", err)
	}
	s.logger.Info("configuración inicial completada", "desde", clientIP(r))

	// Se deja la sesión abierta: obligar a escribir otra vez la contraseña recién elegida
	// no aporta seguridad y rompe el hilo del asistente.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.signer.issue(ahora()),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}
