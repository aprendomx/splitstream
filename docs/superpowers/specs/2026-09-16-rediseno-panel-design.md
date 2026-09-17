# Splitstream — v1.1 «Rediseño del panel»

**Fecha:** 2026-09-16
**Estado:** spec corto para ejecución (petición directa: «rediseña con el skill UI/UX Pro Max»); pendiente de plan de implementación
**Spec base:** `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` §10 (Frontend)
**Spec previo:** `docs/superpowers/specs/2026-09-13-endurecimiento-design.md` (v1.0)
**Sistema de diseño generado:** `web/design-system/splitstream/MASTER.md` (skill UI/UX Pro Max, 2026-09-16; adaptado en §3 de este spec)
**Versión de partida:** `v1.0.0` + Dependabot (`main` @ `4c1072f`: quasar 2.32, vite 8, pinia 4, `@quasar/extras` 2)
**Línea base visual:** capturas del panel actual a 1280 px y 375 px (asistente, entrada, panel, ajustes, grabaciones, historial, créditos, diálogo de alta), tomadas el 2026-09-16 contra un binario local con tres destinos sembrados.

## 1. Qué se construye

Un rediseño **sin cambiar funciones ni API**: el mismo panel (Vue 3 + Quasar 2 + Pinia, bilingüe con `t()`), con un sistema de diseño explícito (tokens semánticos, escala tipográfica y de espacio, estados, movimiento) aplicado a las seis páginas y a todos los componentes, y con los defectos que la línea base dejó a la vista arreglados. Cero dependencias nuevas (`package.json` intacto); ninguna fuente externa (el panel funciona sin internet y no llama a terceros: se usa la pila de fuentes del sistema, con Inter si está instalada).

### 1.1 Defectos visibles en la línea base (se arreglan todos)

1. **Icono roto en la barra**: el desplegable de idioma muestra el texto «arrow_drop_down» porque `q-btn-dropdown` usa el icono por defecto de Quasar (fuente Material Icons, que el panel no carga: los iconos entran como SVG). El texto se solapa con los botones de la barra a cualquier ancho y en móvil pisa el logotipo.
2. **Asa de arrastre duplicada**: `Panel.vue` pinta un asa (`.arrastre`) fuera de cada tarjeta y `TarjetaDestino.vue` pinta otra dentro; se ven dos.
3. **Barra de navegación solo con iconos** (sin etiqueta visible ni indicador de la ruta activa), siete elementos apretados, sin agrupar lo secundario (créditos, salir).
4. **Texto por debajo del mínimo legible**: chips de estado a 10–11 px, pies y leyendas a 12–13 px con grises al 50–62 % sobre fondo oscuro (contraste < 4.5:1 en varios casos).
5. **Desperdicio de anchura**: a 1280 px la lista de canales va a una columna de ancho completo; a la vez el bloque de ingesta reparte mal (icono de copiar lejos del campo, botón «Rotar clave» minúsculo).
6. **Ruido permanente en las tarjetas**: URL RTMP y clave enmascarada siempre visibles; la jerarquía identidad → estado → diagnóstico → cifras no se lee de un vistazo.
7. **Colores a mano**: 20 `rgba(255,255,255,…)` y hex sueltos repartidos por componentes; sin tokens; sin `focus-visible` diseñado; sin `prefers-reduced-motion` salvo en la tarjeta.
8. **Sin estados de carga**: el panel arranca en blanco hasta que llega `/api/status`; los vacíos existen pero sin acción.

## 2. Enmiendas al spec base

- §10 (Frontend): el panel adopta un sistema de diseño propio en `web/src/css/tokens.scss` (tokens semánticos como variables CSS) y `web/src/css/base.scss` (reset ligero, tipografía, foco, movimiento reducido). Quasar sigue siendo la base de componentes; los colores de marca de Quasar (`$primary`, `$positive`, …) se derivan de los mismos tokens. Navegación primaria con etiquetas visibles y ruta activa.
- Nada cambia en la API, en el store (salvo lo que la carga y los vacíos necesiten) ni en las claves de i18n existentes (se añaden las nuevas que pidan los textos nuevos: etiquetas de navegación, «ver detalles», vacíos con acción).

## 3. Sistema de diseño (adaptado del MASTER.md)

El skill propone «Dark Mode (OLED)» con paleta de dashboard IoT/estado y tipografía Inter. Se adopta con tres ajustes: la marca sigue siendo el índigo del logotipo (no se cambia la identidad), las superficies pasan a la escala pizarra (más profundidad que el negro plano actual), y las fuentes no se descargan.

### 3.1 Tokens (`web/src/css/tokens.scss`, variables CSS en `:root`)

