<script setup>
import { ref, computed, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iMas, iEditar, iBorrar, iWebhook, iDescargar, iProbar, iGrabaciones, iDisco, iCuenta } from '@/iconos'
import { api } from '@/api'
import { bytesLegibles } from '@/diagnostico'
import { usePanel } from '@/stores/panel'
import { porId } from '@/plataformas'
import DialogoWebhook from '@/components/DialogoWebhook.vue'

// Ajustes que no caben en el panel principal: avisos por webhook y respaldo. En la v0.9
// gana la grabación.
const $q = useQuasar()
const panel = usePanel()
const webhooks = ref([])
const dialogo = ref(false)
const editando = ref(null)
const probando = ref(null)
const respaldando = ref(false)

async function cargar() {
  try { webhooks.value = await api.webhooks() } catch (e) { $q.notify({ type: 'negative', message: e.message }) }
}
onMounted(() => { cargar(); cargarGrabacion(); cargarCuentas() })

// Cuentas conectadas (Twitch y las que se sumen). Nombre y destino salen del propio
// destino en el panel; aquí solo importa la cuenta y a qué canales sigue vinculada.
const cuentas = ref([])

async function cargarCuentas() {
  try { cuentas.value = await api.cuentas() } catch (e) { $q.notify({ type: 'negative', message: e.message }) }
}

/** El nombre de cada destino vinculado, para la confirmación y la lista. */
function nombresDestinos(c) {
  return c.destinations.map((id) => panel.destinos.find((d) => d.id === id)?.name ?? `#${id}`)
}

function desconectar(c) {
  const nombres = nombresDestinos(c)
  const cuantos = nombres.length
  $q.dialog({
    title: 'Desconectar cuenta',
    message:
      `Se desvincularán ${cuantos} ${cuantos === 1 ? 'canal' : 'canales'}` +
      (cuantos ? ` (${nombres.join(', ')})` : '') +
      '; el chat y el título dejarán de funcionar para ' + (cuantos === 1 ? 'él' : 'ellos') + '.',
    cancel: { flat: true, noCaps: true, label: 'Cancelar' },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: 'Desconectar' },
    persistent: true,
  }).onOk(async () => {
    try {
      await api.borrarCuenta(c.id)
      await cargarCuentas()
      await panel.cargar()
      $q.notify({ type: 'positive', message: 'Cuenta desconectada' })
    } catch (e) {
      $q.notify({ type: 'negative', message: e.message })
    }
  })
}

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
    const href = URL.createObjectURL(blob)
    a.href = href
    a.download = nombre
    // Firefox ignora el click de un enlace que no está en el documento, y revocar el blob
    // en el acto le corta la descarga a Safari: se limpia un minuto después.
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(href), 60_000)
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    respaldando.value = false
  }
}

// Ajustes de grabación. El interruptor se aplica al momento (con sesión viva arranca o
// para la grabación); los demás campos se guardan con el botón y valen para la siguiente
// emisión.
const grabacion = ref(null)
const formGrab = ref({ segment_min: 10, max_gb: 20, keep_days: 30 })
const guardandoGrab = ref(false)

async function cargarGrabacion() {
  try {
    grabacion.value = await api.ajustesGrabacion()
    formGrab.value = {
      segment_min: grabacion.value.segment_min,
      max_gb: grabacion.value.max_gb,
      keep_days: grabacion.value.keep_days,
    }
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  }
}

async function aplicarGrabacion(patch) {
  guardandoGrab.value = true
  try {
    grabacion.value = await api.editarAjustesGrabacion(patch)
    $q.notify({ type: 'positive', message: 'Ajustes de grabación guardados' })
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    guardandoGrab.value = false
  }
}

function alternarGrabacion(encender) {
  // Apagar a mitad de emisión cierra el archivo en curso: se avisa. Encender no destruye
  // nada.
  if (!encender && panel.haySesion && panel.grabacion?.active) {
    $q.dialog({
      title: 'Parar la grabación',
      message: 'Estás emitiendo. Se cerrará el segmento en curso y no se grabará el resto.',
      cancel: { flat: true, noCaps: true, label: 'Cancelar' },
      ok: { color: 'negative', unelevated: true, noCaps: true, label: 'Parar' },
      persistent: true,
    }).onOk(() => aplicarGrabacion({ enabled: false }))
    return
  }
  aplicarGrabacion({ enabled: encender })
}

function guardarGrabacion() {
  aplicarGrabacion({
    segment_min: Number(formGrab.value.segment_min),
    max_gb: Number(formGrab.value.max_gb),
    keep_days: Number(formGrab.value.keep_days),
  })
}

const usoGrabacion = computed(() => {
  const g = grabacion.value
  if (!g) return ''
  return `${bytesLegibles(g.used_bytes)} de ${g.max_gb} GB · ${bytesLegibles(g.free_bytes)} libres en el disco`
})

