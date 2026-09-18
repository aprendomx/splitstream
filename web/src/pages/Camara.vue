<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { usePanel } from '@/stores/panel'
import { t } from '@/i18n'
import { iCamara, iMicrofono, iSenal } from '@/iconos'
import ChipEstado from '@/components/ChipEstado.vue'
import { diagnosticar } from '@/diagnostico'
import { detectarSoporte } from '@/camara/soporte'
import { Emisor } from '@/camara/emisor'

// La cámara del navegador como fuente (spec cámara §5). Esta página es dueña del stream
// de captura y de su ciclo de vida; codificar y enviar es cosa de Emisor (camara/emisor.js).
const panel = usePanel()

const soporte = ref(null)       // null = comprobando; {ok, motivoKey}
const permisoKey = ref(null)    // aviso mientras no hay stream
const camaras = ref([])
const microfonos = ref([])
const camaraId = ref(null)
const microfonoId = ref(null)
const calidad = ref('720p')
const videoEl = ref(null)
const emitiendo = ref(false)
const conectando = ref(false)
const motivoFin = ref(null)     // texto ya traducido del último corte, o null
// El stream no es reactivo a propósito (Vue envolvería cada pista en un proxy); hayStream
// es la señal que la plantilla mira.
let stream = null
const hayStream = ref(false)
let calidadAbierta = null
// Cada llamada a abrirCamara() se lleva un número. Si su getUserMedia resuelve después de
// que ya arrancó una llamada más nueva, lo que consiguió queda obsoleto: se descarta (parando
// sus pistas) en vez de instalarse y pisar la elección más reciente del usuario.
let generacion = 0

// Se deshabilita cuando hay sesión de OTRA fuente: OBS o una cámara en otro dispositivo.
// Cuando la sesión es la nuestra, el botón es «Parar».
const ocupado = computed(() => panel.haySesion && !emitiendo.value)
const listo = computed(() => soporte.value?.ok && hayStream.value && !ocupado.value)

const opcionesCalidad = computed(() => [
  { label: t('camara.calidad_720'), value: '720p' },
  { label: t('camara.calidad_1080'), value: '1080p' },
])
function nombre(d, i) {
  return d.label || t('camara.dispositivo_sin_nombre', { n: i + 1 })
}

async function pararStream() {
  if (stream) { for (const pista of stream.getTracks()) pista.stop() }
  stream = null
  hayStream.value = false
  if (videoEl.value) videoEl.value.srcObject = null
}

// Dice si lo que está abierto ya es lo que piden los selectores: evita reabrir la cámara
// cuando abrirCamara() rellena los ids con los del stream recién abierto.
function yaAbierto() {
  if (!stream || calidadAbierta !== calidad.value) return false
  const cam = stream.getVideoTracks()[0]?.getSettings().deviceId
  const mic = stream.getAudioTracks()[0]?.getSettings().deviceId
  return cam === camaraId.value && mic === microfonoId.value
}

// Abre la cámara y el micrófono elegidos. Sin ids (primera vez) deja que el navegador
// elija: en el móvil, la frontal, que es la que quiere quien se emite a sí mismo.
async function abrirCamara() {
  const mia = ++generacion
  await pararStream()
  const alto = calidad.value === '1080p' ? 1080 : 720
  const restricciones = {
    video: camaraId.value
      ? { deviceId: { exact: camaraId.value }, height: { ideal: alto }, frameRate: { ideal: 30 } }
      : { facingMode: 'user', height: { ideal: alto }, frameRate: { ideal: 30 } },
    audio: microfonoId.value
      ? { deviceId: { exact: microfonoId.value }, echoCancellation: true, noiseSuppression: true }
      : { echoCancellation: true, noiseSuppression: true },
  }
  let nuevo
  try {
    nuevo = await navigator.mediaDevices.getUserMedia(restricciones)
    if (mia !== generacion) { for (const pista of nuevo.getTracks()) pista.stop(); return }
    permisoKey.value = null
  } catch {
    if (mia !== generacion) return
    permisoKey.value = 'camara.permiso_denegado'
    return
  }
  stream = nuevo
  videoEl.value.srcObject = stream
  for (const pista of stream.getTracks()) pista.addEventListener('ended', alTerminarPista)
  calidadAbierta = calidad.value
  hayStream.value = true
  // Las etiquetas de enumerateDevices solo se rellenan tras conceder permiso.
  const dispositivos = await navigator.mediaDevices.enumerateDevices()
  // Otra llamada a abrirCamara() pudo haber reemplazado el stream durante este await.
  if (mia !== generacion) return
  camaras.value = dispositivos.filter((d) => d.kind === 'videoinput')
  microfonos.value = dispositivos.filter((d) => d.kind === 'audioinput')
  camaraId.value = stream.getVideoTracks()[0]?.getSettings().deviceId ?? camaraId.value
  microfonoId.value = stream.getAudioTracks()[0]?.getSettings().deviceId ?? microfonoId.value
}

