package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// El archivo de clave es el único del programa que se abre para ESCRIBIR y cuyo contenido
// no se puede regenerar: sin él, las claves de todos los destinos quedan ilegibles. Por eso
// escribirClave no puede tragarse ningún error de los tres pasos.

func TestEscribirClaveDejaLaClaveYCierra(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "splitstream.key")
	f, err := os.OpenFile(ruta, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if err := escribirClave(f, "una-clave"); err != nil {
		t.Fatalf("escribirClave: %v", err)
	}

	b, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "una-clave\n" {
		t.Errorf("archivo = %q, quería %q", b, "una-clave\n")
	}
	// El archivo tiene que quedar cerrado: si escribirClave no lo cerrara, este Close
	// pasaría en vez de quejarse de que ya está cerrado.
	if err := f.Close(); err == nil {
		t.Error("escribirClave devolvió sin cerrar el archivo")
	}
}

// La regresión de verdad. Antes esto era `defer f.Close()` con solo el WriteString
// comprobado, así que un fallo POSTERIOR a la escritura —Sync o Close— se perdía y
// claveDelArchivo devolvía la clave como persistida sin estarlo.
//
// Una tubería reproduce justo ese reparto sin tocar el disco ni pedir permisos raros: el
// WriteString entra en el buffer y pasa, y el Sync falla porque una tubería no se puede
// sincronizar. Con el código viejo esto devolvía nil.
func TestEscribirClaveDevuelveElFalloPosteriorALaEscritura(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	if err := escribirClave(w, "una-clave"); err == nil {
		t.Fatal("escribirClave devolvió nil aunque el Sync falló: el error se perdió")
	}
}

// Y si CREAR falla, no puede quedar nada que leer detrás.
func TestClaveDelArchivoNoDejaRestosSiFallaAlCrear(t *testing.T) {
	dir := t.TempDir()
	ruta := filepath.Join(dir, "splitstream.key")

	// Un directorio con el nombre del archivo: el OpenFile con O_WRONLY falla, que es el
	// camino de "crear el archivo de clave", no el de escribirlo. Sirve para fijar que un
	// fallo al crear tampoco deja nada que leer detrás.
	if err := os.Mkdir(ruta, 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, err := claveDelArchivo(ruta)
	if err == nil {
		t.Fatal("claveDelArchivo pasó con un directorio en el sitio del archivo")
	}
	if !strings.Contains(err.Error(), ruta) {
		t.Errorf("el error no dice de qué archivo habla: %v", err)
	}
}

// El otro lado, y el que de verdad importa: si falla la ESCRITURA —después de que el
// archivo exista— el archivo a medias no puede sobrevivir. El arranque siguiente lo leería,
// daría por buena una clave que no es, y las claves de todos los destinos quedarían
// ilegibles sin que nada lo avisara.
//
// Provocarlo con un archivo de verdad no se puede: ni WriteString ni Sync fallan a
// voluntad. Así que el archivo se crea igual —para que haya algo a medias que borrar— pero
// el descriptor que recibe claveDelArchivo es el extremo de escritura de una tubería cuyo
// lector ya está cerrado, y ahí el WriteString devuelve EPIPE. Go no convierte eso en
// SIGPIPE porque el descriptor no es ni la salida ni el error estándar.
func TestClaveDelArchivoNoDejaRestosSiFallaLaEscritura(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "splitstream.key")

	original := crearArchivoDeClave
	t.Cleanup(func() { crearArchivoDeClave = original })
	crearArchivoDeClave = func(r string) (*os.File, error) {
		f, err := original(r)
		if err != nil {
			return nil, err
		}
		f.Close()
		lector, escritor, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		lector.Close()
		return escritor, nil
	}

	_, _, err := claveDelArchivo(ruta)
	if err == nil {
		t.Fatal("claveDelArchivo pasó aunque la escritura falló")
	}
	if !strings.Contains(err.Error(), ruta) {
		t.Errorf("el error no dice de qué archivo habla: %v", err)
	}
	// Ese texto es el del caso en que NI SIQUIERA se pudo borrar. Aquí sí se podía, así que
	// verlo significaría que el borrado falló por otro motivo.
	if strings.Contains(err.Error(), "quedó a medias") {
		t.Errorf("el error dice que no pudo borrar el archivo, y sí podía: %v", err)
	}

	if _, serr := os.Stat(ruta); !errors.Is(serr, fs.ErrNotExist) {
		t.Errorf("el archivo a medias sigue ahí (stat: %v): el arranque siguiente lo leería como clave buena", serr)
	}
}
