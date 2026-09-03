package httpapi

import (
	"io/fs"
	"net/http"
	"strings"
)

// serveSPA sirve el panel compilado.
//
// Cualquier ruta que no empiece por /api ni sea /ws cae aquí. Si el archivo pedido existe
// se sirve tal cual; si no, se devuelve index.html, que es lo que hace falta para que las
// rutas del router del cliente funcionen al recargar la página o al abrir un enlace
// directo (spec §10 pide URL limpias, no hash).
//
// Esto NO se monta si el frontend no se ha compilado: en ese caso el binario responde 404
// con un mensaje que dice qué hacer, en vez de servir una página en blanco.
func (s *Server) serveSPA(archivos fs.FS) http.HandlerFunc {
	servidor := http.FileServer(http.FS(archivos))

	return func(w http.ResponseWriter, r *http.Request) {
		// El patrón del mux no lleva método (ver routes), así que se filtra aquí: el panel
		// solo se sirve por GET y HEAD.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, codeInvalidInput,
				"método no permitido en esta ruta")
			return
		}

		ruta := strings.TrimPrefix(r.URL.Path, "/")
		esIndex := ruta == "" || ruta == "index.html"

		// Un archivo que existe se sirve tal cual. El index no pasa por aquí: el
		// FileServer redirige con un 301 cualquier petición a "/index.html" hacia "./",
		// así que recargar en una ruta del cliente devolvería un redirect y no el panel.
		if !esIndex {
			if f, err := archivos.Open(ruta); err == nil {
				f.Close()
				// Los assets llevan hash en el nombre, así que se cachean para siempre. El
				// index no: es lo que apunta a los assets nuevos tras una actualización, y
				// cachearlo dejaría al usuario en la versión vieja.
				if strings.HasPrefix(ruta, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				servidor.ServeHTTP(w, r)
				return
			}

			// Una ruta con extensión es un archivo que no existe, no una ruta del cliente.
			// Devolver el index para /favicon.ico o para un .js cuyo hash cambió da HTML
			// donde el navegador espera otra cosa, y el error que sale por consola no dice
			// nada de lo que pasó realmente.
			if i := strings.LastIndex(ruta, "/"); strings.Contains(ruta[i+1:], ".") {
				writeError(w, http.StatusNotFound, codeNotFound, "no existe ese archivo")
				return
			}
		}

		// Raíz o ruta del cliente: se devuelve el index y el router se encarga.
		index, err := fs.ReadFile(archivos, "index.html")
		if err != nil {
			writeError(w, http.StatusNotFound, codeNotFound,
				"el panel no está compilado en este binario: ejecuta `make build`")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.Write(index)
	}
}
