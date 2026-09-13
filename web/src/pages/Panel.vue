<script setup>
import { iArrastrar, iBroadcast, iChat, iCopiar, iGrabar, iMas, iRotar } from '@/iconos'
import { ref, watch, onMounted, onUnmounted, computed } from 'vue'
import { useQuasar } from 'quasar'
import draggable from 'vuedraggable'
import { usePanel } from '@/stores/panel'
import { api, ApiError } from '@/api'
import { bitrateLegible, bytesLegibles, duracionLegible } from '@/diagnostico'
import { t } from '@/i18n'
import DialogoDestino from '@/components/DialogoDestino.vue'
import TarjetaDestino from '@/components/TarjetaDestino.vue'
import VistaPrevia from '@/components/VistaPrevia.vue'
import RegistroEventos from '@/components/RegistroEventos.vue'
import TituloEnVivo from '@/components/TituloEnVivo.vue'
import Chat from '@/components/Chat.vue'

const $q = useQuasar()
const panel = usePanel()

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
      message: `${escaparHtml(r.message)}<br><br><span class="text-caption text-grey-5">${escaparHtml(r.stage)} · ${(r.elapsed_ms / 1000).toFixed(1)} s</span>`,
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
  <q-page class="q-pa-md q-pb-xl">
    <!-- Estado de la ingesta: lo primero que uno mira al abrir el panel. -->
    <q-card flat bordered class="q-mb-md">
      <q-card-section class="row items-center q-gutter-md">
        <div class="indicador" :class="panel.haySesion ? 'vivo' : 'apagado'" aria-hidden="true" />
        <div class="col">
          <div class="text-subtitle1">
            {{ panel.haySesion ? t('panel.recibiendo_senal') : t('panel.sin_senal') }}
          </div>
          <div class="text-caption text-grey-5">
            <template v-if="panel.haySesion">
              <span v-if="panel.resolucion">{{ panel.resolucion }}</span>
              <span v-else>{{ t('panel.resolucion_pendiente') }}</span>
              · {{ bitrateLegible(panel.sesion.bitrate_bps) }}
              <span v-if="tiempoEmitiendo"> · {{ tiempoEmitiendo }}</span>
            </template>
            <template v-else>{{ t('panel.arranca_obs') }}</template>
          </div>
        </div>
        <q-chip
          v-if="panel.grabacion?.active"
          dense square :icon="iGrabar" text-color="white"
          :color="panel.grabacion.degraded ? 'warning' : 'negative'"
        >
          {{ t('panel.grabando_detalle', { bytes: bytesLegibles(panel.grabacion.bytes), pct: porcentajeGrabacion }) }}
          <q-tooltip>
            {{ panel.grabacion.degraded
              ? t('panel.disco_no_da_abasto')
              : t('panel.segmento_detalle', { segmento: panel.grabacion.segments, libres: bytesLegibles(panel.grabacion.free_bytes) }) }}
          </q-tooltip>
        </q-chip>
        <q-btn v-if="panel.haySesion && !verPrevia" flat dense no-caps size="sm"
               :label="t('panel.vista_previa_boton')" @click="verPrevia = true" />
        <q-btn v-if="panel.haySesion && !verChat && panel.destinos.some((d) => d.account && d.capabilities?.chat)"
               flat dense no-caps size="sm" :icon="iChat" :label="t('panel.chat_boton')" @click="verChat = true" />
      </q-card-section>

      <q-separator />

      <q-card-section v-if="panel.ingesta" class="q-gutter-sm">
        <div class="text-caption text-grey-5">{{ t('panel.configura_obs') }}</div>
        <!-- Ancho acotado: el botón de copiar pegado al texto en vez de al otro extremo
             de un monitor de 27 pulgadas. -->
        <div class="row items-center no-wrap q-gutter-sm bloque-ingesta">
          <div class="col campo-mono">{{ panel.ingesta.url }}</div>
          <q-btn flat round dense :icon="iCopiar" :aria-label="t('panel.copiar_servidor')"
                 @click="copiar(panel.ingesta.url, t('panel.servidor'))" />
        </div>
        <div class="row items-center no-wrap q-gutter-sm bloque-ingesta">
          <div class="col campo-mono">{{ panel.ingesta.key_mask }}</div>
          <q-btn flat dense no-caps size="sm" :label="t('panel.rotar_clave')" :icon="iRotar"
                 :loading="rotando" @click="confirmarRotacion" />
        </div>
      </q-card-section>
    </q-card>

    <VistaPrevia v-if="verPrevia" @cerrar="cerrarPrevia" />
    <Chat v-if="verChat" @cerrar="verChat = false" />

    <TituloEnVivo />

    <div class="row items-center q-mb-sm q-gutter-sm">
      <div class="text-h6">{{ t('panel.canales') }}</div>
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
        :aria-label="todosEncendidos ? t('panel.apagar_todos_canales') : t('panel.encender_todos_canales')"
        @update:model-value="alternarTodos(!todosEncendidos)"
      >
        <q-tooltip>
          {{ todosEncendidos ? t('panel.apagar_todos_canales') : t('panel.pasar_a_todos') }}
        </q-tooltip>
      </q-toggle>
      <q-btn unelevated no-caps color="primary" :icon="iMas" :label="t('panel.vincular_canal')"
             @click="abrirAlta" />
    </div>

    <!-- Estado vacío con la acción, no solo un texto triste. -->
    <q-card v-if="!lista.length" flat bordered class="q-pa-lg text-center">
      <q-icon :name="iBroadcast" size="42px" class="text-grey-7" />
      <div class="text-subtitle1 q-mt-sm">{{ t('panel.sin_canales_titulo') }}</div>
      <div class="text-body2 text-grey-5 q-mt-xs q-mb-md">
        {{ t('panel.sin_canales_detalle') }}
      </div>
      <q-btn unelevated no-caps color="primary" :icon="iMas" :label="t('panel.vincular_primero')"
             @click="abrirAlta" />
    </q-card>

    <draggable
      v-else
      v-model="lista"
      item-key="id"
      handle=".arrastre"
      :animation="180"
      class="q-gutter-y-sm"
      @end="guardarOrden"
    >
      <template #item="{ element }">
        <div class="row items-center no-wrap">
          <!-- Asa explícita: sin ella, arrastrar y pulsar compiten en táctil. -->
          <q-icon :name="iArrastrar" size="22px" class="arrastre text-grey-7 q-mr-xs"
                  :aria-label="t('destino.reordenar', { nombre: element.name })" />
          <TarjetaDestino
            class="col"
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
          />
        </div>
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
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.06);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 14px;
  word-break: break-all;
  /* Seleccionable a mano: es el último recurso si el navegador no deja copiar. */
  user-select: all;
}
</style>

<style scoped>
.indicador {
  width: 12px; height: 12px; border-radius: 50%;
  background: rgba(255, 255, 255, 0.2);
}
.indicador.vivo {
  background: var(--q-positive);
  box-shadow: 0 0 0 4px rgba(34, 197, 94, 0.18);
}
.bloque-ingesta { max-width: 560px; }
.campo-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  word-break: break-all;
  color: rgba(255, 255, 255, 0.85);
}
.rejilla-canales {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
  /* Las tarjetas de una fila igualan altura: con alturas dispares la rejilla se ve rota,
     y aquí las alturas varían según haya consejo de diagnóstico o no. */
  align-items: stretch;
}
</style>
