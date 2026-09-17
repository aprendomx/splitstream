# v1.1 «Rediseño del panel» — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Aplicar al panel un sistema de diseño explícito (tokens, tipografía, espacio, estados, movimiento, navegación con etiquetas) y arreglar los ocho defectos de la línea base, sin cambiar funciones, API ni dependencias.

**Architecture:** Los tokens viven como variables CSS en `web/src/css/tokens.scss` y alimentan tanto los componentes propios (`var(--ss-…)`) como la paleta de Quasar (`quasar.variables.scss`); `web/src/css/base.scss` fija tipografía, foco visible y `prefers-reduced-motion` una sola vez. Un componente nuevo `ChipEstado` unifica el estado (icono + texto + tono) en tarjetas, historial y registro; `App.vue` pasa a `q-tabs` de ruta con etiquetas; `Panel.vue` reparte los canales en una rejilla CSS de 1/2/3 columnas. Cada tarea deja el panel compilando y usable.

**Tech Stack:** Vue 3 + Quasar 2.32 + Pinia 4 + Vite 8 (ya en `main`), SCSS con variables CSS, iconos SVG de `@quasar/extras/mdi-v7` vía `web/src/iconos.js`, `t()` propio con `es.json`/`en.json` y `web/scripts/i18n-check.mjs`.

**Spec:** `docs/superpowers/specs/2026-09-16-rediseno-panel-design.md` (autoridad), con el sistema generado en `web/design-system/splitstream/MASTER.md` adaptado en su §3. Línea base: capturas del 2026-09-16 (en la sesión del controlador; se repiten «después» para comparar).

## Global Constraints

- Cero dependencias nuevas: `web/package.json` y `package-lock.json` intactos; ninguna fuente ni recurso externo (`@import url(…)`, `<link>`, CDN): el panel funciona sin internet. Fuente: `Inter, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif` (Inter solo si está instalada).
- Sin cambios de funciones ni de API: los mismos endpoints, los mismos eventos de componente, el mismo store salvo lo que exija la carga/vacío. `go build ./...` sigue en verde y ningún test de Go cambia.
- Ningún color escrito a mano en componentes: todo por `var(--ss-…)` (tokens del spec §3.1) o clases de Quasar derivadas de la paleta. Ningún texto por debajo de 12 px; 12 px solo con peso 500 y color `--ss-fg-muted` o más claro. Contraste ≥ 4.5:1 (los tokens ya lo cumplen; no inventar tonos).
- Estado nunca solo por color (icono + texto); `aria-label` en todo botón de solo icono; `:focus-visible` con `--ss-ring`; `prefers-reduced-motion` sin pulsos ni transiciones; objetivos táctiles ≥ 44 px y ≥ 8 px entre ellos; sin scroll horizontal a 375 px; sin `v-html` con datos de la API.
- Bilingüe: ningún texto literal en plantillas ni en `script`; claves nuevas en `es.json` y `en.json` (JSON plano, claves ordenadas, sin `ñ`/acentos en las claves); `node scripts/i18n-check.mjs` limpio; textos en español correcto e inglés natural. Las claves existentes no se renombran.
- Iconos solo SVG desde `web/src/iconos.js` (un solo `export { … }`); ninguna dependencia de la fuente Material Icons (`icon="nombre"` como cadena está prohibido: siempre `:icon="iAlgo"`; en `q-btn-dropdown`, `q-select`, `q-expansion-item`, `q-input` con `clearable`, pasar los iconos SVG explícitos: `dropdown-icon`, `expand-icon`, `clear-icon`).
- `cd web && node scripts/i18n-check.mjs && npm run build` limpio al final de cada tarea; comentarios en español; `git commit -F` con los dos trailers de la sesión.

---

## Mapa de archivos

| Archivo | Responsabilidad |
| --- | --- |
| `web/src/css/tokens.scss` (nuevo) | Variables CSS `--ss-*` (color, radio, sombra, espacio, fuente, movimiento) |
| `web/src/css/base.scss` (nuevo) | Fondo/tipografía globales, escala de texto (`.ss-t-*`), foco visible, movimiento reducido, utilidades (`.ss-mono`, `.ss-tabular`, `.ss-surface-2`) |
| `web/src/css/quasar.variables.scss` | Paleta de Quasar derivada de los tokens |
| `web/src/main.js` | Importa `tokens.scss` y `base.scss` |
| `web/src/iconos.js` | Iconos nuevos (`iDesplegar`, `iMasOpciones`, `iPanel`, `iTraducir`, `iSenal`, `iSinSenal`, `iGrabacionActiva`, `iPlegar`) |
| `web/src/components/ChipEstado.vue` (nuevo) | Estado con icono + texto + tono; tamaños `sm`/`md`/`lg` |
| `web/src/App.vue` | Barra con `q-tabs` de ruta, menú de idioma, menú «más», entrada y esqueleto |
| `web/src/pages/Panel.vue`, `web/src/components/TarjetaDestino.vue` | Cabecera de sesión, bloque OBS, rejilla y tarjeta |
| `web/src/components/{DialogoDestino,DialogoWebhook,ConectarCuenta,AsistenteCredenciales,Asistente}.vue` | Diálogos y formularios |
| `web/src/components/{Chat,TituloEnVivo,VistaPrevia,RegistroEventos}.vue` | Tarjetas de la sesión |
| `web/src/pages/{Ajustes,Grabaciones,Historial,Sesion,Creditos}.vue` | Páginas |
| `web/src/i18n/{es,en}.json` | Claves nuevas |
| `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` §10, `docs/manual-de-usuario.md` | Notas del rediseño |

---

### Task 1: Tokens, base global, paleta de Quasar e icono roto de la barra

**Files:**
- Create: `web/src/css/tokens.scss`, `web/src/css/base.scss`
- Modify: `web/src/css/quasar.variables.scss`, `web/src/main.js`, `web/src/iconos.js`, `web/src/App.vue` (solo el `dropdown-icon` del idioma; el resto de la barra es la Task 2)

