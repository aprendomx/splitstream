package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aprendomx/splitstream/internal/store"
)

// errorBody es la forma que el spec §9 fija para TODOS los errores de la API.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Los códigos que el frontend puede encontrarse. Son un conjunto cerrado a propósito: el
// cliente decide qué hacer mirando el code, no el texto, que es para humanos.
const (
	codeInvalidInput = "invalid_input"
	codeNotFound     = "not_found"
	codeConflict     = "conflict"
	codeUnauthorized = "unauthorized"
	codeRateLimited  = "rate_limited"
	codeInternal     = "internal"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		// Si la codificación falla a mitad, la cabecera ya salió y no hay forma de
		// convertirlo en un error HTTP: lo único posible es cortar.
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError escribe un error del spec §9. El `code` es el contrato y sale siempre igual;
// el `message` es texto para personas y se traduce al idioma que pidió quien mira, que
// viaja pegado al ResponseWriter desde el middleware conIdioma (spec v0.13 §2 y §4).
func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: traducir(idiomaDe(w), msg)}})
}

// writeStoreError traduce un error del store a una respuesta HTTP preguntando por su
// CLASE, no por su identidad ni por su texto (spec §15.3).
//
// Los mensajes de las tres clases sí van al cliente: son textos que escribimos nosotros
// para explicar qué tiene de malo la petición, y no llevan estado interno. El del 500 no:
// un error que no sabemos clasificar es un fallo nuestro, y su detalle puede llevar rutas
// o estado del proceso, así que va al log.
func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, codeInvalidInput, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, codeConflict, err.Error())
	default:
		s.logger.Error("fallo no clasificado de la API", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
	}
}
