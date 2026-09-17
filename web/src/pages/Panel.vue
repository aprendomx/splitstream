<script setup>
import { iBroadcast, iChat, iCopiar, iGrabacionActiva, iMas, iRotar, iSenal, iSinSenal } from '@/iconos'
import { ref, watch, onMounted, onUnmounted, computed, nextTick } from 'vue'
import { useQuasar } from 'quasar'
import draggable from 'vuedraggable'
import { usePanel } from '@/stores/panel'
import { api, ApiError } from '@/api'
import { bitrateLegible, bytesLegibles, duracionLegible } from '@/diagnostico'
import { t } from '@/i18n'
import DialogoDestino from '@/components/DialogoDestino.vue'
import TarjetaDestino from '@/components/TarjetaDestino.vue'
import ChipEstado from '@/components/ChipEstado.vue'
import VistaPrevia from '@/components/VistaPrevia.vue'
import RegistroEventos from '@/components/RegistroEventos.vue'
import TituloEnVivo from '@/components/TituloEnVivo.vue'
import Chat from '@/components/Chat.vue'

const $q = useQuasar()
const panel = usePanel()

// Se calcula una sola vez al montar la página: no hace falta que sea reactivo, y así el
// arrastre no reconsulta la preferencia del sistema en cada render.
const reducido = window.matchMedia('(prefers-reduced-motion: reduce)').matches

const dialogo = ref(false)
const editando = ref(null)
const rotando = ref(false)
// Copia local para el arrastre.
//
// Se usa un ref de verdad y no un computed con setter: el getter devolvía un array NUEVO en
// cada evaluación, y vuedraggable con v-model reescribía el valor en cada render, lo que
// realimentaba el computed y bloqueaba el hilo principal en un bucle infinito. Se veía como
// una pestaña que no responde ni a una captura de pantalla.
//
// La copia local también evita que la lista salte bajo el dedo: el WebSocket empuja estado
// cada segundo, y mientras se arrastra hay que ignorarlo.
const lista = ref([])
const arrastrando = ref(false)
// La vista previa gasta subida del servidor mientras está abierta: existe solo tras un
// gesto explícito, y cerrarla (o que el servidor la cierre) la desmonta del todo.
const verPrevia = ref(false)
function cerrarPrevia(motivo) {
  verPrevia.value = false
  if (motivo) $q.notify({ type: 'warning', message: motivo })
}
// El chat también existe solo mientras se enseña: se abre y se cierra a mano.
const verChat = ref(false)

watch(
  () => panel.destinos,
  (nuevos) => {
    if (arrastrando.value) return
    lista.value = [...nuevos]
  },
  { immediate: true, deep: false },
)

// Estado del interruptor maestro. Tres situaciones, no dos: todos encendidos, ninguno, y
// mezcla. La mezcla se pinta indeterminada para no mentir sobre lo que hay.
const todosEncendidos = computed(
  () => lista.value.length > 0 && lista.value.every((d) => d.enabled),
)
const algunoEncendido = computed(() => lista.value.some((d) => d.enabled))
const maestroMixto = computed(() => algunoEncendido.value && !todosEncendidos.value)
const alternandoTodos = ref(false)

const tiempoEmitiendo = computed(() => {
  const inicio = panel.sesion.started_at
  if (!panel.haySesion || !inicio) return null
  return duracionLegible((Date.now() - new Date(inicio).getTime()) / 1000)
})

// Porcentaje del tope de grabaciones que ya está ocupado; es lo que decide cuándo la
// retención empezará a borrar.
const porcentajeGrabacion = computed(() => {
  const g = panel.grabacion
  if (!g || !g.max_bytes) return 0
  return Math.min(100, Math.round((100 * g.used_bytes) / g.max_bytes))
})

let tic
onMounted(() => { tic = setInterval(() => { ahora.value = Date.now() }, 1000) })
onUnmounted(() => clearInterval(tic))
const ahora = ref(Date.now())

