<script setup>
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useQuasar } from 'quasar'
import { iDescargar, iRegistro, iGrabaciones } from '@/iconos'
import { api, ApiError } from '@/api'
import { usePanel } from '@/stores/panel'
import { nombrePorId } from '@/plataformas'
import { bitrateLegible, bytesLegibles, duracionLegible } from '@/diagnostico'
import Chat from '@/components/Chat.vue'
import { t, formatearFecha, formatearNumero } from '@/i18n'
import ChipEstado from '@/components/ChipEstado.vue'

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

// Chip de nivel de la cabecera: el mismo criterio de «peor nivel» que usa Historial, pero
// contado sobre los eventos completos de la ficha en vez de los contadores resumidos.
const NIVEL_TONO = { info: 'neutro', warn: 'atencion', error: 'fallo' }
const NIVEL_CLAVE = { info: 'registro.nivel_info', warn: 'registro.nivel_aviso', error: 'registro.nivel_error' }
const nivelSesion = computed(() => {
  if (!ficha.value) return null
  const ev = ficha.value.events ?? []
  const errores = ev.filter((e) => e.level === 'error').length
  const avisos = ev.filter((e) => e.level === 'warn').length
  if (errores) return { tono: 'fallo', texto: `${formatearNumero(errores)} ${t('registro.nivel_error')}` }
  if (avisos) return { tono: 'atencion', texto: `${formatearNumero(avisos)} ${t('registro.nivel_aviso')}` }
  return { tono: 'emitiendo', texto: `${formatearNumero(ev.length)} ${t('registro.nivel_info')}` }
})

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
  const destinos = new Set()
  for (const e of ev) {
    if (!e.destination_id) continue
    destinos.add(e.destination_id)
    const d = porDestino.get(e.destination_id) ?? { reconexiones: 0, suspendido: false }
    if (KINDS_RECONEXION.includes(e.kind)) d.reconexiones++
    if (KINDS_SUSPENSION.includes(e.kind)) d.suspendido = true
    porDestino.set(e.destination_id, d)
  }
  const grabaciones = ficha.value?.recordings ?? []
  return {
    destinos: destinos.size,
    reconexiones: [...porDestino.values()].reduce((s, d) => s + d.reconexiones, 0),
    // Desglose por destino, solo los que de verdad reconectaron: se enseña como lista
    // compacta bajo el KPI de reconexiones.
    destinosConReconexiones: [...porDestino.entries()].filter(([, d]) => d.reconexiones > 0),
    suspendidos: [...porDestino.values()].filter((d) => d.suspendido).length,
    // Desglose del chat por plataforma, para la lista bajo el KPI de mensajes de chat.
    chat: Object.entries(ficha.value?.chat_by_platform ?? {}).sort((a, b) => b[1] - a[1]),
    grabaciones,
    bytes: grabaciones.reduce((s, r) => s + r.bytes, 0),
  }
})