**Interfaces:**
- Produces: variables `--ss-bg`, `--ss-surface`, `--ss-surface-2`, `--ss-border`, `--ss-border-strong`, `--ss-fg`, `--ss-fg-muted`, `--ss-fg-subtle`, `--ss-primary`, `--ss-primary-hover`, `--ss-on-primary`, `--ss-live`, `--ss-warn`, `--ss-danger`, `--ss-info`, `--ss-ring`, `--ss-radius-sm|md|lg`, `--ss-shadow-1|2`, `--ss-space-1..8`, `--ss-font-sans`, `--ss-font-mono`, `--ss-motion`, `--ss-motion-slow`, `--ss-ease`; clases `.ss-t-12`, `.ss-t-14`, `.ss-t-16`, `.ss-t-18`, `.ss-t-22`, `.ss-t-28` (tamaño + interlineado + peso por defecto), `.ss-mono`, `.ss-tabular`, `.ss-muted`, `.ss-subtle`, `.ss-surface-2`, `.ss-focus` (anillo, aplicado también globalmente a `:focus-visible`); iconos `iDesplegar` (`mdiMenuDown`), `iMasOpciones` (`mdiDotsHorizontal`), `iPanel` (`mdiViewDashboardOutline`), `iTraducir` (`mdiTranslate`), `iSenal` (`mdiSignalVariant`), `iSinSenal` (`mdiSignalOff`), `iGrabacionActiva` (`mdiRecordRec`), `iPlegar` (`mdiChevronUp`), `iDesplegarSeccion` (`mdiChevronDown`).

- [ ] **Step 1: `tokens.scss`**

```scss
// Tokens semánticos del panel (spec v1.1 §3.1). Son la ÚNICA fuente de color, espacio y
// movimiento: los componentes usan var(--ss-…) y la paleta de Quasar se deriva de aquí.
// El contraste de cada par texto/fondo está medido: fg 13:1, fg-muted 6:1, fg-subtle 4.6:1
// sobre --ss-surface. No añadas tonos intermedios a mano.
:root {
  --ss-bg: #0b1020;
  --ss-surface: #131a2b;
  --ss-surface-2: #1b2336;
  --ss-border: #2a3448;
  --ss-border-strong: #475569;
  --ss-fg: #f1f5f9;
  --ss-fg-muted: #a3aec2;
  --ss-fg-subtle: #7c8aa5;
  --ss-primary: #6366f1;
  --ss-primary-hover: #818cf8;
  --ss-on-primary: #ffffff;
  --ss-live: #22c55e;
  --ss-warn: #f59e0b;
  --ss-danger: #ef4444;
  --ss-info: #38bdf8;
  --ss-ring: #a5b4fc;
  --ss-radius-sm: 6px;
  --ss-radius-md: 10px;
  --ss-radius-lg: 14px;
  --ss-shadow-1: 0 1px 2px rgba(0, 0, 0, 0.4);
  --ss-shadow-2: 0 8px 24px rgba(0, 0, 0, 0.45);
  --ss-space-1: 4px;  --ss-space-2: 8px;  --ss-space-3: 12px; --ss-space-4: 16px;
  --ss-space-5: 24px; --ss-space-6: 32px; --ss-space-7: 48px; --ss-space-8: 64px;
  --ss-font-sans: Inter, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  --ss-font-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  --ss-motion: 180ms;
  --ss-motion-slow: 280ms;
  --ss-ease: cubic-bezier(0.2, 0, 0, 1);
}
```

- [ ] **Step 2: `base.scss`**

```scss
// Base global: tipografía, foco y movimiento. Una vez, aquí; los componentes no repiten.
html { color-scheme: dark; }
body, .q-body--dark {
  background: var(--ss-bg);
  color: var(--ss-fg);
  font-family: var(--ss-font-sans);
  font-size: 16px;
  line-height: 1.5;
}
// Escala de texto (spec §3.2): tamaño + interlineado + peso. 12 px solo para etiquetas.
.ss-t-12 { font-size: 12px; line-height: 1.4; font-weight: 500; }
.ss-t-14 { font-size: 14px; line-height: 1.5; }
.ss-t-16 { font-size: 16px; line-height: 1.5; }
.ss-t-18 { font-size: 18px; line-height: 1.3; font-weight: 600; }
.ss-t-22 { font-size: 22px; line-height: 1.3; font-weight: 600; }
.ss-t-28 { font-size: 28px; line-height: 1.25; font-weight: 600; }
.ss-muted { color: var(--ss-fg-muted); }
.ss-subtle { color: var(--ss-fg-subtle); font-weight: 500; }
.ss-mono { font-family: var(--ss-font-mono); }
.ss-tabular { font-variant-numeric: tabular-nums; }
.ss-surface-2 { background: var(--ss-surface-2); border-radius: var(--ss-radius-sm); }
// Texto solo para lectores de pantalla (estados de carga, etiquetas extra).
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
// Foco visible en TODO lo interactivo (spec §3.4). Quasar quita el outline nativo; aquí
// vuelve como anillo con separación, sin depender de que cada componente se acuerde.
:focus-visible,
.q-focus-helper:focus-visible,
.q-btn:focus-visible,
.q-item:focus-visible,
.q-tab:focus-visible,
.q-toggle:focus-visible,
.q-field--focused .q-field__control {
  outline: 2px solid var(--ss-ring);
  outline-offset: 2px;
  border-radius: var(--ss-radius-sm);
}
// Movimiento reducido: sin transiciones ni pulsos. Los componentes con animación propia
// (indicador en vivo, arrastre) consultan la misma media query.
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { transition-duration: 0.01ms !important; animation-duration: 0.01ms !important; animation-iteration-count: 1 !important; }
}
// Tarjeta base de Quasar con los tokens: borde, radio y fondo iguales en todo el panel.
.q-card.q-card--bordered, .q-card--dark.q-card--bordered {
  background: var(--ss-surface);
  border: 1px solid var(--ss-border);
  border-radius: var(--ss-radius-md);
  box-shadow: none;
}
.q-menu, .q-dialog .q-card { background: var(--ss-surface-2); box-shadow: var(--ss-shadow-2); }
.q-dialog__backdrop { background: rgba(0, 0, 0, 0.6); }
```

(Comprueba en el navegador que las reglas de foco no doblan el anillo en `q-input` —si Quasar ya pinta borde de foco en el campo, deja solo el suyo y quita `.q-field--focused .q-field__control` de la lista— y que `.q-card` sin `bordered` no cambia.)

- [ ] **Step 3: `quasar.variables.scss`** — `$primary: #6366f1; $secondary: #818cf8; $accent: #22c55e; $dark: #131a2b; $dark-page: #0b1020; $positive: #22c55e; $negative: #ef4444; $info: #38bdf8; $warning: #f59e0b;` con el comentario existente actualizado (los valores son los tokens; Sass no puede leer variables CSS en tiempo de compilación, por eso se repiten aquí y el comentario dice que `tokens.scss` manda).

