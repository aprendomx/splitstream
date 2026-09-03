package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	// wsInterval es cada cuánto se empuja el estado (spec §9).
	wsInterval = time.Second

	// wsWriteTimeout acota CADA escritura.
	//
	// Es lo que impide que un cliente que no lee bloquee el bucle para siempre: sin plazo,
	// la escritura se queda esperando a que se vacíe el buffer del socket, la goroutine no
	// sale nunca y se acumula una por pestaña abandonada. En un proceso que retransmite
	// durante horas, eso duele.
	wsWriteTimeout = 2 * time.Second
)

// handleWS empuja el estado del servicio cada segundo.
//
// El handshake es una petición HTTP normal y lleva la cookie, así que el WebSocket queda
// protegido por el mismo middleware que el resto: llegar aquí ya implica sesión válida.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Sin InsecureSkipVerify: la comprobación de origen por defecto de la librería exige
	// que Origin coincida con Host, y es lo que impide que otra página abra un WebSocket
	// contra el panel del usuario aprovechando su cookie.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept ya respondió al cliente; aquí solo queda dejar constancia.
		s.logger.Warn("no se pudo abrir el WebSocket", "err", err)
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	ticker := time.NewTicker(wsInterval)
	defer ticker.Stop()

	// El primer envío va sin esperar al tick: la interfaz quiere pintar algo en cuanto
	// conecta, no un segundo después.
	if !s.pushStatus(ctx, conn, r) {
		return
	}

	for {
		select {
		case <-ctx.Done():
			// El cliente se fue o el servidor está cerrando: la goroutine termina aquí.
			return
		case <-ticker.C:
			if !s.pushStatus(ctx, conn, r) {
				return
			}
		}
	}
}

// pushStatus manda un statusDTO. Devuelve false si hay que cerrar la conexión.
//
// Un fallo al componer el estado NO cierra: un error puntual de la base no debe tirar el
// panel de quien está transmitiendo. Un fallo de ESCRITURA sí, porque significa que el
// cliente ya no está o no lee, y no se reintenta: el frontend reconecta solo (spec §10).
func (s *Server) pushStatus(ctx context.Context, conn *websocket.Conn, r *http.Request) bool {
	st, err := s.status(ctx, r)
	if err != nil {
		s.logger.Error("no se pudo componer el estado para el WebSocket", "err", err)
		return true
	}

	escritura, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()

	if err := wsjson.Write(escritura, conn, st); err != nil {
		s.logger.Debug("se cerró el WebSocket", "err", err)
		return false
	}
	return true
}