| Token | Valor | Uso |
| --- | --- | --- |
| `--ss-bg` | `#0B1020` | fondo de página |
| `--ss-surface` | `#131A2B` | tarjetas, barras |
| `--ss-surface-2` | `#1B2336` | elevación 2 (menús, bloques dentro de tarjetas) |
| `--ss-border` | `#2A3448` | bordes; `--ss-border-strong` `#475569` para foco/hover |
| `--ss-fg` | `#F1F5F9` | texto principal (≥ 13:1) |
| `--ss-fg-muted` | `#A3AEC2` | texto secundario (≥ 6:1 sobre surface) |
| `--ss-fg-subtle` | `#7C8AA5` | solo etiquetas ≥ 12 px con peso 500 (≥ 4.5:1) |
| `--ss-primary` | `#6366F1` / hover `#818CF8` / on `#FFFFFF` | acción principal, enlaces, ruta activa |
| `--ss-live` | `#22C55E` | en vivo / conectado |
| `--ss-warn` | `#F59E0B` | degradado / reconectando |
| `--ss-danger` | `#EF4444` | caído / destructivo |
| `--ss-info` | `#38BDF8` | conectando / información |
| `--ss-ring` | `#A5B4FC` | anillo de foco (2 px + 2 px de separación) |
| `--ss-radius-sm/md/lg` | 6 / 10 / 14 px | radios |
| `--ss-shadow-1/2` | `0 1px 2px rgba(0,0,0,.4)` / `0 8px 24px rgba(0,0,0,.45)` | tarjeta / menús y diálogos |
| `--ss-space-1..8` | 4, 8, 12, 16, 24, 32, 48, 64 px | escala de espacio |
| `--ss-font-sans` | `Inter, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif` | texto |
| `--ss-font-mono` | `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace` | URLs, claves, horas, cifras |
| `--ss-motion` | 180 ms; `--ss-motion-slow` 280 ms; easing `cubic-bezier(.2,0,0,1)` | transiciones |

Quasar: `quasar.variables.scss` asigna `$primary: #6366F1`, `$dark: #131A2B`, `$dark-page: #0B1020`, `$positive/$warning/$negative/$info` a los mismos valores. Ningún componente escribe un color a mano: usa `var(--ss-…)` o las clases de Quasar.

### 3.2 Tipografía

Escala: 12 (solo etiquetas y chips, peso 500), 14 (cuerpo secundario y tablas), 16 (cuerpo), 18 (título de tarjeta/sección), 22 (título de página), 28 (bienvenida). Interlineado 1.5 en cuerpo, 1.3 en títulos. Cifras y horas con `font-variant-numeric: tabular-nums`. Nada por debajo de 12 px. `letter-spacing` por defecto.

### 3.3 Espacio y disposición

Rejilla base de 4 px; padding de tarjeta 16 (12 en móvil); separación entre tarjetas 16; secciones 32. Anchuras: `Panel` hasta 1280 px con la lista de canales en rejilla CSS de 1 columna (< 768), 2 (768–1279) y 3 (≥ 1280); páginas de texto (Ajustes, Grabaciones, Historial, Sesión, Créditos) hasta 800 px. Sin scroll horizontal en 375 px. Objetivos táctiles ≥ 44 px (los botones «dense» de la barra ganan área con padding, no se cambia el icono).

### 3.4 Estados, foco, movimiento

- Estado del destino: componente `ChipEstado` con **icono + texto** (nunca solo color), 12 px/500, tono por token (`live`, `warn`, `danger`, `info`, neutro). El mismo componente en tarjetas, historial y ficha.
- `:focus-visible`: anillo `--ss-ring` en todo lo interactivo (botones, enlaces, filas, toggles, asas); nunca `outline: none` sin sustituto.
- Hover/pressed en tarjetas y filas: cambio de borde/fondo con `--ss-motion`, sin mover el layout.
- `prefers-reduced-motion: reduce`: sin pulso del indicador «en vivo», sin transiciones, sin animación del arrastre (`:animation="0"`).
- Cargas > 300 ms: esqueletos (`q-skeleton`) en la cabecera de sesión y en la lista de canales al arrancar; botones con `loading` durante las llamadas.
- Vacíos: icono + frase + acción (por ejemplo, «Vincular canal», «Activar grabación», «Configurar OBS»).

### 3.5 Navegación

Barra superior con logotipo, `q-tabs` de ruta con **icono y etiqueta** para Panel, Historial, Grabaciones y Ajustes (indicador de activa), y a la derecha el idioma (menú con el nombre completo del idioma, icono SVG de desplegar `mdiMenuDown`, opción activa marcada) y un menú «más» (Créditos, Cerrar sesión). Por debajo de 768 px las pestañas pasan a solo icono con etiqueta pequeña (Quasar `q-tabs` `dense` + `narrow-indicator`) y siguen siendo cuatro; nada de bottom-nav extra. Sin sesión: solo logotipo e idioma.

## 4. Pantalla por pantalla