- [ ] **Step 4: `main.js` e iconos** — `import '@/css/tokens.scss'` e `import '@/css/base.scss'` justo después de `quasar/src/css/index.sass`. `iconos.js`: añade los nueve iconos de «Produces» al único `export { … }` (comprobados en `mdi-v7`).

- [ ] **Step 5: Icono roto** — `App.vue`: `<q-btn-dropdown … :dropdown-icon="iDesplegar">` (importa `iDesplegar`). Busca en todo `web/src` cualquier otro componente de Quasar que use un icono por defecto de la fuente (`q-select` → `:dropdown-icon="iDesplegar"`; `q-expansion-item` → `:expand-icon="iDesplegarSeccion"`; `q-input clearable` → `:clear-icon="iCerrar"`; `q-btn-toggle`, `q-checkbox`, `q-toggle` pintan SVG propios y no necesitan nada; `q-table` con paginación usa iconos de fuente: si aparece, pásale `icon-first-page` etc. o evita la paginación): `grep -rn "q-select\|q-expansion-item\|clearable\|q-table\|q-pagination\|q-stepper" web/src`.

- [ ] **Step 6: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; con el binario en `:8099` (o `npm run dev` con proxy), abre la barra: el desplegable de idioma muestra un chevron, no texto; el fondo y las tarjetas usan los tokens.

```bash
git add web/src/css web/src/main.js web/src/iconos.js web/src/App.vue
git commit -m "feat(panel): tokens de diseño, base global, paleta derivada y chevron SVG en el idioma"
```

---

### Task 2: Barra de navegación con etiquetas, menús, entrada y esqueleto

**Files:**
- Modify: `web/src/App.vue`, `web/src/i18n/{es,en}.json`

**Interfaces:**
- Consumes: Task 1 (tokens, clases `.ss-t-*`, iconos `iPanel`, `iHistorial`, `iGrabaciones`, `iAjustes`, `iTraducir`, `iMasOpciones`, `iInfo`, `iSalir`).
- Produces: claves `app.panel`, `app.mas`, `app.idioma_actual` («Idioma: {nombre}»), `app.entrar_ayuda` («La contraseña que elegiste al configurar el panel»), `app.cargando`.

- [ ] **Step 1: Barra** — sustituye los cinco botones redondos por:

```vue
<q-toolbar class="barra">
  <q-icon :name="iBroadcast" size="24px" class="q-mr-sm text-primary" aria-hidden="true" />
  <q-toolbar-title class="ss-t-18">Splitstream</q-toolbar-title>
  <q-tabs v-if="panel.autenticado" dense no-caps narrow-indicator active-color="primary"
          indicator-color="primary" class="pestanas" :aria-label="t('app.navegacion')">
    <q-route-tab :to="{ name: 'panel' }" :icon="iPanel" :label="t('app.panel')" exact />
    <q-route-tab :to="{ name: 'historial' }" :icon="iHistorial" :label="t('app.historial')" />
    <q-route-tab :to="{ name: 'grabaciones' }" :icon="iGrabaciones" :label="t('app.grabaciones')" />
    <q-route-tab :to="{ name: 'ajustes' }" :icon="iAjustes" :label="t('app.ajustes')" />
  </q-tabs>
  <q-space v-else />
  <q-btn-dropdown flat dense no-caps :icon="iTraducir" :dropdown-icon="iDesplegar"
                  :label="$q.screen.gt.xs ? nombreIdioma : undefined"
                  :aria-label="t('app.idioma_actual', { nombre: nombreIdioma })" class="q-ml-sm">
    <q-list role="menu">
      <q-item v-for="l in idiomas" :key="l.id" clickable v-close-popup role="menuitemradio"
              :active="l.id === idioma" :aria-checked="l.id === idioma" @click="cambiarIdioma(l.id)">
        <q-item-section>{{ l.nombre }}</q-item-section>
        <q-item-section v-if="l.id === idioma" side><q-icon :name="iOk" size="18px" /></q-item-section>
      </q-item>
    </q-list>
  </q-btn-dropdown>
  <q-btn-dropdown v-if="panel.autenticado" flat round dense :icon="iMasOpciones" :dropdown-icon="iDesplegar"
                  :aria-label="t('app.mas')" class="q-ml-xs">
    <q-list role="menu">
      <q-item clickable v-close-popup :to="{ name: 'creditos' }" role="menuitem">
        <q-item-section avatar><q-icon :name="iInfo" /></q-item-section>
        <q-item-section>{{ t('app.creditos') }}</q-item-section>
      </q-item>
      <q-separator />
      <q-item clickable v-close-popup role="menuitem" class="text-negative" @click="panel.salir()">
        <q-item-section avatar><q-icon :name="iSalir" /></q-item-section>
        <q-item-section>{{ t('app.cerrar_sesion') }}</q-item-section>
      </q-item>
    </q-list>
  </q-btn-dropdown>
</q-toolbar>
```

`nombreIdioma = computed(() => idiomas.find((l) => l.id === idioma.value)?.nombre)`. Con `q-btn-dropdown` que lleva `dropdown-icon` y sin `label` en móvil, el botón queda redondo con el icono de traducir. Estilos (scoped, con tokens): `.barra { min-height: 56px; gap: var(--ss-space-1); }`, `.pestanas .q-tab { min-height: 44px; padding: 0 var(--ss-space-3); }`, y por debajo de 768 px (`@media (max-width: 767px)`) las pestañas con `.q-tab__label { font-size: 12px; }` y `.q-tab__icon { font-size: 20px; }` (Quasar muestra icono + etiqueta apilados con `dense`; comprueba que las cuatro caben a 375 px sin scroll horizontal; si no, quita `label` por debajo de 480 px con `:label="$q.screen.width >= 480 ? … : undefined"` y deja `aria-label`).

- [ ] **Step 2: Entrada y esqueleto** — la tarjeta de entrada: `style="width: 420px; max-width: 100%"`, título `.ss-t-22`, una línea de ayuda `.ss-t-14 ss-muted` (`app.entrar_ayuda`), `q-input` `outlined` (no `dense`), botón `unelevated color="primary" class="full-width"` con `:loading="entrando"`; el error como `q-banner` bajo el campo con `role="alert"` (ya existe: conserva) y el foco vuelve al campo. El estado `panel.cargando` pasa de spinner a un esqueleto de página (`q-skeleton` de una tarjeta 120 px + tres de 96 px) con `aria-busy="true"` y el texto `app.cargando` en `sr-only` (clase `.sr-only` en `base.scss` si no existe).

