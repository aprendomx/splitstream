# Revisión final de rama — v1.1 «Rediseño del panel»

**Rango:** `2fa5b31..18743fb` (12 commits, 27 archivos, +1467/−704)
**Spec:** `docs/superpowers/specs/2026-09-16-rediseno-panel-design.md` (autoridad)
**Plan:** `docs/superpowers/plans/2026-09-16-rediseno-panel.md`
**Revisor:** revisión de rama completa, solo lectura (árbol limpio, sin build ni servidor).

**Cómo se leyó el diff (210 KB / 4528 líneas), en seis pasadas:**
1. Fundación CSS (`tokens.scss`, `base.scss`, `quasar.variables.scss`, `main.js`), `iconos.js`, `ChipEstado.vue`, `App.vue`.
2. `Panel.vue` + `TarjetaDestino.vue` (diff completo + archivos en HEAD).
3. Diálogos y formularios (`DialogoDestino`, `DialogoWebhook`, `ConectarCuenta`, `AsistenteCredenciales`, `Asistente`).
4. Tarjetas de sesión (`Chat`, `TituloEnVivo`, `VistaPrevia`, `RegistroEventos`).
5. Páginas (`Ajustes`, `Sesion`, `Historial`, `Grabaciones`, `Creditos`).
6. i18n (`i18n-check` + detección de huérfanas), docs y comprobaciones cruzadas por `grep`
   (colores de Quasar sin tokens, `text-caption`/`text-grey`, `q-badge`, `dense`, `:deep()`,
   `font-size`, `.btn-touch`, `maximized`, `color-mix`).

Verificaciones contra el código real de Quasar en `web/node_modules/quasar/src`:
`use-tab.js`/`QTabs.js` (modelo de ruta y `role="tab"`), `use-checkbox.js` + `visibility.sass`
(`.no-outline`), `QIcon.js` (`aria-hidden` forzado), `QBtn.sass` (alturas de `dense`/`round`),
`QChip.js` (`size="sm"` = 10 px), `QItem.sass` (`min-width: 0`), `QToggle.sass` (halo de foco).

---

## Strengths

- **La fundación es sólida y está bien argumentada.** `tokens.scss` y `base.scss` son pequeños,
  comentados y con el porqué escrito (contraste medido, «no añadas tonos a mano»). El barrido
  funcionó: **cero** `text-caption`, `text-grey-*`, `text-h6`, `text-body2` y **cero**
  `rgba(255,255,255,…)` quedan en `web/src` (el único literal es el `#000` de `VistaPrevia.vue:162`,
  documentado como deliberado). Eso es exactamente el defecto §1.1 #7 eliminado de raíz.
- **Los ocho defectos de §1.1 están atacados**: el `arrow_drop_down` roto desaparece (`:dropdown-icon`
  explícito en los cuatro `q-btn-dropdown`/`q-select`/`q-expansion-item` que lo necesitaban); el asa
  duplicada se queda en una sola dentro de la tarjeta; la barra lleva etiquetas; la rejilla 1/2/3
  columnas está bien implementada con `minmax`/`repeat`; la tarjeta gana jerarquía con el pie
  plegable recordado en `localStorage`; hay esqueletos con `aria-busy` + `.sr-only` en App, Panel,
  Sesión, Historial y Grabaciones.
- **Preservación de comportamiento: impecable.** Los ocho `emit` de `TarjetaDestino` siguen
  intactos, igual que el `handle=".arrastre"` + `@end="guardarOrden"`, el `watch` con
  `arrastrando`, los diálogos de confirmación con `escaparHtml`, el fallback de `execCommand` para
  copiar, la rotación de clave, la descarga por blob de Ajustes, los filtros de Sesión y todas las
  rutas. `stores/panel.js` y `api.js` **no se tocan**, `package.json` intacto, `diagnostico.js`
  sin cambios. La restricción «sin cambios de funciones ni de API» se cumplió al pie de la letra.
