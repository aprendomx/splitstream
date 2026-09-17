<script setup>
import { ref, computed, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iMas, iEditar, iBorrar, iWebhook, iDescargar, iProbar, iGrabaciones, iDisco, iCuenta, iAnterior, iSiguiente } from '@/iconos'
import { api } from '@/api'
import { bytesLegibles } from '@/diagnostico'
import { usePanel } from '@/stores/panel'
import { porId } from '@/plataformas'
import { t, formatearNumero } from '@/i18n'
import DialogoWebhook from '@/components/DialogoWebhook.vue'
import ChipEstado from '@/components/ChipEstado.vue'

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
onMounted(() => { cargar(); cargarGrabacion(); panel.cargarCuentas() })

// Cuentas conectadas (Twitch, YouTube por código; Kick por redirect). Nombre y destino
// salen del propio destino en el panel; aquí solo importa la cuenta y a qué canales sigue
// vinculada. Viven en el store (panel.cuentas): el diálogo de destino las carga también y
// así no hay dos copias desincronizadas.
const cuentas = computed(() => panel.cuentas)

/** El nombre de cada destino vinculado, para la confirmación y la lista. */
function nombresDestinos(c) {
  return c.destinations.map((id) => panel.destinos.find((d) => d.id === id)?.name ?? `#${id}`)
}

function desconectar(c) {
  const nombres = nombresDestinos(c)
  const cuantos = nombres.length
  const lista = cuantos ? ` (${nombres.join(', ')})` : ''
  // Sin canales vinculados no se pregunta por «0 canales» —la forma plural de una cuenta
  // que no arrastra nada—, se dice lo que de verdad va a pasar: solo se va la cuenta.
  const mensaje = cuantos
    ? t('ajustes.desconectar_mensaje', { n: cuantos, lista })
    : t('ajustes.desconectar_sin_canales')
  $q.dialog({
    title: t('ajustes.desconectar_titulo'),
    message: mensaje,
    cancel: { flat: true, noCaps: true, label: t('comun.cancelar') },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: t('ajustes.desconectar') },
    persistent: true,
  }).onOk(async () => {
    try {
      await api.borrarCuenta(c.id)
      await panel.cargarCuentas()
      await panel.cargar()
      $q.notify({ type: 'positive', message: t('ajustes.cuenta_desconectada') })
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
    title: t('ajustes.eliminar_aviso_titulo'), message: t('ajustes.eliminar_aviso_mensaje', { nombre: w.name }),
    cancel: { flat: true, noCaps: true, label: t('comun.cancelar') },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: t('comun.eliminar') }, persistent: true,
  }).onOk(async () => {
    try { await api.borrarWebhook(w.id); await cargar() }
    catch (e) { $q.notify({ type: 'negative', message: e.message }) }
  })
}

async function probar(w) {
  probando.value = w.id
  try {
    await api.probarWebhook(w.id)
    $q.notify({ type: 'positive', message: t('ajustes.aviso_entregado', { nombre: w.name }) })
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
    $q.notify({ type: 'positive', message: t('ajustes.ajustes_grabacion_guardados') })
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
      title: t('ajustes.parar_grabacion_titulo'),
      message: t('ajustes.parar_grabacion_mensaje'),
      cancel: { flat: true, noCaps: true, label: t('comun.cancelar') },
      ok: { color: 'negative', unelevated: true, noCaps: true, label: t('ajustes.parar') },
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
  return t('ajustes.uso_grabacion', { usados: bytesLegibles(g.used_bytes), max: g.max_gb, libres: bytesLegibles(g.free_bytes) })
})