// El error manda sobre el código. Un fallo de red no obtiene respuesta y se guarda con
// last_status en null, así que mirar el null primero pintaba «sin enviar aún» un aviso que
// llevaba días sin llegar a su destino.
const estadoEntrega = (w) => {
  if (w.last_error) return { texto: `último envío falló (${w.last_status ?? 'sin respuesta'}): ${w.last_error}`, color: 'negative' }
  if (w.last_status === null) return { texto: 'sin enviar aún', color: 'grey-6' }
  if (w.last_status >= 200 && w.last_status < 300) return { texto: `último envío: ${w.last_status}`, color: 'positive' }
  return { texto: `último envío falló (${w.last_status})`, color: 'negative' }
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

      <div class="text-h6 q-mt-xl q-mb-sm">Cuentas conectadas</div>
      <p class="text-body2 text-grey-5">
        Las cuentas vinculadas por código de dispositivo (Twitch, y las que se sumen) dan título,
        categoría y chat de solo lectura a sus destinos. Se conectan desde el diálogo de cada canal.
      </p>

      <q-card v-if="!cuentas.length" flat bordered class="q-pa-lg text-center">
        <q-icon :name="iCuenta" size="36px" class="text-grey-7" />
        <div class="text-body2 text-grey-5 q-mt-sm">Todavía no hay cuentas conectadas.</div>
      </q-card>

      <q-list v-else bordered separator class="rounded-borders">
        <q-item v-for="c in cuentas" :key="c.id">
          <q-item-section avatar>
            <q-icon :name="porId(c.platform).icono" size="24px" :style="{ color: porId(c.platform).color }" />
          </q-item-section>
          <q-item-section>
            <q-item-label>
              {{ c.display_name }}
              <q-badge v-if="c.status === 'reauth'" color="warning" text-color="black" label="Reconectar" class="q-ml-xs" />
            </q-item-label>
            <q-item-label caption>
              <template v-if="c.destinations.length">
                Vinculada a {{ nombresDestinos(c).join(', ') }}
              </template>
              <template v-else>Sin destinos vinculados</template>
            </q-item-label>
            <q-item-label v-if="c.status === 'reauth'" caption class="text-warning">
              La cuenta necesita reconectarse. Ábrela desde Editar → Cuenta en el destino, en el Panel.
              <q-btn flat dense no-caps size="sm" label="Ir al panel" :to="{ name: 'panel' }" class="q-ml-xs" />
            </q-item-label>
          </q-item-section>
          <q-item-section side>
            <q-btn flat dense no-caps size="sm" :icon="iBorrar" label="Desconectar" class="text-negative"
                   @click="desconectar(c)" />
          </q-item-section>
        </q-item>
      </q-list>

      <div class="text-h6 q-mt-xl q-mb-sm">Grabación</div>
      <q-card flat bordered>
        <q-card-section v-if="grabacion" class="q-gutter-y-md">
          <p class="text-body2 text-grey-5 q-mb-none">
            Guarda una copia de cada emisión en el servidor, en FLV y por segmentos, sin
            transcodificar. Si el disco no da abasto, se degrada la grabación, nunca el directo.
          </p>
          <q-toggle
            :model-value="grabacion.enabled" label="Grabar las emisiones"
            :disable="guardandoGrab" @update:model-value="alternarGrabacion"
          />
          <div class="row q-col-gutter-md">
            <q-input v-model.number="formGrab.segment_min" type="number" min="0" max="240" outlined dense
                     label="Minutos por segmento" hint="0 = un solo archivo" class="col-12 col-sm-4" />
            <q-input v-model.number="formGrab.max_gb" type="number" min="0.1" step="0.5" outlined dense
                     label="Tope en GB" hint="Al llegar, se borran las más antiguas" class="col-12 col-sm-4" />
            <q-input v-model.number="formGrab.keep_days" type="number" min="0" outlined dense
                     label="Días de retención" hint="0 = solo manda el tope en GB" class="col-12 col-sm-4" />
          </div>
          <div class="row items-center q-gutter-sm">
            <q-btn unelevated no-caps color="primary" label="Guardar" :loading="guardandoGrab" @click="guardarGrabacion" />
            <q-btn flat no-caps :icon="iGrabaciones" label="Ver grabaciones" :to="{ name: 'grabaciones' }" />
          </div>
          <div class="text-caption text-grey-5">
            <q-icon :name="iDisco" size="14px" class="q-mr-xs" />{{ usoGrabacion }}
            <br />Carpeta: <span class="mono">{{ grabacion.dir }}</span>
          </div>
        </q-card-section>
      </q-card>

      <div class="text-h6 q-mt-xl q-mb-sm">Respaldo</div>
      <q-card flat bordered>
        <q-card-section>
          <p class="text-body2 text-grey-5 q-mb-md">
            Descarga una copia de la base de datos: canales, claves cifradas y contraseña del
            panel. <b>Sin tu clave maestra el archivo no sirve</b>: guárdala aparte.
          </p>
          <p class="text-body2 text-grey-5 q-mb-md">
            Con una emisión en curso no se puede: la copia retiene la base y frenaría a tus
            canales. Descárgalo al terminar.
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
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; }
</style>