function abrirAlta() { editando.value = null; dialogo.value = true }
function abrirEdicion(d) { editando.value = d; dialogo.value = true }

async function trasGuardar(avisoLogo) {
  await panel.cargar()
  // El destino sí se guardó; lo que pudo fallar es el logo, que va en otra petición.
  if (avisoLogo) {
    $q.notify({ type: 'warning', message: t('panel.guardado_sin_logo', { motivo: avisoLogo }) })
    return
  }
  $q.notify({ type: 'positive', message: t('panel.destino_guardado') })
}

/**
 * Interruptor maestro.
 *
 * Manda el estado deseado y no una orden de invertir: con unos canales encendidos y otros
 * no, invertir dejaría la mitad al revés de lo que el usuario acaba de pulsar.
 *
 * Apagar mientras se emite corta transmisiones en vivo, así que se confirma diciendo
 * cuántas. Encender no pide nada: no destruye nada y es lo que se pulsa con prisa.
 */
function alternarTodos(encender) {
  if (!encender && panel.haySesion && algunoEncendido.value) {
    const cuantos = lista.value.filter((d) => d.enabled).length
    $q.dialog({
      title: t('panel.apagar_todos_canales'),
      message: t('panel.cortara_transmisiones', { n: cuantos }),
      cancel: { flat: true, noCaps: true, label: t('comun.cancelar') },
      ok: { color: 'negative', unelevated: true, noCaps: true, label: t('panel.apagar_todos_boton') },
      persistent: true,
    }).onOk(() => aplicarTodos(false))
    return
  }
  aplicarTodos(encender)
}

async function aplicarTodos(encender) {
  alternandoTodos.value = true
  try {
    await api.alternarTodos(encender)
    await panel.cargar()
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    alternandoTodos.value = false
  }
}

