package httpapi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

func TestNegociarIdioma(t *testing.T) {
	casos := map[string]idioma{
		"": idiomaES, "es": idiomaES, "fr": idiomaES, "en": idiomaEN, "en-US": idiomaEN,
		"en-GB,en;q=0.9,es;q=0.8": idiomaEN, "es-MX,en;q=0.5": idiomaES,
		"fr-FR,en;q=0.7,de;q=0.6": idiomaEN, "*": idiomaES, "EN": idiomaEN,
		"en;q=0": idiomaES, "es;q=0.1,en;q=0.9": idiomaEN,
		// El nombre del parámetro no distingue mayúsculas (RFC 9110 §5.6.6).
		"es;Q=0.1,en;Q=0.9": idiomaEN, "en;Q=0": idiomaES,
		// Entradas rotas: ni pánico ni 500, se atiende en español o con q=1.
		"en;q=abc": idiomaEN, ",,": idiomaES, "en;q=": idiomaEN, ";;;": idiomaES,
		"  ,  en  ;  q = 0.5  ": idiomaEN,
		// Un q que parsea pero no es un qvalue (RFC 9110 §12.4.2: 0..1) no compite: el
		// nan envenenaría el orden entero, y el 5 o el -1 no dicen qué se quería.
		"en;q=nan,es": idiomaES, "en;q=inf,es": idiomaES, "en;q=5,es": idiomaES,
		"en;q=-1,es": idiomaES, "en;q=nan": idiomaES, "es;q=nan,en": idiomaEN,
	}
	for in, want := range casos {
		if got := negociarIdioma(in); got != want {
			t.Errorf("negociarIdioma(%q) = %q, quería %q", in, got, want)
		}
	}
}

func TestTraducirLiteralPlantillaYSinEntrada(t *testing.T) {
	if got := traducir(idiomaEN, "destino no encontrado"); got != "destination not found" {
		t.Errorf("literal: %q", got)
	}
	if got := traducir(idiomaEN, "limit debe ser un número"); got != "limit must be a number" {
		t.Errorf("plantilla: %q", got)
	}
	if got := traducir(idiomaEN, "la cuenta de Ana necesita reconectarse"); got != "the account Ana needs to be reconnected" {
		t.Errorf("plantilla con nombre: %q", got)
	}
	if got := traducir(idiomaEN, "texto que no existe"); got != "texto que no existe" {
		t.Errorf("sin entrada debe devolver el original: %q", got)
	}
	if got := traducir(idiomaES, "destino no encontrado"); got != "destino no encontrado" {
		t.Errorf("es no traduce: %q", got)
	}
}

// TestAplicarNoTocaLoQueYaSustituyo: si el valor de {0} —un nombre de destino, que lo
// escribe quien usa el panel— lleva dentro un «{1}» literal, ese texto es suyo y sale tal
// cual; encadenar ReplaceAll lo habría tomado por un comodín en la pasada siguiente.
func TestAplicarNoTocaLoQueYaSustituyo(t *testing.T) {
	const quiere = "the account is on {1} and the destination on Twitch"
	if got := traducir(idiomaEN, "la cuenta es de {1} y el destino de Twitch"); got != quiere {
		t.Errorf("aplicar = %q, quería %q", got, quiere)
	}
}

// escritorConReadFrom es un ResponseWriter que sabe hacer ReadFrom y lleva la cuenta: así
// se ve si el envoltorio de idioma delega o si se lo come.
type escritorConReadFrom struct {
	http.ResponseWriter
	veces int
}

func (e *escritorConReadFrom) ReadFrom(src io.Reader) (int64, error) {
	e.veces++
	return io.Copy(e.ResponseWriter, src)
}

