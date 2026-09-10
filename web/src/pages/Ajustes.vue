<script setup>
import { ref, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iMas, iEditar, iBorrar, iWebhook, iDescargar, iProbar } from '@/iconos'
import { api } from '@/api'
import DialogoWebhook from '@/components/DialogoWebhook.vue'

// Ajustes que no caben en el panel principal: avisos por webhook y respaldo. En la v0.9
// gana la grabación.
const $q = useQuasar()
const webhooks = ref([])
const dialogo = ref(false)
const editando = ref(null)
const probando = ref(null)
const respaldando = ref(false)

async function cargar() {
  try { webhooks.value = await api.webhooks() } catch (e) { $q.notify({ type: 'negative', message: e.message }) }
}
onMounted(cargar)

function abrirAlta() { editando.value = null; dialogo.value = true }
function abrirEdicion(w) { editando.value = w; dialogo.value = true }

async function alternar(w) {
  try { await api.editarWebhook(w.id, { enabled: !w.enabled }); await cargar() }
  catch (e) { $q.notify({ type: 'negative', message: e.message }) }
}

function borrar(w) {
  $q.dialog({
    title: 'Eliminar aviso', message: `Se eliminará «${w.name}».`,
    cancel: { flat: true, noCaps: true, label: 'Cancelar' },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: 'Eliminar' }, persistent: true,
  }).onOk(async () => {
    try { await api.borrarWebhook(w.id); await cargar() }
    catch (e) { $q.notify({ type: 'negative', message: e.message }) }
  })
}

async function probar(w) {
  probando.value = w.id
  try {
    await api.probarWebhook(w.id)
    $q.notify({ type: 'positive', message: `Aviso entregado a ${w.name}` })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message, timeout: 8000 })
  } finally {
    probando.value = null
    await cargar()
  }
}

/** La descarga del respaldo pasa por un <a download>: fetch con la cookie y luego un blob. */
async function respaldar() {
  respaldando.value = true
  try {
    const { blob, nombre } = await api.descargarRespaldo()
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = nombre
    a.click()
    URL.revokeObjectURL(a.href)
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    respaldando.value = false
  }
}

const estadoEntrega = (w) => {
  if (w.last_status === null) return { texto: 'sin enviar aún', color: 'grey-6' }
  if (w.last_status >= 200 && w.last_status < 300) return { texto: `último envío: ${w.last_status}`, color: 'positive' }
  return { texto: `último envío falló (${w.last_status || 'sin respuesta'}): ${w.last_error}`, color: 'negative' }
}
</script>

<template>
  <q-page class="q-pa-md q-pb-xl ajustes">
    <div class="contenido">
      <div class="row items-center q-mb-sm">
        <div class="text-h6">Avisos</div>
        <q-space />
        <q-btn unelevated no-caps color="primary" :icon="iMas" label="Nuevo aviso" @click="abrirAlta" />
      </div>
      <p class="text-body2 text-grey-5">
        Cuando un canal falla o se corta la emisión, Splitstream puede avisarte en Discord, Slack o
        cualquier servidor que reciba JSON.
      </p>

      <q-card v-if="!webhooks.length" flat bordered class="q-pa-lg text-center">
        <q-icon :name="iWebhook" size="36px" class="text-grey-7" />
        <div class="text-body2 text-grey-5 q-mt-sm">Todavía no hay avisos configurados.</div>
      </q-card>

      <q-list v-else bordered separator class="rounded-borders">
        <q-item v-for="w in webhooks" :key="w.id">
          <q-item-section>
            <q-item-label>{{ w.name }} <q-badge outline color="grey-6" :label="w.format" class="q-ml-xs" /></q-item-label>
            <q-item-label caption class="ellipsis">{{ w.url }}</q-item-label>
            <q-item-label caption :class="`text-${estadoEntrega(w).color}`">{{ estadoEntrega(w).texto }}</q-item-label>
          </q-item-section>
          <q-item-section side>
            <div class="row items-center no-wrap q-gutter-xs">
              <q-btn flat dense no-caps size="sm" :icon="iProbar" label="Probar" :loading="probando === w.id" @click="probar(w)" />
              <q-toggle :model-value="w.enabled" dense @update:model-value="alternar(w)" :aria-label="`${w.enabled ? 'Desactivar' : 'Activar'} ${w.name}`" />
              <q-btn flat round dense :icon="iEditar" size="sm" aria-label="Editar" @click="abrirEdicion(w)" />
              <q-btn flat round dense :icon="iBorrar" size="sm" class="text-negative" aria-label="Eliminar" @click="borrar(w)" />
            </div>
          </q-item-section>
        </q-item>
      </q-list>

      <div class="text-h6 q-mt-xl q-mb-sm">Respaldo</div>
      <q-card flat bordered>
        <q-card-section>
          <p class="text-body2 text-grey-5 q-mb-md">
            Descarga una copia de la base de datos: canales, claves cifradas y contraseña del
            panel. <b>Sin tu clave maestra el archivo no sirve</b>: guárdala aparte.
          </p>
          <q-btn unelevated no-caps color="primary" :icon="iDescargar" label="Descargar respaldo"
                 :loading="respaldando" @click="respaldar" />
        </q-card-section>
      </q-card>
    </div>
    <DialogoWebhook v-model="dialogo" :webhook="editando" @guardado="cargar" />
  </q-page>
</template>

<style scoped>
.contenido { max-width: 760px; margin: 0 auto; }
</style>
