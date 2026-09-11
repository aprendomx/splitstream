package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
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
	// AccountID es json.RawMessage y no *int64 porque hay que distinguir TRES casos: el
	// campo ausente (no tocar el enlace), `null` (desvincular) y un número (vincular). Un
	// *int64 solo distingue dos.
	AccountID json.RawMessage `json:"account_id"`
}

func (s *Server) handleListDestinations(w http.ResponseWriter, r *http.Request) {
	s.escribirListaDestinos(w, r)
}

// escribirListaDestinos responde con el listado completo. Lo comparten el GET del listado y
// el interruptor maestro, que devuelve el estado resultante para que el panel no tenga que
// pedirlo en una segunda petición.
func (s *Server) escribirListaDestinos(w http.ResponseWriter, r *http.Request) {
	dests, err := s.db.ListDestinations(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// Los etags de los logos se piden una vez para toda la lista, no uno por destino.
	etags := s.logoETags(r.Context())
	cuentas, err := s.accountsByID(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	// Slice no nil para que el JSON sea [] y no null: un null obligaría al frontend a
	// comprobarlo antes de iterar.
	out := make([]destinationDTO, 0, len(dests))
	for _, d := range dests {
		dto := newDestinationDTO(d, s.metricsFor(d.ID), etags[d.ID])
		s.decorar(r.Context(), &dto, d, cuentas)
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

// accountsByID carga todas las cuentas indexadas por id, para decorar una lista entera de
// destinos con una sola consulta en vez de una por destino.
func (s *Server) accountsByID(ctx context.Context) (map[int64]store.Account, error) {
	cuentas, err := s.db.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]store.Account, len(cuentas))
	for _, a := range cuentas {
		out[a.ID] = a
	}
	return out, nil
}

// decorar rellena Account y Capabilities de un destinationDTO ya construido.
//
// cuentas es el mapa cargado una vez por lista (accountsByID); nil para decorar un destino
// suelto —alta, PATCH, toggle—, que entonces consulta la cuenta directamente si hace falta.
func (s *Server) decorar(ctx context.Context, dto *destinationDTO, d store.Destination, cuentas map[int64]store.Account) {
	if s.platforms != nil {
		dto.Capabilities = capsDTO(s.platforms.AllCapabilities()[platforms.ID(d.Platform)])
	}
	if d.AccountID == nil {
		return
	}
	var a store.Account
	if cuentas != nil {
		var ok bool
		if a, ok = cuentas[*d.AccountID]; !ok {
			return
		}
	} else {
		ap, err := s.db.AccountByID(ctx, *d.AccountID)
		if err != nil {
			return
		}
		a = *ap
	}
	dto.Account = &accountRefDTO{ID: a.ID, DisplayName: a.DisplayName, Platform: string(a.Platform), Status: a.Status}
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
	dto := newDestinationDTO(*d, s.metricsFor(d.ID), s.logoETag(r.Context(), d.ID))
	s.decorar(r.Context(), &dto, *d, nil)
	writeJSON(w, http.StatusCreated, dto)
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

	// Si el patch vincula una cuenta, la plataforma se valida ANTES de tocar la base: sin
	// esto, un 400 por plataformas distintas dejaba el resto del patch ya aplicado —
	// UpdateDestination confirmaba antes de que LinkDestination lo rechazara.
	var vincular *int64
	if len(in.AccountID) > 0 && !bytes.Equal(bytes.TrimSpace(in.AccountID), []byte("null")) {
		var accountID int64
		if err := json.Unmarshal(in.AccountID, &accountID); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidInput, "account_id inválido")
			return
		}
		acct, err := s.db.AccountByID(r.Context(), accountID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		// La plataforma resultante es la del patch si lo trae, o si no la del destino tal
		// como está ahora: es la que tendrá el destino cuando el patch termine de aplicarse.
		var resultante store.Platform
		if in.Platform != nil {
			resultante = store.Platform(*in.Platform)
		} else {
			actual, err := s.db.DestinationByID(r.Context(), id)
			if err != nil {
				s.writeStoreError(w, err)
				return
			}
			resultante = actual.Platform
		}
		if resultante != acct.Platform {
			writeError(w, http.StatusBadRequest, codeInvalidInput,
				fmt.Sprintf("la cuenta es de %s y el destino de %s", acct.Platform, resultante))
			return
		}
		vincular = &accountID
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

	// El enlace con la cuenta va aparte del resto del patch: distingue "no lo mandaron"
	// (json.RawMessage vacío) de `null` (desvincular) de un número ya validado (vincular).
	if len(in.AccountID) > 0 {
		if vincular == nil {
			if err := s.db.UnlinkDestination(r.Context(), id); err != nil {
				s.writeStoreError(w, err)
				return
			}
		} else {
			if err := s.db.LinkDestination(r.Context(), id, *vincular); err != nil {
				s.writeStoreError(w, err)
				return
			}
		}
		// Se relee: el enlace pudo cambiar aunque nada más del destino lo hiciera.
		d, err = s.db.DestinationByID(r.Context(), id)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
	}

	s.applyHot(r, *d)
	dto := newDestinationDTO(*d, s.metricsFor(d.ID), s.logoETag(r.Context(), d.ID))
	s.decorar(r.Context(), &dto, *d, nil)
	writeJSON(w, http.StatusOK, dto)
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
	dto := newDestinationDTO(*d, s.metricsFor(d.ID), s.logoETag(r.Context(), d.ID))
	s.decorar(r.Context(), &dto, *d, nil)
	writeJSON(w, http.StatusOK, dto)
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

// handleRetryDestination reconstruye el sink de un destino suspendido (spec v0.8 §2.1).
//
// Solo tiene sentido con sesión viva y con el destino en `suspended`: sobre uno en vivo,
// reconstruirlo cortaría la transmisión, y sin sesión el destino conectará solo al empezar
// la siguiente. La reconstrucción es el mismo camino que una edición en caliente: Build +
// AddSink, que reemplaza al sink anterior sin ventana de escritura doble.
func (s *Server) handleRetryDestination(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if !s.liveSession() {
		writeError(w, http.StatusConflict, codeConflict,
			"no hay emisión en curso: el destino conectará solo al empezar la siguiente")
		return
	}

	d, err := s.db.DestinationByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	// Apagado primero: un destino apagado no tiene sink, así que no aparece en Snapshot()
	// y la comprobación de «suspendido» se lo comía con un mensaje que no ayuda a nadie.
	if !d.Enabled {
		writeError(w, http.StatusConflict, codeConflict, "el destino está apagado: enciéndelo")
		return
	}
	if m, ok := s.engine.Snapshot()[id]; !ok || m.State != relay.StateSuspended.String() {
		writeError(w, http.StatusConflict, codeConflict, "el destino no está suspendido")
		return
	}

	if _, err := s.db.LogEvent(r.Context(), store.Event{
		DestinationID: &id, Level: store.LevelInfo, Kind: "destination_retry",
		Message: "se reintenta el destino a petición del usuario",
	}); err != nil {
		s.logger.Error("no se pudo registrar el reintento", "err", err)
	}

	s.applyHot(r, *d)
	dto := newDestinationDTO(*d, s.metricsFor(d.ID), s.logoETag(r.Context(), d.ID))
	s.decorar(r.Context(), &dto, *d, nil)
	writeJSON(w, http.StatusOK, dto)
}