- **Mejoras reales de accesibilidad y robustez que el spec no pedía:** los `<a href="#">` falsos
  pasan a `q-btn` de verdad (DialogoDestino, AsistenteCredenciales, ConectarCuenta), validación de
  campos obligatorios con foco al primer inválido y limpieza al cambiar de plataforma, `role="menu"`
  + `role="menuitemradio"` + `aria-checked` en el idioma, `aria-live` en la espera de ConectarCuenta,
  y el `16/9` reservado en la vista previa (sin salto de layout).
- **`localStorage` y los observadores están bien tratados:** las tres lecturas/escrituras van en
  `try/catch` con comentario de qué pasa si falla; `Ajustes.vue` evita el `IntersectionObserver` a
  propósito y lo documenta, así que no hay nada que limpiar en `onUnmounted`; `Panel.vue` limpia su
  `setInterval`. No encontré ninguna fuga.
- **Los comentarios explican decisiones, no código.** El de `:model-value="route.name"` (carrera de
  `verifyRouteModel`), el de `:deep()` en las pestañas móviles y el de «sin botón de copiar en la
  máscara» son el tipo de comentario que ahorra una tarde a quien venga detrás.
- `node scripts/i18n-check.mjs` limpio (420 claves, 0 faltantes).

---

## Issues

### Critical (Must Fix)

Ninguno. No hay funcionalidad rota, pérdida de datos ni problema de seguridad en esta rama.

### Important (Should Fix)

#### I1. El anillo de foco no llega a los `q-toggle` ni a los `q-checkbox`: la regla es código muerto
`web/src/css/base.scss:34` (y la excepción de `:42`)

`base.scss` lista `.q-toggle:focus-visible` dentro de la regla del anillo, pero Quasar pinta el
`q-toggle` con la clase `.no-outline`, que es `outline: 0 !important`
(`quasar/src/css/core/visibility.sass:11`, confirmado en `use-checkbox.js:98`). `!important` gana
siempre a una regla sin `!important`, así que **esa línea no hace nada**. Se acordaron del problema
para `q-btn` (`.q-btn.no-outline:focus-visible { … !important }` en `:42`, commit `6e985df`) pero no
para los toggles, que son el otro componente de Quasar con `.no-outline`.

Por qué importa: el spec §3.4 nombra los toggles explícitamente («botones, enlaces, filas, **toggles**,
asas»). Lo que queda es el halo propio de Quasar (`QToggle.sass:118-134`: `currentColor` al 12 % de
opacidad detrás del pulgar), idéntico al de `:hover` y muy por debajo del anillo diseñado. Afecta al
interruptor maestro «Todos», al de cada tarjeta de destino, al de cada webhook, al de grabación y al
«Activo» del diálogo de webhook — o sea, a todos los controles de encendido/apagado del panel.

Arreglo (dos líneas, junto a la excepción de `q-btn`):
```scss
.q-toggle.no-outline:focus-visible,
.q-checkbox.no-outline:focus-visible,
.q-radio.no-outline:focus-visible {
  outline: 2px solid var(--ss-ring) !important;
  outline-offset: 2px;
}
```

#### I2. El asa de arrastre es focalizable y a la vez `aria-hidden="true"`; Enter/Espacio no hacen nada
`web/src/components/TarjetaDestino.vue:48-54`

```vue
<q-icon :name="iArrastrar" size="20px" class="arrastre" tabindex="0"
        :aria-label="t('destino.reordenar', { nombre: destino.name })" />
```
`QIcon` **siempre** pone `aria-hidden="true"` en su elemento raíz (`QIcon.js:292`, dentro del objeto
`data` del render); los atributos que caen por herencia (`tabindex`, `aria-label`) se añaden encima,
pero no lo sustituyen. El DOM resultante es
`<i class="q-icon arrastre" aria-hidden="true" tabindex="0" aria-label="Reordenar …">`.

