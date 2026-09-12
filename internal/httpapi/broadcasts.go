package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// emisionTimeout acota crear la emisión: en YouTube son varias llamadas encadenadas
// (insertar la emisión, insertar el stream y enlazarlos), así que el plazo es generoso.
const emisionTimeout = 60 * time.Second

// transicionTimeout acota sacar la emisión al aire y cerrarla. YouTube no pasa a `live`
// hasta que ve llegar vídeo, y el proveedor reintenta hasta un minuto: el plazo de aquí
// tiene que dejarle terminar esos reintentos en vez de cortarlos a mitad.
const transicionTimeout = 90 * time.Second

// privacidadPorDefecto: una emisión creada desde el panel no se anuncia sola. Quien quiera
// que salga en la portada del canal lo pide expresamente.
const privacidadPorDefecto = "unlisted"

// errSinClaveAPI: la plataforma no sabe dar ni una emisión ni la clave del canal (Twitch,
// o un destino sin proveedor). No es un fallo de la plataforma, es que no aplica.
var errSinClaveAPI = errors.New("la plataforma no da la clave por API")

// fromAccountRequest es el alta de un destino a partir de una cuenta conectada: no se pega
// ninguna clave, la trae la plataforma.
type fromAccountRequest struct {
	AccountID int64  `json:"account_id"`
	Name      string `json:"name"`
	Title     string `json:"title"`
	Privacy   string `json:"privacy"`
	// ScheduledAt es para programar la emisión; ausente significa «ahora».
	ScheduledAt *time.Time `json:"scheduled_at"`
}

// broadcastRequest es el cuerpo —opcional— de crear una emisión sobre un destino que ya
// existe. Mismos campos que el alta, sin la cuenta ni el nombre: los da el destino.
type broadcastRequest struct {
	Title       string     `json:"title"`
	Privacy     string     `json:"privacy"`
	ScheduledAt *time.Time `json:"scheduled_at"`
}

// peticionEmision compone lo que se le pide a la plataforma. El título por defecto es el
// nombre del destino: es lo que la persona acaba de escribir y lo que espera ver.
func peticionEmision(titulo, privacidad, nombre string, cuando *time.Time) platforms.BroadcastRequest {
	req := platforms.BroadcastRequest{Title: strings.TrimSpace(titulo), Privacy: strings.TrimSpace(privacidad)}
	if req.Title == "" {
		req.Title = nombre
	}
	if req.Privacy == "" {
		req.Privacy = privacidadPorDefecto
	}
	if cuando != nil {
		req.ScheduledAt = *cuando
	}
	return req
}

// plataformaDeCuenta devuelve el proveedor de la cuenta y un token vigente, o escribe el
// error y devuelve false. El token no sale de aquí ni aparece en ningún mensaje.
func (s *Server) plataformaDeCuenta(ctx context.Context, w http.ResponseWriter, acct store.Account) (platforms.Provider, crypto.Secret, bool) {
	if acct.Status != store.AccountStatusOK {
		writeError(w, http.StatusConflict, codeConflict, "la cuenta de "+acct.DisplayName+" necesita reconectarse")
		return nil, "", false
	}
	var p platforms.Provider
	if s.platforms != nil {
		p, _ = s.platforms.Get(platforms.ID(acct.Platform))
	}
	if p == nil {
		writeError(w, http.StatusConflict, codeConflict, "esta plataforma no da la clave por API")
		return nil, "", false
	}
	if s.tokens == nil {
		writeError(w, http.StatusConflict, codeConflict, "hablar con las plataformas no está disponible en este arranque")
		return nil, "", false
	}
	tok, err := s.tokens.Token(ctx, acct.ID)
	if err != nil {
		writeError(w, http.StatusConflict, codeConflict, "la cuenta de "+acct.DisplayName+" necesita reconectarse")
		return nil, "", false
	}
	return p, tok, true
}

