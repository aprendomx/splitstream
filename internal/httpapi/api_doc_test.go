package httpapi

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// actualizar reescribe docs/api.md en vez de compararlo. Es un flag y no una variable de
// entorno para que `go test ./internal/httpapi/ -run APIContract -update` sea todo el
// procedimiento de regeneración.
var actualizar = flag.Bool("update", false, "reescribe docs/api.md con el contrato actual")

// dtosDocumentados son los tipos que viajan por la API: respuestas y cuerpos de petición.
// Documentarlos es lo mismo que comprobarlos, así que esta lista es a la vez la fuente de
// «Formas» en docs/api.md y la que recorre TestDTOFieldNamesAreSnakeCase.
//
// Añadir un tipo nuevo a la API sin apuntarlo aquí no rompe nada, pero lo deja fuera del
// contrato escrito; el sitio para apuntarlo es este.
var dtosDocumentados = []any{
	accountDTO{}, accountRefDTO{}, authStartDTO{}, authStartRequest{}, authStatusDTO{},
	broadcastDTO{}, broadcastRequest{}, capabilitiesDTO{}, chatMessageDTO{},
	destinationCreate{}, destinationDTO{}, destinationPatch{}, errorBody{}, errorDetail{},
	eventDTO{}, fromAccountRequest{}, healthDTO{}, ingestDTO{}, liveResultDTO{},
	liveTitleRequest{}, loginRequest{}, metricsDTO{}, panelDTO{}, platformDTO{}, probeDTO{},
	recordingDTO{}, recordingSettingsDTO{}, recordingSettingsPatch{}, recordingStatusDTO{},
	reorderRequest{}, rotateKeyRequest{}, rotateKeyResponse{}, sessionDTO{},
	sessionDetailDTO{}, sessionSummaryDTO{}, setupEstadoDTO{}, setupRequest{}, statusDTO{},
	testSkippedDTO{}, toggleAllRequest{}, updateDTO{}, webhookCreate{}, webhookDTO{},
	webhookPatch{},
}

