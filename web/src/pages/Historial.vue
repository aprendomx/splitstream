<script setup>
import { ref, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iGrabaciones, iHistorial } from '@/iconos'
import { api } from '@/api'
import { bitrateLegible, duracionLegible } from '@/diagnostico'
import { t, formatearFecha, formatearNumero } from '@/i18n'
import ChipEstado from '@/components/ChipEstado.vue'

// Historial de sesiones, de la más reciente a la más antigua. Cada fila lleva a su ficha.
//
// La lista no se refresca sola: una sesión terminada no cambia, y la que está en curso ya
// se ve entera en el panel. Recargar la página basta.
const $q = useQuasar()
const lista = ref([])
const cargando = ref(false)
const hayMas = ref(false)
const PAGINA = 50

async function cargar(before = 0) {
  cargando.value = true
  try {
    const nuevas = await api.sesiones(PAGINA, before)
    lista.value = before ? [...lista.value, ...nuevas] : nuevas
    hayMas.value = nuevas.length === PAGINA
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    cargando.value = false
  }
}
onMounted(cargar)

function cargarMas() {
  const ultima = lista.value.at(-1)
  if (ultima) cargar(ultima.id)
}

// Sin ended_at la sesión sigue viva: no hay duración que calcular todavía, y quien la
// llama la salta (lo dice el chip «en curso» de la fila).
const duracion = (s) => duracionLegible((new Date(s.ended_at) - new Date(s.started_at)) / 1000)

// width y height llegan null si la sesión murió antes del primer sequence header.
const resolucion = (s) => (s.width && s.height ? `${s.width}×${s.height}` : '—')

// Un único chip por fila con el peor nivel que dejó la sesión: fallo si hubo errores,
// atención si solo hubo avisos, y el tono «emitiendo» (limpio) si no hubo ninguno de los
// dos. El texto reutiliza las mismas claves de registro.nivel_* que ya existían.
const NIVEL_TONO = { info: 'emitiendo', warn: 'atencion', error: 'fallo' }
const NIVEL_CLAVE = { info: 'registro.nivel_info', warn: 'registro.nivel_aviso', error: 'registro.nivel_error' }
function peorNivel(s) {
  if ((s.events?.error ?? 0) > 0) return 'error'
  if ((s.events?.warn ?? 0) > 0) return 'warn'
  return 'info'
}
function chipNivel(s) {
  const nivel = peorNivel(s)
  const n = s.events?.[nivel] ?? 0
  return { tono: NIVEL_TONO[nivel], texto: `${formatearNumero(n)} ${t(NIVEL_CLAVE[nivel])}` }
}
</script>

<template>
  <q-page class="q-pa-md q-pb-xl">
    <div class="pagina">
      <div class="row items-center q-mb-md">
        <div class="ss-t-22">{{ t('app.historial') }}</div>
        <q-space />
        <q-btn flat no-caps :label="t('app.grabaciones')" :to="{ name: 'grabaciones' }" />
      </div>

      <div v-if="cargando && !lista.length" aria-busy="true">
        <span class="sr-only">{{ t('app.cargando') }}</span>
        <q-skeleton type="rect" height="64px" class="q-mb-sm" />
        <q-skeleton type="rect" height="64px" class="q-mb-sm" />
        <q-skeleton type="rect" height="64px" />
      </div>

      <q-card v-else-if="!lista.length" flat bordered class="vacio">
        <q-icon :name="iHistorial" size="32px" class="ss-muted" aria-hidden="true" />
        <div class="ss-t-16">{{ t('historial.sin_sesiones') }}</div>
        <div class="ss-t-14 ss-muted">{{ t('historial.sin_sesiones_detalle') }}</div>
      </q-card>

      <q-list v-else bordered separator class="lista">
        <q-item v-for="s in lista" :key="s.id" clickable :to="{ name: 'sesion', params: { id: s.id } }">
          <q-item-section>
            <q-item-label class="ss-t-16">
              {{ formatearFecha(s.started_at) }}
              <ChipEstado v-if="!s.ended_at" tam="sm" tono="trabajando" :texto="t('historial.en_curso')" class="q-ml-xs" />
            </q-item-label>
            <q-item-label caption class="ss-t-14 ss-muted ss-tabular">
              <template v-if="s.ended_at">{{ duracion(s) }} · </template>{{ resolucion(s) }}
              · {{ bitrateLegible(s.bitrate_bps) }}
            </q-item-label>
          </q-item-section>
          <q-item-section side>
            <div class="row items-center no-wrap q-gutter-sm">
              <span v-if="s.has_recording" role="img" :aria-label="t('historial.con_grabacion')" :title="t('historial.con_grabacion')">
                <q-icon :name="iGrabaciones" size="18px" class="ss-muted" />
              </span>
              <ChipEstado tam="sm" :tono="chipNivel(s).tono" :texto="chipNivel(s).texto" />
            </div>
          </q-item-section>
        </q-item>
      </q-list>

      <div v-if="hayMas" class="text-center q-mt-md">
        <q-btn outline no-caps :label="t('historial.cargar_mas')" :loading="cargando" @click="cargarMas" />
      </div>
    </div>
  </q-page>
</template>

<style scoped>
.pagina { max-width: 960px; margin: 0 auto; padding: var(--ss-space-5) var(--ss-space-4); }
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