- [ ] **Step 3: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; en el navegador a 1280 y 375 px: cuatro pestañas con etiqueta, activa marcada, idioma con nombre completo (≥ 600 px) e icono, menú «más» con Créditos y Cerrar sesión; Tab recorre la barra con anillo visible.

```bash
git add web/src/App.vue web/src/i18n
git commit -m "feat(panel): navegación con pestañas etiquetadas, menús de idioma y «más», entrada y esqueleto"
```

---

### Task 3: `ChipEstado`, tarjeta de destino, rejilla y cabecera del panel

**Files:**
- Create: `web/src/components/ChipEstado.vue`
- Modify: `web/src/components/TarjetaDestino.vue`, `web/src/pages/Panel.vue`, `web/src/diagnostico.js` (solo si `TONOS` necesita el nombre del token), `web/src/i18n/{es,en}.json`

**Interfaces:**
- Consumes: Task 1.
- Produces: `ChipEstado` con props `tono` (`emitiendo|atencion|fallo|neutro|trabajando`, los mismos de `TONOS`), `icono` (opcional; por defecto el de `TONOS[tono]`), `texto` (string), `tam` (`sm|md|lg`, por defecto `md`), `pulso` (Boolean: punto animado solo en `lg` y solo sin `prefers-reduced-motion`). Claves `destino.detalles`, `destino.ocultar_detalles`, `panel.servidor_etiqueta`, `panel.clave_etiqueta`, `panel.copiado`, `panel.en_vivo`, `panel.sin_senal_chip`, `panel.cargando_canales`.

- [ ] **Step 1: `ChipEstado.vue`**

```vue
<script setup>
import { computed } from 'vue'
import { TONOS } from '@/diagnostico'
const props = defineProps({
  tono: { type: String, default: 'neutro' },
  icono: { type: String, default: '' },
  texto: { type: String, required: true },
  tam: { type: String, default: 'md' },
  pulso: Boolean,
})
const icono = computed(() => props.icono || TONOS[props.tono]?.icono)
</script>
<template>
  <!-- Estado siempre con icono y texto (spec §3.4): el color acompaña, no informa solo. -->
  <span class="chip" :class="[`tono-${tono}`, `tam-${tam}`]" role="status">
    <span v-if="pulso && tam === 'lg'" class="punto" aria-hidden="true" />
    <q-icon v-else :name="icono" class="icono" aria-hidden="true" />
    <span class="texto">{{ texto }}</span>
  </span>
</template>
<style scoped>
.chip { display: inline-flex; align-items: center; gap: 6px; border-radius: 999px; border: 1px solid var(--ss-border); background: var(--ss-surface-2); color: var(--ss-fg); white-space: nowrap; font-weight: 500; }
.tam-sm { font-size: 12px; line-height: 1; padding: 4px 8px; }
.tam-md { font-size: 14px; line-height: 1; padding: 6px 10px; }
.tam-lg { font-size: 16px; line-height: 1; padding: 8px 14px; }
.icono { font-size: 1.15em; }
.tono-emitiendo { border-color: var(--ss-live); color: var(--ss-live); }
.tono-atencion { border-color: var(--ss-warn); color: var(--ss-warn); }
.tono-fallo { border-color: var(--ss-danger); color: var(--ss-danger); }
.tono-trabajando { border-color: var(--ss-info); color: var(--ss-info); }
.tono-neutro { color: var(--ss-fg-muted); }
.punto { width: 10px; height: 10px; border-radius: 50%; background: currentColor; animation: pulso 1.6s var(--ss-ease) infinite; }
@keyframes pulso { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
@media (prefers-reduced-motion: reduce) { .punto { animation: none; } }
</style>
```

(Los tonos sobre `--ss-surface-2` cumplen 4.5:1: verde `#22c55e` 7.3:1, ámbar 8.1:1, rojo 4.6:1, cian 8.6:1; si un tono no llega, se aclara SOLO en el token, no aquí.)

- [ ] **Step 2: `TarjetaDestino.vue`** — estructura nueva (misma lógica, mismos `emit`):

1. Cabecera: asa (`iArrastrar`, clase `arrastre`, 44 px de alto, `tabindex="0"`, `aria-label` como hoy; en puntero fino se muestra al `:hover`/`:focus-within` de la tarjeta y siempre en táctil: `@media (hover: hover) { .arrastre { opacity: 0.35 } .tarjeta-destino:hover .arrastre, .tarjeta-destino:focus-within .arrastre { opacity: 1 } }`), avatar/plataforma, nombre `.ss-t-18 ellipsis`, `q-toggle` (con `aria-label` como hoy), menú (sin cambios de opciones).
2. Fila de estado: `<ChipEstado :tono="diag.tono" :texto="t(diag.tituloKey)" />` + chip de cuenta (`ChipEstado tam="sm" tono="neutro" :icono="iCuenta"` con el nombre, o `tono="atencion"` si `reauth`) o «sin cuenta» como texto `.ss-t-12 ss-subtle`.
3. Diagnóstico: `detalle` en `.ss-t-14` con color del tono (`.text-…` como hoy) y `consejo` en `.ss-t-14 ss-muted` con icono. Botón «Reintentar» si `suspendido`.
4. Cifras (solo `conCifras`): fila `.ss-t-14 ss-tabular` con tres pares etiqueta/valor (bitrate, descartes, reconexiones) usando `formatearNumero`; etiquetas `.ss-t-12 ss-subtle`.
5. Pie plegable: `q-expansion-item` `dense` con `:expand-icon="iDesplegarSeccion"` y `:label="t('destino.detalles')"` (el `label` cambia a `destino.ocultar_detalles` al abrir), contenido: URL y máscara en `.ss-mono ss-t-14 ss-muted`; estado abierto/cerrado guardado en `localStorage` bajo `splitstream.detalles.<id>` (try/catch). Los botones «Salir al aire»/«Terminar» y «ver en YouTube» quedan FUERA del plegable, en una fila de acciones al final, `unelevated` `size="md"` (≥ 44 px).
6. Borde izquierdo de 3 px por tono (`.tono-emitiendo { border-left-color: var(--ss-live) }` …) manteniendo el chip.
Estilos con tokens; quita todos los `rgba(...)` y el `#1d1d1d`; transición solo de `border-color`/`background` con `--ss-motion`.