// TestRespuestaConIdiomaConservaReadFrom: sin esto, http.ServeContent sobre un *os.File
// —una grabación, el respaldo— deja de usar el sendfile del sistema y copia el archivo
// entero a mano mientras el relay emite.
func TestRespuestaConIdiomaConservaReadFrom(t *testing.T) {
	t.Run("delega cuando el original sabe", func(t *testing.T) {
		rec := httptest.NewRecorder()
		espia := &escritorConReadFrom{ResponseWriter: rec}
		var w http.ResponseWriter = &respuestaConIdioma{ResponseWriter: espia, lang: idiomaEN}
		rf, ok := w.(io.ReaderFrom)
		if !ok {
			t.Fatal("el envoltorio esconde io.ReaderFrom")
		}
		n, err := rf.ReadFrom(strings.NewReader("hola"))
		if err != nil || n != 4 {
			t.Fatalf("ReadFrom = %d, %v", n, err)
		}
		if espia.veces != 1 {
			t.Errorf("el original recibió %d ReadFrom, quería 1", espia.veces)
		}
		if rec.Body.String() != "hola" {
			t.Errorf("cuerpo = %q", rec.Body.String())
		}
	})
	t.Run("copia cuando el original no sabe", func(t *testing.T) {
		// httptest.ResponseRecorder no implementa io.ReaderFrom: se cae a io.Copy.
		rec := httptest.NewRecorder()
		w := &respuestaConIdioma{ResponseWriter: rec, lang: idiomaES}
		n, err := w.ReadFrom(strings.NewReader("adiós"))
		if err != nil || int(n) != len("adiós") {
			t.Fatalf("ReadFrom = %d, %v", n, err)
		}
		if rec.Body.String() != "adiós" {
			t.Errorf("cuerpo = %q", rec.Body.String())
		}
	})
}

// TestNingunaPlantillaEsDemasiadoGenerica protege la mesa de las plantillas de sí misma:
// una clave que apenas tenga texto literal casaría con mensajes que no son suyos, y el
// cliente en inglés leería una frase equivocada en vez del español.
func TestNingunaPlantillaEsDemasiadoGenerica(t *testing.T) {
	for _, p := range plantillas() {
		if p.peso < 8 {
			t.Errorf("plantilla demasiado genérica (%d caracteres literales): %q", p.peso, p.clave)
		}
	}
}

// TestTodoMensajeDeErrorTieneTraduccion recorre el código de httpapi y del store y exige
// que cada literal que puede llegar al cliente tenga entrada en `traducciones` (o case con
// una plantilla). Quien añade un mensaje sin traducción rompe este test, no el panel de
// alguien en inglés.
//
// «Llegar al cliente» no es solo writeError: los DTO con campo Message —el resultado por
// destino de /api/live/title, el estado de un flujo de autorización, el diagnóstico de
// «probar destino»— son texto para personas y se traducen igual (spec v0.13 §3.3).
//
// El recolector resuelve también las variables locales asignadas con un literal. Si el
// argumento de mensaje de un writeError tiene una forma que el recolector NO entiende
// —una variable que no puede seguir, una llamada a una función que no está en sus listas,
// un campo de struct, un índice de mapa—, el test falla diciendo el archivo y la línea en
// vez de callarse: un mensaje que el recolector no ve es un mensaje que nadie traducirá, y
// el fallo silencioso sería justo el agujero que este test existe para tapar. Las formas
// que sí se aceptan, y por qué se acepta cada una, están en formaReconocida. Si algún día
// hace falta un mensaje compuesto de verdad, se saca a una función y se añade a
// funcionesDeMensaje.
func TestTodoMensajeDeErrorTieneTraduccion(t *testing.T) {
	literales := append(literalesDeWriteError(t, "."), literalesDeErroresDelStore(t, "../store")...)
	if len(literales) < 80 {
		t.Fatalf("solo se encontraron %d literales: el recolector está roto", len(literales))
	}
	t.Logf("%d mensajes recogidos por AST", len(literales))
	var faltan []string
	for _, l := range literales {
		if traducir(idiomaEN, l.texto) == l.texto {
			t.Errorf("%s: sin traducción: %q", l.pos, l.texto)
			faltan = append(faltan, "\t"+strconv.Quote(l.texto)+": "+strconv.Quote(l.texto)+",")
		}
	}
	// El volcado es para la siguiente persona: se pega en i18n_en.go y se redacta el
	// inglés encima, en vez de ir cazando el mensaje que falta uno a uno.
	if len(faltan) > 0 {
		sort.Strings(faltan)
		t.Logf("pendientes de traducir:\n%s", strings.Join(faltan, "\n"))
	}
}