async function alternar(d) {
  try {
    await api.alternarDestino(d.id)
    await panel.cargar()
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}

async function reintentar(d) {
  try {
    await api.reintentarDestino(d.id)
    await panel.cargar()
    $q.notify({ type: 'info', message: t('panel.reintentando', { nombre: d.name }) })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}

async function alAire(d) {
  try {
    await api.salirAlAire(d.id)
    await panel.cargar()
    $q.notify({ type: 'positive', message: t('panel.salio_al_aire', { nombre: d.name }) })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}

async function terminar(d) {
  try {
    await api.terminarEmision(d.id)
    await panel.cargar()
    $q.notify({ type: 'info', message: t('panel.termino_emision', { nombre: d.name }) })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}

// Claves, no texto: se resuelven con t() al mostrarlas, para que la sonda hable en el
// idioma activo aunque este objeto se construya una sola vez.
const TITULOS_SONDA = {
  plausible: { tituloKey: 'panel.sonda.plausible', tipo: 'positive' },
  closed_early: { tituloKey: 'panel.sonda.closed_early', tipo: 'warning' },
  rejected: { tituloKey: 'panel.sonda.rejected', tipo: 'negative' },
  unreachable: { tituloKey: 'panel.sonda.unreachable', tipo: 'negative' },
}

// Los diálogos de esta página van con html: true porque necesitan marcas —un <code> para
// la clave, un salto de línea en el diagnóstico—, y Quasar pinta título y mensaje tal cual.
// Así que todo dato que entre ahí sale escapado: el nombre de un destino lo escribe quien
// usa el panel, el diagnóstico trae trozos de la URL y la clave viene de la plataforma.
function escaparHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[c])
}

async function probar(d) {
  // Facebook cuenta cada publicación como emisión activa, y las cuenta contra un cupo.
  if (d.platform === 'facebook') {
    const seguir = await new Promise((resolve) => {
      $q.dialog({
        title: t('panel.probar_facebook_titulo'),
        message: t('panel.probar_facebook_mensaje'),
        cancel: { flat: true, noCaps: true, label: t('comun.cancelar') },
        ok: { unelevated: true, noCaps: true, color: 'primary', label: t('panel.probar_igual') },
      }).onOk(() => resolve(true)).onCancel(() => resolve(false))
    })
    if (!seguir) return
  }
  const aviso = $q.notify({ type: 'ongoing', message: t('panel.probando', { nombre: d.name }), timeout: 0 })
  try {
    const r = await api.probarDestino(d.id)
    const sonda = TITULOS_SONDA[r.outcome]
    // r.outcome sin mapear (caso raro) se enseña tal cual: es más útil que ocultarlo.
    const titulo = sonda ? t(sonda.tituloKey) : escaparHtml(r.outcome)
    aviso()
    $q.dialog({
      title: titulo,
      message: `${escaparHtml(r.message)}<br><br><span class="ss-t-12 ss-muted">${escaparHtml(r.stage)} · ${(r.elapsed_ms / 1000).toFixed(1)} s</span>`,
      html: true,
      ok: { flat: true, noCaps: true, label: t('comun.cerrar') },
    })
  } catch (e) {
    aviso()
    $q.notify({ type: 'negative', message: e.message })
  }
}

function borrar(d) {
  // Confirmación antes de una acción irreversible, nombrando lo que se va a borrar.
  $q.dialog({
    title: t('panel.eliminar_destino_titulo'),
    message: t('panel.eliminar_destino_mensaje', { nombre: d.name }),
    cancel: { flat: true, noCaps: true, label: t('comun.cancelar') },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: t('comun.eliminar') },
    persistent: true,
  }).onOk(async () => {
    try {
      await api.borrarDestino(d.id)
      await panel.cargar()
      $q.notify({ type: 'positive', message: t('panel.destino_eliminado') })
    } catch (e) {
      $q.notify({ type: 'negative', message: e.message })
    }
  })
}

async function revelar(d) {
  try {
    const { key } = await api.revelarClave(d.id)
    $q.dialog({
      title: t('panel.clave_de', { nombre: escaparHtml(d.name) }),
      message: `<code class="clave-revelada">${escaparHtml(key)}</code>`,
      html: true,
      ok: { flat: true, noCaps: true, label: t('comun.cerrar') },
    })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}

/**
 * Reordena con el teclado (↑/↓ desde el asa): intercambia el destino con su vecino,
 * sin salir de los límites de la lista, y persiste con la misma función que usa el
 * arrastre. Al terminar, el foco vuelve al asa de la misma tarjeta —ya en su nueva
 * posición— para poder seguir moviéndola sin buscarla de nuevo con Tab.
 */
async function mover(destino, delta) {
  const i = lista.value.findIndex((d) => d.id === destino.id)
  const j = i + delta
  if (i === -1 || j < 0 || j >= lista.value.length) return
  const copia = [...lista.value]
  ;[copia[i], copia[j]] = [copia[j], copia[i]]
  lista.value = copia
  arrastrando.value = true
  await guardarOrden()
  await nextTick()
  document.querySelector(`[data-id="${destino.id}"] .arrastre`)?.focus()
}

async function guardarOrden() {
  const ids = lista.value.map((d) => d.id)
  try {
    await api.reordenarDestinos(ids)
    await panel.cargar()
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    // Se suelta después de recargar: hasta entonces manda la copia local, o la lista
    // volvería un instante al orden viejo delante del usuario.
    arrastrando.value = false
  }
}

/**
 * Copia al portapapeles.
 *
 * navigator.clipboard SOLO existe en contextos seguros: HTTPS o localhost. Al abrir el
 * panel por la IP de la red —que es justo el caso de mirarlo desde el móvil— no existe, y
 * con encadenamiento opcional la llamada era un no-op silencioso: ni copiaba ni avisaba.
 *
 * El respaldo con execCommand está obsoleto pero funciona en contexto inseguro, que es
 * donde hace falta. Si tampoco puede, se dice, en vez de fingir que copió.
 */
async function copiar(texto, que) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(texto)
      $q.notify({ type: 'positive', message: t('panel.copiado', { que }) })
      return true
    }
    const ta = document.createElement('textarea')
    ta.value = texto
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    if (!ok) throw new Error('execCommand devolvió false')
    $q.notify({ type: 'positive', message: t('panel.copiado', { que }) })
    return true
  } catch {
    $q.notify({
      type: 'warning',
      message: t('asistente_credenciales.no_se_pudo_copiar'),
      timeout: 6000,
    })
    return false
  }
}

