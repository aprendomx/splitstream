package httpapi

import (
	"net/http"

	"github.com/aprendomx/splitstream/internal/store"
)

// toggleAllRequest lleva el estado deseado, no una orden de invertir.
//
// Invertir sería ambiguo cuando unos canales están encendidos y otros no: la mitad
// quedaría al revés de lo que el usuario ve en el interruptor. Con un estado deseado, el
// resultado es el mismo se pulse desde donde se pulse.
type toggleAllRequest struct {
	// Puntero para distinguir "false" de "no vino el campo". Sin esto, un cuerpo vacío
	// apagaría todos los canales del usuario en mitad de una emisión.
	Enabled *bool `json:"enabled"`
}

// handleToggleAllDestinations enciende o apaga todos los destinos de una vez.
func (s *Server) handleToggleAllDestinations(w http.ResponseWriter, r *http.Request) {
	var in toggleAllRequest
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		writeError(w, http.StatusBadRequest, codeInvalidInput,
			"falta el campo «enabled»: hay que decir si se encienden o se apagan")
		return
	}

	ctx := r.Context()
	dests, err := s.db.ListDestinations(ctx)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// Solo se tocan los que cambian. Reescribir los que ya estaban como se pide movería su
	// updated_at sin motivo, y un doble clic del panel se vería como dos cambios.
	var cambiados []store.Destination
	err = s.db.InTx(ctx, func(tx *store.DB) error {
		for _, d := range dests {
			if d.Enabled == *in.Enabled {
				continue
			}
			nuevo, err := tx.UpdateDestination(ctx, s.cipher, d.ID,
				store.DestinationPatch{Enabled: in.Enabled})
			if err != nil {
				return err
			}
			cambiados = append(cambiados, *nuevo)
		}
		return nil
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// Enganchar o soltar los sinks va FUERA de la transacción: abre conexiones de red, y
	// mantener una transacción abierta mientras se conecta a YouTube bloquearía la única
	// conexión a la base durante todo ese tiempo.
	for _, d := range cambiados {
		s.applyHot(r, d)
	}

	s.escribirListaDestinos(w, r)
}