// literalDeError es un mensaje encontrado en el código, con dónde estaba para poder ir.
type literalDeError struct {
	pos   string
	texto string
}

// paqueteDe parsea los .go de un directorio saltándose los tests: un mensaje que solo
// existe en un test no le llega a nadie.
func paqueteDe(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	entradas, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", dir, err)
	}
	var archivos []*ast.File
	for _, e := range entradas {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("no se pudo parsear %s: %v", n, err)
		}
		archivos = append(archivos, f)
	}
	if len(archivos) == 0 {
		t.Fatalf("ningún archivo en %s", dir)
	}
	return fset, archivos
}

// reComodinVerbo reconoce los verbos de fmt para cambiarlos por {n}.
var reComodinVerbo = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)

// envoltoriosTransparentes son las funciones que no cambian el texto que reciben: se mira
// lo de dentro. `textoSeguro` solo lo limpia y `traducir` es, precisamente, esto.
var envoltoriosTransparentes = map[string]bool{"textoSeguro": true, "traducir": true}

// reconstructor arma el mensaje que verá el cliente a partir de una expresión. Los trozos
// literales se conservan y cada parte que solo se conoce en ejecución (un nombre, un
// campo, un valor) se convierte en el comodín {n}.
type reconstructor struct {
	// locales son las variables de la función que se está mirando cuyo valor es un
	// literal: así `msg := "…"; writeError(…, msg)` se ve igual que el literal.
	locales map[string]string
}

func (rc reconstructor) texto(e ast.Expr) (string, bool) {
	n := 0
	return rc.rec(e, &n)
}

func comodin(n *int) string {
	s := "{" + strconv.Itoa(*n) + "}"
	*n++
	return s
}

func (rc reconstructor) rec(e ast.Expr, n *int) (string, bool) {
	switch v := e.(type) {
	case *ast.ParenExpr:
		return rc.rec(v.X, n)
	case *ast.Ident:
		s, ok := rc.locales[v.Name]
		return s, ok
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		izq, okI := rc.rec(v.X, n)
		if !okI {
			izq = comodin(n)
		}
		der, okD := rc.rec(v.Y, n)
		if !okD {
			der = comodin(n)
		}
		return izq + der, okI || okD
	case *ast.CallExpr:
		if esLlamadaA(v.Fun, "fmt", "Sprintf") && len(v.Args) > 0 {
			f, ok := rc.rec(v.Args[0], n)
			if !ok {
				return "", false
			}
			return sustituirVerbos(f, n), true
		}
		if id, ok := v.Fun.(*ast.Ident); ok && envoltoriosTransparentes[id.Name] && len(v.Args) > 0 {
			// traducir(lang, msg): el mensaje es el último argumento.
			return rc.rec(v.Args[len(v.Args)-1], n)
		}
	}
	return "", false
}

// sustituirVerbos cambia cada %s/%d/%q/%v por {n} y deja %% como el % que es.
func sustituirVerbos(f string, n *int) string {
	return reComodinVerbo.ReplaceAllStringFunc(f, func(m string) string {
		if m == "%%" {
			return "%"
		}
		return comodin(n)
	})
}

func esLlamadaA(fun ast.Expr, paquete, nombre string) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != nombre {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == paquete
}

// esMetodo reconoce `algo.nombre(…)` sin mirar el receptor.
func esMetodo(fun ast.Expr, nombre string) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == nombre
}

