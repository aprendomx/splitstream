<script setup>
import { ref, onMounted } from 'vue'
import { useQuasar } from 'quasar'
import { iDescargar, iBorrar, iGrabaciones } from '@/iconos'
import { api } from '@/api'
import { bytesLegibles, duracionLegible } from '@/diagnostico'
import { t, formatearFecha } from '@/i18n'

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
    title: t('grabaciones.eliminar_titulo'),
    message: t('grabaciones.eliminar_mensaje', { nombre: nombre(g) }),
    cancel: { flat: true, noCaps: true, label: t('comun.cancelar') },
    ok: { color: 'negative', unelevated: true, noCaps: true, label: t('comun.eliminar') },
    persistent: true,
  }).onOk(async () => {
    try {
      await api.borrarGrabacion(g.id)
      lista.value = lista.value.filter((x) => x.id !== g.id)
      $q.notify({ type: 'positive', message: t('grabaciones.eliminada') })
    } catch (e) {
      $q.notify({ type: 'negative', message: e.message })
    }
  })
}

const nombre = (g) => g.path.split('/').at(-1)
</script>

<template>
  <q-page class="q-pa-md q-pb-xl">
    <div class="contenido">
      <div class="row items-center q-mb-sm">
        <div class="text-h6">{{ t('app.grabaciones') }}</div>
        <q-space />
        <q-btn flat no-caps :label="t('app.ajustes')" :to="{ name: 'ajustes' }" />
      </div>

      <q-card v-if="!lista.length && !cargando" flat bordered class="q-pa-lg text-center">
        <q-icon :name="iGrabaciones" size="36px" class="text-grey-7" />
        <div class="text-body2 text-grey-5 q-mt-sm">{{ t('grabaciones.sin_grabaciones') }}</div>
      </q-card>

      <q-list v-else bordered separator class="rounded-borders">
        <q-item v-for="g in lista" :key="g.id">
          <q-item-section>
            <q-item-label>
              {{ nombre(g) }}
              <q-badge v-if="g.in_progress" color="negative" :label="t('grabaciones.en_curso')" class="q-ml-xs" />
            </q-item-label>
            <q-item-label caption>
              {{ formatearFecha(g.started_at) }} · {{ t('grabaciones.sesion', { id: g.session_id ?? '—' }) }} · {{ t('grabaciones.segmento', { n: g.segment }) }}
              · {{ duracionLegible(g.duration_ms / 1000) }} · {{ bytesLegibles(g.bytes) }}
            </q-item-label>
          </q-item-section>
          <q-item-section side>
            <div class="row items-center no-wrap q-gutter-xs">
              <q-btn flat round dense :icon="iDescargar" size="sm" :aria-label="t('grabaciones.descargar')" :disable="g.in_progress"
                     type="a" :href="api.urlDescargaGrabacion(g.id)" />
              <q-btn flat round dense :icon="iBorrar" size="sm" class="text-negative" :aria-label="t('comun.eliminar')"
                     :disable="g.in_progress" @click="borrar(g)" />
            </div>
          </q-item-section>
        </q-item>
      </q-list>

      <div v-if="hayMas" class="text-center q-mt-md">
        <q-btn flat no-caps :label="t('grabaciones.cargar_mas')" :loading="cargando" @click="cargarMas" />
      </div>

      <p class="text-caption text-grey-6 q-mt-lg">
        {{ t('grabaciones.nota_flv') }}
        <code>ffmpeg -i grabacion.flv -c copy grabacion.mp4</code>
      </p>
    </div>
  </q-page>
</template>

<style scoped>
.contenido { max-width: 760px; margin: 0 auto; }
</style>