onMounted(async () => {
  soporte.value = await detectarSoporte()
  if (!soporte.value.ok) return
  permisoKey.value = 'camara.permiso'
  await abrirCamara()
})
onBeforeUnmount(() => { pararStream() })

// Cambiar de dispositivo o de calidad reabre la captura; mientras se emite los controles
// están deshabilitados, así que aquí nunca hay emisión en curso.
watch([camaraId, microfonoId, calidad], () => { if (!emitiendo.value && !yaAbierto()) abrirCamara() })

let emisor = null
let wakeLock = null

function textoFin(motivo) {
  if (!motivo) return t('camara.se_corto')
  // Plantilla, no concatenación: 'camara.' + motivo haría que i18n-check.mjs (que busca
  // llamadas a t con cadena literal) confundiera "camara." con una clave real.
  return motivo.includes(' ') ? motivo : t(`camara.${motivo}`)
}

async function pedirWakeLock() {
  try { wakeLock = await navigator.wakeLock?.request('screen') } catch { wakeLock = null }
}
function soltarWakeLock() {
  wakeLock?.release().catch(() => {})
  wakeLock = null
}

async function emitir() {
  if (!listo.value) return
  motivoFin.value = null
  conectando.value = true
  const nuevo = new Emisor({
    video: videoEl.value, stream, calidad: calidad.value,
    onFin: (motivo) => {
      emitiendo.value = false
      soltarWakeLock()
      motivoFin.value = textoFin(motivo)
      emisor = null
    },
  })
  // Se asigna antes de iniciar(): si onBeforeUnmount llama a parar() mientras arranca,
  // debe poder cortar este intento en vuelo, no uno ya sustituido.
  emisor = nuevo
  try {
    await nuevo.iniciar()
    emitiendo.value = true
    await pedirWakeLock()
  } catch (e) {
    nuevo.parar()
    emisor = null
    motivoFin.value = textoFin(e.message === 'ws' ? '' : e.message)
  } finally {
    conectando.value = false
  }
}

function parar() {
  emisor?.parar()
  emisor = null
  emitiendo.value = false
  soltarWakeLock()
}

// Página oculta = cámara suspendida en el móvil (spec §1.4): se para y se dice por qué,
// en vez de dejar una emisión medio viva que la plataforma corta un minuto después.
function alCambiarVisibilidad() {
  if (document.hidden && emitiendo.value) {
    parar()
    motivoFin.value = t('camara.parada_oculta')
  }
}
// Si la cámara o el micrófono desaparecen (cable, otro app que los toma), no hay nada
// que codificar: se para con motivo.
function alTerminarPista() {
  if (emitiendo.value) {
    parar()
    motivoFin.value = t('camara.parada_dispositivo')
  }
}
function antesDeSalir(ev) {
  if (!emitiendo.value) return
  ev.preventDefault()
  ev.returnValue = t('camara.aviso_salir')
}

onMounted(() => {
  document.addEventListener('visibilitychange', alCambiarVisibilidad)
  window.addEventListener('beforeunload', antesDeSalir)
})
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', alCambiarVisibilidad)
  window.removeEventListener('beforeunload', antesDeSalir)
  parar()
})
</script>

