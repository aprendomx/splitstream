package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// maxTitle es el tope de Twitch: se recorta aquí para no recibir un 400 por un título
// una letra más largo de lo que la persona veía en el campo.
const maxTitle = 140

func (p *Provider) SetTitle(ctx context.Context, acct store.Account, token crypto.Secret, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("el título no puede estar vacío")
	}
	if r := []rune(title); len(r) > maxTitle {
		title = string(r[:maxTitle])
	}
	return p.patchChannel(ctx, acct, token, map[string]string{"title": title})
}

// SetCategory con id vacío quita la categoría: Twitch usa "0" para eso.
func (p *Provider) SetCategory(ctx context.Context, acct store.Account, token crypto.Secret, id string) error {
	if strings.TrimSpace(id) == "" {
		id = "0"
	}
	return p.patchChannel(ctx, acct, token, map[string]string{"game_id": id})
}

func (p *Provider) patchChannel(ctx context.Context, acct store.Account, token crypto.Secret, body map[string]string) error {
	if !p.Configured() {
		return platforms.ErrNoClientID
	}
	b, _ := json.Marshal(body)
	req, err := p.helix(ctx, http.MethodPatch, "/helix/channels?broadcaster_id="+url.QueryEscape(acct.ExternalID), token, bytes.NewReader(b))
	if err != nil {
		return err
	}
	_, err = p.do(req, http.StatusNoContent)
	return err
}

func (p *Provider) SearchCategories(ctx context.Context, token crypto.Secret, q string) ([]platforms.Category, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []platforms.Category{}, nil
	}
	req, err := p.helix(ctx, http.MethodGet, "/helix/search/categories?first=20&query="+url.QueryEscape(q), token, nil)
	if err != nil {
		return nil, err
	}
	body, err := p.do(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			BoxArtURL string `json:"box_art_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, errors.New("twitch: respuesta de categorías ilegible")
	}
	cats := make([]platforms.Category, 0, len(out.Data))
	for _, c := range out.Data {
		// La URL trae {width}x{height} literales.
		cats = append(cats, platforms.Category{ID: c.ID, Name: c.Name,
			BoxArtURL: strings.Replace(c.BoxArtURL, "{width}x{height}", boxArtSize, 1)})
	}
	return cats, nil
}