Por qué importa: es la regla `aria-hidden-focus` de axe (severidad *serious*) y una violación de
WCAG 4.1.2 — un elemento oculto al árbol de accesibilidad no puede ser focalizable. Quien navega con
lector de pantalla recibe una parada de tabulación **muda** por cada tarjeta, y el `aria-label`
cuidadosamente traducido no se anuncia nunca. Además, una vez ahí, `Enter`/`Espacio` no reordenan
nada: la parada no lleva a ninguna acción. Es una regresión introducida por la rama: antes
(`Panel.vue`, base) el mismo icono llevaba `aria-label` pero **no** `tabindex`, así que era
decorativo y no violaba nada.

Arreglo: convertir el asa en un control real, con el icono decorativo dentro:
```vue
<button type="button" class="arrastre" :aria-label="t('destino.reordenar', { nombre: destino.name })">
  <q-icon :name="iArrastrar" size="20px" aria-hidden="true" />
</button>
```
y, o bien darle teclado de verdad (`ArrowUp`/`ArrowDown` → mover y `guardarOrden()`), o bien quitar
la parada de tabulación si el reordenado por teclado queda fuera de alcance. Dejar un `tabindex="0"`
que no hace nada es peor que no tenerlo.

#### I3. Objetivos táctiles por debajo de 44 px, y la clase `.btn-touch` es inerte en dos archivos
`web/src/pages/Ajustes.vue:386` (regla), `:306`, `:310`; `web/src/pages/Grabaciones.vue:97`;
`web/src/components/TarjetaDestino.vue:69`, `:124`; `web/src/App.vue:97`, `:109`; `web/src/pages/Panel.vue:408`

El patrón `.btn-touch` se aplicó de forma desigual:

- `Ajustes.vue:386` define **`.acciones-fila .btn-touch`**, es decir, solo dentro de ese contenedor.
  Los otros dos usos de la clase (`:306` «Ir al panel», `:310` «Desconectar») están fuera de
  `.acciones-fila`, así que la clase **no hace nada** ahí: esos botones se quedan en el alto de
  `q-btn flat dense size="sm"` (`QBtn.sass:105`, `min-height: 2em` × 12 px ≈ **24 px**).
- `Grabaciones.vue:97` usa `.btn-touch` y el archivo **no define la clase** (solo
  `.acciones .q-btn { min-height: 44px }` en `:126`, que le da alto pero no ancho): el botón de
  borrar queda en 44 × **28,8 px**.
- Sin ninguna corrección: el menú de la tarjeta (`TarjetaDestino.vue:69`, `flat round dense size="sm"`
  → 2,4 em × 12 px ≈ **28,8 px**), «Reintentar» (`:124`, ≈ 24 px de alto), y los dos desplegables de
  la barra (`App.vue:97`, `:109`, `flat round dense` ≈ **34,5 px**), pese a que el spec §3.3 dice
  literalmente «los botones *dense* de la barra ganan área con padding».
- Relacionado, misma familia: `Ajustes.vue:331-335` conserva los **tres únicos `q-input dense`** que
  quedan en todo el panel (el resto perdió `dense` en las tareas 4 y 5). Son ~40 px de alto y rompen
  la consistencia visual del único formulario de la página.

Por qué importa: §3.3 y §5 exigen ≥ 44 px con ≥ 8 px de separación, y son controles que se pulsan con
el pulgar a mitad de una transmisión (encender un canal, borrar una grabación, desconectar una cuenta).

Arreglo: mover `.btn-touch { min-height: 44px; min-width: 44px; }` a `base.scss` como utilidad global
(ya está duplicada en `Sesion.vue:334` y mal anidada en `Ajustes.vue:386`), aplicarla a los seis
botones que faltan y quitar `dense` de los tres inputs de grabación.

