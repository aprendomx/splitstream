package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

type webhookCreate struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Format   string `json:"format"`
	Secret   string `json:"secret"`
	MinLevel string `json:"min_level"`
	Enabled  bool   `json:"enabled"`
}

// webhookPatch usa punteros para distinguir "no vino" de "vino vacío": un secret vacío
// QUITA el secreto, y un secret ausente lo deja como está.
type webhookPatch struct {
	Name     *string `json:"name"`
	URL      *string `json:"url"`
	Format   *string `json:"format"`
	Secret   *string `json:"secret"`
	MinLevel *string `json:"min_level"`
	Enabled  *bool   `json:"enabled"`
}

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := s.db.ListWebhooks(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	out := make([]webhookDTO, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, newWebhookDTO(h))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var in webhookCreate
	if !decodeBody(w, r, &in) {
		return
	}
	h, err := s.db.CreateWebhook(r.Context(), s.cipher, store.NewWebhook{
		Name: in.Name, URL: in.URL, Format: store.WebhookFormat(in.Format),
		Secret: crypto.Secret(in.Secret), MinLevel: store.Level(in.MinLevel), Enabled: in.Enabled,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	w.Header().Set("Location", "/api/webhooks/"+strconv.FormatInt(h.ID, 10))
	writeJSON(w, http.StatusCreated, newWebhookDTO(*h))
}

func (s *Server) handlePatchWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	var in webhookPatch
	if !decodeBody(w, r, &in) {
		return
	}
	patch := store.WebhookPatch{Name: in.Name, URL: in.URL, Enabled: in.Enabled}
	if in.Format != nil {
		f := store.WebhookFormat(*in.Format)
		patch.Format = &f
	}
	if in.MinLevel != nil {
		l := store.Level(*in.MinLevel)
		patch.MinLevel = &l
	}
	if in.Secret != nil {
		sec := crypto.Secret(*in.Secret)
		patch.Secret = &sec
	}
	h, err := s.db.UpdateWebhook(r.Context(), s.cipher, id, patch)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newWebhookDTO(*h))
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if err := s.db.DeleteWebhook(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestWebhook manda un evento sintético, con reintentos y plazo, y responde 204 si
// llegó o 502 si no. No pasa por el bus: el usuario quiere la respuesta ahora.
func (s *Server) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	if s.webhooks == nil {
		writeError(w, http.StatusConflict, codeConflict, "los webhooks no están disponibles en este arranque")
		return
	}
	hooks, err := s.db.ListWebhooks(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	var hook *store.Webhook
	for i := range hooks {
		if hooks[i].ID == id {
			hook = &hooks[i]
		}
	}
	if hook == nil {
		s.writeStoreError(w, store.ErrWebhookNotFound)
		return
	}
	ev := store.Event{
		Level: store.LevelInfo, Kind: "webhook_test",
		Message:   "prueba de aviso desde Splitstream: si lees esto, el webhook funciona",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.webhooks.Send(r.Context(), *hook, ev); err != nil {
		writeError(w, http.StatusBadGateway, codeConflict, "no se pudo entregar: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