// pedirEmision consigue con qué emitir: una emisión creada (YouTube) o la clave del canal
// (Kick). El orden importa: una plataforma que sepa crear emisiones tiene que crearla, o
// el vídeo llegaría a un canal sin nada al aire.
func pedirEmision(ctx context.Context, p platforms.Provider, acct store.Account, tok crypto.Secret,
	req platforms.BroadcastRequest,
) (platforms.Broadcast, error) {
	if bs, ok := p.(platforms.BroadcastScheduler); ok {
		return bs.CreateBroadcast(ctx, acct, tok, req)
	}
	if ik, ok := p.(platforms.IngestKeyProvider); ok {
		url, key, err := ik.IngestKey(ctx, acct, tok)
		return platforms.Broadcast{IngestURL: url, Key: key}, err
	}
	return platforms.Broadcast{}, errSinClaveAPI
}

// escribirErrorEmision traduce el fallo de pedirEmision. errSinClaveAPI es 409 —la
// petición no tiene sentido para esta plataforma— y el resto 502: falló la plataforma, no
// quien llamó.
func (s *Server) escribirErrorEmision(w http.ResponseWriter, err error) {
	if errors.Is(err, errSinClaveAPI) {
		writeError(w, http.StatusConflict, codeConflict, "esta plataforma no da la clave por API")
		return
	}
	writeError(w, http.StatusBadGateway, codeInternal, mensajePlataforma(err))
}

// guardarEmision deja la emisión vinculada al destino y la registra. KeyFromAPI va SIEMPRE
// a true: se llega aquí solo cuando la clave la dio la plataforma, y es lo que hace que
// «probar destino» se salte y que el panel no invite a editar la clave.
func (s *Server) guardarEmision(ctx context.Context, d store.Destination, acct store.Account, b platforms.Broadcast) error {
	if err := s.db.SetBroadcast(ctx, store.Broadcast{
		DestinationID: d.ID, AccountID: acct.ID, Platform: acct.Platform,
		BroadcastRef: b.Ref, StreamRef: b.StreamRef, LiveChatID: b.LiveChatID, KeyFromAPI: true,
	}); err != nil {
		return err
	}
	id := d.ID
	nombre := nombresPlataforma[platforms.ID(acct.Platform)]
	sinCancelar := context.WithoutCancel(ctx)
	if b.Ref != "" {
		s.db.LogEvent(sinCancelar, store.Event{DestinationID: &id, Level: store.LevelInfo, Kind: "broadcast_created",
			Message: "emisión creada en " + nombre + " para " + d.Name})
	}
	s.db.LogEvent(sinCancelar, store.Event{DestinationID: &id, Level: store.LevelInfo, Kind: "destination_key_from_api",
		Message: "la clave de " + d.Name + " la dio " + nombre})
	return nil
}

// handleCreateDestinationFromAccount crea el destino con lo que da la plataforma: ni la
// URL ni la clave se escriben a mano (spec §8.2). La clave viaja como crypto.Secret desde
// el proveedor hasta el cifrado del store; del destino solo sale su máscara.
func (s *Server) handleCreateDestinationFromAccount(w http.ResponseWriter, r *http.Request) {
	var in fromAccountRequest
	if !decodeBody(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, codeInvalidInput, "ponle un nombre al destino")
		return
	}
	acct, err := s.db.AccountByID(r.Context(), in.AccountID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	p, tok, ok := s.plataformaDeCuenta(r.Context(), w, *acct)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), emisionTimeout)
	defer cancel()
	b, err := pedirEmision(ctx, p, *acct, tok, peticionEmision(in.Title, in.Privacy, in.Name, in.ScheduledAt))
	if err != nil {
		s.escribirErrorEmision(w, err)
		return
	}
	if b.IngestURL == "" || b.Key.Reveal() == "" {
		writeError(w, http.StatusBadGateway, codeInternal, "la plataforma no dio la clave de emisión")
		return
	}

	d, err := s.db.CreateDestination(ctx, s.cipher, store.NewDestination{
		Name: in.Name, Platform: acct.Platform, RTMPURL: b.IngestURL, Key: b.Key, Enabled: true,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := s.db.LinkDestination(ctx, d.ID, acct.ID); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := s.guardarEmision(ctx, *d, *acct, b); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.applyHot(r, *d)

	w.Header().Set("Location", "/api/destinations/"+strconv.FormatInt(d.ID, 10))
	s.escribirDestino(ctx, w, http.StatusCreated, d.ID)
}

// handleCreateBroadcast repite la operación sobre un destino que ya existe: sirve para
// empezar una emisión nueva mañana sin volver a crear el destino, y para recuperar una
// clave que la plataforma haya rotado.
func (s *Server) handleCreateBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var in broadcastRequest
	if r.ContentLength > 0 && !decodeBody(w, r, &in) {
		return
	}
	d, err := s.db.DestinationByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	acct, err := s.db.AccountForDestination(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusConflict, codeConflict, "este destino no tiene cuenta vinculada")
		return
	}
	p, tok, ok := s.plataformaDeCuenta(r.Context(), w, *acct)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), emisionTimeout)
	defer cancel()
	b, err := pedirEmision(ctx, p, *acct, tok, peticionEmision(in.Title, in.Privacy, d.Name, in.ScheduledAt))
	if err != nil {
		s.escribirErrorEmision(w, err)
		return
	}
	if b.IngestURL == "" || b.Key.Reveal() == "" {
		writeError(w, http.StatusBadGateway, codeInternal, "la plataforma no dio la clave de emisión")
		return
	}

	clave := b.Key
	d, err = s.db.UpdateDestination(ctx, s.cipher, id, store.DestinationPatch{RTMPURL: &b.IngestURL, Key: &clave})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := s.guardarEmision(ctx, *d, *acct, b); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.applyHot(r, *d)
	s.escribirDestino(ctx, w, http.StatusOK, id)
}

