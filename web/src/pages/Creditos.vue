<script setup>
import { computed } from 'vue'
import { iBroadcast } from '@/iconos'
import { usePanel } from '@/stores/panel'
import { t } from '@/i18n'

const panel = usePanel()
const version = computed(() => panel.estado?.version || 'dev')

const REPOSITORIO = 'https://github.com/aprendomx/splitstream'

// Licencias leídas de los propios paquetes, no supuestas. La explicación de cada una (qKey)
// es nuestra y se traduce; el nombre y la licencia son datos y no se tocan.
const MOTOR = [
  { n: 'go-rtmp', l: 'Boost Software License 1.0', u: 'https://github.com/yutopp/go-rtmp',
    qKey: 'creditos.motor.go_rtmp' },
  { n: 'modernc.org/sqlite', l: 'BSD-3-Clause', u: 'https://gitlab.com/cznic/sqlite',
    qKey: 'creditos.motor.sqlite' },
  { n: 'coder/websocket', l: 'ISC', u: 'https://github.com/coder/websocket',
    qKey: 'creditos.motor.websocket' },
  { n: 'golang.org/x/crypto', l: 'BSD-3-Clause', u: 'https://pkg.go.dev/golang.org/x/crypto',
    qKey: 'creditos.motor.crypto' },
  { n: 'golang.org/x/time', l: 'BSD-3-Clause', u: 'https://pkg.go.dev/golang.org/x/time',
    qKey: 'creditos.motor.time' },
]

const PANEL = [
  { n: 'Vue', l: 'MIT', u: 'https://vuejs.org' },
  { n: 'Quasar', l: 'MIT', u: 'https://quasar.dev' },
  { n: 'Pinia', l: 'MIT', u: 'https://pinia.vuejs.org' },
  { n: 'Vue Router', l: 'MIT', u: 'https://router.vuejs.org' },
  { n: 'vuedraggable / SortableJS', l: 'MIT', u: 'https://github.com/SortableJS/vue.draggable.next' },
  { n: 'Vite', l: 'MIT', u: 'https://vite.dev' },
  { n: 'Material Design Icons', l: 'Apache-2.0', u: 'https://pictogrammers.com/library/mdi/' },
]

const HERRAMIENTAS = [
  { n: 'mediamtx', l: 'MIT', u: 'https://github.com/bluenviron/mediamtx',
    qKey: 'creditos.herramientas.mediamtx' },
  { n: 'FFmpeg', l: 'LGPL-2.1 / GPL-2.0', u: 'https://ffmpeg.org',
    qKey: 'creditos.herramientas.ffmpeg' },
]
</script>

<template>
  <q-page class="q-pa-md q-pb-xl creditos">
    <div class="contenido">
      <div class="text-center q-mb-lg">
        <q-icon :name="iBroadcast" size="40px" class="text-primary" />
        <div class="text-h5 q-mt-sm">Splitstream</div>
        <div class="text-caption text-grey-5">{{ t('creditos.version', { version }) }}</div>
        <div class="text-body2 text-grey-4 q-mt-sm">
          {{ t('creditos.subtitulo') }}
        </div>
        <q-btn
          flat no-caps color="primary" class="q-mt-sm"
          :label="t('creditos.ver_en_github')"
          type="a" :href="REPOSITORIO" target="_blank" rel="noopener noreferrer"
        />
      </div>

      <q-card flat bordered class="q-mb-md">
        <q-card-section>
          <div class="text-subtitle1">{{ t('creditos.licencia_titulo') }}</div>
          <p class="text-body2 text-grey-4 q-mt-sm q-mb-none">
            {{ t('creditos.licencia_pre') }} <strong>MIT</strong>{{ t('creditos.licencia_post') }}
          </p>
        </q-card-section>
      </q-card>

      <q-card flat bordered class="q-mb-md">
        <q-card-section>
          <div class="text-subtitle1">{{ t('creditos.gracias_titulo') }}</div>
          <p class="text-body2 text-grey-4 q-mt-sm q-mb-none">
            {{ t('creditos.gracias_texto') }}
          </p>
        </q-card-section>
      </q-card>

      <div class="text-subtitle2 text-grey-5 q-mb-sm">{{ t('creditos.el_motor') }}</div>
      <q-list bordered separator class="rounded-borders q-mb-md">
        <q-item v-for="d in MOTOR" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section>
            <q-item-label>{{ d.n }}</q-item-label>
            <q-item-label caption class="porque">{{ t(d.qKey) }}</q-item-label>
          </q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-6" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <div class="text-subtitle2 text-grey-5 q-mb-sm">{{ t('creditos.el_panel') }}</div>
      <q-list bordered separator class="rounded-borders q-mb-md">
        <q-item v-for="d in PANEL" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section>{{ d.n }}</q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-6" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <div class="text-subtitle2 text-grey-5 q-mb-sm">{{ t('creditos.construir_probar') }}</div>
      <q-list bordered separator class="rounded-borders q-mb-md">
        <q-item v-for="d in HERRAMIENTAS" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section>
            <q-item-label>{{ d.n }}</q-item-label>
            <q-item-label caption class="porque">{{ t(d.qKey) }}</q-item-label>
          </q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-6" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <p class="text-caption text-grey-6 text-center q-mt-lg">
        {{ t('creditos.marcas') }}
      </p>
    </div>
  </q-page>
</template>

<style scoped>
.contenido { max-width: 720px; margin: 0 auto; }
.porque { line-height: 1.45; }
</style>