- [ ] **Step 3: `Panel.vue`** —
1. Cabecera de sesión: `<ChipEstado tam="lg" :tono="panel.haySesion ? 'emitiendo' : 'neutro'" :icono="panel.haySesion ? iSenal : iSinSenal" :pulso="panel.haySesion" :texto="panel.haySesion ? t('panel.en_vivo') : t('panel.sin_senal_chip')" />`, la línea de resolución · bitrate · tiempo en `.ss-t-14 ss-muted ss-tabular`, la instrucción `arranca_obs` en `.ss-t-14 ss-muted`; el chip de grabación pasa a `ChipEstado tam="sm"` (`tono="fallo"` grabando normal → mejor `tono="emitiendo"` con `iGrabacionActiva`; `atencion` si `degraded`); botones «Vista previa» y «Chat» como `outline no-caps` `size="md"` a la derecha (en < 600 px bajan a una segunda fila con `wrap`).
2. Bloque OBS: dos «campos» de solo lectura: etiqueta `.ss-t-12 ss-subtle` (`panel.servidor_etiqueta`, `panel.clave_etiqueta`), valor `.ss-mono ss-t-14` en un `.ss-surface-2` con padding 8/12 y el botón de copiar (`flat round` 44 px) dentro del mismo contenedor a la derecha; «Rotar clave» como `outline no-caps size="md"` bajo la clave (conserva `confirmarRotacion`). Al copiar, `Notify` con `panel.copiado` si no existe ya un aviso.
3. «Canales»: título `.ss-t-22`; toggle «Todos» con etiqueta; botón principal `unelevated color="primary" size="md"`.
4. Rejilla: el `draggable` pasa a `tag="div"` con clase `rejilla-canales` y `:animation="reducido ? 0 : 180"` (`reducido = window.matchMedia('(prefers-reduced-motion: reduce)').matches`, calculado una vez); el `<template #item>` renderiza SOLO `<TarjetaDestino … />` (quita el asa exterior: la única asa vive en la tarjeta); CSS: `.rejilla-canales { display: grid; grid-template-columns: 1fr; gap: var(--ss-space-4); } @media (min-width: 768px) { … repeat(2, 1fr) } @media (min-width: 1280px) { … repeat(3, 1fr) }`; `q-page` con `max-width: 1280px; margin: 0 auto`.
5. Esqueleto: mientras `panel.cargando` o `!panel.estado`, tres `q-skeleton type="rect" height="180px"` en la rejilla con `aria-busy` y `panel.cargando_canales` en `sr-only`.
6. Vacío: como hoy, con `iBroadcast` 42 px `ss-muted`, título `.ss-t-18`, texto `.ss-t-14 ss-muted`, botón principal.

- [ ] **Step 4: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; en el navegador: a 1280 px tres columnas, a 800 px dos, a 375 px una; una sola asa por tarjeta; chips ≥ 12 px con icono; el pie «Detalles» cerrado por defecto y recordado; Tab llega a asa, toggle, menú y plegable con anillo.

```bash
git add web/src/components/ChipEstado.vue web/src/components/TarjetaDestino.vue web/src/pages/Panel.vue web/src/diagnostico.js web/src/i18n
git commit -m "feat(panel): ChipEstado, tarjeta con jerarquía y detalles plegables, rejilla de canales y cabecera de sesión"
```

---

### Task 4: Diálogos y formularios (destino, webhook, conectar cuenta, asistente de credenciales, asistente inicial)

**Files:**
- Modify: `web/src/components/DialogoDestino.vue`, `web/src/components/DialogoWebhook.vue`, `web/src/components/ConectarCuenta.vue`, `web/src/components/AsistenteCredenciales.vue`, `web/src/components/Asistente.vue`, `web/src/i18n/{es,en}.json`

**Interfaces:**
- Consumes: Task 1 (tokens, `.ss-t-*`, `iDesplegar`, `iDesplegarSeccion`, `iCerrar`), Task 3 (`ChipEstado`).
- Produces: patrón de diálogo (cabecera `.ss-t-18` + botón cerrar 44 px con `aria-label` `comun.cerrar`; cuerpo `scroll`; pie con acción principal `unelevated color="primary"` a la derecha y «Cancelar» `flat` a su izquierda) que Task 6 no toca pero copia si añade diálogos; claves `dialogo_destino.paso_de` («Paso {n} de 2»), `comun.cerrar` (si no existe), `comun.campo_obligatorio`.

- [ ] **Step 1: Estructura común** — en `DialogoDestino.vue` y `DialogoWebhook.vue`:
  - `q-dialog :maximized="$q.screen.lt.sm"` (ya existe en destino; añade en webhook), `q-card` con `style="width: 640px; max-width: 100vw"` y `class="column no-wrap dialogo"` (`.dialogo { max-height: 90vh }`; en maximizado 100 %).
  - Cabecera: `q-card-section class="row items-center no-wrap cabecera"` con título `.ss-t-18 col`, en destino el subtítulo `.ss-t-12 ss-subtle` con `dialogo_destino.paso_de` y el botón cerrar `flat round :icon="iCerrar" v-close-popup size="md"` (44 px) con `:aria-label="t('comun.cerrar')"`; la cabecera lleva `border-bottom: 1px solid var(--ss-border)`.
  - Cuerpo: `q-card-section class="col scroll cuerpo"` con `padding: var(--ss-space-4) var(--ss-space-5)`; entre campos `gap: var(--ss-space-4)` (sustituye `q-gutter-y-md` por `column` + gap para no desplazar el primer campo).
  - Pie: `q-card-actions class="pie"` con `border-top: 1px solid var(--ss-border)`, «Atrás»/«Cancelar» `flat no-caps` a la izquierda (con `q-space`) y la acción principal `unelevated color="primary" no-caps` a la derecha, `:loading` como hoy; en < 600 px los dos botones ocupan `col` y el principal va arriba (`flex-direction: column-reverse` con `gap: var(--ss-space-2)`).