// formaReconocida dice si el recolector entiende la forma de un argumento de mensaje. No
// es lo mismo que poder reconstruir el texto: hay formas que se aceptan precisamente
// porque el texto se recoge por otro sitio. Las aceptadas son:
//
//   - un literal de cadena, o una suma de literales y partes variables;
//   - una variable local que se asignó una sola vez con un literal;
//   - fmt.Sprintf(formato, …) con el formato literal;
//   - los envoltoriosTransparentes (textoSeguro, traducir), que no cambian el texto;
//   - una llamada a una función de funcionesDeMensaje, cuyos `return` ya se recogen;
//   - cualquier método .Error(), cuyo texto entra por el recolector del store.
//
// Todo lo demás —una llamada a una función que no está en las listas, un campo de struct,
// un índice de mapa— es un mensaje que nadie va a traducir, y el test falla diciendo
// dónde. Callarse sería justo el agujero que este test existe para tapar.
func formaReconocida(rc reconstructor, e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.ParenExpr:
		return formaReconocida(rc, v.X)
	case *ast.BasicLit:
		return v.Kind == token.STRING
	case *ast.Ident:
		_, ok := rc.locales[v.Name]
		return ok
	case *ast.BinaryExpr:
		// Una suma vale si al menos un lado aporta texto conocido: el otro será un {n}.
		return v.Op == token.ADD && (formaReconocida(rc, v.X) || formaReconocida(rc, v.Y))
	case *ast.CallExpr:
		if esLlamadaA(v.Fun, "fmt", "Sprintf") {
			return len(v.Args) > 0 && formaReconocida(rc, v.Args[0])
		}
		if esMetodo(v.Fun, "Error") {
			return true
		}
		id, ok := v.Fun.(*ast.Ident)
		if !ok {
			return false
		}
		if funcionesDeMensaje[id.Name] {
			return true
		}
		return envoltoriosTransparentes[id.Name] && len(v.Args) > 0 && formaReconocida(rc, v.Args[len(v.Args)-1])
	}
	return false
}

// funcionesDeMensaje componen un texto para personas: sus `return` son mensajes aunque no
// pasen por writeError en el mismo sitio.
var funcionesDeMensaje = map[string]bool{
	"mensajePlataforma": true, "mensajeTitulo": true, "mensajeCambios": true, "probeMessage": true,
}

// funcionesQueAsignanMensaje: dentro de ellas, cada `algo.Message = …` es texto que sale
// por writeJSON hacia una persona.
var funcionesQueAsignanMensaje = map[string]bool{"aplicarEnDestino": true}

// dtosConMensaje son los DTO cuyo campo Message se traduce antes de salir (spec §3.3). Se
// listan por nombre para NO recoger de paso los Message de store.Event, que son registro
// interno y se quedan en español.
var dtosConMensaje = map[string]bool{
	"liveResultDTO": true, "authStatusDTO": true, "testSkippedDTO": true, "probeDTO": true,
}

// literalesLocales devuelve las variables de una función asignadas con un texto que se
// conoce entero en tiempo de compilación. Una variable asignada dos veces con textos
// distintos se descarta: no se sabe cuál llega al writeError.
func literalesLocales(d *ast.FuncDecl) map[string]string {
	locales := map[string]string{}
	dudosas := map[string]bool{}
	ast.Inspect(d, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignStmt)
		if !ok || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
			return true
		}
		id, ok := a.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		s, ok := reconstructor{}.texto(a.Rhs[0])
		if !ok || strings.Contains(s, "{0}") {
			dudosas[id.Name] = true
			return true
		}
		if anterior, visto := locales[id.Name]; visto && anterior != s {
			dudosas[id.Name] = true
		}
		locales[id.Name] = s
		return true
	})
	for n := range dudosas {
		delete(locales, n)
	}
	return locales
}