/** Rota la clave de ingesta, tras confirmar y avisando de lo que implica. */
function confirmarRotacion() {
  $q.dialog({
    title: t('panel.rotar_clave_titulo'),
    message:
      t('panel.rotar_clave_mensaje') +
      (panel.haySesion
        ? `<br><br><b>${t('panel.transmitiendo_ahora')}</b> ${t('panel.rotar_clave_advertencia')}`
        : ''),
    html: true,
    cancel: { flat: true, noCaps: true, label: t('comun.cancelar') },
    ok: { color: 'primary', unelevated: true, noCaps: true, label: t('panel.rotar') },
  }).onOk(rotarClave)
}

async function rotarClave() {
  rotando.value = true
  try {
    const { key } = await api.rotarClave(false)
    await panel.cargar()

    // La clave se enseña UNA sola vez: es la única ocasión de copiarla. Por eso el diálogo
    // no se puede cerrar por accidente pulsando fuera.
    $q.dialog({
      title: t('panel.clave_nueva_titulo'),
      message:
        `<p>${t('panel.pegala_en_obs')} <b>${t('panel.no_volvera_mostrarse')}</b></p>` +
        `<p class="clave-nueva">${escaparHtml(key)}</p>`,
      html: true,
      persistent: true,
      ok: { flat: true, noCaps: true, label: t('panel.ya_la_copie') },
      cancel: { unelevated: true, color: 'primary', noCaps: true, label: t('panel.copiar_boton') },
    }).onCancel(() => {
      // El botón "Copiar" ocupa el sitio de cancelar para que quede a la derecha, que es
      // donde va la acción principal.
      copiar(key, t('panel.clave'))
    })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    rotando.value = false
  }
}
</script>

