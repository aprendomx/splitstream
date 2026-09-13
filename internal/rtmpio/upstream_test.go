package rtmpio

// TestGoRTMPCopyMatchesUpstreamPlusPatches: third_party/go-rtmp tiene que ser exactamente
// la v0.0.7 de la caché de módulos más los parches de patches/, ni un byte más. Así nadie
// edita la copia sin dejar el diff que la explica (spec v1.0 §3.4). Si la caché no tiene
// el módulo (clon limpio sin red) el test se salta: no toca internet.

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGoRTMPCopyMatchesUpstreamPlusPatches(t *testing.T) {
	up := filepath.Join(goEnv(t, "GOMODCACHE"), "github.com", "yutopp", "go-rtmp@v0.0.7")
	if _, err := os.Stat(up); err != nil {
		t.Skip("go-rtmp v0.0.7 no está en la caché de módulos; sin red no se puede comparar")
	}
	copia := filepath.Join(raizDelRepo(t), "third_party", "go-rtmp")
	parches := map[string]string{} // archivo → contenido esperado del diff
	entries, _ := filepath.Glob(filepath.Join(copia, "patches", "*.diff"))
	for _, p := range entries {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range archivosDelDiff(string(b)) {
			parches[f] += cuerpoDelDiff(string(b), f)
		}
	}
	// 1) Todo archivo de upstream (salvo example/) existe en la copia y es igual, o tiene parche.
	filepath.WalkDir(up, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(up, path)
		if strings.HasPrefix(rel, "example"+string(filepath.Separator)) {
			return nil
		}
		a, _ := os.ReadFile(path)
		b, errB := os.ReadFile(filepath.Join(copia, rel))
		if errB != nil {
			t.Errorf("%s: falta en la copia", rel)
			return nil
		}
		if bytes.Equal(a, b) {
			if _, tiene := parches[rel]; tiene {
				t.Errorf("%s: tiene parche pero es idéntico a upstream", rel)
			}
			return nil
		}
		want, ok := parches[rel]
		if !ok {
			t.Errorf("%s: difiere de upstream sin parche en patches/", rel)
			return nil
		}
		got := diffNoIndex(t, path, filepath.Join(copia, rel), rel)
		if got != want {
			t.Errorf("%s: el parche no coincide con el diff real\n--- esperado\n%s\n--- real\n%s", rel, want, got)
		}
		return nil
	})
	// 2) Nada en la copia que no esté en upstream, salvo UPSTREAM.md y patches/.
	filepath.WalkDir(copia, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(copia, path)
		if rel == "UPSTREAM.md" || strings.HasPrefix(rel, "patches"+string(filepath.Separator)) {
			return nil
		}
		if _, err := os.Stat(filepath.Join(up, rel)); err != nil {
			t.Errorf("%s: no existe en upstream", rel)
		}
		return nil
	})
}

// goEnv devuelve el valor de una variable de entorno de Go (p.ej. GOMODCACHE).
func goEnv(t *testing.T, k string) string {
	t.Helper()
	out, err := exec.Command("go", "env", k).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", k, err)
	}
	return strings.TrimSpace(string(out))
}

// raizDelRepo devuelve la raíz del repositorio git actual.
func raizDelRepo(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// diffNoIndex ejecuta `git diff --no-index` entre a y b, ignora el código de salida 1
// (que solo indica que hay diferencias) y normaliza las cabeceras a las rutas relativas
// rel, quitando la línea `index …` que varía según el sistema de archivos.
func diffNoIndex(t *testing.T, a, b, rel string) string {
	t.Helper()
	cmd := exec.Command("git", "diff", "--no-index", "--src-prefix=a/", "--dst-prefix=b/", a, b)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// git diff --no-index sale con 1 cuando hay diferencias; eso es lo esperado.
			if exitErr.ExitCode() != 1 {
				t.Fatalf("git diff --no-index %s %s: %v (%s)", a, b, err, exitErr.Stderr)
			}
		} else {
			t.Fatalf("git diff --no-index %s %s: %v", a, b, err)
		}
	}
	return normalizarDiff(string(out), rel)
}

var reIndexLinea = regexp.MustCompile(`(?m)^index [0-9a-f]+\.\.[0-9a-f]+.*\n`)

// normalizarDiff reescribe las cabeceras de un diff producido por `git diff --no-index`
// para que solo dependan de la ruta relativa rel, sin importar de dónde se leyeron los
// archivos temporales al generarlo.
func normalizarDiff(diff, rel string) string {
	diff = reIndexLinea.ReplaceAllString(diff, "")
	lineas := strings.Split(diff, "\n")
	for i, l := range lineas {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			lineas[i] = "diff --git a/" + rel + " b/" + rel
		case strings.HasPrefix(l, "--- "):
			lineas[i] = "--- a/" + rel
		case strings.HasPrefix(l, "+++ "):
			lineas[i] = "+++ b/" + rel
		}
	}
	return strings.Join(lineas, "\n")
}

var reArchivoDiff = regexp.MustCompile(`(?m)^\+\+\+ b/(.+)$`)

// archivosDelDiff devuelve, en orden, la lista de rutas relativas (b/<rel>) que toca un
// diff con uno o varios archivos concatenados.
func archivosDelDiff(s string) []string {
	var out []string
	for _, m := range reArchivoDiff.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// cuerpoDelDiff devuelve el bloque del diff (desde su `diff --git a/<rel> b/<rel>` hasta
// el siguiente, o el final) correspondiente al archivo rel, ya normalizado.
func cuerpoDelDiff(s, rel string) string {
	cabecera := "diff --git a/" + rel + " b/" + rel
	idx := strings.Index(s, cabecera)
	if idx < 0 {
		return ""
	}
	resto := s[idx:]
	siguiente := strings.Index(resto[len(cabecera):], "\ndiff --git a/")
	if siguiente < 0 {
		return resto
	}
	return resto[:len(cabecera)+siguiente+1]
}