- [ ] **Step 2: Campos** — todo `q-input`/`q-select`: `outlined` sin `dense` (altura 44 px), `:label` visible (no depender de placeholder), `hint` con el texto de ayuda que hoy va en `.text-caption` debajo (mueve el `text-caption text-grey-5` de `cuenta_caption`, `clave_api_caption` a `hint` del campo correspondiente o a un `<p class="ss-t-14 ss-muted">` inmediatamente antes del grupo si aplica a varios campos), errores con `:error`/`error-message` junto al campo (mantén la validación existente; si un campo obligatorio se envía vacío, marca `error` con `comun.campo_obligatorio` y enfoca el primero inválido con `ref.focus()`), `q-select` con `:dropdown-icon="iDesplegar"`, `q-input clearable` con `:clear-icon="iCerrar"`, contraseñas con el ojo (`iVer`/`iOcultar`) y `aria-label` como hoy. Los enlaces «pegar clave a mano»/«traer clave por API» pasan de `<a class="text-caption">` a `q-btn flat dense no-caps color="primary" class="ss-t-14"` (objetivo ≥ 44 px, foco visible).
- [ ] **Step 3: Rejilla de plataformas (paso 1)** — `.rejilla-plataformas { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: var(--ss-space-3) }`; cada `.tarjeta-plataforma` es un `<button type="button">` (no `div`) con `min-height: 96px`, logo 32 px, nombre `.ss-t-14`, borde `var(--ss-border)`, `:hover` `var(--ss-border-strong)`, seleccionada `border-color: var(--ss-primary); background: color-mix(in srgb, var(--ss-primary) 12%, var(--ss-surface))` (si `color-mix` no compila con el Sass de Quasar, usa `outline: 2px solid var(--ss-primary)`), foco por el anillo global; `aria-pressed` cuando está seleccionada. Quita los `rgba(255,255,255,…)` y `font-size: 12/13px` de estilos: usa `.ss-t-14` y `.ss-t-12 ss-subtle`.
- [ ] **Step 4: Conectar cuenta y asistente de credenciales** — `ConectarCuenta.vue`: el código de dispositivo en `.ss-t-28 ss-mono ss-tabular` centrado con `letter-spacing: 0.08em`, dentro de `.ss-surface-2` con padding 16/24 y borde `var(--ss-border)`; «Cancelar» como `q-btn flat no-caps` (no `<a>`); los `q-spinner` + `.text-caption` pasan a `.ss-t-14 ss-muted` con `role="status" aria-live="polite"`. `AsistenteCredenciales.vue`: `q-stepper` con `:done-icon="iOk" :active-icon="iEditar" :error-icon="iFallo"` (comprueba nombres en `iconos.js`; añade a `iconos.js` si falta, mdi `mdiPencilOutline`), `.text-caption text-grey-5` → `.ss-t-14 ss-muted`, el enlace «por qué» → `q-btn flat dense no-caps`, y el estilo `font-size: 13px; color: rgba(255,255,255,0.85)` → `.ss-t-14`.
- [ ] **Step 5: Asistente inicial (`Asistente.vue`)** — tarjeta `width: 480px; max-width: 100%`, icono 40 px `text-primary`, título `.ss-t-22`, descripción `.ss-t-14 ss-muted`; el `q-btn-toggle` de idioma con `no-caps unelevated toggle-color="primary"` y `aria-label` `app.idioma`; campos `outlined` (no `dense`) con ayuda como `hint`; el botón principal `unelevated color="primary" class="full-width" size="md"`; requisitos de contraseña como lista `.ss-t-14 ss-muted` (si ya hay texto, conserva la clave).
- [ ] **Step 6: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; en el navegador: «Vincular canal» a 1280 (diálogo 640 px, dos pasos con «Paso 1 de 2»/«Paso 2 de 2», rejilla de botones, Tab recorre plataformas con anillo) y a 375 (maximizado, pie apilado, botón principal arriba); «Nuevo aviso» en Ajustes; «Conectar cuenta» muestra el código grande.

```bash
git add web/src/components/DialogoDestino.vue web/src/components/DialogoWebhook.vue web/src/components/ConectarCuenta.vue web/src/components/AsistenteCredenciales.vue web/src/components/Asistente.vue web/src/iconos.js web/src/i18n
git commit -m "feat(panel): diálogos con cabecera/cuerpo/pie, campos con etiqueta y ayuda, rejilla de plataformas accesible"
```

---

### Task 5: Tarjetas de la sesión: chat, título en vivo, vista previa y registro de eventos

**Files:**
- Modify: `web/src/components/Chat.vue`, `web/src/components/TituloEnVivo.vue`, `web/src/components/VistaPrevia.vue`, `web/src/components/RegistroEventos.vue`, `web/src/i18n/{es,en}.json`

**Interfaces:**
- Consumes: Task 1, Task 3 (`ChipEstado`).
- Produces: patrón de cabecera de tarjeta (`.cabecera-tarjeta`: fila con icono 20 px + título `.ss-t-16` peso 600 + `q-space` + acciones; `padding: var(--ss-space-3) var(--ss-space-4); border-bottom: 1px solid var(--ss-border)`) que Task 6 reutiliza en Sesión; claves `chat.cuota_restante` (si el texto de cuota no existe ya), `registro.nivel_info|aviso|error` (texto del chip de nivel, si no existen).

- [ ] **Step 1: `Chat.vue`** — cabecera `.cabecera-tarjeta` con `q-tabs dense no-caps` (pestañas ≥ 44 px de alto, `narrow-indicator`, icono de plataforma + nombre); barra de cuota `q-linear-progress size="4px"` con `color="warning"` solo cuando `fraccionCuota > 0.8`, `color="primary"` si no, `track-color` por token (`--ss-surface-2` vía clase, no `grey-9`), y el texto de cuota `.ss-t-12 ss-subtle ss-tabular` a la derecha; `.mensajes` en `.ss-t-14` (`line-height: 1.5`), autor en peso 600 con el color de plataforma, `.insignia` como `ChipEstado tam="sm" tono="neutro"` o, si el chip pesa demasiado en listas largas, una clase `.insignia { font-size: 12px; font-weight: 500; padding: 1px 6px; border: 1px solid var(--ss-border); border-radius: 999px; color: var(--ss-fg-muted) }` (sin `rgba`); vacío `.ss-t-14 ss-muted` centrado con icono `iChat` 28 px; entrada de mensaje `outlined` 44 px con botón enviar `unelevated color="primary"` (`aria-label` como hoy).
- [ ] **Step 2: `TituloEnVivo.vue`** — cabecera `.cabecera-tarjeta` (`iTitulo` + `titulo.titulo`); `q-input`/`q-select` `outlined` sin `dense`, `q-select` con `:dropdown-icon="iDesplegar"`, `hint` con `titulo.se_aplica_a`; botón «Aplicar» `unelevated color="primary" no-caps`; resultados por destino como filas con `ChipEstado tam="sm"` (`emitiendo` si ok, `fallo` si error) + nombre + mensaje `.ss-t-14 ss-muted`.
- [ ] **Step 3: `VistaPrevia.vue`** — cabecera `.cabecera-tarjeta` (el icono que ya usa el botón «Vista previa» del panel, p. ej. `iPlayBox`, + `vista_previa.encabezado`, botón cerrar 44 px con `aria-label`); `.cuadro { aspect-ratio: 16 / 9; background: #000 }` (el negro del lienzo es intencional y es el único color literal permitido, comentado) para reservar espacio y evitar saltos; el aviso `.ss-t-14 ss-muted` centrado; mientras no llega el primer cuadro, `q-skeleton type="rect"` cubriendo el cuadro con `aria-busy`.
- [ ] **Step 4: `RegistroEventos.vue`** — cabecera `.cabecera-tarjeta` (`iRegistro` + `registro.titulo`, contador `.ss-t-12 ss-subtle ss-tabular`); filas con `grid-template-columns: 72px 84px 1fr` (hora `.ss-mono ss-t-12 ss-subtle ss-tabular`, nivel como `ChipEstado tam="sm"` con `tono` `neutro|atencion|fallo` según `info|warn|error` e icono por defecto, mensaje `.ss-t-14`); en < 600 px `grid-template-columns: 72px 1fr` y el chip pasa a la segunda línea; `.fila` con `padding: var(--ss-space-2) var(--ss-space-4)` y `border-bottom: 1px solid var(--ss-border)`; vacío `.ss-t-14 ss-muted`; sin `rgba` ni 10/11 px.
- [ ] **Step 5: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; en el navegador (panel con destinos y sin sesión): registro con chips legibles; abre «Chat» y «Vista previa» desde la cabecera de sesión: cabeceras uniformes, vista previa con relación 16:9 reservada y aviso legible.

