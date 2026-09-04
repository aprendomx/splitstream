// Package imagen normaliza las imágenes que sube el usuario: valida el formato, acota el
// tamaño y devuelve siempre un PNG pequeño.
//
// Es un paquete aparte y no un par de funciones en httpapi porque tiene una regla de
// seguridad propia —qué formatos entran y cuánto se permite decodificar— y eso se prueba
// mejor sin levantar un servidor.
package imagen

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
)

// MaxLado es el lado mayor del PNG que se guarda. Un avatar de 28 px en el panel; 256 da
// margen de sobra para pantallas de alta densidad y deja el archivo en pocos KB.
const MaxLado = 256

// MaxPixeles acota lo que se decodifica. Es lo que protege de una bomba de descompresión:
// un PNG de pocos KB puede declarar 30000x30000 y pedir gigabytes al decodificarlo, así que
// el límite de tamaño del cuerpo HTTP no sirve para esto.
//
// 40 millones de píxeles son unos 160 MB en RGBA. Es más de lo que cabe esperar de un logo
// y bastante menos de lo que tumba el proceso.
const MaxPixeles = 40_000_000

var (
	// ErrFormato: no es un PNG ni un JPEG.
	ErrFormato = errors.New("el archivo no es una imagen PNG o JPEG")

	// ErrDemasiadosPixeles: la imagen declara más píxeles de los que se aceptan decodificar.
	ErrDemasiadosPixeles = errors.New("la imagen tiene demasiados píxeles")
)

// Normalizar lee una imagen, la reduce para que quepa en MaxLado y la devuelve como PNG.
//
// Lo que entra no se guarda nunca tal cual: re-codificar garantiza que lo almacenado es
// siempre PNG y de paso tira los metadatos del archivo original, que en una foto de móvil
// incluyen la geolocalización.
func Normalizar(r io.Reader) ([]byte, error) {
	datos, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("leer la imagen: %w", err)
	}

	// El formato se decide aquí y se despacha a mano, en vez de dejárselo a image.Decode.
	//
	// image.Decode usa un registro GLOBAL AL PROCESO que se llena con importaciones en
	// blanco. Restringir los formatos importando solo png y jpeg no restringe nada: basta
	// con que cualquier otro paquete del binario —o del binario de test— importe
	// image/gif para que aquí empiecen a aceptarse GIFs sin que nadie lo decida. Lo
	// descubrió un test que importaba image/gif para fabricar el GIF que debía rechazar.
	//
	// Con el despacho explícito, la lista de formatos aceptados es esta función y nada más.
	decodeConfig, decode, err := decodificadorPara(datos)
	if err != nil {
		return nil, err
	}

	// Dos pasadas sobre los mismos bytes: la primera solo lee la cabecera para conocer el
	// tamaño, y hasta que ese tamaño no pasa el filtro no se decodifica ningún píxel.
	cfg, err := decodeConfig(bytes.NewReader(datos))
	if err != nil {
		return nil, ErrFormato
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrFormato
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixeles {
		return nil, fmt.Errorf("%w: %dx%d", ErrDemasiadosPixeles, cfg.Width, cfg.Height)
	}

	src, err := decode(bytes.NewReader(datos))
	if err != nil {
		return nil, ErrFormato
	}

	var salida bytes.Buffer
	if err := png.Encode(&salida, reducir(src)); err != nil {
		return nil, fmt.Errorf("codificar el PNG: %w", err)
	}
	return salida.Bytes(), nil
}

// firmaPNG y firmaJPEG son los bytes iniciales de cada formato. El JPEG se reconoce por
// SOI seguido de un marcador; los tres bytes son los que usa la propia detección de la
// biblioteca estándar.
var (
	firmaPNG  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
	firmaJPEG = []byte{0xFF, 0xD8, 0xFF}
)

type configDecoder func(io.Reader) (image.Config, error)
type imageDecoder func(io.Reader) (image.Image, error)

// decodificadorPara elige el decodificador por los BYTES del archivo, nunca por su
// extensión ni por el Content-Type que declare el cliente: los dos los escribe quien sube.
func decodificadorPara(datos []byte) (configDecoder, imageDecoder, error) {
	switch {
	case bytes.HasPrefix(datos, firmaPNG):
		return png.DecodeConfig, png.Decode, nil
	case bytes.HasPrefix(datos, firmaJPEG):
		return jpeg.DecodeConfig, jpeg.Decode, nil
	default:
		return nil, nil, ErrFormato
	}
}

// reducir escala la imagen para que su lado mayor sea MaxLado, promediando el área.
//
// Cada píxel de salida es la media de los píxeles de entrada que le tocan. Para reducir
// mucho —que es este caso— da mejor resultado que tomar un píxel de cada N, que produce
// bordes dentados, y evita traer una dependencia de escalado por una sola función.
//
// No amplía: un logo de 64 px estirado a 256 se vería peor, no mejor.
func reducir(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	mayor := w
	if h > mayor {
		mayor = h
	}
	if mayor <= MaxLado {
		return src
	}

	escala := float64(MaxLado) / float64(mayor)
	nw := maxInt(1, int(math.Round(float64(w)*escala)))
	nh := maxInt(1, int(math.Round(float64(h)*escala)))

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}

			// RGBA() devuelve los componentes ya multiplicados por el alfa, y la media de
			// valores premultiplicados sigue siendo un color premultiplicado válido. Por
			// eso el destino es image.RGBA, que también los guarda así: no hay que
			// deshacer y rehacer la multiplicación, que es donde se pierde precisión.
			var sr, sg, sb, sa uint64
			var n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, ca := src.At(xx, yy).RGBA()
					sr += uint64(cr)
					sg += uint64(cg)
					sb += uint64(cb)
					sa += uint64(ca)
					n++
				}
			}

			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = uint8(sr / n >> 8)
			dst.Pix[i+1] = uint8(sg / n >> 8)
			dst.Pix[i+2] = uint8(sb / n >> 8)
			dst.Pix[i+3] = uint8(sa / n >> 8)
		}
	}
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