- **Asistente inicial y entrada**: tarjeta centrada de 420 px, título 28/22, campo con etiqueta visible y ayuda, botón principal a ancho completo, selector de idioma como `q-btn-toggle` arriba; el error de entrada bajo el campo (no solo notificación).
- **Panel**: (1) cabecera de sesión: indicador (punto + texto «En vivo»/«Sin señal» con `ChipEstado` grande), resolución · bitrate · tiempo en tabular, acciones «Vista previa» y «Chat» como botones secundarios alineados a la derecha; (2) bloque «Configura esto en OBS» como dos campos etiquetados (Servidor, Clave) con botón de copiar pegado a cada campo y «Rotar clave» como botón secundario con confirmación; (3) «Canales»: título 22, interruptor «Todos» con etiqueta, botón principal «Vincular canal»; rejilla de `TarjetaDestino`; vacío con acción. Título en vivo, vista previa y chat conservan su sitio (bajo la cabecera) con la misma tarjeta base.
- **TarjetaDestino**: cabecera (asa única a la izquierda —solo visible al pasar el ratón o con teclado, siempre con `aria-label`—, logotipo/plataforma, nombre 18/600, toggle, menú); fila de estado (`ChipEstado` + cuenta vinculada como chip neutro o enlace «conectar»); una línea de diagnóstico (`diagnosticar`) 14 px y, si hay consejo, una segunda línea con icono; fila de cifras (bitrate, descartes, reconexiones) en tabular 14 px, solo cuando hay sesión; pie plegable «Detalles» (URL y máscara de clave, monoespaciado) cerrado por defecto y recordado por destino en `localStorage`; acciones «Probar»/«Editar» en el menú como hoy; con emisión de YouTube, los botones «Salir al aire»/«Terminar» como secundarios en el pie. Tono de borde izquierdo de 3 px por estado (además del chip, no en lugar de él).
- **DialogoDestino / DialogoWebhook / ConectarCuenta / AsistenteCredenciales**: `q-dialog` con `maximized` por debajo de 600 px, cabecera fija con título y cierre, cuerpo con scroll, pie con acciones (secundaria a la izquierda, principal a la derecha); todos los campos con etiqueta visible, ayuda persistente y error bajo el campo; la rejilla de plataformas con tarjetas de 44 px mínimo, estado seleccionado con borde `--ss-primary` y marca; el código de dispositivo en 28 px monoespaciado con botón de copiar.
- **Chat**: mensajes a 14 px, autor 500, insignias a 12 px con icono; pestañas por plataforma con icono; entrada de sesión (modo lectura) con «cargar más» al principio; barra de cuota como `q-linear-progress` con etiqueta.
- **TituloEnVivo, VistaPrevia, RegistroEventos**: misma tarjeta base; el registro con hora en tabular, nivel como `ChipEstado` pequeño y mensaje a 14 px; la vista previa con proporción 16:9 reservada (sin salto de layout).
- **Ajustes**: índice lateral pegajoso (≥ 1024) o `q-tabs` arriba (< 1024) con las secciones (Avisos, Cuentas, Grabación, Respaldo, Contraseña…), cada sección como tarjeta con título 18 y descripción 14 `--ss-fg-muted`; vacíos con acción.
- **Grabaciones / Historial**: lista/tabla con filas de 44 px, cifras tabulares, chips de nivel, `has_recording` como icono con `aria-label`, «cargar más» como botón secundario; vacíos con acción.
- **Sesión**: cabecera con cifras en tabular; resumen como rejilla de 2–4 KPI (número 22/600 + etiqueta 12/500); línea de tiempo con hora relativa en monoespaciado y filtro por nivel como `q-btn-toggle`; chat y grabaciones en tarjetas.
- **Créditos**: sin cambios de contenido; tipografía y tarjetas con los tokens.

## 5. Accesibilidad y calidad (lista de verificación del skill, §1–§5)

Contraste ≥ 4.5:1 en todo texto (medido con los tokens); foco visible; orden de tabulación = orden visual; `aria-label` en todo botón de solo icono (ya existe: se conserva) y `aria-current` en la ruta activa; sin información solo por color; `prefers-reduced-motion`; objetivos ≥ 44 px con ≥ 8 px entre ellos; sin scroll horizontal a 375 px; `viewport` sin bloquear el zoom (ya es así); `lang` del documento sigue al idioma (ya es así); toasts con `aria-live` (Quasar Notify lo hace); diálogos con Escape y botón de cierre.

## 6. Pruebas y puerta

- `cd web && node scripts/i18n-check.mjs && npm run build` limpios; `package.json` y `package-lock.json` intactos.
- Un test de Go no cambia; `go build ./...` (el panel va embebido).
- **Verificación visual** (la hace el controlador con el navegador, como la línea base): capturas «después» de las mismas ocho pantallas a 1280 y 375 px, comparadas con las de «antes»; comprobación de foco con teclado (Tab por la barra, una tarjeta y un diálogo), de `prefers-reduced-motion` (emulación en DevTools) y de contraste con los tokens.
- Gate del usuario: abrir el panel real, cambiar el idioma, vincular un canal y dar el visto bueno al aspecto.

## 7. Fuera de esta entrega

Modo claro (el spec base fija oscuro por defecto; los tokens lo dejan preparado pero no se implementa); tests de componentes (sin runner, sin dependencias nuevas); cambios de funciones, de API o de textos existentes más allá de los que el rediseño necesite; fuentes descargadas.
