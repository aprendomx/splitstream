package httpapi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestNegociarIdioma(t *testing.T) {
	casos := map[string]idioma{
		"": idiomaES, "es": idiomaES, "fr": idiomaES, "en": idiomaEN, "en-US": idiomaEN,
		"en-GB,en;q=0.9,es;q=0.8": idiomaEN, "es-MX,en;q=0.5": idiomaES,
		"fr-FR,en;q=0.7,de;q=0.6": idiomaEN, "*": idiomaES, "EN": idiomaEN,
		"en;q=0": idiomaES, "es;q=0.1,en;q=0.9": idiomaEN,
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

// TestNingunaPlantillaEsDemasiadoGenerica protege la mesa de las plantillas de sí misma:
// una clave que empiece por comodín y apenas tenga texto literal casaría con mensajes que
// no son suyos, y el cliente en inglés leería una frase equivocada en vez del español.
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
			faltan = append(faltan, strconv.Quote(l.texto)+": "+strconv.Quote(l.texto)+",")
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

// textoDe reconstruye el mensaje que verá el cliente: los trozos literales se conservan y
// cada parte que solo se conoce en ejecución (un nombre, un campo, un valor) se convierte
// en el comodín {n}. Devuelve false si no hay ni un literal del que tirar.
func textoDe(e ast.Expr) (string, bool) {
	n := 0
	return reconstruir(e, &n)
}

func comodin(n *int) string {
	s := "{" + strconv.Itoa(*n) + "}"
	*n++
	return s
}

func reconstruir(e ast.Expr, n *int) (string, bool) {
	switch v := e.(type) {
	case *ast.ParenExpr:
		return reconstruir(v.X, n)
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
		izq, okI := reconstruir(v.X, n)
		if !okI {
			izq = comodin(n)
		}
		der, okD := reconstruir(v.Y, n)
		if !okD {
			der = comodin(n)
		}
		return izq + der, okI || okD
	case *ast.CallExpr:
		if esLlamadaA(v.Fun, "fmt", "Sprintf") && len(v.Args) > 0 {
			f, ok := reconstruir(v.Args[0], n)
			if !ok {
				return "", false
			}
			return sustituirVerbos(f, n), true
		}
		// textoSeguro(x) no cambia el texto, solo lo limpia: se mira lo de dentro.
		if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "textoSeguro" && len(v.Args) == 1 {
			return reconstruir(v.Args[0], n)
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

// funcionesDeMensaje son las que componen un texto para el cliente a partir de un error:
// sus `return` son mensajes aunque no pasen por writeError en el mismo sitio.
var funcionesDeMensaje = map[string]bool{"mensajePlataforma": true, "mensajeTitulo": true}

// literalesDeWriteError recoge, de un paquete HTTP: el cuarto argumento de cada
// writeError, los `return` de las funciones que componen mensajes y el texto de las
// variables de paquete errXxx.
func literalesDeWriteError(t *testing.T, dir string) []literalDeError {
	t.Helper()
	fset, archivos := paqueteDe(t, dir)
	var out []literalDeError
	añadir := func(p token.Pos, e ast.Expr) {
		if s, ok := textoDe(e); ok && strings.TrimSpace(s) != "" {
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
						añadir(nombre.Pos(), c.Args[0])
					}
				}
			case *ast.FuncDecl:
				if !funcionesDeMensaje[d.Name.Name] {
					continue
				}
				ast.Inspect(d, func(n ast.Node) bool {
					ret, ok := n.(*ast.ReturnStmt)
					if !ok {
						return true
					}
					for _, r := range ret.Results {
						añadir(r.Pos(), r)
					}
					return true
				})
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := c.Fun.(*ast.Ident)
			if !ok || id.Name != "writeError" || len(c.Args) != 4 {
				return true
			}
			// El cuarto argumento puede ser err.Error() o mensajePlataforma(err): esos
			// textos se cubren por el store y por las funciones de mensaje, no aquí.
			añadir(c.Args[3].Pos(), c.Args[3])
			return true
		})
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
					if s, ok := textoDe(c.Args[0]); ok {
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
				if s, ok := textoDe(c.Args[0]); ok {
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

// TestAcceptLanguageTraduceLosErrores es la prueba de que las piezas encajan de punta a
// punta: middleware, negociación, writeStoreError y tabla.
func TestAcceptLanguageTraduceLosErrores(t *testing.T) {
	srv, _ := newTestServer(t)
	cookies := login(t, srv)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	pedir := func(lang string) errorBody {
		req, _ := http.NewRequest("PATCH", ts.URL+"/api/destinations/999999", strings.NewReader(`{"name":"x"}`))
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
		var body errorBody
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("respuesta ilegible: %v", err)
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
