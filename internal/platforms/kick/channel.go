package kick

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

func (p *Provider) SetTitle(ctx context.Context, _ store.Account, token crypto.Secret, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("el título no puede estar vacío")
	}
	return p.patchChannel(ctx, token, map[string]any{"stream_title": title})
}

// SetCategory con id vacío no quita la categoría: a diferencia de Twitch (game_id "0"),
// Kick no documenta una forma de dejar el canal sin categoría, así que se rechaza antes
// de llamar. Un id no numérico también se rechaza sin llamar: category_id va como
// entero en el cuerpo.
func (p *Provider) SetCategory(ctx context.Context, _ store.Account, token crypto.Secret, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("Kick no permite quitar la categoría")
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("Kick necesita un id de categoría numérico: %q", id)
	}
	return p.patchChannel(ctx, token, map[string]any{"category_id": n})
}

func (p *Provider) patchChannel(ctx context.Context, token crypto.Secret, body map[string]any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := p.api(ctx, http.MethodPatch, "/public/v1/channels", token, bytes.NewReader(b))
	if err != nil {
		return err
	}
	_, err = p.do(req, http.StatusNoContent)
	return err
}

// buscarCategoriasV1 usa GET /public/v1/categories?q=, el único endpoint de búsqueda de
// categorías que Kick documenta hoy. Kick lo tiene marcado como v1 deprecada: se aísla
// aquí para que, cuando publiquen la v2, el cambio sea sustituir esta función sin tocar
// SearchCategories ni a quien lo llama.
func (p *Provider) buscarCategoriasV1(ctx context.Context, token crypto.Secret, q string) ([]platforms.Category, error) {
	req, err := p.api(ctx, http.MethodGet, "/public/v1/categories?q="+url.QueryEscape(q), token, nil)
	if err != nil {
		return nil, err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			Thumbnail string `json:"thumbnail"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, errors.New("kick: respuesta de categorías ilegible")
	}
	cats := make([]platforms.Category, 0, len(out.Data))
	for _, c := range out.Data {
		cats = append(cats, platforms.Category{ID: strconv.Itoa(c.ID), Name: c.Name, BoxArtURL: c.Thumbnail})
	}
	return cats, nil
}

func (p *Provider) SearchCategories(ctx context.Context, token crypto.Secret, q string) ([]platforms.Category, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []platforms.Category{}, nil
	}
	return p.buscarCategoriasV1(ctx, token, q)
}

// IngestKey lee el canal propio (GET /public/v1/channels?broadcaster_user_id=) para
// devolver la URL RTMP y la clave de stream: en Kick la clave es del canal, no de una
// emisión creada aparte. Sin streamkey:read la API no incluye stream.key: el error lo
// explica sin filtrar nada de lo que sí llegó.
func (p *Provider) IngestKey(ctx context.Context, acct store.Account, token crypto.Secret) (string, crypto.Secret, error) {
	req, err := p.api(ctx, http.MethodGet, "/public/v1/channels?broadcaster_user_id="+url.QueryEscape(acct.ExternalID), token, nil)
	if err != nil {
		return "", "", err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return "", "", err
	}
	var out struct {
		Data []struct {
			Stream struct {
				URL string `json:"url"`
				Key string `json:"key"`
			} `json:"stream"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Data) == 0 {
		return "", "", errors.New("kick: respuesta de canales ilegible")
	}
	s := out.Data[0].Stream
	if s.Key == "" {
		return "", "", errors.New("Kick no devolvió la clave: ¿falta el permiso streamkey:read? Reconecta la cuenta")
	}
	return s.URL, crypto.Secret(s.Key), nil
}
