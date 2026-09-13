<script setup>
import { ref, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iGrabaciones, iHistorial } from '@/iconos'
import { api } from '@/api'
import { bitrateLegible, duracionLegible } from '@/diagnostico'
import { t, formatearFecha, formatearNumero } from '@/i18n'

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
// llama la salta (lo dice la insignia «en curso» de la fila).
const duracion = (s) => duracionLegible((new Date(s.ended_at) - new Date(s.started_at)) / 1000)

// width y height llegan null si la sesión murió antes del primer sequence header.
const resolucion = (s) => (s.width && s.height ? `${s.width}×${s.height}` : '—')

// Los tres contadores de eventos, pero solo los que tienen algo que contar: una fila con
// «0 avisos · 0 errores» ocupa sitio para decir que no pasó nada.
const NIVELES = [
  { clave: 'info', color: 'grey-6', etiqueta: () => t('registro.nivel_info') },
  { clave: 'warn', color: 'warning', etiqueta: () => t('registro.nivel_aviso') },
  { clave: 'error', color: 'negative', etiqueta: () => t('registro.nivel_error') },
]
const niveles = (s) =>
  NIVELES.filter((n) => (s.events?.[n.clave] ?? 0) > 0).map((n) => ({
    clave: n.clave,
    color: n.color,
    texto: `${formatearNumero(s.events[n.clave])} ${n.etiqueta()}`,
  }))
</script>

<template>
  <q-page class="q-pa-md q-pb-xl">
    <div class="contenido">
      <div class="row items-center q-mb-sm">
        <div class="text-h6">{{ t('app.historial') }}</div>
        <q-space />
        <q-btn flat no-caps :label="t('app.grabaciones')" :to="{ name: 'grabaciones' }" />
      </div>

      <q-card v-if="!lista.length && !cargando" flat bordered class="q-pa-lg text-center">
        <q-icon :name="iHistorial" size="36px" class="text-grey-7" />
        <div class="text-body2 text-grey-5 q-mt-sm">{{ t('historial.sin_sesiones') }}</div>
      </q-card>

      <q-list v-else bordered separator class="rounded-borders">
        <q-item v-for="s in lista" :key="s.id" clickable :to="{ name: 'sesion', params: { id: s.id } }">
          <q-item-section>
            <q-item-label>
              {{ formatearFecha(s.started_at) }}
              <q-badge v-if="!s.ended_at" color="positive" :label="t('historial.en_curso')" class="q-ml-xs" />
            </q-item-label>
            <q-item-label caption>
              <template v-if="s.ended_at">{{ duracion(s) }} · </template>{{ resolucion(s) }}
              · {{ bitrateLegible(s.bitrate_bps) }}
            </q-item-label>
          </q-item-section>
          <q-item-section side>
            <div class="row items-center no-wrap q-gutter-xs">
              <q-icon v-if="s.has_recording" :name="iGrabaciones" size="18px" class="text-grey-5">
                <q-tooltip>{{ t('historial.con_grabacion') }}</q-tooltip>
              </q-icon>
              <q-badge v-for="n in niveles(s)" :key="n.clave" :color="n.color" :label="n.texto" class="nivel" />
            </div>
          </q-item-section>
        </q-item>
      </q-list>

      <div v-if="hayMas" class="text-center q-mt-md">
        <q-btn flat no-caps :label="t('historial.cargar_mas')" :loading="cargando" @click="cargarMas" />
      </div>
    </div>
  </q-page>
</template>

<style scoped>
.contenido { max-width: 760px; margin: 0 auto; }
.nivel { font-size: 10px; }
</style>
