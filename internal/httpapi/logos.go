package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/aprendomx/splitstream/internal/imagen"
	"github.com/aprendomx/splitstream/internal/store"
)

// maxLogoSubida acota el cuerpo de la subida. No protege de una bomba de descompresión
// —eso lo hace el límite de píxeles de internal/imagen—, sino de que alguien ocupe memoria
// y disco subiendo archivos enormes.
const maxLogoSubida = 2 << 20 // 2 MiB

// logoETags devuelve el mapa id -> etag. Un fallo de lectura no debe tumbar la respuesta
// entera: sin logo el panel enseña el icono de la plataforma, que es exactamente lo que
// pasaba antes de que existiera esta función.
func (s *Server) logoETags(ctx context.Context) map[int64]string {
	m, err := s.db.DestinationLogoETags(ctx)
	if err != nil {
		s.logger.Error("no se pudieron leer los logos", "err", err)
		return nil
	}
	return m
}

// logoETag es la versión de un solo destino, para las respuestas que devuelven uno.
func (s *Server) logoETag(ctx context.Context, id int64) string {
	return s.logoETags(ctx)[id]
}

// handlePutDestinationLogo recibe la imagen, la normaliza y la guarda.
func (s *Server) handlePutDestinationLogo(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}

	// El límite se pone ANTES de leer nada. MaxBytesReader corta la lectura en cuanto se
	// pasa, así que un cuerpo de un gigabyte no llega a ocupar un gigabyte.
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoSubida)

	archivo, _, err := r.FormFile("file")
	if err != nil {
		var tamano *http.MaxBytesError
		if errors.As(err, &tamano) {
			writeError(w, http.StatusRequestEntityTooLarge, codeInvalidInput,
				"la imagen pesa más de 2 MB")
			return
		}
		writeError(w, http.StatusBadRequest, codeInvalidInput,
			"no llegó ninguna imagen en el campo «file»")
		return
	}
	defer archivo.Close()

	png, err := imagen.Normalizar(archivo)
	if err != nil {
		switch {
		case errors.Is(err, imagen.ErrFormato):
			writeError(w, http.StatusBadRequest, codeInvalidInput,
				"ese archivo no es una imagen PNG o JPEG")
		case errors.Is(err, imagen.ErrDemasiadosPixeles):
			writeError(w, http.StatusBadRequest, codeInvalidInput,
				"la imagen tiene demasiados píxeles; recórtala antes de subirla")
		default:
			// El error interno no se le enseña al usuario: puede llevar detalles del
			// archivo que subió y no le dice nada útil.
			s.logger.Error("no se pudo normalizar el logo", "destino_id", id, "err", err)
			writeError(w, http.StatusBadRequest, codeInvalidInput, "no se pudo leer la imagen")
		}
		return
	}

	if _, err := s.db.SetDestinationLogo(r.Context(), id, png); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.responderDestino(w, r, id)
}

// handleGetDestinationLogo sirve la imagen guardada.
func (s *Server) handleGetDestinationLogo(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}

	logo, err := s.db.DestinationLogo(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	etag := `"` + logo.ETag + `"`
	w.Header().Set("ETag", etag)
	// private: es contenido de un usuario autenticado y no debe quedarse en una caché
	// compartida. must-revalidate con max-age=0: el navegador puede guardarlo, pero
	// pregunta siempre, y la respuesta normal es un 304 sin cuerpo.
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	// El navegador no debe adivinar el tipo: si algún día entrara algo que no es PNG, el
	// sniffing lo convertiría en el problema que la validación de la subida evita.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write(logo.Image)
}

// handleDeleteDestinationLogo quita el logo y devuelve el destino ya sin él.
func (s *Server) handleDeleteDestinationLogo(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	// Se comprueba que el destino existe: borrar el logo es idempotente, pero pedirlo de un
	// canal que no existe es un error del cliente y merece un 404.
	if _, err := s.destinoPorID(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := s.db.DeleteDestinationLogo(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.responderDestino(w, r, id)
}

// destinoPorID busca un destino en el listado. El store no expone una lectura individual
// pública, y el listado es una tabla pequeña.
func (s *Server) destinoPorID(ctx context.Context, id int64) (*store.Destination, error) {
	dests, err := s.db.ListDestinations(ctx)
	if err != nil {
		return nil, err
	}
	for i := range dests {
		if dests[i].ID == id {
			return &dests[i], nil
		}
	}
	return nil, store.ErrDestinationNotFound
}

// responderDestino devuelve el DTO actualizado de un destino. Lo usan los handlers del
// logo para que el panel no tenga que recargar la lista tras subir o quitar la imagen.
func (s *Server) responderDestino(w http.ResponseWriter, r *http.Request, id int64) {
	d, err := s.destinoPorID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newDestinationDTO(*d, s.metricsFor(id), s.logoETag(r.Context(), id)))
}