#### I4. Restos del diseño viejo con contraste por debajo de 4,5:1 (el defecto §1.1 #4, sin arreglar del todo)
`web/src/components/DialogoDestino.vue:444-446`; `web/src/diagnostico.js:24` →
`web/src/components/TarjetaDestino.vue:117`; `web/src/pages/Creditos.vue:89,100,114`;
`web/src/pages/Ajustes.vue:255,295`; `web/src/pages/Grabaciones.vue:85`;
`web/src/components/ChipEstado.vue:29`

El barrido llegó a las clases de texto pero no a los componentes que llevan el color en una *prop*:

| Sitio | Qué hay | Medida |
| --- | --- | --- |
| `DialogoDestino.vue:444` | `q-chip … size="sm"` = **10 px** (`QChip.js:24-30`, `sm: 10`), `color="grey-8"` + `text-color="grey-5"` | 10 px y **3,75:1**; los activos, blanco sobre `primary`, **4,46:1** |
| `diagnostico.js:24` (`neutro: { color: 'grey-6' }`) usado en `TarjetaDestino.vue:117` como `text-grey-6` | línea de diagnóstico de todo destino **apagado** (el estado más común) | 14 px, **3,77:1** |
| `Creditos.vue:89/100/114`, `Ajustes.vue:255/295` | `q-badge outline color="grey-6"` | 12 px, **3,77:1** |
| `Grabaciones.vue:85` | `q-badge color="negative"` con texto blanco | 12 px, **3,76:1** |
| `ChipEstado.vue:29` | `.tono-fallo` = `--ss-danger` sobre `--ss-surface-2` | 12 px (`tam-sm`), **4,16:1** |

Los cinco `q-badge` son además el patrón «estado como insignia» que el rediseño sustituyó por
`ChipEstado` en todas partes menos aquí (el de `Grabaciones` sí es un estado: «en curso»).

Por qué importa: es literalmente el defecto que el spec §1.1 #4 mandaba arreglar («chips de estado a
10–11 px, grises al 50–62 %, contraste < 4.5:1 en varios casos») y la restricción global
«Contraste ≥ 4.5:1». Que el resto del panel esté impecable hace que estos cinco sitios canten más.

Arreglo: `size="md"` (o sin `size`) en los chips de capacidades y colores de la paleta derivada;
`neutro: { color: 'grey-6' }` → un tono del sistema (`--ss-fg-muted`, p. ej. añadiendo `.ss-muted`
en vez de `text-${tono.color}` cuando el tono es neutro); los `q-badge` a `ChipEstado tam="sm"` o al
menos con color de token; y para `.tono-fallo`, o bien aclarar el rojo de texto (un
`--ss-danger-text: #f87171` da 6,3:1) o poner el chip sobre `--ss-surface`.

#### I5. `ChipEstado` con `white-space: nowrap` y texto de longitud arbitraria → scroll horizontal a 375 px
`web/src/components/ChipEstado.vue:22` usado desde `web/src/pages/Ajustes.vue:257`

```vue
<ChipEstado tam="sm" :tono="tonoEntrega(w)" :texto="estadoEntrega(w).texto" />
```
y `estadoEntrega` (`Ajustes.vue:186`) devuelve
`t('ajustes.envio_fallo_status', { status, error: w.last_error })` = «último envío falló (500):
*mensaje del servidor*». `w.last_error` es texto arbitrario que viene de la API.

`.chip` es `display: inline-flex; white-space: nowrap` sin `max-width`. El
`.q-item__section--main` que lo contiene tiene `min-width: 0; max-width: 100%`
(`QItem.sass:66-68`), así que la sección sí encoge, pero el chip **desborda** su caja; con
`overflow: visible` en toda la cadena de ancestros, el desbordamiento llega al documento y aparece
scroll horizontal. Es el único sitio donde el texto del chip no está acotado, y es justo el que
rompe la restricción «sin scroll horizontal a 375 px».