// literalesDeWriteError recoge, de un paquete HTTP: el cuarto argumento de cada
// writeError y de cada terminar(), los `return` de las funciones que componen mensajes,
// las asignaciones a .Message, el campo Message de los DTO que se traducen y el texto de
// las variables de paquete errXxx.
func literalesDeWriteError(t *testing.T, dir string) []literalDeError {
	t.Helper()
	fset, archivos := paqueteDe(t, dir)
	var out []literalDeError
	añadir := func(p token.Pos, rc reconstructor, e ast.Expr) {
		if s, ok := rc.texto(e); ok && strings.TrimSpace(s) != "" {
			out = append(out, literalDeError{pos: fset.Position(p).String(), texto: s})
		}
	}
	for _, f := range archivos {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, nombre := range vs.Names {
						if !strings.HasPrefix(nombre.Name, "err") || i >= len(vs.Values) {
							continue
						}
						c, ok := vs.Values[i].(*ast.CallExpr)
						if !ok || !esLlamadaA(c.Fun, "errors", "New") || len(c.Args) != 1 {
							continue
						}
						añadir(nombre.Pos(), reconstructor{}, c.Args[0])
					}
				}
			case *ast.FuncDecl:
				rc := reconstructor{locales: literalesLocales(d)}
				devuelveMensaje := funcionesDeMensaje[d.Name.Name]
				asignaMensaje := funcionesQueAsignanMensaje[d.Name.Name]
				ast.Inspect(d, func(n ast.Node) bool {
					switch v := n.(type) {
					case *ast.ReturnStmt:
						if devuelveMensaje {
							for _, r := range v.Results {
								añadir(r.Pos(), rc, r)
							}
						}
					case *ast.AssignStmt:
						if !asignaMensaje || len(v.Lhs) != 1 || len(v.Rhs) != 1 {
							return true
						}
						if sel, ok := v.Lhs[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "Message" {
							añadir(v.Rhs[0].Pos(), rc, v.Rhs[0])
						}
					case *ast.CompositeLit:
						id, ok := v.Type.(*ast.Ident)
						if !ok || !dtosConMensaje[id.Name] {
							return true
						}
						for _, el := range v.Elts {
							kv, ok := el.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Message" {
								añadir(kv.Value.Pos(), rc, kv.Value)
							}
						}
					case *ast.CallExpr:
						// terminar(f, status, acct, msg) deja el mensaje del flujo de
						// autorización, que sale por authStatusDTO.
						if esMetodo(v.Fun, "terminar") && len(v.Args) == 4 {
							añadir(v.Args[3].Pos(), rc, v.Args[3])
							return true
						}
						id, ok := v.Fun.(*ast.Ident)
						if !ok || id.Name != "writeError" || len(v.Args) != 4 {
							return true
						}
						arg := v.Args[3]
						// Una forma que el recolector no entiende NO se calla: ver el
						// comentario de TestTodoMensajeDeErrorTieneTraduccion.
						if ident, ok := arg.(*ast.Ident); ok {
							if _, resuelta := rc.locales[ident.Name]; !resuelta {
								t.Errorf("argumento no literal en %s: writeError recibe la variable %q y el recolector no puede seguirla; saca el mensaje a un literal o a una función de funcionesDeMensaje",
									fset.Position(arg.Pos()), ident.Name)
								return true
							}
						} else if !formaReconocida(rc, arg) {
							t.Errorf("argumento no reconocido en %s: el recolector no sabe leer esta forma y el mensaje se quedaría sin traducir; ver formaReconocida para las formas que se aceptan",
								fset.Position(arg.Pos()))
							return true
						}
						// err.Error() y mensajePlataforma(err) sí se ignoran: sus textos
						// entran por el store y por las funciones de mensaje.
						añadir(arg.Pos(), rc, arg)
					}
					return true
				})
			}
		}
	}
	return out
}

// constructoresDelStore son las tres funciones con las que el store marca la clase de un
// error. Su argumento es, literalmente, lo que lee quien usa la API.
var constructoresDelStore = map[string]bool{"notFound": true, "invalidInput": true, "conflict": true}

