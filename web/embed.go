// Package web embebe el panel compilado en el binario.
//
// El paquete vive aquí y no en internal/web como decía el spec §4 por una restricción de
// go:embed: sus patrones no admiten "..", así que el archivo que embebe dist/spa tiene que
// estar en un directorio que lo contenga. La alternativa era copiar el dist a internal/ en
// cada build, que añade un paso frágil para no ganar nada.
package web

import (
	"embed"
	"io/fs"
)

// all: incluye los archivos que empiezan por punto. Hace falta para que el .gitkeep del
// repositorio limpio cuente como contenido: sin él, go:embed falla al compilar en un clon
// que todavía no haya construido el frontend.
//
//go:embed all:dist/spa
var compilado embed.FS

// FS devuelve el panel listo para servir, con dist/spa ya como raíz.
func FS() (fs.FS, error) {
	return fs.Sub(compilado, "dist/spa")
}