<template>
  <q-page class="q-pa-md q-pb-xl pagina-panel">
    <!-- Estado de la ingesta: lo primero que uno mira al abrir el panel. -->
    <q-card flat bordered class="q-mb-md">
      <q-card-section class="row items-center q-gutter-md wrap cabecera-sesion">
        <ChipEstado
          tam="lg"
          :tono="panel.haySesion ? 'emitiendo' : 'neutro'"
          :icono="panel.haySesion ? iSenal : iSinSenal"
          :pulso="panel.haySesion"
          :texto="panel.haySesion ? t('panel.en_vivo') : t('panel.sin_senal')"
          anuncia
        />
        <div class="col ss-t-14 ss-muted ss-tabular linea-sesion">
          <template v-if="panel.haySesion">
            <span v-if="panel.resolucion">{{ panel.resolucion }}</span>
            <span v-else>{{ t('panel.resolucion_pendiente') }}</span>
            · {{ bitrateLegible(panel.sesion.bitrate_bps) }}
            <span v-if="tiempoEmitiendo"> · {{ tiempoEmitiendo }}</span>
          </template>
          <template v-else>{{ t('panel.arranca_obs') }}</template>
        </div>
        <span
          v-if="panel.grabacion?.active"
          :title="panel.grabacion.degraded
            ? t('panel.disco_no_da_abasto')
            : t('panel.segmento_detalle', { segmento: panel.grabacion.segments, libres: bytesLegibles(panel.grabacion.free_bytes) })"
        >
          <ChipEstado
            tam="sm"
            :tono="panel.grabacion.degraded ? 'atencion' : 'emitiendo'"
            :icono="iGrabacionActiva"
            :texto="t('panel.grabando_detalle', { bytes: bytesLegibles(panel.grabacion.bytes), pct: porcentajeGrabacion })"
          />
        </span>
        <div class="row items-center q-gutter-sm botones-sesion">
          <q-btn v-if="panel.haySesion && !verPrevia" outline no-caps size="md"
                 :label="t('panel.vista_previa_boton')" @click="verPrevia = true" />
          <q-btn v-if="panel.haySesion && !verChat && panel.destinos.some((d) => d.account && d.capabilities?.chat)"
                 outline no-caps size="md" :icon="iChat" :label="t('panel.chat_boton')" @click="verChat = true" />
        </div>
      </q-card-section>

      <q-separator />

      <q-card-section v-if="panel.ingesta" class="q-gutter-sm">
        <div class="ss-t-14 ss-muted">{{ t('panel.configura_obs') }}</div>
        <!-- Ancho acotado: el botón de copiar pegado al texto en vez de al otro extremo
             de un monitor de 27 pulgadas. -->
        <div class="campo-ingesta">
          <div class="ss-t-12 ss-subtle">{{ t('panel.servidor') }}</div>
          <div class="row items-center no-wrap valor-ingesta ss-surface-2">
            <div class="col ss-mono ss-t-14">{{ panel.ingesta.url }}</div>
            <q-btn flat round dense :icon="iCopiar" size="md" :aria-label="t('panel.copiar_servidor')"
                   @click="copiar(panel.ingesta.url, t('panel.servidor'))" />
          </div>
        </div>
        <div class="campo-ingesta">
          <div class="ss-t-12 ss-subtle">{{ t('panel.clave') }}</div>
          <!-- Sin botón de copiar: esto es una máscara, no la clave real. Copiarla
               engañaría a quien la pegue en OBS; la clave de verdad solo se ve (y se
               copia) una vez, en el diálogo que abre «Rotar clave». -->
          <div class="row items-center no-wrap valor-ingesta ss-surface-2">
            <div class="col ss-mono ss-t-14">{{ panel.ingesta.key_mask }}</div>
          </div>
          <q-btn outline no-caps size="md" class="q-mt-sm" :label="t('panel.rotar_clave')" :icon="iRotar"
                 :loading="rotando" @click="confirmarRotacion" />
        </div>
      </q-card-section>
    </q-card>

    <VistaPrevia v-if="verPrevia" @cerrar="cerrarPrevia" />
    <Chat v-if="verChat" @cerrar="verChat = false" />

    <TituloEnVivo />

    <div class="row items-center q-mb-sm q-gutter-sm">
      <div class="ss-t-22">{{ t('panel.canales') }}</div>
      <q-space />
      <q-toggle
        v-if="lista.length > 1"
        :model-value="maestroMixto ? null : todosEncendidos"
        toggle-indeterminate
        :indeterminate-value="null"
        :disable="alternandoTodos"
        :label="t('panel.todos')"
        dense
        class="q-mr-sm"
        :aria-label="`${t('panel.todos')} — ${todosEncendidos ? t('panel.apagar_todos_canales') : t('panel.encender_todos_canales')}`"
        @update:model-value="alternarTodos(!todosEncendidos)"
      >
        <q-tooltip>
          {{ todosEncendidos ? t('panel.apagar_todos_canales') : t('panel.pasar_a_todos') }}
        </q-tooltip>
      </q-toggle>
      <q-btn unelevated no-caps color="primary" size="md" :icon="iMas" :label="t('panel.vincular_canal')"
             @click="abrirAlta" />
    </div>

    <!-- Esqueleto mientras llega el primer estado: sin él, la rejilla aparece vacía un
         instante y parece que no hay canales. -->
    <div v-if="panel.cargando || !panel.estado" class="rejilla-canales" aria-busy="true">
      <span class="sr-only">{{ t('panel.cargando_canales') }}</span>
      <q-skeleton v-for="n in 3" :key="n" type="rect" height="180px" />
    </div>

    <!-- Estado vacío con la acción, no solo un texto triste. -->
    <q-card v-else-if="!lista.length" flat bordered class="q-pa-lg text-center">
      <q-icon :name="iBroadcast" size="42px" class="ss-muted" />
      <div class="ss-t-18 q-mt-sm">{{ t('panel.sin_canales_titulo') }}</div>
      <div class="ss-t-14 ss-muted q-mt-xs q-mb-md">
        {{ t('panel.sin_canales_detalle') }}
      </div>
      <q-btn unelevated no-caps color="primary" size="md" :icon="iMas" :label="t('panel.vincular_primero')"
             @click="abrirAlta" />
    </q-card>

    <draggable
      v-else
      v-model="lista"
      item-key="id"
      handle=".arrastre"
      tag="div"
      class="rejilla-canales"
      :animation="reducido ? 0 : 180"
      @end="guardarOrden"
    >
      <template #item="{ element }">
        <TarjetaDestino
          :destino="element"
          :hay-sesion="panel.haySesion"
          @editar="abrirEdicion(element)"
          @alternar="alternar(element)"
          @borrar="borrar(element)"
          @revelar="revelar(element)"
          @reintentar="reintentar(element)"
          @probar="probar(element)"
          @al-aire="alAire(element)"
          @terminar="terminar(element)"
          @mover="mover(element, $event)"
        />
      </template>
    </draggable>

    <RegistroEventos class="q-mt-md" />

    <DialogoDestino v-model="dialogo" :destino="editando" @guardado="trasGuardar" />
  </q-page>
