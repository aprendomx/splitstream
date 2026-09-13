# Copia parcheada de github.com/yutopp/go-rtmp

- **Origen:** `github.com/yutopp/go-rtmp v0.0.7` (2024-07-15), `h1:` de go.sum: `github.com/yutopp/go-rtmp v0.0.7 h1:sKKm1MVV3ANbJHZlf3Kq8ecq99y5U7XnDUDxSjuK7KU=`
- **Licencia:** MIT (`LICENCE.txt`, sin cambios)
- **Por qué una copia y no un fork:** el módulo compila igual (`replace` en `go.mod` al mismo path), no hace falta otro repositorio, los parches viven al lado del código que los usa y se pueden proponer aguas arriba tal cual.
- **Excluido:** `example/` (dependencias propias, no se compila).

## Parches (en orden; cada uno es un `patches/NNNN-*.diff` aplicable con `git apply -p1` sobre la v0.0.7 limpia)

| N.º | Archivo | Qué arregla | Test |
| --- | --- | --- | --- |
| 1 | `conn.go`, `stream.go` | `Stream.Write` tenía 5 s cableados (`// TODO: Fix 5s`), así que una plataforma que deja de consumir tardaba 5 s en dar error y el sink no podía reconectar antes. Ahora el plazo sale de `ConnConfig.WriteTimeout` (0 → los 5 s de siempre, comportamiento por defecto intacto) y se añade `Stream.WriteContext` para quien quiera traer su propio contexto. | `TestWriteToAStalledPeerFailsWithinThreeSeconds` (`internal/rtmpio/publisher_test.go`) |

## Regenerar

1. `UP=$(go env GOMODCACHE)/github.com/yutopp/go-rtmp@v0.0.7`
2. `cp -R "$UP" /tmp/go-rtmp && chmod -R u+w /tmp/go-rtmp && rm -rf /tmp/go-rtmp/example`
3. `for p in patches/*.diff; do (cd /tmp/go-rtmp && git apply -p1 "$OLDPWD/$p"); done`
4. `diff -r /tmp/go-rtmp third_party/go-rtmp` sin salida más allá de `UPSTREAM.md` y `patches/`.

Cuando aguas arriba publique una versión con estos arreglos: quitar el `replace`, subir la versión y borrar este directorio.