// TestAPIContractDocIsCurrent: docs/api.md es el contrato público y se genera desde la
// tabla de rutas y los DTO, así que no puede quedarse atrás: cambiar una ruta o un campo
// sin regenerarlo rompe este test (spec v1.0 §5.1). `go test ./internal/httpapi/ -run
// APIContract -update` lo reescribe.
func TestAPIContractDocIsCurrent(t *testing.T) {
	srv, _ := newTestServer(t)
	got := generarDocAPI(srv.rutas(), dtosDocumentados)
	ruta := filepath.Join(raizDelRepo(t), "docs", "api.md")
	if *actualizar {
		if err := os.WriteFile(ruta, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("falta docs/api.md: genera con -update (%v)", err)
	}
	if string(want) != got {
		t.Errorf("docs/api.md no coincide con el contrato actual; regenera con -update.\n%s", diffLineas(string(want), got))
	}
}

// TestRutasNoRepiten: dos entradas con el mismo método y patrón harían panic al registrar
// en el mux, y el panic saldría al arrancar el servidor, no aquí. Mejor aquí.
func TestRutasNoRepiten(t *testing.T) {
	srv, _ := newTestServer(t)
	vistas := map[string]bool{}
	for _, r := range srv.rutas() {
		clave := r.Metodo + " " + r.Patron
		if vistas[clave] {
			t.Errorf("la ruta %q está dos veces en la tabla", clave)
		}
		vistas[clave] = true
		if r.Handler == nil {
			t.Errorf("la ruta %q no tiene handler", clave)
		}
		if r.Grupo == "" || r.Resumen == "" {
			t.Errorf("la ruta %q necesita grupo y resumen para docs/api.md", clave)
		}
	}
}

// TestRutasPublicasSonLasDeSiempre fija la lista de endpoints que NO piden sesión. No
// comprueba nada que el código no diga ya: existe para que añadir una ruta pública sea una
// decisión visible en el diff de este test, y no un `true` que se cuela en una tabla larga.
func TestRutasPublicasSonLasDeSiempre(t *testing.T) {
	quiero := []string{
		"GET /api/platforms/{p}/callback",
		"GET /api/setup",
		"GET /healthz",
		"POST /api/auth/login",
		"POST /api/auth/logout",
		"POST /api/platforms/kick/webhook",
		"POST /api/setup",
	}
	srv, _ := newTestServer(t)
	var tengo, conEnvoltorio []string
	for _, r := range srv.rutas() {
		if r.Publica {
			tengo = append(tengo, r.Metodo+" "+r.Patron)
		}
		if r.Envoltorio != nil {
			conEnvoltorio = append(conEnvoltorio, r.Metodo+" "+r.Patron)
		}
	}
	sort.Strings(tengo)
	if !reflect.DeepEqual(tengo, quiero) {
		t.Errorf("rutas públicas:\n tengo %q\nquiero %q\nsi es a propósito, actualiza la lista", tengo, quiero)
	}
	// /metrics no es pública, pero tampoco pasa por requireSession a secas: es la única
	// excepción, y se comprueba aquí para que una segunda no pase inadvertida.
	if !reflect.DeepEqual(conEnvoltorio, []string{"GET /metrics"}) {
		t.Errorf("rutas con autenticación propia: tengo %q, quiero [\"GET /metrics\"]", conEnvoltorio)
	}
}

// raizDelRepo. Copiado de internal/rtmpio (paquete distinto, seis líneas): los tests no
// pueden suponer desde qué directorio se les invoca, y docs/api.md vive en la raíz.
func raizDelRepo(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// gruposDeRutas es el orden en que docs/api.md presenta los grupos, con su título. Es fijo
// y a mano: ordenarlo alfabéticamente pondría "accounts" antes que "auth" y el documento
// dejaría de leerse de lo general a lo concreto. Un grupo que no esté aquí no se pierde:
// generarDocAPI lo saca al final.
var gruposDeRutas = []struct{ Clave, Titulo string }{
	{"auth", "Sesión del panel"},
	{"setup", "Configuración inicial"},
	{"health", "Salud"},
	{"metrics", "Métricas"},
	{"ingest", "Ingesta"},
	{"destinations", "Destinos"},
	{"live", "Estado y emisión en curso"},
	{"platforms", "Plataformas y autorización"},
	{"accounts", "Cuentas vinculadas"},
	{"sessions", "Historial de sesiones"},
	{"recordings", "Grabaciones"},
	{"chat", "Chat"},
	{"webhooks", "Webhooks de notificación"},
	{"backup", "Copia de seguridad"},
	{"ws", "Canales WebSocket"},
}

const cabeceraDocAPI = "# Contrato de la API\n" + `
Este documento lo genera ` + "`TestAPIContractDocIsCurrent`" + ` a partir de la tabla de rutas de
` + "`internal/httpapi/server.go`" + ` y de los tipos que viajan por la API, leídos por reflexión.
**No se edita a mano**: se regenera con

` + "```" + `
go test ./internal/httpapi/ -run APIContract -update
` + "```" + `

y el test falla mientras el archivo no coincida con el código, así que un cambio en la API
que no se refleje aquí no puede pasar de CI.

## Versionado

` + "`/api/`" + ` es la **v1** de esta API.

Son cambios **compatibles**, y entran sin más: añadir rutas, añadir campos —siempre con un
valor por defecto— y añadir valores a un enumerado que el panel tolere.

Son cambios **incompatibles**: quitar o renombrar rutas o campos, cambiar el tipo de un
campo y cambiar el significado de un ` + "`code`" + ` de error. Cualquiera de ellos exige publicar
` + "`/api/v2/`" + ` conviviendo con ` + "`/api/`" + ` durante al menos una versión menor, y dejar la decisión
escrita en el spec.

Todos los errores tienen la misma forma (` + "`errorBody`" + `): el cliente decide mirando el
` + "`code`" + `, que es un conjunto cerrado, y enseña el ` + "`message`" + `, que es para personas.

## Rutas

La columna **Sesión** dice qué hace falta para entrar: ` + "`no`" + ` es público, ` + "`sí`" + ` pide la cookie
del panel y ` + "`sí (o token)`" + ` acepta además el token del recolector de métricas.
`

// generarDocAPI escribe el Markdown del contrato: primero las rutas por grupo y luego las
// formas de los cuerpos. Es determinista —orden de grupos fijo, rutas por patrón y método,
// tipos por nombre, campos en orden de declaración— porque el test lo compara byte a byte.
func generarDocAPI(rutas []ruta, dtos []any) string {
	var b strings.Builder
	b.WriteString(cabeceraDocAPI)

	porGrupo := map[string][]ruta{}
	for _, r := range rutas {
		porGrupo[r.Grupo] = append(porGrupo[r.Grupo], r)
	}
	orden := make([]struct{ Clave, Titulo string }, 0, len(porGrupo))
	vistos := map[string]bool{}
	for _, g := range gruposDeRutas {
		if len(porGrupo[g.Clave]) > 0 {
			orden = append(orden, g)
			vistos[g.Clave] = true
		}
	}
	// Un grupo que nadie apuntó en gruposDeRutas sale igual, al final y por orden
	// alfabético: es preferible un título feo a una ruta que no aparece en el contrato.
	var sueltos []string
	for g := range porGrupo {
		if !vistos[g] {
			sueltos = append(sueltos, g)
		}
	}
	sort.Strings(sueltos)
	for _, g := range sueltos {
		orden = append(orden, struct{ Clave, Titulo string }{g, g})
	}

	for _, g := range orden {
		rs := porGrupo[g.Clave]
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].Patron != rs[j].Patron {
				return rs[i].Patron < rs[j].Patron
			}
			return rs[i].Metodo < rs[j].Metodo
		})
		fmt.Fprintf(&b, "\n### %s — %s\n\n", g.Clave, g.Titulo)
		b.WriteString("| Método | Ruta | Sesión | Qué hace |\n| --- | --- | --- | --- |\n")
		for _, r := range rs {
			fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", r.Metodo, r.Patron, sesionDeRuta(r), r.Resumen)
		}
	}

	b.WriteString("\n## Formas\n\nLos cuerpos de petición y de respuesta, leídos de los tipos Go.\n" +
		"**Opcional** marca los campos que pueden faltar o venir a `null`.\n")

	tipos := make([]reflect.Type, 0, len(dtos))
	for _, d := range dtos {
		tipos = append(tipos, reflect.TypeOf(d))
	}
	sort.Slice(tipos, func(i, j int) bool { return tipos[i].Name() < tipos[j].Name() })
	for _, rt := range tipos {
		fmt.Fprintf(&b, "\n### %s\n\n", rt.Name())
		b.WriteString("| Campo | Tipo | Opcional |\n| --- | --- | --- |\n")
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			nombre, opciones, _ := strings.Cut(f.Tag.Get("json"), ",")
			if nombre == "-" {
				continue
			}
			tipo, opcional := tipoLegible(f.Type)
			if strings.Contains(opciones, "omitempty") {
				opcional = true
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", nombre, tipo, siNo(opcional))
		}
	}
	return b.String()
}