</template>

<!-- El diálogo de Quasar se monta fuera de este componente, así que su estilo no puede ir
     en el bloque scoped. -->
<style>
.clave-nueva, .clave-revelada {
  display: block;
  margin-top: 8px;
  padding: 10px 12px;
  border-radius: var(--ss-radius-sm);
  background: var(--ss-surface-2);
  font-family: var(--ss-font-mono);
  font-size: 14px;
  word-break: break-all;
  /* Seleccionable a mano: es el último recurso si el navegador no deja copiar. */
  user-select: all;
}
</style>

<style scoped>
.pagina-panel { max-width: 1280px; margin: 0 auto; }
.cabecera-sesion { row-gap: var(--ss-space-2); }
.linea-sesion { min-width: 180px; }
.botones-sesion { margin-left: auto; }
/* Bajo los 600 px los botones de sesión no caben junto al chip y la línea de resolución:
   se van a una segunda fila, a la derecha. */
@media (max-width: 599px) {
  .botones-sesion { flex-basis: 100%; justify-content: flex-end; margin-left: 0; }
}
.campo-ingesta { max-width: 560px; }
.campo-ingesta + .campo-ingesta { margin-top: var(--ss-space-3); }
.valor-ingesta {
  padding: 8px 12px;
  border-radius: var(--ss-radius-sm);
  margin-top: 2px;
}
/* Un host largo en la URL RTMP no debe empujar la fila fuera de la pantalla a 375 px
   (Minor 13 de la revisión): sin min-width: 0 un hijo flex no encoge por debajo de su
   contenido, y overflow-wrap deja partir la palabra si hace falta. */
.valor-ingesta .col {
  min-width: 0;
  overflow-wrap: anywhere;
}
.rejilla-canales {
  display: grid;
  grid-template-columns: 1fr;
  gap: var(--ss-space-4);
}
@media (min-width: 768px) {
  .rejilla-canales { grid-template-columns: repeat(2, 1fr); }
}
@media (min-width: 1280px) {
  .rejilla-canales { grid-template-columns: repeat(3, 1fr); }
}
</style>