Arreglo: sacar el mensaje de error fuera del chip (chip = «falló», el mensaje en una
`q-item-label caption` debajo), o dar al chip `max-width: 100%; min-width: 0` con
`overflow: hidden; text-overflow: ellipsis` en `.texto`.

#### I6. `role="status"` en los doce `ChipEstado`, incluidas listas largas
`web/src/components/ChipEstado.vue:15`

El `role="status"` está en el componente, no en el punto de uso, así que lo llevan por igual el chip
grande de la cabecera de sesión (donde es correcto: cambia solo, en vivo) y:

- una fila por evento en `RegistroEventos.vue:41` (el registro se realimenta del WebSocket cada segundo),
- una fila por evento en la línea de tiempo de `Sesion.vue:233` (puede ser toda la sesión),
- una por sesión en `Historial.vue:102` (con «cargar más», que **añade** filas al DOM),
- una por webhook en `Ajustes.vue:257`,
- y el **nombre de la cuenta vinculada** en `TarjetaDestino.vue:106`, que no es un estado.

Mi juicio sobre la pregunta del brief: sí, es un problema, pero no por la carga inicial. `role="status"`
es una región viva `polite` + `aria-atomic`; las regiones presentes al cargar no se anuncian, así que
una lista de 100 eventos recién pintada es inofensiva. El daño está en la **inserción**: NVDA y
VoiceOver anuncian regiones vivas insertadas después de la carga en buena parte de los casos, y aquí
se insertan constantemente (eventos nuevos por WebSocket, «cargar más» en Historial). El resultado es
que el lector de pantalla recita «aviso», «info», «info», «error»… mientras la persona intenta leer
otra cosa. Y aplicar `role="status"` al nombre de una cuenta es semánticamente incorrecto, sin más.

Arreglo: una prop `vivo` (por defecto `false`) que decida el `role`; ponerla a `true` solo en
`Panel.vue:361` (cabecera de sesión) y `TarjetaDestino.vue:102` (estado del destino).

#### I7. Con `:model-value="route.name"` no hay pestaña activa en `/historial/:id`
`web/src/App.vue:79-93`

El *ruling* de fijar el modelo a `route.name` no se discute aquí; lo que señalo es su efecto
colateral, que el brief pide mirar. Las rutas son planas (`router.js:8-16`), pero `sesion`
(`/historial/:id`) no es ninguna de las cuatro pestañas: al abrir la ficha de una sesión,
`currentModel` vale `'sesion'`, **ninguna** pestaña coincide, y todas quedan con
`aria-selected="false"` y sin indicador. Con la detección automática de Quasar (`verifyRouteModel`,
`QTabs.js:597`) y `exact` desactivado en la pestaña de Historial, ese caso sí se iluminaba.

Por qué importa: el §1.1 #3 y el §3.5 piden «indicador de la ruta activa», y la ficha de sesión es
una pantalla completa del panel donde se pierde. (Nota aparte: `QRouteTab` no emite `aria-current`
—no aparece en `use-router-link.js`—; el `aria-selected` del patrón *tab* lo cubre, salvo
precisamente en este caso.)

Arreglo de una línea:
```js
const pestanaActiva = computed(() => (route.name === 'sesion' ? 'historial' : route.name))
```
y `:model-value="pestanaActiva"`.

#### I8. El icono de «tiene grabación» del historial no tiene `aria-label` (§4 lo pedía por su nombre)
`web/src/pages/Historial.vue:99-101`

```vue
<q-icon v-if="s.has_recording" :name="iGrabaciones" size="18px" class="ss-muted">
  <q-tooltip>{{ t('historial.con_grabacion') }}</q-tooltip>
</q-icon>
```
`QIcon` fuerza `aria-hidden="true"` y el `q-tooltip` solo aparece al pasar el ratón sobre un
elemento que no es focalizable: la información no existe ni para lector de pantalla ni para teclado
ni en táctil. El spec §4 lo dice literal: «`has_recording` como icono **con `aria-label`**».