```bash
git add web/src/components/Chat.vue web/src/components/TituloEnVivo.vue web/src/components/VistaPrevia.vue web/src/components/RegistroEventos.vue web/src/i18n
git commit -m "feat(panel): chat, título en vivo, vista previa y registro con cabecera uniforme y chips de estado"
```

---

### Task 6: Páginas: ajustes, grabaciones, historial, sesión y créditos

**Files:**
- Modify: `web/src/pages/Ajustes.vue`, `web/src/pages/Grabaciones.vue`, `web/src/pages/Historial.vue`, `web/src/pages/Sesion.vue`, `web/src/pages/Creditos.vue`, `web/src/i18n/{es,en}.json`

**Interfaces:**
- Consumes: Task 1, Task 3 (`ChipEstado`), Task 5 (`.cabecera-tarjeta`: cópiala en `Sesion.vue` con los mismos valores; no la muevas a `base.scss`).
- Produces: patrón de página (`.pagina { max-width: 960px; margin: 0 auto; padding: var(--ss-space-5) var(--ss-space-4) }`, título `.ss-t-22` en fila con acción principal, secciones `.ss-t-18` con `margin-top: var(--ss-space-6)`); claves `ajustes.indice` («Secciones»), `comun.cargando`, `historial.sin_sesiones_detalle`, `grabaciones.sin_grabaciones_detalle`, `sesion.duracion`, `sesion.destinos`, `sesion.reconexiones_etiqueta`, `sesion.chat_etiqueta`.

- [ ] **Step 1: `Ajustes.vue`** — `.contenido` → `.pagina` (960 px); título `.ss-t-22` («Ajustes» con `app.ajustes`); índice de secciones bajo el título como `q-tabs dense no-caps` con cuatro pestañas (`ajustes.avisos_titulo`, `cuentas_titulo`, `grabacion_titulo`, `respaldo_titulo`) que hacen `scrollIntoView({ behavior: reducido ? 'auto' : 'smooth', block: 'start' })` al `<section id>` correspondiente (`aria-label` `ajustes.indice`; la pestaña activa sigue la sección visible con un `IntersectionObserver`, o simplemente se marca al hacer clic si el observer complica); cada sección es `<section :id>` con título `.ss-t-18` y la acción a la derecha; listas `q-list bordered separator` con `q-item` `min-height: 56px`; el estado de cada webhook (`estadoEnvio`) pasa a `ChipEstado tam="sm"` (`tono`: `emitiendo` si ok, `fallo` si error, `neutro` si `sin_enviar_aun`); en cuentas, `reauth` → `ChipEstado tam="sm" tono="atencion"`; `.mono` → `.ss-mono ss-t-14`; los `text-caption text-grey-6` → `.ss-t-14 ss-muted`; vacíos con icono 32 px `ss-muted` + `.ss-t-16` + `.ss-t-14 ss-muted` + acción.
- [ ] **Step 2: `Grabaciones.vue` e `Historial.vue`** — `.pagina`; título `.ss-t-22`; mientras `cargando`, tres `q-skeleton type="rect" height="64px"` con `aria-busy` (`comun.cargando` en `sr-only`); vacío con icono + título `.ss-t-16` + detalle `.ss-t-14 ss-muted` (claves `*_detalle` nuevas: «Cuando termine tu primera emisión aparecerá aquí» / «Activa la grabación en Ajustes para conservar tus emisiones»); filas `q-item min-height: 64px` con `q-item-label` `.ss-t-16` y `caption` `.ss-t-14 ss-muted ss-tabular`; en historial, el nivel de la sesión (`.nivel` 10 px) → `ChipEstado tam="sm"` (tono según el peor nivel: `fallo` si hubo errores, `atencion` si avisos, `emitiendo` si limpia); en grabaciones, tamaño y duración en `.ss-tabular`, botón descargar `outline no-caps size="md"` con icono, borrar `flat` `color="negative"` con `aria-label` (mantén el `Dialog` de confirmación).
- [ ] **Step 3: `Sesion.vue`** — `.pagina`; cabecera con «Sesión #id» `.ss-t-22`, fecha `.ss-t-14 ss-muted` y `ChipEstado` de nivel; el resumen pasa de párrafos a una rejilla de KPIs: `.kpis { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: var(--ss-space-3) }` con celdas `.kpi { background: var(--ss-surface-2); border-radius: var(--ss-radius-sm); padding: var(--ss-space-3) }` (etiqueta `.ss-t-12 ss-subtle`, valor `.ss-t-22 ss-tabular`): duración (`sesion.duracion`), destinos (`sesion.destinos`), reconexiones (`sesion.reconexiones_etiqueta`), suspendidos, mensajes de chat (`sesion.chat_etiqueta`), grabaciones; las frases «sin X» existentes se muestran como valor «0» + la frase en `.ss-t-12 ss-subtle` bajo el número (sin borrar claves); `sesion.sin_metricas` queda como nota `.ss-t-14 ss-muted` bajo la rejilla; línea de tiempo con `.cabecera-tarjeta`, `q-btn-toggle` `no-caps unelevated toggle-color="primary"` (44 px, `aria-label` de filtro) y filas iguales a `RegistroEventos` (misma rejilla 72/84/1fr, `ChipEstado` de nivel, sin `rgba` ni 10/11 px); chat y grabaciones con los mismos vacíos que las páginas.
- [ ] **Step 4: `Creditos.vue`** — `.pagina` con `max-width: 720px`; logo 48 px `text-primary`, «Splitstream» `.ss-t-28`, versión `.ss-t-14 ss-muted ss-tabular`; tarjetas con `.cabecera-tarjeta` (títulos `.ss-t-16`) y cuerpo `.ss-t-14`; enlaces con `color: var(--ss-primary-hover)` y subrayado (foco por anillo).
- [ ] **Step 5: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build`; en el navegador: Ajustes con índice que salta a cada sección; Historial y Grabaciones con esqueleto → lista o vacío; Sesión con KPIs en rejilla; Créditos; todo a 375 px sin scroll horizontal.

```bash
git add web/src/pages web/src/i18n
git commit -m "feat(panel): páginas con patrón común, índice de ajustes, KPIs de sesión, esqueletos y vacíos"
```

---

### Task 7: Barrido final de accesibilidad y consistencia, y documentación

**Files:**
- Modify: cualquier archivo de `web/src` que incumpla el barrido; `docs/superpowers/specs/2026-09-01-rtmp-relay-design.md` (§10 «Panel», si existe la sección; si no, la sección que describa el panel), `docs/manual-de-usuario.md` (menciones a la barra: «icono de engrane» → pestaña «Ajustes», «icono de reloj» → «Historial», etc.), `CHANGELOG.md` si existe.

**Interfaces:**
- Consumes: todo lo anterior.
- Produces: nada nuevo; deja el árbol listo para la revisión visual del controlador.

- [ ] **Step 1: Barrido automático** — ejecuta desde `web/`:

```bash
grep -rn "rgba(\|#[0-9a-fA-F]\{6\}\b\|#[0-9a-fA-F]\{3\}\b" src --include=*.vue | grep -v "tokens.scss\|quasar.variables\|VistaPrevia.vue:.*#000"
grep -rn "font-size: *1[01]px\|font-size: *[0-9]px\|text-grey-[0-9]\|text-caption\|text-h[0-9]\|text-subtitle\|text-body" src --include=*.vue
grep -rn "icon=\"[a-z_]*\"" src --include=*.vue
grep -rn "q-btn[^>]*round" src --include=*.vue | grep -v "aria-label"
grep -rn "v-html" src
```

Todas deben devolver cero líneas (la primera puede devolver el `#000` del lienzo con su comentario; la segunda, ninguna: sustituye lo que quede por `.ss-t-*`/`.ss-muted`/`.ss-subtle`; la cuarta lista botones redondos sin `aria-label`: añádelo). Corrige cada resto en su archivo.