<template>
  <q-page class="q-pa-md q-pb-xl">
    <div class="pagina">
      <div class="row items-center q-mb-md">
        <div class="ss-t-22">{{ t('camara.titulo') }}</div>
        <q-space />
        <ChipEstado v-if="emitiendo" tono="emitiendo" :icono="iSenal" pulso tam="lg" anuncia :texto="t('camara.en_vivo')" />
      </div>
      <p class="ss-t-14 ss-muted">{{ t('camara.intro') }}</p>

      <q-banner v-if="soporte === null" class="q-mb-md" aria-busy="true">{{ t('camara.comprobando') }}</q-banner>
      <q-banner v-else-if="!soporte.ok" class="q-mb-md bg-warning text-dark" role="alert">{{ t(soporte.motivoKey) }}</q-banner>

      <template v-else>
        <q-card flat bordered class="q-mb-md">
          <div class="cuadro">
            <!-- muted y playsinline: sin ellos iOS no reproduce la vista local ni la deja
                 en la página; el sonido lo oye la plataforma, no quien emite. -->
            <video ref="videoEl" class="video" autoplay muted playsinline />
            <div v-if="permisoKey" class="aviso ss-t-14 ss-muted">{{ t(permisoKey) }}</div>
          </div>
        </q-card>

        <q-card flat bordered class="q-pa-md q-mb-md">
          <div class="row q-col-gutter-md">
            <div class="col-12 col-sm-6">
              <q-select v-model="camaraId" :options="camaras.map((d, i) => ({ label: nombre(d, i), value: d.deviceId }))"
                        emit-value map-options outlined dense :label="t('camara.camara')" :disable="emitiendo">
                <template #prepend><q-icon :name="iCamara" /></template>
              </q-select>
            </div>
            <div class="col-12 col-sm-6">
              <q-select v-model="microfonoId" :options="microfonos.map((d, i) => ({ label: nombre(d, i), value: d.deviceId }))"
                        emit-value map-options outlined dense :label="t('camara.microfono')" :disable="emitiendo">
                <template #prepend><q-icon :name="iMicrofono" /></template>
              </q-select>
            </div>
            <div class="col-12">
              <q-btn-toggle v-model="calidad" :options="opcionesCalidad" no-caps outline toggle-color="primary"
                            :disable="emitiendo" :aria-label="t('camara.calidad')" />
            </div>
          </div>
          <p class="ss-t-14 ss-muted q-mt-md q-mb-none">{{ t('camara.consejo_orientacion') }}</p>
        </q-card>

        <q-banner v-if="ocupado" class="q-mb-md" role="status">{{ t('camara.ocupado') }}</q-banner>
        <q-banner v-if="motivoFin" class="q-mb-md bg-warning text-dark" role="alert">{{ motivoFin }}</q-banner>

        <div class="row items-center q-gutter-sm q-mb-lg">
          <q-btn v-if="!emitiendo" unelevated no-caps color="primary" size="lg" :label="t('camara.emitir')"
                 :disable="!listo" :loading="conectando" @click="emitir" />
          <q-btn v-else unelevated no-caps color="negative" size="lg" :label="t('camara.parar')" @click="parar" />
        </div>

        <div class="ss-t-16 q-mb-sm">{{ t('camara.destinos') }}</div>
        <p v-if="!panel.destinos.some((d) => d.enabled)" class="ss-t-14 ss-muted">{{ t('camara.sin_destinos') }}</p>
        <div v-else class="row q-gutter-sm">
          <ChipEstado v-for="d in panel.destinos.filter((x) => x.enabled)" :key="d.id"
                      :tono="diagnosticar(d, panel.haySesion).tono" :texto="d.name" />
        </div>

        <p class="ss-t-14 ss-muted q-mt-lg">{{ t('camara.limites') }}</p>
      </template>
    </div>
  </q-page>
</template>

<style scoped>
.pagina { max-width: 960px; margin: 0 auto; padding: var(--ss-space-5) var(--ss-space-4); }
.cuadro {
  position: relative;
  aspect-ratio: 16 / 9;
  /* Negro: el fondo de «sin señal» de cualquier pantalla de vídeo, como en VistaPrevia. */
  background: #000;
}
.video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; display: block; }
.aviso {
  position: absolute; inset: 0;
  display: flex; align-items: center; justify-content: center; text-align: center;
  padding: var(--ss-space-4);
}
</style>
