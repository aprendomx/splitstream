package httpapi

import (
	"bufio"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// El idioma de una respuesta lo decide quien mira, no el proceso: viaja con la petición
// (Accept-Language) y se aplica solo a los `message` de error, que son texto para
// personas. El `code` es el contrato y no se toca (spec v0.13 §2 y §4).

type idioma string

const (
	idiomaES idioma = "es"
	idiomaEN idioma = "en"
)

// negociarIdioma lee Accept-Language: gana el primer idioma reconocido por orden de q
// descendente, y a igualdad de q el que aparece antes. Solo se conoce `en`; todo lo demás
// —incluido `*`— cae a `es`, que es el idioma en el que están escritos los mensajes.
func negociarIdioma(h string) idioma {
	type candidato struct {
		tag string
		q   float64
	}
	var cs []candidato
	for _, parte := range strings.Split(h, ",") {
		parte = strings.TrimSpace(parte)
		if parte == "" {
			continue
		}
		tag, resto, _ := strings.Cut(parte, ";")
		q := 1.0
		// El nombre del parámetro NO distingue mayúsculas (RFC 9110 §5.6.6): hay clientes
		// que mandan «Q=0.9». Un q= que no es un número se ignora y vale 1, que es lo que
		// dice el RFC para un parámetro mal formado: mejor atender la petición que
		// rechazarla por un decimal.
		if resto = strings.ToLower(strings.TrimSpace(resto)); strings.HasPrefix(resto, "q=") {
			if v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(resto, "q=")), 64); err == nil {
				q = v
			}
		}
		cs = append(cs, candidato{strings.ToLower(strings.TrimSpace(tag)), q})
	}
	// Estable: el orden de aparición desempata, que es lo que dice el RFC 9110 §12.5.4.
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].q > cs[j].q })
	for _, c := range cs {
		// q=0 significa «este idioma no, gracias».
		if c.q <= 0 {
			continue
		}
		switch {
		case c.tag == "en" || strings.HasPrefix(c.tag, "en-"):
			return idiomaEN
		case c.tag == "es" || strings.HasPrefix(c.tag, "es-"):
			return idiomaES
		}
	}
	return idiomaES
}

// respuestaConIdioma lleva el idioma negociado pegado al ResponseWriter.
//
// Va ahí y no en el contexto de la petición porque writeError solo recibe el writer: así
// los ~90 sitios de llamada no cambian ni tienen que acarrear el *http.Request.
type respuestaConIdioma struct {
	http.ResponseWriter
	lang idioma
}

// Unwrap deja que http.ResponseController llegue al ResponseWriter original (Flush,
// Hijack, SetWriteDeadline).
func (r *respuestaConIdioma) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush y Hijack se reexponen a mano porque una aserción directa —`w.(http.Flusher)` en
// webhook.go, `w.(http.Hijacker)` dentro de la librería de WebSockets— NO atraviesa un
// struct que solo embebe la interfaz http.ResponseWriter.
func (r *respuestaConIdioma) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *respuestaConIdioma) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// conIdioma envuelve el mux entero: negociar una vez por petición sale más barato que
// hacerlo en cada writeError, y deja el idioma disponible aunque el error se escriba desde
// una función que no ve la petición.
func conIdioma(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&respuestaConIdioma{
			ResponseWriter: w,
			lang:           negociarIdioma(r.Header.Get("Accept-Language")),
		}, r)
	})
}

// idiomaDe desenvuelve hasta encontrar el idioma. Devuelve `es` si no está envuelto, que
// es lo que pasa en los tests que llaman a un handler con un httptest.ResponseRecorder a
// pelo: siguen viendo el español de siempre.
func idiomaDe(w http.ResponseWriter) idioma {
	for w != nil {
		if r, ok := w.(*respuestaConIdioma); ok {
			return r.lang
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = u.Unwrap()
	}
	return idiomaES
}

// traducir devuelve msg en el idioma pedido. Prueba primero el literal exacto y luego las
// plantillas con {n}, de la más larga a la más corta; las partes variables (nombres,
// campos, valores) no se traducen. Sin entrada devuelve el original: nunca se pierde
// información, y el test de AST es quien se encarga de que eso no pase en producción.
func traducir(l idioma, msg string) string {
	if l != idiomaEN {
		return msg
	}
	if t, ok := traducciones[msg]; ok {
		return t
	}
	for _, p := range plantillas() {
		if vars, ok := p.casar(msg); ok {
			return p.aplicar(vars)
		}
	}
	return msg
}

// plantilla es una clave de `traducciones` con comodines {n} ya compilada a regexp.
type plantilla struct {
	clave   string
	re      *regexp.Regexp
	indices []int  // el {n} de cada grupo de captura, en orden de aparición
	destino string // el texto en inglés, todavía con sus {n}
	peso    int    // cuántos caracteres literales tiene: se prueban de más a menos
}

var (
	reComodin        = regexp.MustCompile(`\{(\d+)\}`)
	plantillasUnaVez sync.Once
	plantillasCache  []plantilla
)

// plantillas compila una sola vez las claves con comodín y las ordena por cantidad de
// texto literal, de más a menos: así «{0} no encontrado» nunca le gana a una clave más
// específica que también case. Por eso tampoco se escribe nunca una clave «{0}» a secas,
// que casaría con cualquier mensaje.
func plantillas() []plantilla {
	plantillasUnaVez.Do(func() {
		for clave, valor := range traducciones {
			ms := reComodin.FindAllStringSubmatchIndex(clave, -1)
			if len(ms) == 0 {
				continue
			}
			var patron strings.Builder
			var indices []int
			peso, ultimo := 0, 0
			patron.WriteString("^")
			for _, m := range ms {
				trozo := clave[ultimo:m[0]]
				patron.WriteString(regexp.QuoteMeta(trozo))
				peso += len(trozo)
				n, _ := strconv.Atoi(clave[m[2]:m[3]])
				indices = append(indices, n)
				// No greedy: con dos comodines seguidos, el primero se queda con lo
				// justo y el resto del patrón decide dónde cortar.
				patron.WriteString("(.+?)")
				ultimo = m[1]
			}
			cola := clave[ultimo:]
			patron.WriteString(regexp.QuoteMeta(cola))
			patron.WriteString("$")
			peso += len(cola)
			plantillasCache = append(plantillasCache, plantilla{
				clave: clave, re: regexp.MustCompile(patron.String()),
				indices: indices, destino: valor, peso: peso,
			})
		}
		// El desempate por clave hace el orden determinista: el recorrido de un map no lo
		// es, y un orden que cambia entre arranques sería un fallo imposible de repetir.
		sort.Slice(plantillasCache, func(i, j int) bool {
			if plantillasCache[i].peso != plantillasCache[j].peso {
				return plantillasCache[i].peso > plantillasCache[j].peso
			}
			return plantillasCache[i].clave < plantillasCache[j].clave
		})
	})
	return plantillasCache
}

func (p plantilla) casar(msg string) ([]string, bool) {
	m := p.re.FindStringSubmatch(msg)
	if m == nil {
		return nil, false
	}
	return m[1:], true
}

func (p plantilla) aplicar(vars []string) string {
	out := p.destino
	for i, n := range p.indices {
		if i >= len(vars) {
			break
		}
		out = strings.ReplaceAll(out, "{"+strconv.Itoa(n)+"}", vars[i])
	}
	return out
}
