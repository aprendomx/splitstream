package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// maxBody es el tope del cuerpo de una petición. 64 KiB sobran para cualquiera de estos
// endpoints, y el tope evita que un cuerpo enorme consuma memoria del proceso que está
// retransmitiendo.
const maxBody = 64 << 10

// pathID saca el {id} de la ruta.
//
// Un id no numérico es 400 y no 404: la ruta existe, lo que no vale es lo que mandaron. Un
// 404 aquí haría pensar que el destino se borró.
func (s *Server) pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidInput, "el id debe ser un número")
		return 0, false
	}
	return id, true
}

// decodeBody lee el cuerpo JSON acotado. Devuelve false si ya escribió el error.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidInput, "cuerpo JSON inválido")
		return false
	}
	return true
}

// metricsFor devuelve las métricas del destino, o nil si no hay sesión viva o el motor no
// sabe nada de él. El nil se convierte en el null del JSON, que es lo que la interfaz usa
// para distinguir "sin métricas" de "métricas en cero".
func (s *Server) metricsFor(id int64) *relay.Metrics {
	if s.engine == nil || s.engine.Session().ID == 0 {
		return nil
	}
	m, ok := s.engine.Snapshot()[id]
	if !ok {
		return nil
	}
	return &m
}

// destinationCreate es el cuerpo del alta. La clave entra como string y se convierte a
// crypto.Secret en cuanto se lee: cuanto menos viva como string, menos sitios puede acabar
// impresa.
type destinationCreate struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
	RTMPURL  string `json:"rtmp_url"`
	Key      string `json:"key"`
	Enabled  bool   `json:"enabled"`
}

// destinationPatch usa punteros para distinguir "no lo mandaron" de "lo mandaron vacío".
// Sin esa distinción, editar el nombre borraría la clave.
type destinationPatch struct {
	Name     *string `json:"name"`
	Platform *string `json:"platform"`
	RTMPURL  *string `json:"rtmp_url"`
	Key      *string `json:"key"`
	Enabled  *bool   `json:"enabled"`
}