// literalesDeErroresDelStore recoge los mensajes del store: los de notFound/invalidInput/
// conflict, el texto propio de los tres centinelas y los fmt.Errorf("%w: …") que le pegan
// un sufijo a un error de paquete (ahí el cliente lee las dos partes juntas).
func literalesDeErroresDelStore(t *testing.T, dir string) []literalDeError {
	t.Helper()
	fset, archivos := paqueteDe(t, dir)
	var out []literalDeError
	añadir := func(p token.Pos, texto string) {
		if strings.TrimSpace(texto) != "" {
			out = append(out, literalDeError{pos: fset.Position(p).String(), texto: texto})
		}
	}

	// Primera pasada: el texto de cada variable de paquete que es un error clasificado,
	// para poder componer los fmt.Errorf("%w: …") que la envuelven.
	textoDeVar := map[string]string{}
	for _, f := range archivos {
		for _, decl := range f.Decls {
			d, ok := decl.(*ast.GenDecl)
			if !ok || d.Tok != token.VAR {
				continue
			}
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, nombre := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					c, ok := vs.Values[i].(*ast.CallExpr)
					if !ok || len(c.Args) != 1 {
						continue
					}
					id, ok := c.Fun.(*ast.Ident)
					esConstructor := ok && constructoresDelStore[id.Name]
					if !esConstructor && !esLlamadaA(c.Fun, "errors", "New") {
						continue
					}
					if s, ok := (reconstructor{}).texto(c.Args[0]); ok {
						textoDeVar[nombre.Name] = s
					}
				}
			}
		}
	}
	// Los tres centinelas tienen texto propio y salen tal cual cuando nadie envuelve.
	for _, n := range []string{"ErrNotFound", "ErrInvalidInput", "ErrConflict"} {
		if s, ok := textoDeVar[n]; ok {
			añadir(token.NoPos, s)
		}
	}

	for _, f := range archivos {
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := c.Fun.(*ast.Ident); ok && constructoresDelStore[id.Name] && len(c.Args) == 1 {
				if s, ok := (reconstructor{}).texto(c.Args[0]); ok {
					añadir(c.Pos(), s)
				}
				return true
			}
			if esLlamadaA(c.Fun, "fmt", "Errorf") && len(c.Args) >= 2 {
				formato, ok := c.Args[0].(*ast.BasicLit)
				if !ok || formato.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(formato.Value)
				if err != nil || !strings.HasPrefix(s, "%w: ") {
					return true
				}
				envuelto, ok := c.Args[1].(*ast.Ident)
				if !ok {
					return true
				}
				base, ok := textoDeVar[envuelto.Name]
				if !ok {
					return true
				}
				k := 0
				añadir(c.Pos(), base+": "+sustituirVerbos(strings.TrimPrefix(s, "%w: "), &k))
			}
			return true
		})
	}
	return out
}

// pedirConIdioma hace una petición por el Handler completo —el único camino por el que
// pasa el middleware— y devuelve el cuerpo.
func pedirConIdioma(t *testing.T, ts *httptest.Server, cookies []*http.Cookie, metodo, ruta, lang, body string) []byte {
	t.Helper()
	var cuerpo io.Reader
	if body != "" {
		cuerpo = strings.NewReader(body)
	}
	req, err := http.NewRequest(metodo, ts.URL+ruta, cuerpo)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestAcceptLanguageTraduceLosErrores es la prueba de que las piezas encajan de punta a
// punta: middleware, negociación, writeStoreError y tabla.
func TestAcceptLanguageTraduceLosErrores(t *testing.T) {
	srv, _ := newTestServer(t)
	cookies := login(t, srv)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	pedir := func(lang string) errorBody {
		var body errorBody
		b := pedirConIdioma(t, ts, cookies, http.MethodPatch, "/api/destinations/999999", lang, `{"name":"x"}`)
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatalf("respuesta ilegible: %v — %s", err, b)
		}
		return body
	}
	if b := pedir("en"); b.Error.Code != codeNotFound || !strings.Contains(b.Error.Message, "not found") {
		t.Errorf("en: %+v", b)
	}
	if b := pedir(""); b.Error.Code != codeNotFound || !strings.Contains(b.Error.Message, "no encontrado") {
		t.Errorf("es: %+v", b)
	}
	if b := pedir("fr"); !strings.Contains(b.Error.Message, "no encontrado") {
		t.Errorf("fr cae a es: %+v", b)
	}
}

