<script setup>
import { iArrastrar, iBroadcast, iCopiar, iMas, iRotar } from '@/iconos'
import { ref, onMounted, onUnmounted, computed } from 'vue'
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
const claveIngestaVisible = ref(false)
const claveIngesta = ref(null)
// Copia local para el arrastre: mutar directamente lo que empuja el WebSocket haría que la
// lista saltara bajo el dedo en mitad del gesto.
const ordenLocal = ref(null)

const lista = computed({
  get: () => ordenLocal.value ?? panel.destinos,
  set: (v) => { ordenLocal.value = v },
})

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
  if (!ordenLocal.value) return
  const ids = ordenLocal.value.map((d) => d.id)
  try {
    await api.reordenarDestinos(ids)
    await panel.cargar()
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    ordenLocal.value = null
  }
}

async function verClaveIngesta() {
  claveIngestaVisible.value = !claveIngestaVisible.value
}

function copiar(texto, que) {
  navigator.clipboard?.writeText(texto).then(
    () => $q.notify({ type: 'positive', message: `${que} copiado` }),
    () => $q.notify({ type: 'negative', message: 'No se pudo copiar' }),
  )
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
        <div class="row items-center no-wrap q-gutter-sm">
          <div class="col campo-mono">{{ panel.ingesta.url }}</div>
          <q-btn flat round dense :icon="iCopiar" aria-label="Copiar el servidor"
                 @click="copiar(panel.ingesta.url, 'Servidor')" />
        </div>
        <div class="row items-center no-wrap q-gutter-sm">
          <div class="col campo-mono">{{ panel.ingesta.key_mask }}</div>
          <q-btn flat dense no-caps size="sm" label="Rotar clave" :icon="iRotar"
                 @click="$emit('rotar')" />
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
    <q-card v-if="!panel.destinos.length" flat bordered class="q-pa-lg text-center">
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

<style scoped>
.indicador {
  width: 12px; height: 12px; border-radius: 50%;
  background: rgba(255, 255, 255, 0.2);
}
.indicador.vivo {
  background: var(--q-positive);
  box-shadow: 0 0 0 4px rgba(34, 197, 94, 0.18);
}
.campo-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  word-break: break-all;
  color: rgba(255, 255, 255, 0.85);
}
.arrastre { cursor: grab; touch-action: none; }
</style>