// El error manda sobre el código. Un fallo de red no obtiene respuesta y se guarda con
// last_status en null, así que mirar el null primero pintaba «sin enviar aún» un aviso que
// llevaba días sin llegar a su destino.
//
// El chip solo enseña el estado corto (ok / falló / sin enviar aún): el status HTTP y el
// last_error —texto arbitrario que viene de la API— van en «detalle», que la plantilla
// pinta aparte, bajo la etiqueta, para no desbordar el chip a 375 px.
const estadoEntrega = (w) => {
  if (w.last_error) {
    return {
      texto: t('ajustes.entrega_fallo'),
      color: 'negative',
      detalle: t('ajustes.envio_fallo_status', { status: w.last_status ?? t('ajustes.sin_respuesta'), error: w.last_error }),
    }
  }
  if (w.last_status === null) return { texto: t('ajustes.sin_enviar_aun'), color: 'grey-5' }
  if (w.last_status >= 200 && w.last_status < 300) {
    return { texto: t('ajustes.entrega_ok'), color: 'positive', detalle: t('ajustes.ultimo_envio_ok', { status: w.last_status }) }
  }
  return { texto: t('ajustes.entrega_fallo'), color: 'negative', detalle: t('ajustes.envio_fallo', { status: w.last_status }) }
}
// El tono del chip de estado de entrega sale del color que ya calculaba estadoEntrega: solo
// se traduce a la paleta de ChipEstado (spec §3.4, estado nunca solo por color).
const TONO_ENTREGA = { negative: 'fallo', positive: 'emitiendo', 'grey-5': 'neutro' }
const tonoEntrega = (w) => TONO_ENTREGA[estadoEntrega(w).color] ?? 'neutro'

// Índice de secciones: pestañas que no navegan, solo desplazan hasta la sección
// correspondiente. Sin IntersectionObserver —la pestaña activa se marca al elegirla, que es
// la alternativa que deja abierta el propio spec cuando el observer complica más de lo que
// resuelve.
const reducido = window.matchMedia('(prefers-reduced-motion: reduce)').matches
const seccionActiva = ref('avisos')
const secciones = computed(() => [
  { id: 'avisos', label: t('ajustes.avisos_titulo') },
  { id: 'cuentas', label: t('ajustes.cuentas_titulo') },
  { id: 'grabacion', label: t('ajustes.grabacion_titulo') },
  { id: 'respaldo', label: t('ajustes.respaldo_titulo') },
])
function irASeccion(id) {
  document.getElementById(id)?.scrollIntoView({ behavior: reducido ? 'auto' : 'smooth', block: 'start' })
}
</script>

