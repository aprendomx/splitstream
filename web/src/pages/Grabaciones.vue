<script setup>
import { ref, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iDescargar, iBorrar, iGrabaciones } from '@/iconos'
import { api } from '@/api'
import { bytesLegibles, duracionLegible } from '@/diagnostico'

// Lista de segmentos grabados, del más reciente al más antiguo, con descarga y borrado.
// La descarga es un enlace: el navegador manda la cookie y el backend responde con
// attachment, así que no hay que pasar por un blob.
const $q = useQuasar()
const lista = ref([])
const cargando = ref(false)
const hayMas = ref(false)
const PAGINA = 50

async function cargar(before = 0) {
  cargando.value = true
  try {
    const nuevas = await api.grabaciones(0, PAGINA, before)
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

function borrar(g) {
  $q.dialog({
    title: 'Eliminar grabación',
    message: `Se borrará «${nombre(g)}» del disco. No se puede deshacer.`,
    cancel: { flat: true, noCaps: true, label: 'Cancelar' },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: 'Eliminar' },
    persistent: true,
  }).onOk(async () => {
    try {
      await api.borrarGrabacion(g.id)
      lista.value = lista.value.filter((x) => x.id !== g.id)
      $q.notify({ type: 'positive', message: 'Grabación eliminada' })
    } catch (e) {
      $q.notify({ type: 'negative', message: e.message })
    }
  })
}

const nombre = (g) => g.path.split('/').at(-1)
const fecha = (iso) => new Date(iso).toLocaleString('es', { dateStyle: 'medium', timeStyle: 'short' })
</script>

<template>
  <q-page class="q-pa-md q-pb-xl">
    <div class="contenido">
      <div class="row items-center q-mb-sm">
        <div class="text-h6">Grabaciones</div>
        <q-space />
        <q-btn flat no-caps label="Ajustes" :to="{ name: 'ajustes' }" />
      </div>

      <q-card v-if="!lista.length && !cargando" flat bordered class="q-pa-lg text-center">
        <q-icon :name="iGrabaciones" size="36px" class="text-grey-7" />
        <div class="text-body2 text-grey-5 q-mt-sm">Todavía no hay grabaciones. Actívalas en Ajustes.</div>
      </q-card>

      <q-list v-else bordered separator class="rounded-borders">
        <q-item v-for="g in lista" :key="g.id">
          <q-item-section>
            <q-item-label>
              {{ nombre(g) }}
              <q-badge v-if="g.in_progress" color="negative" label="en curso" class="q-ml-xs" />
            </q-item-label>
            <q-item-label caption>
              {{ fecha(g.started_at) }} · sesión {{ g.session_id ?? '—' }} · segmento {{ g.segment }}
              · {{ duracionLegible(g.duration_ms / 1000) }} · {{ bytesLegibles(g.bytes) }}
            </q-item-label>
          </q-item-section>
          <q-item-section side>
            <div class="row items-center no-wrap q-gutter-xs">
              <q-btn flat round dense :icon="iDescargar" size="sm" aria-label="Descargar" :disable="g.in_progress"
                     type="a" :href="api.urlDescargaGrabacion(g.id)" />
              <q-btn flat round dense :icon="iBorrar" size="sm" class="text-negative" aria-label="Eliminar"
                     :disable="g.in_progress" @click="borrar(g)" />
            </div>
          </q-item-section>
        </q-item>
      </q-list>

      <div v-if="hayMas" class="text-center q-mt-md">
        <q-btn flat no-caps label="Cargar más" :loading="cargando" @click="cargarMas" />
      </div>

      <p class="text-caption text-grey-6 q-mt-lg">
        Los archivos son FLV, que cualquier reproductor abre. Para pasar uno a MP4 sin recodificar:
        <code>ffmpeg -i grabacion.flv -c copy grabacion.mp4</code>
      </p>
    </div>
  </q-page>
</template>

<style scoped>
.contenido { max-width: 760px; margin: 0 auto; }
</style>
