package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

type liveTitleRequest struct {
	Title        string  `json:"title"`
	CategoryID   *string `json:"category_id"`
	Destinations []int64 `json:"destinations"`
}

// maxDestinosPorPeticion acota POST /api/live/title: cada destino es una petición a la
// plataforma, y una lista larga tendría a la API ocupada minutos. Veinte pasa de sobra
// cualquier panel real.
const maxDestinosPorPeticion = 20

// liveTimeoutPorDefecto es el plazo por destino: sin él, una plataforma que no contesta
// dejaba colgada la petición entera y con ella los destinos que faltaban por procesar.
const liveTimeoutPorDefecto = 10 * time.Second

type liveResultDTO struct {
	DestinationID int64  `json:"destination_id"`
	OK            bool   `json:"ok"`
	Message       string `json:"message"`
}

// handleLiveTitle aplica título y/o categoría a cada destino que pueda. Devuelve un
// resultado por destino, nunca todo o nada: que YouTube falle no tiene que impedir que
// Twitch cambie.
func (s *Server) handleLiveTitle(w http.ResponseWriter, r *http.Request) {
	var in liveTitleRequest
	if !decodeBody(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" && in.CategoryID == nil {
		writeError(w, http.StatusBadRequest, codeInvalidInput, "manda un título, una categoría o las dos")
		return
	}
	if len(in.Destinations) == 0 {
		writeError(w, http.StatusBadRequest, codeInvalidInput, "elige al menos un destino")
		return
	}
	if len(in.Destinations) > maxDestinosPorPeticion {
		writeError(w, http.StatusBadRequest, codeInvalidInput, "como mucho 20 destinos por petición")
		return
	}
	// Se deduplica conservando el orden: un id repetido haría dos veces la misma llamada a
	// la plataforma y devolvería dos resultados para el mismo destino, que el panel no
	// sabría casar.
	vistos := make(map[int64]bool, len(in.Destinations))
	out := make([]liveResultDTO, 0, len(in.Destinations))
	for _, id := range in.Destinations {
		if vistos[id] {
			continue
		}
		vistos[id] = true
		out = append(out, s.aplicarEnDestino(r.Context(), id, in))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) aplicarEnDestino(ctx context.Context, id int64, in liveTitleRequest) liveResultDTO {
	// Plazo POR destino: los destinos se procesan en serie, así que sin esto una sola
	// plataforma colgada se llevaría por delante a las demás y a la petición entera.
	plazo := s.liveTimeout
	if plazo <= 0 {
		plazo = liveTimeoutPorDefecto
	}
	ctx, cancel := context.WithTimeout(ctx, plazo)
	defer cancel()

	res := liveResultDTO{DestinationID: id}
	d, err := s.db.DestinationByID(ctx, id)
	if err != nil {
		res.Message = "destino no encontrado"
		return res
	}
	var p platforms.Provider
	if s.platforms != nil {
		p, _ = s.platforms.Get(platforms.ID(d.Platform))
	}
	if p == nil {
		res.Message = d.Name + " no permite cambiar el título desde aquí"
		return res
	}
	acct, err := s.db.AccountForDestination(ctx, id)
	if err != nil {
		res.Message = d.Name + " no tiene cuenta vinculada"
		return res
	}
	if acct.Status != store.AccountStatusOK {
		res.Message = "la cuenta de " + acct.DisplayName + " necesita reconectarse"
		return res
	}
	if s.tokens == nil {
		res.Message = "sin gestor de tokens"
		return res
	}
	tok, err := s.tokens.Token(ctx, acct.ID)
	if err != nil {
		res.Message = "la cuenta de " + acct.DisplayName + " necesita reconectarse"
		return res
	}
	var cambios []string
	if in.Title != "" {
		ts, ok := p.(platforms.TitleSetter)
		if !ok {
			res.Message = d.Name + " no permite cambiar el título"
			return res
		}
		if err := ts.SetTitle(ctx, *acct, tok, in.Title); err != nil {
			res.Message = mensajePlataforma(err)
			return res
		}
		cambios = append(cambios, "título")
	}
	if in.CategoryID != nil {
		cs, ok := p.(platforms.CategorySetter)
		if !ok {
			res.Message = d.Name + " no permite cambiar la categoría"
			return res
		}
		if err := cs.SetCategory(ctx, *acct, tok, *in.CategoryID); err != nil {
			res.Message = mensajePlataforma(err)
			return res
		}
		cambios = append(cambios, "categoría")
	}
	res.OK = true
	res.Message = strings.Join(cambios, " y ") + " aplicados en " + d.Name
	s.db.LogEvent(context.WithoutCancel(ctx), store.Event{DestinationID: &id, Level: store.LevelInfo, Kind: "channel_updated",
		Message: "canal actualizado (" + strings.Join(cambios, ", ") + ") en " + d.Name})
	return res
}

// mensajePlataforma convierte un error del proveedor en algo que leer. Nunca lleva el
// token: los proveedores no lo ponen en sus errores.
func mensajePlataforma(err error) string {
	switch {
	case errors.Is(err, platforms.ErrUnauthorized):
		return "la plataforma rechazó la cuenta; reconéctala"
	case errors.Is(err, platforms.ErrRateLimited):
		return "la plataforma pide esperar un momento"
	case errors.Is(err, context.DeadlineExceeded):
		// Se agotó el plazo por destino: no es culpa de la cuenta, así que no se sugiere
		// reconectarla.
		return "la plataforma tardó demasiado"
	}
	return "la plataforma respondió con un error: " + err.Error()
}

func (s *Server) handleSearchCategories(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []platforms.Category{})
		return
	}
	var p platforms.Provider
	if s.platforms != nil {
		p, _ = s.platforms.Get(platforms.Twitch)
	}
	cs, ok := p.(platforms.CategorySetter)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "twitch no está disponible")
		return
	}
	cuentas, err := s.db.Accounts(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	var acct *store.Account
	for i := range cuentas {
		if cuentas[i].Platform == store.PlatformTwitch && cuentas[i].Status == store.AccountStatusOK {
			acct = &cuentas[i]
			break
		}
	}
	if acct == nil || s.tokens == nil {
		writeError(w, http.StatusConflict, codeConflict, "conecta una cuenta de Twitch para buscar categorías")
		return
	}
	tok, err := s.tokens.Token(r.Context(), acct.ID)
	if err != nil {
		writeError(w, http.StatusConflict, codeConflict, "la cuenta de Twitch necesita reconectarse")
		return
	}
	cats, err := cs.SearchCategories(r.Context(), tok, q)
	if err != nil {
		writeError(w, http.StatusBadGateway, codeInternal, mensajePlataforma(err))
		return
	}
	if cats == nil {
		cats = []platforms.Category{}
	}
	writeJSON(w, http.StatusOK, cats)
}