Arreglo: `<q-icon … role="img" aria-hidden="false" :aria-label="t('historial.con_grabacion')">`
(los atributos heredados pisan los del componente) o un `<span class="sr-only">` al lado.

#### I9. El manual manda pulsar un botón de copiar que esta rama eliminó
`docs/manual-de-usuario.md:23`

> | Clave de retransmisión | La que muestra el panel (usa el botón de copiar) |

El *ruling* de la Task 3 quitó el botón de copiar de la clave enmascarada (correcto: copiaba
`key_mask`, no la clave). Pero el manual —que esta rama sí tocó, en las líneas 374 y 526— sigue
enviando al usuario a ese botón en el **paso 1**, el de configurar OBS, que es el primero que hace
cualquiera. Y ahora mismo no queda ninguna vía en el panel para obtener la clave real salvo
**rotarla** (`Panel.vue:334`), lo que invalida la que ya esté puesta en OBS.

Arreglo: actualizar la fila 23 («la clave se enseña una sola vez, al crearla o al rotarla; si no la
tienes, pulsa **Rotar clave**») y, si el equipo quiere cerrar el círculo, considerar un «ver la clave
de ingesta» equivalente al que ya existe por destino (`Panel.vue:244`, `revelar()`, que además queda
registrado) — eso sería función nueva, así que fuera de esta entrega, pero merece una nota en el
seguimiento.

### Minor (Nice to Have)

1. **El anillo de foco global cambia la forma de los botones redondos.**
   `web/src/css/base.scss:37` añade `border-radius: var(--ss-radius-sm)` a la regla `:focus-visible`.
   `:focus-visible` (0,1,0) y `.q-btn--round` (0,1,0) empatan en especificidad, y `base.scss` se
   importa después de Quasar (`main.js:5-7`), así que gana: **todo botón redondo se convierte en un
   cuadrado de 6 px de radio mientras tiene el foco** (idioma, «más», cerrar de los diálogos, menú de
   la tarjeta). El `outline` ya sigue el radio propio del elemento; basta con borrar esa declaración.
2. **Ocho claves de i18n quedaron huérfanas** (×2 archivos): `destino.descartes`,
   `destino.descartes_plural`, `destino.reconexiones`, `destino.reconexiones_plural` (sustituidas por
   `*_etiqueta` + `formatearNumero` en `TarjetaDestino.vue:129-141`), `sesion.chat_total`,
   `sesion.chat_total_plural`, `sesion.suspendidos`, `sesion.suspendidos_plural` (sustituidas por los
   KPI de `Sesion.vue:191-204`). El criterio de la rama ya fue borrar `panel.recibiendo_senal` al
   quedarse huérfana; estas se escaparon. `i18n-check` no las detecta (solo las cuenta).
3. **Anchos de página fuera de lo que dice el spec.** §3.3 fija 800 px para las páginas de texto;
   `Ajustes`, `Historial`, `Grabaciones` y `Sesion` usan 960 px y `Creditos` 720 px
   (`Ajustes.vue:373`, `Historial.vue:116`, `Grabaciones.vue:116`, `Sesion.vue:275`,
   `Creditos.vue:127`). El 960 viene del plan (Task 6, «Produces»), así que **la desviación está en el
   plan, no en la implementación**; el 720 de Créditos sí es una tercera anchura sin justificar.
4. **El índice lateral pegajoso de Ajustes (§4, ≥ 1024 px) no existe:** hay `q-tabs` a todas las
   anchuras (`Ajustes.vue:225-232`). Otra vez, la decisión está en el plan (Task 6, Step 1, que
   describe solo las pestañas); si el spec manda, falta el caso ≥ 1024. Nota menor asociada: usar
   `q-tabs` como índice de desplazamiento produce `role="tab"`/`aria-selected` sin `tabpanel`; unos
   enlaces `<a href="#seccion">` serían más honestos semánticamente.
