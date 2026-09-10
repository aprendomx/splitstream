# SDD ledger — plan: docs/superpowers/plans/2026-09-10-instalacion.md

Spec: docs/superpowers/specs/2026-09-10-instalacion-design.md (autoridad vinculante), sobre el spec base 2026-09-01-rtmp-relay-design.md y el plan maestro §5.
Rama: feat/instalacion (desde main @ 3ef4cba, merge de la v0.9; v0.9.0 etiquetada ahí). Commit de documentación previo: f28f78a (spec + plan v0.10).
Ejecución: 2026-09-10, subagent-driven, un implementador por tarea + revisión por tarea.

Ruling: se trabaja en el checkout actual sobre la rama, sin worktree (misma razón que en v0.8 y v0.9) — coste si es erróneo: ninguno mientras no se toque main.
Ruling: la línea base es la CI en verde sobre 9a01de2 (run 34496777869, mismo código que 3ef4cba: vet, -race, panel, Docker, integración) — coste si es erróneo: ninguno.
Ruling: la cabecera de docs/lanzamiento.md («Escrito para la v0.8.0») se actualiza en la Task 9 de esta entrega en vez de con un commit directo a main al etiquetar la v0.9.0 — coste si es erróneo: la v0.9.0 publicada lleva la cabecera vieja, cosmético.
Ruling (T3): el test de cadencia de `tls_certificate_error` NO llama a `GetCertificate` con el propio dominio: autocert saldría a la red de Let's Encrypt de verdad (registro de cuenta) desde un test. Se prueba: nombre ajeno → sin aviso (HostWhitelist rechaza sin red) y la cadencia con `avisador` expuesto en `export_test.go` — coste si es erróneo: ninguno; el envoltorio de GetCertificate queda cubierto por el caso del nombre ajeno.
Ruling (T2): una dirección que no parsea en X-Forwarded-For devuelve la IP del proxy (no el texto): el spec §5 decía «se devuelven tal cual»; el plan y el código devuelven `netip.Addr`, y agrupar la basura bajo el proxy es más conservador (nunca local, un solo cubo del limitador). Se enmienda el spec §5 en la Task 9 — coste si es erróneo: ninguno.

## Preflight (tabla de conflictos)