// TestAcceptLanguageTraduceLosMensajesDeLosDTO cubre los `message` que NO salen por
// writeError: el resultado por destino de /api/live/title, el estado de un flujo de
// autorización y el «no hay nada que probar» de probar destino (spec v0.13 §3.3).
func TestAcceptLanguageTraduceLosMensajesDeLosDTO(t *testing.T) {
	srv, db, cookies := servidorPlataformas(t, &fakeProvider{configured: true})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx := t.Context()

	t.Run("live", func(t *testing.T) {
		sinCuenta, err := db.CreateDestination(ctx, srv.cipher, store.NewDestination{
			Name: "Canal", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		cuerpo := `{"title":"Hola","destinations":[` + itoa(sinCuenta.ID) + `]}`
		var res []liveResultDTO
		b := pedirConIdioma(t, ts, cookies, http.MethodPost, "/api/live/title", "en", cuerpo)
		if err := json.Unmarshal(b, &res); err != nil || len(res) != 1 {
			t.Fatalf("respuesta = %s (%v)", b, err)
		}
		if res[0].Message != "Canal has no linked account" {
			t.Errorf("en: %q", res[0].Message)
		}
		b = pedirConIdioma(t, ts, cookies, http.MethodPost, "/api/live/title", "", cuerpo)
		json.Unmarshal(b, &res)
		if res[0].Message != "Canal no tiene cuenta vinculada" {
			t.Errorf("es: %q", res[0].Message)
		}
	})

	t.Run("auth", func(t *testing.T) {
		srv.auths.mu.Lock()
		srv.auths.flows["st-en"] = &authFlow{status: "error", message: "rechazaste la autorización",
			expira: time.Now().Add(time.Minute), platform: platforms.Twitch}
		srv.auths.flows["st-es"] = &authFlow{status: "error", message: "rechazaste la autorización",
			expira: time.Now().Add(time.Minute), platform: platforms.Twitch}
		srv.auths.mu.Unlock()
		var out authStatusDTO
		b := pedirConIdioma(t, ts, cookies, http.MethodGet, "/api/platforms/twitch/auth/st-en", "en", "")
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("respuesta = %s (%v)", b, err)
		}
		if out.Status != "error" || out.Message != "you turned down the authorization" {
			t.Errorf("en: %+v", out)
		}
		b = pedirConIdioma(t, ts, cookies, http.MethodGet, "/api/platforms/twitch/auth/st-es", "es", "")
		json.Unmarshal(b, &out)
		if out.Message != "rechazaste la autorización" {
			t.Errorf("es: %+v", out)
		}
	})

	t.Run("probar destino con clave de la API", func(t *testing.T) {
		acct := cuentaViaStore(t, srv, db)
		d, err := db.CreateDestination(ctx, srv.cipher, store.NewDestination{
			Name: "Con emisión", Platform: store.PlatformTwitch, RTMPURL: "rtmp://x/app", Key: "k", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SetBroadcast(ctx, store.Broadcast{DestinationID: d.ID, AccountID: acct.ID,
			Platform: store.PlatformTwitch, BroadcastRef: "b1", KeyFromAPI: true}); err != nil {
			t.Fatal(err)
		}
		var out testSkippedDTO
		b := pedirConIdioma(t, ts, cookies, http.MethodPost, destPath(d.ID)+"/test", "en", "")
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("respuesta = %s (%v)", b, err)
		}
		if !out.Skipped || out.Message != "the key came from the API: there is no invalid key to test" {
			t.Errorf("en: %+v", out)
		}
		b = pedirConIdioma(t, ts, cookies, http.MethodPost, destPath(d.ID)+"/test", "", "")
		json.Unmarshal(b, &out)
		if out.Message != "la clave vino por API: no hay clave inválida que probar" {
			t.Errorf("es: %+v", out)
		}
	})
}