5. **Vacíos sin acción**, contra §3.4 («icono + frase + acción»): `Grabaciones.vue:75-79` (el propio
   texto dice «actívala en Ajustes» pero no hay botón) e `Historial.vue:79-83`. Los de Panel y
   Ajustes sí la tienen, de ahí la inconsistencia.
6. **«Cargar más» como `flat` en vez de secundario `outline`** (`Historial.vue:109`,
   `Grabaciones.vue:103`); §4 lo pide como botón secundario. Por lo demás la jerarquía
   `unelevated primary` / `outline` / `flat` es consistente en toda la rama.
7. **Colores de Quasar no derivados de los tokens** en los banners: `bg-red-10 text-red-2`
   (`App.vue:176`, `DialogoDestino.vue:482,561,568`, `DialogoWebhook.vue:138`,
   `ConectarCuenta.vue:123`) y `bg-grey-9` (`DialogoDestino.vue:395,453`,
   `AsistenteCredenciales.vue:113,164`). Contrastan bien y son consistentes entre sí, pero el §3.1
   dice «ningún componente escribe un color a mano: usa `var(--ss-…)` o las clases de Quasar
   **derivadas de la paleta**», y estas no lo están.
8. **No hay ni un solo elemento de encabezado** (`<h1>`–`<h6>`) en todo `web/src`: los títulos son
   `div.ss-t-22` / `div.ss-t-18`. Con lector de pantalla no se puede navegar por encabezados. El
   spec no lo pide, pero es barato: `.ss-t-*` son clases, se pueden poner sobre `<h1>`/`<h2>`.
9. **El esqueleto del Panel no tiene salida de error.** `Panel.vue:456` muestra el esqueleto mientras
   `panel.cargando || !panel.estado`; si `cargar()` falla con algo que no sea un 401
   (`stores/panel.js:78`), `errorConexion` se rellena pero **no se pinta en ninguna parte**
   (0 usos en la vista), así que queda un esqueleto animándose para siempre. Antes se veía el vacío
   con su acción. El fallo de fondo es previo a la rama; el esqueleto lo hace más silencioso.
10. **Clase muerta**: `header-class="pie-cabecera …"` en `TarjetaDestino.vue:152` — `.pie-cabecera`
    no tiene ninguna regla en el archivo.
11. **`color-mix()`** en `DialogoDestino.vue:650` no tiene *fallback* explícito, pero degrada bien
    (si el navegador no lo soporta, la declaración se descarta y queda el fondo de `.tarjeta-plataforma`;
    el borde `--ss-primary` sigue marcando la selección). Solo lo anoto para que conste que se miró.
12. **`max-width: 100vw` en las tarjetas de diálogo** (`DialogoDestino.vue:344`,
    `DialogoWebhook.vue:100`): `100vw` incluye la barra de desplazamiento, así que en escritorio con
    barra clásica puede pedir unos píxeles más de los que hay. `100%` es lo correcto.
13. **El bloque de ingesta puede desbordar a 375 px**: `Panel.vue:406-407`, `.col.ss-mono` dentro de
    un `row no-wrap` sin `min-width: 0` ni `overflow-wrap`. Una URL RTMP larga (nombre de host, no IP)
    no rompe línea y empuja la fila. Un `min-width: 0; overflow-wrap: anywhere` lo cierra.
14. **`matchMedia` se lee una sola vez** (`Panel.vue:23`, `Ajustes.vue:218`): si el sistema cambia la
    preferencia de movimiento con el panel abierto, no se entera hasta recargar. Aceptable y
    documentado; un `addEventListener('change', …)` lo cerraría.
15. **`ChipEstado` ignora `icono` cuando `pulso && tam === 'lg'`** (`ChipEstado.vue:16-17`): el
    `:icono="iSenal"` que le pasa `Panel.vue:364` se descarta en silencio a favor del punto. No rompe
    §3.4 (el texto sigue ahí), pero la API del componente engaña.