| Par / tarea | Produce → consume | Hallazgo |
| --- | --- | --- |
| T1 → T2, T3, T4, T6 | `Config.TLS()`, `TLSCacheDir`, `TLSCertFile/KeyFile`, `TLSRedirectAddr`, `TrustedProxies []netip.Prefix`, `UpdateCheck`, `SecureCookiesDesactivadas` → `httpapi.Config.TrustedProxies`, `webtls.Build(cfg, …)`, `main.go` | Coinciden. T3 construye `&config.Config{…}` a mano en sus tests: solo usa campos exportados. |
| T1 texto | `LoadFrom` redeclara `err`: el bloque nuevo usa `cfg.TrustedProxies, err = …` antes de `level, err := …` | Anotado en el plan: declarar `var err error` arriba y quitar el `:=`; `go vet` lo vigila. |
| T2 → T4 | Métodos `s.clientIP`, `s.esLocal`; se borran las funciones libres | T4 no las usa. Tests existentes que llamen a `esLocal(r)` libre: el brief pide `grep`. |
| T2 texto | `TestLoginLimiterForgetsIdleAddresses` mezcla reloj falso (purga) con `rate.Limiter` real | Determinista: 6 `allow` seguidos agotan la ráfaga de 5 en tiempo real. |
| T3 → T4 | `webtls.Setup{TLSConfig, Redirect, PublicURL}`, `Build(cfg, onError)` (`nil, nil` sin TLS) | Coinciden; `main.go` comprueba `tlsSetup != nil`. |
| T3 texto | Test de cadencia con el propio dominio → red real | Ruling arriba: se acota. |
| T4 ↔ T6 | Ambas tocan `dto.go`, `status.go`, `server.go`, `dto_test.go`, `main.go` | Secuenciales; T6 añade `Update` después de `Panel`. `TestDTOFieldNamesAreSnakeCase` recibe `panelDTO{}` (T4) y `updateDTO{}` (T6). |
| T4 texto | `servidorAutenticado`/`getJSON` son nombres supuestos; código del login (200/204) | Anotado: leer `recording_test.go` y `auth.go`; el brief lo repite. |
| T4 texto | `TestRunWithOwnCertificateServesHTTPS` hace `POST /api/setup` desde loopback | Con T2, `esLocal` = `clientIP(r).IsLoopback()` y sin proxies devuelve `RemoteAddr` = 127.0.0.1: local, sin código. OK. |
| T5 → T6 | `update.Info{Latest, URL string; Available bool}` → `httpapi.UpdateStatus(chk.Latest())` | Conversión válida: mismos campos, orden y tipos. |
| T6 texto | `fondo` (WaitGroup) existe en `main.go` desde la v0.8 (webhooks + mantenimiento) | Sí (línea ~364). El bloque va después de su declaración. |
| T7 → T8 | Job `instaladores` con `shellcheck deploy/*.sh`; T8 amplía a `homebrew/` y `winget/` y añade `render_test.sh` | Anotado en ambas. |
| T7 texto | `install_test.sh`: `! command -v sudo` en ubuntu:24.04 y alpine:3.20 | Ninguna de las dos imágenes trae sudo. `[ -r /dev/tty ]` sin TTY: no se llega a preguntar porque el dir es escribible y `SERVICE=no`. |
| T7 ↔ release | La unidad viaja en el `.tar.gz` (T7) y la fórmula (T8) hace `bin.install "splitstream"` | La carpeta extra no molesta a Homebrew. |
| T8 texto | `if: env.TAP_TOKEN != ''` con `env` de job desde `secrets` | Patrón válido en Actions (los secretos no se pueden usar en `if` directamente; por `env` sí). |
| T8 ↔ CI docker | `FROM --platform=$BUILDPLATFORM` exige BuildKit | `ubuntu-latest` usa BuildKit por defecto; el brief pide comprobar cómo construye el job `docker`. |
| T9 ↔ T2 | Spec §5 «se devuelven tal cual» vs. `netip.Addr` | Ruling arriba; T9 enmienda el spec. |
| Global | Sin migraciones; `go.sum` puede ganar entradas por autocert sin `go mod tidy` | Anotado en T3. |
| Puerta | brew en Mac limpio, winget en VM, curl\|sh en VPS con dominio; tap, `TAP_TOKEN`, `WINGET_TOKEN`, visibilidad del paquete GHCR | Del usuario; los jobs degradan sin secretos. Push y PR los hace el controlador tras la revisión final. |

