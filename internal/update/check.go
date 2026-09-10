// Package update consulta si hay una versión más nueva publicada. Solo avisa: nunca
// descarga ni instala nada, y no manda más que la versión que corre en el User-Agent.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LatestURL es la release más reciente del repositorio. La API pública de GitHub no
// necesita token para esto; su cuota (60 por hora por IP) sobra para una consulta al día.
const LatestURL = "https://api.github.com/repos/aprendomx/splitstream/releases/latest"

// Info es lo que sabe el checker de la última release.
type Info struct {
	Latest    string
	URL       string
	Available bool
}

// Checker compara la versión del binario con la última etiqueta publicada.
type Checker struct {
	// Current es la versión del binario. Si no es vX.Y.Z (dev, docker, -dirty), el
	// checker no consulta nada: no hay con qué comparar y avisar sería ruido.
	Current string
	// URL sustituye a LatestURL en los tests.
	URL    string
	Client *http.Client
	Logger *slog.Logger

	mu   sync.Mutex
	info Info
}

// Latest devuelve el último resultado. Vacío hasta la primera consulta con éxito.
func (c *Checker) Latest() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Run consulta tras initialDelay y luego cada interval, hasta que ctx termine. onNew se
// llama la primera vez que se ve una versión nueva y cada vez que esa versión cambie.
// Ningún fallo sale de aquí: se loguea a debug y se reintenta en la siguiente vuelta.
func (c *Checker) Run(ctx context.Context, initialDelay, interval time.Duration, onNew func(Info)) {
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if _, ok := ParseVersion(c.Current); !ok {
		logger.Debug("aviso de versión desactivado: el binario no lleva versión", "version", c.Current)
		return
	}
	// Un interval de 0 o negativo dejaría el Reset del bucle disparando sin pausa: este es
	// el único componente que sale a internet por su cuenta, y girar así sería martillear
	// la API de GitHub hasta agotar su cuota (60 por hora e IP). Se cae al día.
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	t := time.NewTimer(initialDelay)
	defer t.Stop()
	var anunciada string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		info, err := c.Check(ctx)
		if err != nil {
			logger.Debug("no se pudo consultar la última versión", "err", err)
		} else if info.Available && info.Latest != anunciada && onNew != nil {
			anunciada = info.Latest
			onNew(info)
		}
		t.Reset(interval)
	}
}

// Check hace una sola consulta. Exportado para probarlo sin esperas.
func (c *Checker) Check(ctx context.Context) (Info, error) {
	actual, ok := ParseVersion(c.Current)
	if !ok {
		return Info{}, fmt.Errorf("la versión del binario %q no es comparable", c.Current)
	}
	url := c.URL
	if url == "" {
		url = LatestURL
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// La versión, y nada más: es lo único que hace falta para que quien mire los logs de
	// GitHub sepa qué versiones hay en uso, y no identifica a nadie.
	req.Header.Set("User-Agent", "splitstream/"+c.Current)

	resp, err := client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("GitHub respondió %d", resp.StatusCode)
	}
	var cuerpo struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&cuerpo); err != nil {
		return Info{}, fmt.Errorf("respuesta inesperada: %w", err)
	}
	// Una pre-release o una etiqueta rara no es una versión que anunciar: no es un
	// error (la consulta salió bien) pero tampoco hay nada disponible. Se guarda igual,
	// para que Latest() refleje siempre la última consulta y no una anterior ya vieja.
	info := Info{Latest: cuerpo.TagName, URL: cuerpo.HTMLURL}
	if ultima, ok := ParseVersion(cuerpo.TagName); ok {
		info.Available = newer(ultima, actual)
	}
	c.mu.Lock()
	c.info = info
	c.mu.Unlock()
	return info, nil
}

// ParseVersion lee "vX.Y.Z" o "X.Y.Z" en tres enteros. Cualquier sufijo (-rc1, -dirty)
// lo hace incomparable a propósito: no es una release.
func ParseVersion(s string) ([3]int, bool) {
	var v [3]int
	partes := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(partes) != 3 {
		return v, false
	}
	for i, p := range partes {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
