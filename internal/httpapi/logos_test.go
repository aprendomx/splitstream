package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pngDePrueba genera un PNG real del tamaño pedido. Real, no un blob inventado: los
// handlers lo normalizan de verdad y un blob inventado se rechazaría por otro motivo.
func pngDePrueba(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 0x30, 0xFF})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// subirLogo manda un multipart con el campo "file", como hará el panel.
func subirLogo(t *testing.T, srv *Server, cookies []*http.Cookie, path string, nombre string, datos []byte) *httptest.ResponseRecorder {
	t.Helper()
	var cuerpo bytes.Buffer
	mw := multipart.NewWriter(&cuerpo)
	parte, err := mw.CreateFormFile("file", nombre)
	if err != nil {
		t.Fatal(err)
	}
	parte.Write(datos)
	mw.Close()

	r := httptest.NewRequest(http.MethodPut, path, &cuerpo)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

func TestSubirLogoDejaElDestinoConEtag(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	d := crearDest(t, db, srv, "Canal", "clave-1234", true)

	rec := subirLogo(t, srv, cookies, "/api/destinations/1/logo", "logo.png", pngDePrueba(t, 400, 400))
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body)
	}
	dto := decodeDest(t, rec)
	if dto.LogoETag == "" {
		t.Error("el DTO volvió sin logo_etag")
	}

	// Y el listado lo ve igual.
	lista := do(t, srv, cookies, http.MethodGet, "/api/destinations", "")
	var dtos []destinationDTO
	json.Unmarshal(lista.Body.Bytes(), &dtos)
	if len(dtos) != 1 || dtos[0].LogoETag != dto.LogoETag {
		t.Errorf("el listado no trae el mismo etag: %+v", dtos)
	}
	_ = d
}

func TestDestinoSinLogoTieneEtagVacio(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations", "")
	var dtos []destinationDTO
	json.Unmarshal(rec.Body.Bytes(), &dtos)
	if len(dtos) != 1 {
		t.Fatalf("destinos = %d", len(dtos))
	}
	if dtos[0].LogoETag != "" {
		t.Errorf("logo_etag = %q, quería vacío", dtos[0].LogoETag)
	}
}

func TestDescargarLogoDevuelvePNGYEtag(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)
	sub := subirLogo(t, srv, cookies, "/api/destinations/1/logo", "logo.png", pngDePrueba(t, 400, 400))
	etag := decodeDest(t, sub).LogoETag

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations/1/logo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := rec.Header().Get("ETag"); got != `"`+etag+`"` {
		t.Errorf("ETag = %q, quería %q", got, `"`+etag+`"`)
	}
	if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Errorf("el cuerpo no es un PNG: %v", err)
	}
}

// TestLaImagenSeGuardaReducida: el usuario sube lo que tenga; lo que se almacena y se
// sirve tiene un techo.
func TestLaImagenSeGuardaReducida(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)
	subirLogo(t, srv, cookies, "/api/destinations/1/logo", "grande.png", pngDePrueba(t, 1200, 600))

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations/1/logo", "")
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() > 256 || b.Dy() > 256 {
		t.Errorf("se guardó %dx%d, el techo es 256", b.Dx(), b.Dy())
	}
}

// TestDescargarLogoResponde304 es lo que evita que el panel se baje todos los logos en
// cada recarga.
func TestDescargarLogoResponde304(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)
	sub := subirLogo(t, srv, cookies, "/api/destinations/1/logo", "logo.png", pngDePrueba(t, 300, 300))
	etag := decodeDest(t, sub).LogoETag

	r := httptest.NewRequest(http.MethodGet, "/api/destinations/1/logo", nil)
	r.Header.Set("If-None-Match", `"`+etag+`"`)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)

	if rec.Code != http.StatusNotModified {
		t.Errorf("código = %d, quería 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("un 304 no lleva cuerpo, llevaba %d bytes", rec.Body.Len())
	}
}

func TestDescargarLogoQueNoExiste(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)

	rec := do(t, srv, cookies, http.MethodGet, "/api/destinations/1/logo", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("código = %d, quería 404", rec.Code)
	}
}

func TestBorrarLogo(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)
	subirLogo(t, srv, cookies, "/api/destinations/1/logo", "logo.png", pngDePrueba(t, 300, 300))

	rec := do(t, srv, cookies, http.MethodDelete, "/api/destinations/1/logo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("código = %d: %s", rec.Code, rec.Body)
	}
	if dto := decodeDest(t, rec); dto.LogoETag != "" {
		t.Errorf("el DTO sigue con etag %q", dto.LogoETag)
	}
	if g := do(t, srv, cookies, http.MethodGet, "/api/destinations/1/logo", ""); g.Code != http.StatusNotFound {
		t.Errorf("sigue habiendo logo: %d", g.Code)
	}
}

// TestRechazaSVGDisfrazadoDePNG: el nombre del archivo dice .png y el multipart declarará
// lo que sea; lo que manda son los bytes.
func TestRechazaSVGDisfrazadoDePNG(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	rec := subirLogo(t, srv, cookies, "/api/destinations/1/logo", "logo.png", svg)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("código = %d, quería 400. Cuerpo: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "script") {
		t.Error("el error devuelve el contenido del archivo")
	}
}

// TestRechazaUnCuerpoEnorme: el límite se aplica sin cargar el cuerpo entero en memoria.
func TestRechazaUnCuerpoEnorme(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)

	enorme := bytes.Repeat([]byte{0x41}, 3*1024*1024)
	rec := subirLogo(t, srv, cookies, "/api/destinations/1/logo", "grande.png", enorme)
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		t.Errorf("código = %d, quería 413 o 400", rec.Code)
	}
}

func TestLogoDeUnDestinoQueNoExiste(t *testing.T) {
	srv, _, _, _, cookies := newDestServer(t)

	rec := subirLogo(t, srv, cookies, "/api/destinations/99/logo", "logo.png", pngDePrueba(t, 100, 100))
	if rec.Code != http.StatusNotFound {
		t.Errorf("código = %d, quería 404", rec.Code)
	}
}

// TestElLogoExigeSesion: es una imagen que el dueño subió, y su sola existencia dice que
// hay un canal configurado. Va detrás de la sesión como todo lo demás.
func TestElLogoExigeSesion(t *testing.T) {
	srv, db, _, _, cookies := newDestServer(t)
	crearDest(t, db, srv, "Canal", "clave-1234", true)
	subirLogo(t, srv, cookies, "/api/destinations/1/logo", "logo.png", pngDePrueba(t, 100, 100))

	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		r := httptest.NewRequest(m, "/api/destinations/1/logo", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s sin sesión = %d, quería 401", m, rec.Code)
		}
	}
}
