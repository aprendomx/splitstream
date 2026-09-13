<script setup>
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useQuasar } from 'quasar'
import { iDescargar, iFiltro } from '@/iconos'
import { api, ApiError } from '@/api'
import { usePanel } from '@/stores/panel'
import { nombrePorId } from '@/plataformas'
import { bitrateLegible, bytesLegibles, duracionLegible } from '@/diagnostico'
import Chat from '@/components/Chat.vue'
import { t, formatearFecha, formatearNumero } from '@/i18n'

// Ficha de una sesión pasada: qué pasó, cuándo y con qué quedó.
//
// Todo lo que se enseña aquí viene de una sola petición (GET /api/sessions/{id}) menos el
// chat, que pagina aparte porque una emisión larga deja miles de mensajes.
const props = defineProps({ id: { type: [String, Number], required: true } })
const idSesion = computed(() => Number(props.id))

const $q = useQuasar()
const router = useRouter()
const panel = usePanel()
const ficha = ref(null)
const cargando = ref(true)

// La carga cuelga del id de la ruta y no de onMounted: al ir de /historial/5 a /historial/7
// —o con el botón de atrás— Vue Router reutiliza esta misma instancia, y con onMounted la
// ficha vieja se quedaba debajo de la URL nueva.
//
// `pedida` descarta la respuesta que llega tarde: si el id ya cambió mientras la petición
// estaba en vuelo, esos datos son de otra sesión.
let pedida = 0
async function cargar() {
  const id = idSesion.value
  pedida = id
  ficha.value = null
  cargando.value = true
  // Los nombres de los destinos salen del estado del panel. Si esta página es la primera
  // que se abre —una recarga directa sobre /historial/7— todavía no hay estado que mirar.
  if (!panel.estado) panel.cargar()
  try {
    const datos = await api.sesion(id)
    if (pedida !== id) return
    ficha.value = datos
  } catch (e) {
    if (pedida !== id) return
    if (e instanceof ApiError && e.status === 404) {
      $q.notify({ type: 'negative', message: t('sesion.no_existe') })
      router.replace({ name: 'historial' })
      return
    }
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    if (pedida === id) cargando.value = false
  }
}
watch(() => props.id, cargar, { immediate: true })

// Un destino borrado después de la emisión sigue apareciendo en sus eventos: se enseña por
// id en vez de dejar el hueco, que es peor pista que un número.
const nombreDestino = (id) => panel.destinos.find((d) => d.id === id)?.name ?? t('sesion.destino_borrado', { id })

const duracion = computed(() =>
  ficha.value?.ended_at ? duracionLegible(ficha.value.duration_s) : t('sesion.en_curso'),
)
const resolucion = computed(() =>
  ficha.value?.width && ficha.value?.height ? `${ficha.value.width}×${ficha.value.height}` : '—',
)

// Resumen de la sesión, calculado aquí y no en el backend: son los mismos eventos que ya
// viajan en la ficha, contados por destino.
//
// Cuenta episodios de reconexión, no minutos degradados: «degradado» es una métrica viva
// del motor y no se persiste, así que al terminar la sesión ya no existe. La línea de
// sesion.sin_metricas lo dice en la propia tarjeta para que nadie busque lo que no hay.
const KINDS_RECONEXION = ['destination_retry', 'destination_disconnected', 'connect_failed']
const KINDS_SUSPENSION = ['destination_suspended']
const resumen = computed(() => {
  const ev = ficha.value?.events ?? []
  const porDestino = new Map()
  for (const e of ev) {
    if (!e.destination_id) continue
    const d = porDestino.get(e.destination_id) ?? { reconexiones: 0, suspendido: false }
    if (KINDS_RECONEXION.includes(e.kind)) d.reconexiones++
    if (KINDS_SUSPENSION.includes(e.kind)) d.suspendido = true
    porDestino.set(e.destination_id, d)
  }
  return {
    destinosConReconexiones: [...porDestino.entries()].filter(([, d]) => d.reconexiones > 0),
    suspendidos: [...porDestino.entries()].filter(([, d]) => d.suspendido).length,
    chat: Object.entries(ficha.value?.chat_by_platform ?? {}).sort((a, b) => b[1] - a[1]),
    grabaciones: ficha.value?.recordings ?? [],
    bytes: (ficha.value?.recordings ?? []).reduce((s, r) => s + r.bytes, 0),
  }
})