## Progreso
BASE T1: f28f78a
Task 1: complete (commits f28f78a..fbd3942, review clean; diferible: imports reordenados; el aviso en log de SecureCookiesDesactivadas lo hace la Task 4 en main.go)
BASE T2: fbd3942
Task 2: complete (commits fbd3942..750a7ab, review clean; diferible: purga O(n) en cada allow del limitador, aceptado por diseño)
BASE T3: 750a7ab
Ruling (T3): go.mod gana golang.org/x/net y golang.org/x/text como // indirect (las trae autocert); las cinco directas no cambian. Aceptado; la Task 9 lo anota en el spec base §5 — coste si es erróneo: ninguno, es lo que exige -mod=readonly.
Task 3: review WITH FIXES — Important: redirigirAHTTPS duplica corchetes con Host IPv6 sin puerto y puerto HTTPS distinto de 443 ([[::1]]:8443); Minor diferible: r.Host vacío. Fix round 1 (resume implementer).
Task 3: fix round 1 aplicado por el controlador (el implementador cayó por límite de sesión): commit de arreglo tras 8b2b78c; tests webtls en verde.
Task 3: complete (commits 750a7ab..09657c6, review clean tras 1 fix round; diferible: r.Host vacío en redirigirAHTTPS)
BASE T4: 09657c6
Task 4: complete (commits 09657c6..d6617b9, review clean; los tres diferibles —gofmt, comentario 26→28 s, Timeout en el cliente del test— aplicados por el controlador en el commit siguiente; informativo: un bind fallido del HTTP no cancela ctx, patrón preexistente)
BASE T5: 2450d50
Task 5: review WITH FIXES — Important: Check no guardaba info cuando tag_name no parsea; arreglado por el controlador (commit siguiente a 3061ec6), tests -race x2 con GOMAXPROCS=2 en verde. Nota: el plan traía errors.New para ese caso, incompatible con su propio test; el implementador lo resolvió devolviendo Available=false sin error (correcto). Diferible: sin guarda para interval<=0 (no configurable desde fuera).
Task 5: complete (commits 2450d50..605b28b, review clean tras 1 fix round del controlador)
BASE T6: 605b28b
Task 6: complete (commits 605b28b..76c5bb4, review clean)
BASE T7: 76c5bb4
Task 7: complete (commits 76c5bb4..56e52b6, review clean; install_test.sh queda para la CI —sin Docker local—; diferible: sanear $VERSION antes de construir rutas locales)
BASE T8: 56e52b6
Task 8: complete (commits 56e52b6..72e37e0, review clean; diferible: los render.sh no validan que la etiqueta sea v* antes del sed)
BASE T9: 72e37e0
Task 9: review WITH FIXES — Important x3 (README «Con Docker» sin la vía del TLS integrado; cabecera de env.example con «deploy/.env»; cabecera del compose con -f y deploy/.env); arreglados por el controlador en 7886b71. Diferible: «al arrancar» vs 30 s en README y manual.
Task 9: complete (commits 72e37e0..7886b71, review clean tras 1 fix round del controlador; suite completa en verde sobre 0ccbcd9: 18 paquetes -race, npm run build)
Suite completa sobre 7886b71 (controlador, en paralelo con la revisión final): go vet limpio, go test ./... -race sin ningún FAIL, npm run build exit 0.
Revisión final de la rama (f28f78a..7886b71, opus): «With fixes». 1 crítico, 5 importantes, 6 menores.
Ruling: crítico #1 (Docker + TLS): se quita SPLITSTREAM_HTTP_ADDR del ENV del Dockerfile (era redundante con el defecto de config.go y anulaba el :443 con TLS); los docs ya dicen lo correcto — coste si es erróneo: ninguno, sin TLS el defecto sigue :8080.
Ruling: importante #2 (-healthcheck sin SNI con autocert): ServerName = SPLITSTREAM_TLS_DOMAIN en el cliente del healthcheck; con certificado propio queda vacío — coste si es erróneo: ninguno.
Ruling: importante #3 (XFF con loopback desde un proxy de confianza): una dirección loopback o unspecified en la cabecera devuelve la IP del proxy, nunca local — coste si es erróneo: ninguno; el cliente local legítimo detrás de un proxy local ya cae en el prefijo de confianza.
Ruling: importante #4 (TLSCacheDir no escribible): MkdirAll + sonda de escritura en Build; error de arranque claro — coste si es erróneo: ninguno.
Ruling: importante #5 (ghcr sin needs; pre-releases): needs: binarios, if !contains(ref_name, "-") en ghcr/tap/winget, permissions mínimas por job, y validación de la etiqueta en los render.sh (diferible T8) — coste si es erróneo: la imagen tarda lo que tarden los binarios.
Ruling: importante #6 (bind fallido deja el proceso vivo sin panel): el listener del panel y el de redirección se abren antes de las goroutines y su fallo hace fallar run(); cambia también el camino sin TLS (fail-fast en vez de vivir sin panel), deliberado — coste si es erróneo: un puerto ocupado ahora reinicia el servicio en bucle bajo systemd, que es lo visible.
Ruling: menores #8 (+T7), #10, #11, #12 y diferible T5 entran en la misma ola (baratos). #7 (wingetcreate de aka.ms sin checksum) se parquea: HTTPS a Microsoft, runner efímero, token acotado a public_repo. #12 red fija en install_test.sh: sin acción. T1, T2, T3, T9: esperan — coste si es erróneo: ninguno.
Ola final: brief en final-fix-brief.md; un solo implementador (opus), una re-revisión acotada después.
Ola final: commits 7886b71..f0d161d (9, uno por apartado A–I). Suite del implementador: 19 paquetes ok con -race, npm run build limpio, guards y shellcheck limpios. Desviación aceptada: el test de B usa un servidor TLS montado a mano (net.Listen + ServeTLS solo con GetCertificate) porque httptest.StartTLS rellena Certificates y el test pasaba sin el arreglo.
Ola final: re-revisión acotada 7886b71..f0d161d (sonnet): A–I ADDRESSED, sin roturas nuevas. Parqueados: #7 wingetcreate sin checksum; #12 red fija en install_test.sh; T1, T2, T3, T9.
Cierre: rama lista para PR sobre main. Del usuario antes de la primera release: repo aprendomx/homebrew-tap + secreto TAP_TOKEN (opcional WINGET_TOKEN); tras ella: paquete GHCR público. Puerta: brew en Mac limpio, winget en VM, curl|sh en VPS con dominio y certificado. Jobs instaladores y docker de la CI. Fusionar y etiquetar v0.10.0.