- [ ] **Step 2: Barrido manual** — con `npm run dev` (proxy a `:8099`) o el binario recompilado: (a) Tab por Panel, Ajustes y un diálogo: cada control muestra el anillo y el orden sigue el visual; (b) `prefers-reduced-motion` activado en DevTools (Rendering → Emulate CSS media): sin pulso en «En vivo», sin animación de arrastre; (c) 375 × 812 en Panel, Ajustes, Sesión y diálogo de canal: sin scroll horizontal (`document.documentElement.scrollWidth <= innerWidth`); (d) zoom 200 % en Panel: nada se solapa, los chips envuelven. Anota los ajustes hechos en el informe.
- [ ] **Step 3: Documentación** — spec base: párrafo «Rediseño v1.1» que remite a `docs/superpowers/specs/2026-09-16-rediseno-panel-design.md` y resume tokens en `web/src/css/tokens.scss`, `ChipEstado`, barra con pestañas; manual: actualiza las referencias a la navegación e inserta una nota de que el idioma se cambia desde el menú con el icono de traducción de la barra; `CHANGELOG.md` (si existe): entrada «v1.1 — Rediseño del panel» con los ocho defectos corregidos.
- [ ] **Step 4: Comprobar y commit**

Run: `cd web && node scripts/i18n-check.mjs && npm run build && cd .. && go build ./... && go test ./internal/httpapi/ -run 'TestAPIContract|TestEmbed' -count=1` (los tests del panel embebido, si existen, deben seguir en verde; si ninguno coincide, `go test ./... -count=1` completo).

```bash
git add web/src docs CHANGELOG.md
git commit -m "chore(panel): barrido de accesibilidad y consistencia del rediseño; docs"
```

---

## Self-review

- **Cobertura del spec:** §1.1 defectos → icono roto (T1 paso 5), asa duplicada (T3 paso 3.4), nav solo iconos (T2), texto pequeño/contraste (T1 base + T7 barrido), una columna a 1280 (T3 rejilla), tarjetas ruidosas (T3 paso 2 con pie plegable), rgba a mano (T1 tokens + T7 barrido), sin estados de carga (T2 esqueleto, T3 paso 5, T5 vista previa, T6 listas). §3 tokens/tipo/rejilla/ChipEstado/nav/diálogos/esqueletos/foco/movimiento → T1–T4. §4 por pantalla → T2 (entrada/asistente en T4), T3 (panel), T4 (diálogos), T5 (sesión en vivo), T6 (páginas). §6 gate → cada tarea ejecuta build + i18n-check; las capturas «después» las toma el controlador (fuera del plan, como dice el spec). §7 fuera de alcance respetado: sin modo claro ni tests de componentes.
- **Marcadores:** ninguno «TBD»; los pasos con juicio de implementación (observer de Ajustes, `color-mix`) llevan la alternativa concreta.
- **Consistencia de nombres:** `ChipEstado` props `tono|icono|texto|tam|pulso` iguales en T3, T5, T6; iconos `iDesplegar`, `iDesplegarSeccion`, `iMasOpciones`, `iPanel`, `iTraducir`, `iSenal`, `iSinSenal`, `iGrabacionActiva`, `iPlegar` definidos en T1 y usados con ese nombre; `.cabecera-tarjeta` definida en T5 y copiada en T6; `.pagina` definida en T6; clases `.ss-t-*`, `.ss-muted`, `.ss-subtle`, `.ss-mono`, `.ss-tabular`, `.ss-surface-2`, `.sr-only` de T1/T2.