// Línea de tiempo. La hora es relativa al inicio (+mm:ss): en una emisión de tres horas,
// «+02:14» dice más que la hora del reloj de lo que se estaba haciendo entonces.
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
      tono: NIVEL_TONO[e.level] ?? NIVEL_TONO.info,
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
    <div class="pagina">
      <div class="row items-center q-gutter-sm cabecera-pagina">
        <div class="ss-t-22">{{ t('sesion.titulo', { id: idSesion }) }}</div>
        <ChipEstado v-if="nivelSesion" tam="sm" :tono="nivelSesion.tono" :texto="nivelSesion.texto" />
        <q-space />
        <q-btn flat no-caps :label="t('app.historial')" :to="{ name: 'historial' }" />
      </div>

      <div v-if="cargando" aria-busy="true">
        <span class="sr-only">{{ t('app.cargando') }}</span>
        <q-skeleton type="rect" height="96px" class="q-mb-md" />
        <q-skeleton type="rect" height="180px" />
      </div>

      <template v-else-if="ficha">
        <div class="ss-t-14 ss-muted">
          {{ formatearFecha(ficha.started_at) }} · {{ resolucion }} · {{ bitrateLegible(ficha.bitrate_bps) }}
        </div>

        <div class="ss-t-18 titulo-seccion">{{ t('sesion.resumen_titulo') }}</div>
        <div class="kpis">
          <div class="kpi">
            <div class="ss-t-12 ss-subtle">{{ t('sesion.duracion') }}</div>
            <div class="ss-t-22 ss-tabular">{{ duracion }}</div>
          </div>
          <div class="kpi">
            <div class="ss-t-12 ss-subtle">{{ t('sesion.destinos') }}</div>
            <div class="ss-t-22 ss-tabular">{{ formatearNumero(resumen.destinos) }}</div>
          </div>
          <div class="kpi">
            <div class="ss-t-12 ss-subtle">{{ t('sesion.reconexiones_etiqueta') }}</div>
            <div class="ss-t-22 ss-tabular">{{ formatearNumero(resumen.reconexiones) }}</div>
            <div v-if="!resumen.reconexiones" class="ss-t-12 ss-subtle">{{ t('sesion.sin_reconexiones') }}</div>
            <!-- Una línea por destino que tuvo reconexiones; sin lista cuando el total es 0. -->
            <div v-for="[idDestino, d] in resumen.destinosConReconexiones" :key="idDestino" class="ss-t-12 ss-subtle">
              {{ t('sesion.reconexiones_destino', { destino: nombreDestino(idDestino), n: d.reconexiones }) }}
            </div>
          </div>
          <div class="kpi">
            <div class="ss-t-12 ss-subtle">{{ t('sesion.suspendidos_etiqueta') }}</div>
            <div class="ss-t-22 ss-tabular">{{ formatearNumero(resumen.suspendidos) }}</div>
            <div v-if="!resumen.suspendidos" class="ss-t-12 ss-subtle">{{ t('sesion.sin_suspendidos') }}</div>
          </div>
          <div class="kpi">
            <div class="ss-t-12 ss-subtle">{{ t('sesion.chat_etiqueta') }}</div>
            <div class="ss-t-22 ss-tabular">{{ formatearNumero(ficha.chat_count) }}</div>
            <div v-if="!ficha.chat_count" class="ss-t-12 ss-subtle">{{ t('sesion.sin_chat') }}</div>
            <!-- Una línea por plataforma con mensajes; sin lista cuando el total es 0. -->
            <div v-for="[plataforma, n] in resumen.chat" :key="plataforma" class="ss-t-12 ss-subtle">
              {{ t('sesion.chat_plataforma', { plataforma: nombrePorId(plataforma), n: formatearNumero(n) }) }}
            </div>
          </div>
          <div class="kpi">
            <div class="ss-t-12 ss-subtle">{{ t('app.grabaciones') }}</div>
            <div class="ss-t-22 ss-tabular">{{ formatearNumero(resumen.grabaciones.length) }}</div>
            <div v-if="!resumen.grabaciones.length" class="ss-t-12 ss-subtle">{{ t('sesion.sin_grabaciones') }}</div>
            <div v-else class="ss-t-12 ss-subtle">
              {{ t('sesion.grabaciones_resumen', { n: resumen.grabaciones.length, bytes: bytesLegibles(resumen.bytes) }) }}
            </div>
          </div>
        </div>
        <p class="ss-t-14 ss-muted nota-metricas">{{ t('sesion.sin_metricas') }}</p>

        <q-card flat bordered class="q-mt-md">
          <div class="cabecera-tarjeta row items-center no-wrap">
            <q-icon :name="iRegistro" size="20px" class="q-mr-sm" aria-hidden="true" />
            <span class="ss-t-16 titulo-texto">{{ t('sesion.linea_tiempo') }}</span>
            <q-space />
            <q-btn-toggle
              v-model="filtro"
              no-caps unelevated
              toggle-color="primary"
              :options="opcionesFiltro"
              :aria-label="t('sesion.filtro_aria')"
              class="filtro-toggle"
            />
          </div>
          <div class="linea">
            <div v-for="e in eventos" :key="e.id" class="fila">
              <span class="hora ss-mono ss-t-12 ss-subtle ss-tabular">{{ e.hora }}</span>
              <ChipEstado class="nivel" tam="sm" :tono="e.tono" :texto="e.etiqueta" />
              <span class="mensaje ss-t-14">
                <span v-if="e.destino" class="text-weight-medium">{{ e.destino }} · </span>{{ e.message }}
              </span>
            </div>
            <div v-if="!eventos.length" class="vacio-linea ss-t-14 ss-muted">{{ t('sesion.sin_eventos') }}</div>
            <div v-if="ficha.events_truncated" class="vacio-linea ss-t-14 ss-muted">{{ t('sesion.eventos_truncados') }}</div>
          </div>
        </q-card>

        <div class="ss-t-18 titulo-seccion">{{ t('sesion.chat_titulo') }}</div>
        <Chat :key="idSesion" :sesion-id="idSesion" />

        <div class="ss-t-18 titulo-seccion">{{ t('app.grabaciones') }}</div>
        <q-card v-if="!resumen.grabaciones.length" flat bordered class="vacio">
          <q-icon :name="iGrabaciones" size="32px" class="ss-muted" aria-hidden="true" />
          <div class="ss-t-16">{{ t('sesion.sin_grabaciones') }}</div>
        </q-card>
        <q-list v-else bordered separator class="lista">
          <q-item v-for="g in resumen.grabaciones" :key="g.id">
            <q-item-section>
              <q-item-label class="ss-t-16">{{ nombreGrabacion(g) }}</q-item-label>
              <q-item-label caption class="ss-t-14 ss-muted ss-tabular">
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
.pagina { max-width: 960px; margin: 0 auto; padding: var(--ss-space-5) var(--ss-space-4); }
.cabecera-pagina { margin-bottom: var(--ss-space-3); }
.titulo-seccion { margin-top: var(--ss-space-6); margin-bottom: var(--ss-space-2); }
.kpis {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--ss-space-3);
}
.kpi {
  background: var(--ss-surface-2);
  border-radius: var(--ss-radius-sm);
  padding: var(--ss-space-3);
  display: flex;
  flex-direction: column;
  gap: var(--ss-space-1);
}
.nota-metricas { margin-top: var(--ss-space-2); }

