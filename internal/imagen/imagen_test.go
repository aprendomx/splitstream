package imagen_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/aprendomx/splitstream/internal/imagen"
)

// lienzo hace una imagen de w x h con un degradado, para que reducirla signifique algo:
// una imagen de un solo color pasaría cualquier promediado, incluso uno mal escrito.
func lienzo(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 0x40, 0xFF})
		}
	}
	return img
}

func pngDe(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegDe(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decodificar(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, formato, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("la salida no se puede decodificar: %v", err)
	}
	if formato != "png" {
		t.Errorf("la salida es %q, quería png", formato)
	}
	return img
}

func TestReduceLoGrandeManteniendoLaProporcion(t *testing.T) {
	out, err := imagen.Normalizar(bytes.NewReader(pngDe(t, lienzo(1024, 512))))
	if err != nil {
		t.Fatalf("Normalizar: %v", err)
	}
	b := decodificar(t, out).Bounds()
	if b.Dx() != 256 || b.Dy() != 128 {
		t.Errorf("tamaño = %dx%d, quería 256x128", b.Dx(), b.Dy())
	}
}

func TestLaAlturaTambienLimita(t *testing.T) {
	out, err := imagen.Normalizar(bytes.NewReader(pngDe(t, lienzo(300, 900))))
	if err != nil {
		t.Fatalf("Normalizar: %v", err)
	}
	b := decodificar(t, out).Bounds()
	if b.Dy() != 256 {
		t.Errorf("alto = %d, quería 256", b.Dy())
	}
	if b.Dx() > 256 {
		t.Errorf("ancho = %d, se pasó del límite", b.Dx())
	}
}

// TestNoAmplia: un logo de 64 px subido a 256 se vería peor, no mejor.
func TestNoAmplia(t *testing.T) {
	out, err := imagen.Normalizar(bytes.NewReader(pngDe(t, lienzo(64, 32))))
	if err != nil {
		t.Fatalf("Normalizar: %v", err)
	}
	b := decodificar(t, out).Bounds()
	if b.Dx() != 64 || b.Dy() != 32 {
		t.Errorf("tamaño = %dx%d, quería 64x32 sin tocar", b.Dx(), b.Dy())
	}
}

// TestLaSalidaSiempreEsPNG: entre otras cosas, re-codificar tira los metadatos del
// archivo original, que en una foto de móvil incluyen la geolocalización.
func TestLaSalidaSiempreEsPNG(t *testing.T) {
	out, err := imagen.Normalizar(bytes.NewReader(jpegDe(t, lienzo(400, 400))))
	if err != nil {
		t.Fatalf("Normalizar un JPEG: %v", err)
	}
	decodificar(t, out)
}

func TestPromediaDeVerdad(t *testing.T) {
	// Mitad izquierda negra, mitad derecha blanca. Al reducir, cada borde conserva su
	// color: si el promediado estuviera mal, saldría gris uniforme o se invertiría.
	src := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			c := color.RGBA{0, 0, 0, 0xFF}
			if x >= 256 {
				c = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
			}
			src.Set(x, y, c)
		}
	}
	out, err := imagen.Normalizar(bytes.NewReader(pngDe(t, src)))
	if err != nil {
		t.Fatal(err)
	}
	img := decodificar(t, out)

	rIzq, _, _, _ := img.At(10, 128).RGBA()
	rDer, _, _, _ := img.At(245, 128).RGBA()
	if rIzq > 0x2000 {
		t.Errorf("la izquierda salió clara (r=%#x), debería ser negra", rIzq)
	}
	if rDer < 0xD000 {
		t.Errorf("la derecha salió oscura (r=%#x), debería ser blanca", rDer)
	}
}

// TestRechazaGIF vale por partida doble: comprueba que el GIF no entra, y —por el hecho de
// que este archivo importa image/gif para fabricarlo— que la lista de formatos aceptados no
// depende del registro global de image.Decode. Con la implementación anterior, que confiaba
// en importar solo png y jpeg, este test pasaba a verde el GIF: la importación del propio
// test lo habilitaba. No borrar la importación de image/gif de aquí; es parte de la prueba.
func TestRechazaGIF(t *testing.T) {
	var b bytes.Buffer
	if err := gif.Encode(&b, lienzo(32, 32), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := imagen.Normalizar(&b); !errors.Is(err, imagen.ErrFormato) {
		t.Errorf("error = %v, quería ErrFormato", err)
	}
}

// TestRechazaSVG es el caso que importa de verdad: un SVG puede llevar <script> y se
// serviría desde el mismo origen que el panel, con la cookie de sesión presente.
func TestRechazaSVG(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if _, err := imagen.Normalizar(bytes.NewReader(svg)); !errors.Is(err, imagen.ErrFormato) {
		t.Errorf("error = %v, quería ErrFormato", err)
	}
}

func TestRechazaBasura(t *testing.T) {
	if _, err := imagen.Normalizar(bytes.NewReader([]byte("esto no es una imagen"))); !errors.Is(err, imagen.ErrFormato) {
		t.Errorf("error = %v, quería ErrFormato", err)
	}
}

func TestRechazaVacio(t *testing.T) {
	if _, err := imagen.Normalizar(bytes.NewReader(nil)); !errors.Is(err, imagen.ErrFormato) {
		t.Errorf("error = %v, quería ErrFormato", err)
	}
}

// pngBomba fabrica una cabecera PNG que DECLARA un tamaño enorme. Pesa unos cien bytes y
// al decodificarla pediría gigabytes de memoria: es el ataque clásico de descompresión.
// Solo lleva firma e IHDR, que es todo lo que DecodeConfig necesita leer.
func pngBomba(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'})

	ihdr := new(bytes.Buffer)
	ihdr.WriteString("IHDR")
	binary.Write(ihdr, binary.BigEndian, w)
	binary.Write(ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{8, 6, 0, 0, 0}) // 8 bits, RGBA, sin entrelazado

	binary.Write(&b, binary.BigEndian, uint32(ihdr.Len()-4))
	b.Write(ihdr.Bytes())
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	return b.Bytes()
}

// TestRechazaLaBombaDeDescompresion: el límite de 2 MB de la subida no protege de esto.
// Un PNG de pocos kilobytes puede declarar 30000x30000 y reventar el proceso al decodificar.
// Por eso se miran las dimensiones ANTES de decodificar los píxeles.
func TestRechazaLaBombaDeDescompresion(t *testing.T) {
	_, err := imagen.Normalizar(bytes.NewReader(pngBomba(30000, 30000)))
	if !errors.Is(err, imagen.ErrDemasiadosPixeles) {
		t.Errorf("error = %v, quería ErrDemasiadosPixeles", err)
	}
}