// Línea de tiempo. La hora es relativa al inicio (+mm:ss): en una emisión de tres horas,
// «+02:14» dice más que la hora del reloj de lo que se estaba haciendo entonces.
const NIVEL_COLOR = { info: 'grey-6', warn: 'warning', error: 'negative' }
const NIVEL_CLAVE = { info: 'registro.nivel_info', warn: 'registro.nivel_aviso', error: 'registro.nivel_error' }
const filtro = ref('todos')
const opcionesFiltro = computed(() => [
  { value: 'todos', label: t('sesion.filtro_todos') },
  { value: 'avisos', label: t('sesion.filtro_avisos') },
])

function relativo(iso) {
  const inicio = new Date(ficha.value.started_at).getTime()
  const s = Math.max(0, Math.round((new Date(iso).getTime() - inicio) / 1000))
  return `+${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
}

const eventos = computed(() => {
  if (!ficha.value) return []
  return ficha.value.events
    .filter((e) => filtro.value === 'todos' || e.level !== 'info')
    .map((e) => ({
      id: e.id,
      color: NIVEL_COLOR[e.level] ?? NIVEL_COLOR.info,
      etiqueta: t(NIVEL_CLAVE[e.level] ?? NIVEL_CLAVE.info),
      destino: e.destination_id ? nombreDestino(e.destination_id) : null,
      hora: relativo(e.created_at),
      message: e.message,
    }))
})

const nombreGrabacion = (g) => g.path.split('/').at(-1)
</script>

<template>
  <q-page class="q-pa-md q-pb-xl">
    <div class="contenido">
      <div class="row items-center q-mb-sm">
        <div class="text-h6">{{ t('sesion.titulo', { id: idSesion }) }}</div>
        <q-space />
        <q-btn flat no-caps :label="t('app.historial')" :to="{ name: 'historial' }" />
      </div>

      <div v-if="cargando" class="text-center q-pa-lg">
        <q-spinner size="28px" color="primary" />
      </div>

      <template v-else-if="ficha">
        <div class="text-caption text-grey-5 q-mb-md">
          {{ formatearFecha(ficha.started_at) }} · {{ duracion }} · {{ resolucion }}
          · {{ bitrateLegible(ficha.bitrate_bps) }}
          · {{ t('sesion.chat_total', { n: ficha.chat_count, total: formatearNumero(ficha.chat_count) }) }}
        </div>

        <q-card flat bordered class="q-mb-md">
          <q-card-section class="q-py-sm">
            <div class="text-subtitle2">{{ t('sesion.resumen_titulo') }}</div>
          </q-card-section>
          <q-separator />
          <q-card-section class="q-py-sm text-body2 q-gutter-y-xs">
            <div v-if="resumen.destinosConReconexiones.length">
              <div v-for="[idDestino, d] in resumen.destinosConReconexiones" :key="idDestino">
                {{ t('sesion.reconexiones_destino', { destino: nombreDestino(idDestino), n: d.reconexiones }) }}
              </div>
            </div>
            <div v-else>{{ t('sesion.sin_reconexiones') }}</div>

            <div>
              {{ resumen.suspendidos ? t('sesion.suspendidos', { n: resumen.suspendidos }) : t('sesion.sin_suspendidos') }}
            </div>

            <div v-if="ficha.chat_count">
              {{ t('sesion.chat_total', { n: ficha.chat_count, total: formatearNumero(ficha.chat_count) }) }}
              <span v-for="[plataforma, n] in resumen.chat" :key="plataforma" class="text-grey-5">
                · {{ t('sesion.chat_plataforma', { plataforma: nombrePorId(plataforma), n: formatearNumero(n) }) }}
              </span>
            </div>
            <div v-else>{{ t('sesion.sin_chat') }}</div>

            <div v-if="resumen.grabaciones.length">
              {{ t('sesion.grabaciones_resumen', { n: resumen.grabaciones.length, bytes: bytesLegibles(resumen.bytes) }) }}
            </div>
            <div v-else>{{ t('sesion.sin_grabaciones') }}</div>

            <div class="text-caption text-grey-6">{{ t('sesion.sin_metricas') }}</div>
          </q-card-section>
        </q-card>

        <q-card flat bordered class="q-mb-md">
          <q-card-section class="row items-center q-py-sm">
            <div class="text-subtitle2">{{ t('sesion.linea_tiempo') }}</div>
            <q-space />
            <q-icon :name="iFiltro" size="18px" class="q-mr-sm text-grey-6" />
            <q-btn-toggle
              v-model="filtro"
              dense flat no-caps
              toggle-color="primary"
              :options="opcionesFiltro"
              :aria-label="t('sesion.filtro_aria')"
            />
          </q-card-section>
          <q-separator />
          <q-list dense class="linea">
            <q-item v-for="e in eventos" :key="e.id" class="fila">
              <q-item-section side class="hora">{{ e.hora }}</q-item-section>
              <q-item-section side>
                <q-badge :color="e.color" :label="e.etiqueta" class="nivel" />
              </q-item-section>
              <q-item-section>
                <q-item-label class="mensaje">
                  <span v-if="e.destino" class="text-weight-medium">{{ e.destino }} · </span>{{ e.message }}
                </q-item-label>
              </q-item-section>
            </q-item>
            <q-item v-if="!eventos.length">
              <q-item-section class="text-grey-6 text-caption">{{ t('sesion.sin_eventos') }}</q-item-section>
            </q-item>
            <!-- El servidor corta la lista en su tope: sin este aviso, una línea de
                 tiempo incompleta se leería como la sesión entera. -->
            <q-item v-if="ficha.events_truncated">
              <q-item-section class="text-grey-6 text-caption">{{ t('sesion.eventos_truncados') }}</q-item-section>
            </q-item>
          </q-list>
        </q-card>

        <div class="text-subtitle2 q-mb-sm">{{ t('sesion.chat_titulo') }}</div>
        <Chat :key="idSesion" :sesion-id="idSesion" />

        <div class="text-subtitle2 q-mb-sm">{{ t('app.grabaciones') }}</div>
        <q-card v-if="!resumen.grabaciones.length" flat bordered class="q-pa-md text-center text-body2 text-grey-5">
          {{ t('sesion.sin_grabaciones') }}
        </q-card>
        <q-list v-else bordered separator class="rounded-borders">
          <q-item v-for="g in resumen.grabaciones" :key="g.id">
            <q-item-section>
              <q-item-label>{{ nombreGrabacion(g) }}</q-item-label>
              <q-item-label caption>
                {{ t('grabaciones.segmento', { n: g.segment }) }}
                · {{ duracionLegible(g.duration_ms / 1000) }} · {{ bytesLegibles(g.bytes) }}
              </q-item-label>
            </q-item-section>
            <q-item-section side>
              <q-btn
                flat round dense size="sm" :icon="iDescargar"
                :aria-label="t('grabaciones.descargar')" :disable="g.in_progress"
                type="a" :href="api.urlDescargaGrabacion(g.id)"
              />
            </q-item-section>
          </q-item>
        </q-list>
      </template>
    </div>
  </q-page>
</template>

<style scoped>
.contenido { max-width: 760px; margin: 0 auto; }
.linea { max-height: 420px; overflow-y: auto; }
.hora {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: rgba(255, 255, 255, 0.5);
  min-width: 54px;
}
.nivel { font-size: 10px; min-width: 42px; justify-content: center; }
.mensaje { font-size: 13px; white-space: normal; }
</style>