func (s *Server) handleListDestinations(w http.ResponseWriter, r *http.Request) {
	dests, err := s.db.ListDestinations(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// Slice no nil para que el JSON sea [] y no null: un null obligaría al frontend a
	// comprobarlo antes de iterar.
	out := make([]destinationDTO, 0, len(dests))
	for _, d := range dests {
		out = append(out, newDestinationDTO(d, s.metricsFor(d.ID)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateDestination(w http.ResponseWriter, r *http.Request) {
	var in destinationCreate
	if !decodeBody(w, r, &in) {
		return
	}

	d, err := s.db.CreateDestination(r.Context(), s.cipher, store.NewDestination{
		Name:     in.Name,
		Platform: store.Platform(in.Platform),
		RTMPURL:  in.RTMPURL,
		Key:      crypto.Secret(in.Key),
		Enabled:  in.Enabled,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	s.applyHot(r, *d)

	w.Header().Set("Location", "/api/destinations/"+strconv.FormatInt(d.ID, 10))
	writeJSON(w, http.StatusCreated, newDestinationDTO(*d, s.metricsFor(d.ID)))
}

func (s *Server) handlePatchDestination(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}

	var in destinationPatch
	if !decodeBody(w, r, &in) {
		return
	}

	patch := store.DestinationPatch{Name: in.Name, RTMPURL: in.RTMPURL, Enabled: in.Enabled}
	if in.Platform != nil {
		p := store.Platform(*in.Platform)
		patch.Platform = &p
	}
	if in.Key != nil {
		k := crypto.Secret(*in.Key)
		patch.Key = &k
	}

	d, err := s.db.UpdateDestination(r.Context(), s.cipher, id, patch)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	s.applyHot(r, *d)
	writeJSON(w, http.StatusOK, newDestinationDTO(*d, s.metricsFor(d.ID)))
}

func (s *Server) handleDeleteDestination(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}

	if err := s.db.DeleteDestination(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}

	// Se quita del hub DESPUÉS de que el borrado haya ido bien: al revés, un fallo de la
	// base dejaría un destino cortado que sigue existiendo en la configuración.
	s.removeHot(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleToggleDestination(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}

	// Se lee para invertir. No hay carrera que valga la pena cerrar: es un servicio de un
	// solo usuario, y dos toggles simultáneos del mismo destino no son un escenario real.
	actual, err := s.db.ListDestinations(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	var encontrado bool
	var enabled bool
	for _, d := range actual {
		if d.ID == id {
			encontrado, enabled = true, d.Enabled
			break
		}
	}
	if !encontrado {
		s.writeStoreError(w, store.ErrDestinationNotFound)
		return
	}

	nuevo := !enabled
	d, err := s.db.UpdateDestination(r.Context(), s.cipher, id, store.DestinationPatch{Enabled: &nuevo})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	s.applyHot(r, *d)
	writeJSON(w, http.StatusOK, newDestinationDTO(*d, s.metricsFor(d.ID)))
}

type reorderRequest struct {
	IDs []int64 `json:"ids"`
}

func (s *Server) handleReorderDestinations(w http.ResponseWriter, r *http.Request) {
	var in reorderRequest
	if !decodeBody(w, r, &in) {
		return
	}

	if err := s.db.ReorderDestinations(r.Context(), in.IDs); err != nil {
		s.writeStoreError(w, err)
		return
	}
	// No hace falta tocar el hub: reordenar no cambia qué destinos están conectados, solo
	// en qué orden se enseñan y se conectan la próxima vez.
	s.handleListDestinations(w, r)
}

func (s *Server) handleRevealDestinationKey(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}

	// RevealDestinationKey audita en la misma transacción (spec §15.5): no hay forma de
	// llegar aquí sin dejar rastro.
	key, err := s.db.RevealDestinationKey(r.Context(), s.cipher, id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// La única salida en claro de una clave de destino en toda la API. Existe porque el
	// usuario necesita poder recuperar lo que pegó, y está detrás de sesión y auditada.
	writeJSON(w, http.StatusOK, map[string]string{"key": key.Reveal()})
}

// applyHot aplica un alta o una edición sobre la sesión en curso.
//
// Se llama SIEMPRE después de que la escritura en la base haya ido bien: al revés, un fallo
// de la base dejaría un sink conectado que no corresponde a ninguna fila.
//
// Un fallo aquí se registra y NO convierte la respuesta en un error: la petición hizo lo
// que pedía —persistir el cambio—, y un 500 haría que el usuario lo repitiera y creara un
// destino duplicado. El destino entrará igualmente en la siguiente sesión.
func (s *Server) applyHot(r *http.Request, d store.Destination) {
	if !s.liveSession() {
		return
	}
	if !d.Enabled {
		s.removeHot(d.ID)
		return
	}
	if s.sinks == nil {
		return
	}

	sink, err := s.sinks.Build(r.Context(), d)
	if err != nil {
		s.logger.Error("no se pudo aplicar el destino en caliente",
			"destino_id", d.ID, "destino", d.Name, "err", err)
		return
	}
	// AddSink arranca el sink y lo mete en el hub. Reemplaza uno con el mismo id sin dejar
	// ventana de escritura doble (fase 2), así que sirve igual para el alta y la edición.
	s.engine.AddSink(sink)
}

// removeHot para el sink de un destino si hay sesión en curso.
func (s *Server) removeHot(id int64) {
	if !s.liveSession() {
		return
	}
	s.engine.RemoveSink(id)
}

// liveSession dice si hay algo que tocar en caliente. Con el motor o el hub sin cablear
// —arranque parcial, o un test que no los ejercita— la respuesta es no.
func (s *Server) liveSession() bool {
	return s.engine != nil && s.engine.Session().ID != 0
}