// escribirDestino relee el destino y lo escribe decorado. La relectura no sobra: la clave
// por API y la emisión se guardan en otra tabla, y el DTO las lee de la fila ya unida.
func (s *Server) escribirDestino(ctx context.Context, w http.ResponseWriter, status int, id int64) {
	d, err := s.db.DestinationByID(ctx, id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	dto := newDestinationDTO(*d, s.metricsFor(d.ID), s.logoETag(ctx, d.ID))
	s.decorar(ctx, &dto, *d, nil, nil)
	writeJSON(w, status, dto)
}

func (s *Server) handleGetBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	b, err := s.db.BroadcastFor(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newBroadcastDTO(*b))
}

func (s *Server) handleStartBroadcast(w http.ResponseWriter, r *http.Request) {
	s.transicionEmision(w, r, true)
}

func (s *Server) handleEndBroadcast(w http.ResponseWriter, r *http.Request) {
	s.transicionEmision(w, r, false)
}

// transicionEmision saca la emisión al aire o la cierra. Solo tiene sentido donde la
// emisión existe como objeto aparte del RTMP (YouTube): en Kick o Twitch el canal sale al
// aire en cuanto llega vídeo, y no hay nada que pulsar.
func (s *Server) transicionEmision(w http.ResponseWriter, r *http.Request, arrancar bool) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	b, err := s.db.BroadcastFor(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	acct, err := s.db.AccountForDestination(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusConflict, codeConflict, "este destino no tiene cuenta vinculada")
		return
	}
	p, tok, ok := s.plataformaDeCuenta(r.Context(), w, *acct)
	if !ok {
		return
	}
	bs, esProgramable := p.(platforms.BroadcastScheduler)
	if !esProgramable || b.BroadcastRef == "" {
		writeError(w, http.StatusConflict, codeConflict, "esta plataforma sale al aire sola")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), transicionTimeout)
	defer cancel()
	estado, kind, texto := store.BroadcastLive, "broadcast_started", "la emisión salió al aire en "
	if arrancar {
		err = bs.StartBroadcast(ctx, *acct, tok, b.BroadcastRef)
	} else {
		estado, kind, texto = store.BroadcastComplete, "broadcast_ended", "la emisión se cerró en "
		err = bs.EndBroadcast(ctx, *acct, tok, b.BroadcastRef)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, codeInternal, mensajePlataforma(err))
		return
	}
	if err := s.db.SetBroadcastStatus(ctx, id, estado); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.db.LogEvent(context.WithoutCancel(ctx), store.Event{DestinationID: &id, Level: store.LevelInfo, Kind: kind,
		Message: texto + nombresPlataforma[platforms.ID(acct.Platform)]})

	b.Status = estado
	writeJSON(w, http.StatusOK, newBroadcastDTO(*b))
}