/* Patrón de cabecera de tarjeta (spec v1.1 §3.3), copiado tal cual de RegistroEventos.vue. */
.cabecera-tarjeta {
  padding: var(--ss-space-3) var(--ss-space-4);
  border-bottom: 1px solid var(--ss-border);
}
.titulo-texto { font-weight: 600; }
/* El grupo de botones del filtro es un nodo interno de Quasar: hace falta :deep() para
   llegar a su alto y garantizar el objetivo táctil de 44px. */
.filtro-toggle :deep(.q-btn) { min-height: 44px; min-width: 44px; }

.linea { max-height: 420px; overflow-y: auto; }
.fila {
  display: grid;
  grid-template-columns: 72px 84px 1fr;
  grid-template-areas: "hora nivel mensaje";
  align-items: center;
  column-gap: var(--ss-space-2);
  padding: var(--ss-space-2) var(--ss-space-4);
  border-bottom: 1px solid var(--ss-border);
}
.fila .hora { grid-area: hora; }
.fila .nivel { grid-area: nivel; justify-self: start; }
.fila .mensaje { grid-area: mensaje; white-space: normal; }
@media (max-width: 599px) {
  .fila {
    grid-template-columns: 72px 1fr;
    grid-template-areas: "hora mensaje" "nivel mensaje";
    row-gap: var(--ss-space-1);
  }
}
.vacio-linea { padding: var(--ss-space-4); }

.lista :deep(.q-item) { min-height: 64px; }
.vacio {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--ss-space-2);
  padding: var(--ss-space-6) var(--ss-space-4);
  text-align: center;
}
</style>