// sesionDeRuta traduce a la columna «Sesión» lo que el bucle de routes() hace con la ruta.
func sesionDeRuta(r ruta) string {
	switch {
	case r.Publica:
		return "no"
	case r.Envoltorio != nil:
		return "sí (o token)"
	default:
		return "sí"
	}
}

func siNo(b bool) string {
	if b {
		return "sí"
	}
	return "no"
}

// tipoLegible pone un tipo Go en palabras que sirvan a quien escribe un cliente, y dice si
// el campo es opcional (los punteros lo son: viajan como `null`).
func tipoLegible(t reflect.Type) (string, bool) {
	switch {
	case t == reflect.TypeOf(time.Time{}):
		return "time (RFC 3339)", false
	case t == reflect.TypeOf(json.RawMessage{}):
		// Crudo a propósito: distingue el campo ausente, el `null` y el valor.
		return "json (crudo)", true
	}
	switch t.Kind() {
	case reflect.Pointer:
		nombre, _ := tipoLegible(t.Elem())
		return nombre, true
	case reflect.Slice, reflect.Array:
		nombre, _ := tipoLegible(t.Elem())
		return "[]" + nombre, false
	case reflect.Map:
		clave, _ := tipoLegible(t.Key())
		valor, _ := tipoLegible(t.Elem())
		return "map[" + clave + "]" + valor, false
	case reflect.Struct:
		if t.Name() != "" {
			return t.Name(), false
		}
		// Struct anónimo: no tiene nombre que citar, así que se enseñan sus campos.
		campos := make([]string, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			nombre, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			campos = append(campos, nombre)
		}
		return "objeto {" + strings.Join(campos, ", ") + "}", false
	default:
		return t.Kind().String(), false
	}
}

// diffLineas enseña las primeras diferencias entre lo que hay en el archivo y lo que el
// código dice hoy. Con 20 basta para ver qué cambió sin volcar el documento entero.
func diffLineas(quiero, tengo string) string {
	a, b := strings.Split(quiero, "\n"), strings.Split(tengo, "\n")
	var sb strings.Builder
	distintas := 0
	for i := 0; i < len(a) || i < len(b); i++ {
		var la, lb string
		if i < len(a) {
			la = a[i]
		}
		if i < len(b) {
			lb = b[i]
		}
		if la == lb {
			continue
		}
		distintas++
		if distintas > 20 {
			fmt.Fprintf(&sb, "… y más líneas distintas\n")
			break
		}
		fmt.Fprintf(&sb, "línea %d:\n  archivo: %s\n  código:  %s\n", i+1, la, lb)
	}
	return sb.String()
}