16. **El párrafo nuevo del spec base promete más de lo que hay**
    (`docs/superpowers/specs/2026-09-01-rtmp-relay-design.md`, §10): «anillo de foco visible en todo
    lo interactivo» (I1), «objetivos táctiles ≥ 44 px» (I3) y «nunca un color suelto» (I4, punto 7).
    Al arreglar I1/I3/I4 el párrafo pasa a ser cierto; si no, conviene matizarlo.
17. **`aria-label` que no contiene la etiqueta visible** en el interruptor maestro
    (`Panel.vue:440-443`): etiqueta «Todos», nombre accesible «Apagar todos los canales» — WCAG 2.5.3
    (*Label in Name*) para control por voz. Es previo a la rama.

---

## Recommendations

1. **Antes del PR, tres arreglos de CSS/marcado que valen minutos y cierran la mitad del informe:**
   la excepción `.no-outline` para toggles (I1), quitar `border-radius` de la regla de foco
   (Minor 1) y mover `.btn-touch` a `base.scss` aplicándola a los seis botones que faltan (I3).
2. **Convertir el asa en un `<button>` con el icono decorativo dentro** (I2). Si el reordenado por
   teclado no entra en esta entrega, quitar el `tabindex` y anotarlo: una parada de tabulación muda
   y sin acción es peor que ninguna.
3. **Rematar el barrido de la Task 7 en los componentes que llevan el color como *prop***
   (`q-chip size`, `q-badge color`, `TONOS` de `diagnostico.js`) (I4). El barrido buscó clases en las
   plantillas; el patrón que se le escapó es `:color="'grey-8'"` y `color: 'grey-6'` en JS. Un
   `grep -rn "grey-[0-9]\|size=\"sm\"" web/src` como parte de la puerta evitaría que vuelva.
4. **Prop `vivo` en `ChipEstado`** (I6) y **`max-width` en el chip** (I5): dos cambios en el mismo
   archivo que arreglan el problema de lector de pantalla y el de scroll horizontal a la vez.
5. **Actualizar `docs/manual-de-usuario.md:23`** (I9) en el mismo commit que el resto de la
   documentación; y decidir en el seguimiento si hace falta un «ver la clave de ingesta» (función
   nueva, entrega aparte).
6. **Para la QA visual del controlador**, los cuatro sitios que conviene mirar con el navegador
   porque razoné desde el CSS y no desde píxeles: el desbordamiento del chip de error de webhook a
   375 px (I5), el bloque de ingesta con una URL larga (Minor 13), la forma de los botones redondos
   con foco de teclado (Minor 1) y la pestaña activa al entrar en `/historial/:id` (I7).
7. **Dos desviaciones están en el plan, no en el código** (anchura de 960 px y ausencia de índice
   lateral en Ajustes ≥ 1024 px, Minor 3 y 4): o se acepta y se enmienda el spec §3.3/§4 en el mismo
   PR, o se abre una tarea de seguimiento. No es trabajo que el implementador dejara a medias.
8. **Lo que está fuera de alcance se respetó**: no hay modo claro, no hay tests de componentes, no
   hay dependencias ni fuentes nuevas, `package.json`/`package-lock.json` intactos y ninguna llamada
   a terceros. Nada que corregir ahí.

---

## Assessment

**Ready to merge?** With fixes

**Reasoning:** La rama cumple el grueso del spec sin tocar funciones ni API —los ocho defectos de
§1.1 están atacados, la fundación de tokens es limpia y la preservación de comportamiento es
impecable—, pero deja tres cosas que yo bloquearía: dos regresiones de accesibilidad introducidas por
el propio rediseño (el asa focalizable y `aria-hidden`, y el anillo de foco muerto en todos los
toggles) y restos del diseño viejo con contraste por debajo de 4,5:1, que es justo el defecto §1.1 #4
que esta entrega existía para arreglar. Son arreglos pequeños y localizados: con I1–I5 resueltos,
esto entra.
