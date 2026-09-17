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
    <div class="pagina">
      <div class="row items-center q-mb-md">
        <div class="ss-t-22">{{ t('app.grabaciones') }}</div>
        <q-space />
        <q-btn flat no-caps :label="t('app.ajustes')" :to="{ name: 'ajustes' }" />
      </div>

      <div v-if="cargando && !lista.length" aria-busy="true">
        <span class="sr-only">{{ t('app.cargando') }}</span>
        <q-skeleton type="rect" height="64px" class="q-mb-sm" />
        <q-skeleton type="rect" height="64px" class="q-mb-sm" />
        <q-skeleton type="rect" height="64px" />
      </div>

      <q-card v-else-if="!lista.length" flat bordered class="vacio">
        <q-icon :name="iGrabaciones" size="32px" class="ss-muted" aria-hidden="true" />
        <div class="ss-t-16">{{ t('grabaciones.sin_grabaciones') }}</div>
        <div class="ss-t-14 ss-muted">{{ t('grabaciones.sin_grabaciones_detalle') }}</div>
      </q-card>

      <q-list v-else bordered separator class="lista">
        <q-item v-for="g in lista" :key="g.id">
          <q-item-section>
            <q-item-label class="ss-t-16">
              {{ nombre(g) }}
              <q-badge v-if="g.in_progress" color="negative" :label="t('grabaciones.en_curso')" class="q-ml-xs" />
            </q-item-label>
            <q-item-label caption class="ss-t-14 ss-muted ss-tabular">
              {{ formatearFecha(g.started_at) }} · {{ t('grabaciones.sesion', { id: g.session_id ?? '—' }) }} · {{ t('grabaciones.segmento', { n: g.segment }) }}
              · {{ duracionLegible(g.duration_ms / 1000) }} · {{ bytesLegibles(g.bytes) }}
            </q-item-label>
          </q-item-section>
          <q-item-section side>
            <div class="row items-center no-wrap q-gutter-sm acciones">
              <q-btn outline no-caps size="md" :icon="iDescargar" :label="t('grabaciones.descargar')" :disable="g.in_progress"
                     type="a" :href="api.urlDescargaGrabacion(g.id)" />
              <q-btn flat round dense :icon="iBorrar" size="sm" class="text-negative" :aria-label="t('comun.eliminar')"
                     :disable="g.in_progress" @click="borrar(g)" />
            </div>
          </q-item-section>
        </q-item>
      </q-list>

      <div v-if="hayMas" class="text-center q-mt-md">
        <q-btn outline no-caps :label="t('grabaciones.cargar_mas')" :loading="cargando" @click="cargarMas" />
      </div>

      <p class="ss-t-14 ss-muted q-mt-lg">
        {{ t('grabaciones.nota_flv') }}
        <code>ffmpeg -i grabacion.flv -c copy grabacion.mp4</code>
      </p>
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