<template>
  <q-page class="q-pa-md q-pb-xl ajustes">
    <div class="pagina">
      <div class="ss-t-22">{{ t('app.ajustes') }}</div>

      <q-tabs
        v-model="seccionActiva"
        dense no-caps
        active-color="primary" indicator-color="primary"
        class="indice-secciones"
        :aria-label="t('ajustes.indice')"
        :left-icon="iAnterior" :right-icon="iSiguiente"
        @update:model-value="irASeccion"
      >
        <q-tab v-for="s in secciones" :key="s.id" :name="s.id" :label="s.label" />
      </q-tabs>

      <section id="avisos">
        <div class="row items-center titulo-seccion">
          <div class="ss-t-18">{{ t('ajustes.avisos_titulo') }}</div>
          <q-space />
          <q-btn unelevated no-caps color="primary" :icon="iMas" :label="t('dialogo_webhook.nuevo_aviso')" @click="abrirAlta" />
        </div>
        <p class="ss-t-14 ss-muted">
          {{ t('ajustes.avisos_explicacion') }}
        </p>

        <!-- Sin repetir el título de la sección: el icono ya da el contexto, y el
             mensaje «sin X» hace de encabezado del vacío. -->
        <q-card v-if="!webhooks.length" flat bordered class="vacio">
          <q-icon :name="iWebhook" size="32px" class="ss-muted" aria-hidden="true" />
          <div class="ss-t-16">{{ t('ajustes.sin_avisos') }}</div>
          <q-btn unelevated no-caps color="primary" :icon="iMas" :label="t('dialogo_webhook.nuevo_aviso')" @click="abrirAlta" />
        </q-card>

        <q-list v-else bordered separator class="lista">
          <q-item v-for="w in webhooks" :key="w.id">
            <q-item-section>
              <q-item-label>{{ w.name }} <q-badge outline color="grey-5" :label="w.format" class="q-ml-xs" /></q-item-label>
              <q-item-label caption class="ellipsis ss-t-14 ss-muted">{{ w.url }}</q-item-label>
              <ChipEstado tam="sm" :tono="tonoEntrega(w)" :texto="estadoEntrega(w).texto" class="q-mt-xs" />
              <q-item-label v-if="estadoEntrega(w).detalle" caption class="ss-t-14 ss-muted detalle-entrega">
                {{ estadoEntrega(w).detalle }}
              </q-item-label>
            </q-item-section>
            <q-item-section side>
              <div class="row items-center no-wrap q-gutter-sm acciones-fila">
                <q-btn flat dense no-caps size="sm" :icon="iProbar" :label="t('destino.probar')" :loading="probando === w.id" @click="probar(w)" />
                <q-toggle :model-value="w.enabled" dense @update:model-value="alternar(w)" :aria-label="`${w.enabled ? t('ajustes.desactivar') : t('ajustes.activar')} ${w.name}`" />
                <q-btn flat round dense :icon="iEditar" size="sm" :aria-label="t('comun.editar')" @click="abrirEdicion(w)" />
                <q-btn flat round dense :icon="iBorrar" size="sm" class="text-negative" :aria-label="t('comun.eliminar')" @click="borrar(w)" />
              </div>
            </q-item-section>
          </q-item>
        </q-list>
      </section>

      <section id="cuentas">
        <div class="row items-center titulo-seccion">
          <div class="ss-t-18">{{ t('ajustes.cuentas_titulo') }}</div>
        </div>
        <p class="ss-t-14 ss-muted">
          {{ t('ajustes.cuentas_explicacion') }}
        </p>
        <p class="ss-t-14 ss-muted">{{ t('ajustes.idioma_nota') }}</p>

        <q-card v-if="!cuentas.length" flat bordered class="vacio">
          <q-icon :name="iCuenta" size="32px" class="ss-muted" aria-hidden="true" />
          <div class="ss-t-16">{{ t('ajustes.sin_cuentas') }}</div>
          <q-btn outline no-caps :label="t('panel.vincular_canal')" :to="{ name: 'panel' }" />
        </q-card>

        <q-list v-else bordered separator class="lista">
          <q-item v-for="c in cuentas" :key="c.id">
            <q-item-section avatar>
              <q-icon :name="porId(c.platform).icono" size="24px" :style="{ color: porId(c.platform).color }" />
            </q-item-section>
            <q-item-section>
              <q-item-label>
                {{ c.display_name }}
                <ChipEstado v-if="c.status === 'reauth'" tam="sm" tono="atencion" :texto="t('ajustes.reconectar_badge')" class="q-ml-xs" />
                <q-badge v-if="c.own_app" outline color="grey-5" :label="t('ajustes.app_propia_badge')" class="q-ml-xs" />
              </q-item-label>
              <q-item-label caption class="ss-t-14 ss-muted">
                <template v-if="c.destinations.length">
                  {{ t('ajustes.vinculada_a', { nombres: nombresDestinos(c).join(', ') }) }}
                </template>
                <template v-else>{{ t('ajustes.sin_destinos_vinculados') }}</template>
                <template v-if="c.quota_used_today != null"> · {{ t('ajustes.unidades_hoy', { n: formatearNumero(c.quota_used_today) }) }}</template>
              </q-item-label>
              <q-item-label v-if="c.status === 'reauth'" caption class="ss-t-14 text-warning">
                {{ t('ajustes.necesita_reconectar') }}
                <q-btn flat dense no-caps size="sm" :label="t('ajustes.ir_al_panel')" :to="{ name: 'panel' }" class="q-ml-xs" />
              </q-item-label>
            </q-item-section>
            <q-item-section side>
              <q-btn flat dense no-caps size="sm" :icon="iBorrar" :label="t('ajustes.desconectar')" class="text-negative"
                     @click="desconectar(c)" />
            </q-item-section>
          </q-item>
        </q-list>
      </section>

      <section id="grabacion">
        <div class="row items-center titulo-seccion">
          <div class="ss-t-18">{{ t('ajustes.grabacion_titulo') }}</div>
        </div>
        <q-card flat bordered>
          <q-card-section v-if="grabacion" class="q-gutter-y-md">
            <p class="ss-t-14 ss-muted q-mb-none">
              {{ t('ajustes.grabacion_explicacion') }}
            </p>
            <q-toggle
              :model-value="grabacion.enabled" :label="t('ajustes.grabar_emisiones')"
              :disable="guardandoGrab" @update:model-value="alternarGrabacion"
            />
            <div class="row q-col-gutter-md">
              <q-input v-model.number="formGrab.segment_min" type="number" min="0" max="240" outlined
                       :label="t('ajustes.minutos_por_segmento')" :hint="t('ajustes.minutos_hint')" class="col-12 col-sm-4" />
              <q-input v-model.number="formGrab.max_gb" type="number" min="0.1" step="0.5" outlined
                       :label="t('ajustes.tope_gb')" :hint="t('ajustes.tope_gb_hint')" class="col-12 col-sm-4" />
              <q-input v-model.number="formGrab.keep_days" type="number" min="0" outlined
                       :label="t('ajustes.dias_retencion')" :hint="t('ajustes.dias_retencion_hint')" class="col-12 col-sm-4" />
            </div>
            <div class="row items-center q-gutter-sm">
              <q-btn unelevated no-caps color="primary" :label="t('comun.guardar')" :loading="guardandoGrab" @click="guardarGrabacion" />
              <q-btn flat no-caps :icon="iGrabaciones" :label="t('ajustes.ver_grabaciones')" :to="{ name: 'grabaciones' }" />
            </div>
            <div class="ss-t-14 ss-muted">
              <q-icon :name="iDisco" size="14px" class="q-mr-xs" />{{ usoGrabacion }}
              <br />{{ t('ajustes.carpeta') }} <span class="ss-mono ss-t-14">{{ grabacion.dir }}</span>
            </div>
          </q-card-section>
        </q-card>
      </section>

      <section id="respaldo">
        <div class="row items-center titulo-seccion">
          <div class="ss-t-18">{{ t('ajustes.respaldo_titulo') }}</div>
        </div>
        <q-card flat bordered>
          <q-card-section>
            <p class="ss-t-14 ss-muted q-mb-md">
              {{ t('ajustes.respaldo_p1_pre') }} <b>{{ t('ajustes.respaldo_p1_bold') }}</b>{{ t('ajustes.respaldo_p1_post') }}
            </p>
            <p class="ss-t-14 ss-muted q-mb-md">
              {{ t('ajustes.respaldo_p2') }}
            </p>
            <q-btn unelevated no-caps color="primary" :icon="iDescargar" :label="t('ajustes.descargar_respaldo')"
                   :loading="respaldando" @click="respaldar" />
          </q-card-section>
        </q-card>
      </section>
    </div>
    <DialogoWebhook v-model="dialogo" :webhook="editando" @guardado="cargar" />
  </q-page>
</template>

<style scoped>
.pagina { max-width: 960px; margin: 0 auto; padding: var(--ss-space-5) var(--ss-space-4); }
.indice-secciones { margin: var(--ss-space-4) 0; border-bottom: 1px solid var(--ss-border); }
.indice-secciones :deep(.q-tab) { min-height: 44px; }
.titulo-seccion { margin-top: var(--ss-space-6); margin-bottom: var(--ss-space-2); }
.lista :deep(.q-item) { min-height: 56px; }
/* El mensaje de error de un webhook es texto arbitrario de la API: puede llegar sin
   espacios donde partir línea, y a 375 px eso empujaría scroll horizontal. */
.detalle-entrega { overflow-wrap: anywhere; }
.vacio {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--ss-space-2);
  padding: var(--ss-space-6) var(--ss-space-4);
  text-align: center;
}
</style>
