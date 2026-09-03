<script setup>
import { iArrastrar, iBroadcast, iCopiar, iMas, iRotar } from '@/iconos'
import { ref, watch, onMounted, onUnmounted, computed } from 'vue'
import { useQuasar } from 'quasar'
import draggable from 'vuedraggable'
import { usePanel } from '@/stores/panel'
import { api, ApiError } from '@/api'
import { bitrateLegible, duracionLegible } from '@/diagnostico'
import DialogoDestino from '@/components/DialogoDestino.vue'
import TarjetaDestino from '@/components/TarjetaDestino.vue'

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

watch(
  () => panel.destinos,
  (nuevos) => {
    if (arrastrando.value) return
    lista.value = [...nuevos]
  },
  { immediate: true, deep: false },
)

const tiempoEmitiendo = computed(() => {
  const t = panel.sesion.started_at
  if (!panel.haySesion || !t) return null
  return duracionLegible((Date.now() - new Date(t).getTime()) / 1000)
})

let tic
onMounted(() => { tic = setInterval(() => { ahora.value = Date.now() }, 1000) })
onUnmounted(() => clearInterval(tic))
const ahora = ref(Date.now())

function abrirAlta() { editando.value = null; dialogo.value = true }
function abrirEdicion(d) { editando.value = d; dialogo.value = true }

async function trasGuardar() {
  await panel.cargar()
  $q.notify({ type: 'positive', message: 'Destino guardado' })
}

async function alternar(d) {
  try {
    await api.alternarDestino(d.id)
    await panel.cargar()
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}

function borrar(d) {
  // Confirmación antes de una acción irreversible, nombrando lo que se va a borrar.
  $q.dialog({
    title: 'Eliminar destino',
    message: `Se eliminará «${d.name}». Los eventos que ya registró se conservan.`,
    cancel: { flat: true, noCaps: true, label: 'Cancelar' },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: 'Eliminar' },
    persistent: true,
  }).onOk(async () => {
    try {
      await api.borrarDestino(d.id)
      await panel.cargar()
      $q.notify({ type: 'positive', message: 'Destino eliminado' })
    } catch (e) {
      $q.notify({ type: 'negative', message: e.message })
    }
  })
}

async function revelar(d) {
  try {
    const { key } = await api.revelarClave(d.id)
    $q.dialog({
      title: `Clave de ${d.name}`,
      message: `<code class="clave-revelada">${key}</code>`,
      html: true,
      ok: { flat: true, noCaps: true, label: 'Cerrar' },
    })
    // El backend deja constancia de cada revelado; refrescamos para que se vea en el log.
    panel.refrescarEventos()
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
      $q.notify({ type: 'positive', message: `${que} copiado` })
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
    $q.notify({ type: 'positive', message: `${que} copiado` })
    return true
  } catch {
    $q.notify({
      type: 'warning',
      message: 'Tu navegador no deja copiar aquí. Selecciona el texto y cópialo a mano.',
      timeout: 6000,
    })
    return false
  }
}

/** Rota la clave de ingesta, tras confirmar y avisando de lo que implica. */
function confirmarRotacion() {
  $q.dialog({
    title: 'Rotar la clave de ingesta',
    message:
      'La clave actual dejará de servir y tendrás que pegar la nueva en OBS.' +
      (panel.haySesion
        ? '<br><br><b>Estás transmitiendo ahora mismo.</b> La transmisión en curso ' +
          'continúa; la clave nueva hará falta la próxima vez que arranques OBS.'
        : ''),
    html: true,
    cancel: { flat: true, noCaps: true, label: 'Cancelar' },
    ok: { color: 'primary', unelevated: true, noCaps: true, label: 'Rotar' },
  }).onOk(rotarClave)
}

async function rotarClave() {
  rotando.value = true
  try {
    const { key } = await api.rotarClave(false)
    await panel.cargar()
    panel.refrescarEventos()

    // La clave se enseña UNA sola vez: es la única ocasión de copiarla. Por eso el diálogo
    // no se puede cerrar por accidente pulsando fuera.
    $q.dialog({
      title: 'Tu clave nueva',
      message:
        '<p>Pégala en OBS ahora. <b>No volverá a mostrarse.</b></p>' +
        `<p class="clave-nueva">${key}</p>`,
      html: true,
      persistent: true,
      ok: { flat: true, noCaps: true, label: 'Ya la copié' },
      cancel: { unelevated: true, color: 'primary', noCaps: true, label: 'Copiar' },
    }).onCancel(() => {
      // El botón "Copiar" ocupa el sitio de cancelar para que quede a la derecha, que es
      // donde va la acción principal.
      copiar(key, 'Clave')
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
            {{ panel.haySesion ? 'Recibiendo señal' : 'Sin señal' }}
          </div>
          <div class="text-caption text-grey-5">
            <template v-if="panel.haySesion">
              <span v-if="panel.resolucion">{{ panel.resolucion }}</span>
              <span v-else>resolución pendiente</span>
              · {{ bitrateLegible(panel.sesion.bitrate_bps) }}
              <span v-if="tiempoEmitiendo"> · {{ tiempoEmitiendo }}</span>
            </template>
            <template v-else>Arranca la transmisión en OBS para empezar</template>
          </div>
        </div>
      </q-card-section>

      <q-separator />

      <q-card-section v-if="panel.ingesta" class="q-gutter-sm">
        <div class="text-caption text-grey-5">Configura esto en OBS</div>
        <!-- Ancho acotado: el botón de copiar pegado al texto en vez de al otro extremo
             de un monitor de 27 pulgadas. -->
        <div class="row items-center no-wrap q-gutter-sm bloque-ingesta">
          <div class="col campo-mono">{{ panel.ingesta.url }}</div>
          <q-btn flat round dense :icon="iCopiar" aria-label="Copiar el servidor"
                 @click="copiar(panel.ingesta.url, 'Servidor')" />
        </div>
        <div class="row items-center no-wrap q-gutter-sm bloque-ingesta">
          <div class="col campo-mono">{{ panel.ingesta.key_mask }}</div>
          <q-btn flat dense no-caps size="sm" label="Rotar clave" :icon="iRotar"
                 :loading="rotando" @click="confirmarRotacion" />
        </div>
      </q-card-section>
    </q-card>

    <div class="row items-center q-mb-sm">
      <div class="text-h6">Canales</div>
      <q-space />
      <q-btn unelevated no-caps color="primary" :icon="iMas" label="Vincular canal"
             @click="abrirAlta" />
    </div>

    <!-- Estado vacío con la acción, no solo un texto triste. -->
    <q-card v-if="!lista.length" flat bordered class="q-pa-lg text-center">
      <q-icon :name="iBroadcast" size="42px" class="text-grey-7" />
      <div class="text-subtitle1 q-mt-sm">Todavía no hay canales vinculados</div>
      <div class="text-body2 text-grey-5 q-mt-xs q-mb-md">
        Vincula YouTube, Twitch, Facebook o cualquier servidor RTMP y emitirás a todos a la vez.
      </div>
      <q-btn unelevated no-caps color="primary" :icon="iMas" label="Vincular el primero"
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
                  :aria-label="`Reordenar ${element.name}`" />
          <TarjetaDestino
            class="col"
            :destino="element"
            :hay-sesion="panel.haySesion"
            @editar="abrirEdicion(element)"
            @alternar="alternar(element)"
            @borrar="borrar(element)"
            @revelar="revelar(element)"
          />
        </div>
      </template>
    </draggable>

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
