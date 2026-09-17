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
    <div class="pagina">
      <div class="cabecera text-center">
        <q-icon :name="iBroadcast" size="48px" class="text-primary" aria-hidden="true" />
        <div class="ss-t-28 q-mt-sm">Splitstream</div>
        <div class="ss-t-14 ss-muted ss-tabular">{{ t('creditos.version', { version }) }}</div>
        <div class="ss-t-14 ss-muted q-mt-sm">
          {{ t('creditos.subtitulo') }}
        </div>
        <q-btn
          flat no-caps color="primary" class="q-mt-sm"
          :label="t('creditos.ver_en_github')"
          type="a" :href="REPOSITORIO" target="_blank" rel="noopener noreferrer"
        />
      </div>

      <q-card flat bordered class="q-mb-md q-mt-lg">
        <div class="cabecera-tarjeta">
          <span class="ss-t-16 titulo-texto">{{ t('creditos.licencia_titulo') }}</span>
        </div>
        <q-card-section class="ss-t-14">
          {{ t('creditos.licencia_pre') }} <strong>MIT</strong>{{ t('creditos.licencia_post') }}
        </q-card-section>
      </q-card>

      <q-card flat bordered class="q-mb-md">
        <div class="cabecera-tarjeta">
          <span class="ss-t-16 titulo-texto">{{ t('creditos.gracias_titulo') }}</span>
        </div>
        <q-card-section class="ss-t-14">
          {{ t('creditos.gracias_texto') }}
        </q-card-section>
      </q-card>

      <div class="ss-t-18 titulo-seccion">{{ t('creditos.el_motor') }}</div>
      <q-list bordered separator class="lista q-mb-md">
        <q-item v-for="d in MOTOR" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section>
            <q-item-label class="enlace">{{ d.n }}</q-item-label>
            <q-item-label caption class="ss-t-14 ss-muted">{{ t(d.qKey) }}</q-item-label>
          </q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-5" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <div class="ss-t-18 titulo-seccion">{{ t('creditos.el_panel') }}</div>
      <q-list bordered separator class="lista q-mb-md">
        <q-item v-for="d in PANEL" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section class="enlace">{{ d.n }}</q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-5" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <div class="ss-t-18 titulo-seccion">{{ t('creditos.construir_probar') }}</div>
      <q-list bordered separator class="lista q-mb-md">
        <q-item v-for="d in HERRAMIENTAS" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section>
            <q-item-label class="enlace">{{ d.n }}</q-item-label>
            <q-item-label caption class="ss-t-14 ss-muted">{{ t(d.qKey) }}</q-item-label>
          </q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-5" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <p class="ss-t-14 ss-muted text-center q-mt-lg">
        {{ t('creditos.marcas') }}
      </p>
    </div>
  </q-page>
</template>

<style scoped>
.pagina { max-width: 720px; margin: 0 auto; padding: var(--ss-space-5) var(--ss-space-4); }
.titulo-seccion { margin-top: var(--ss-space-6); margin-bottom: var(--ss-space-2); }
.cabecera-tarjeta { padding: var(--ss-space-3) var(--ss-space-4); border-bottom: 1px solid var(--ss-border); }
.titulo-texto { font-weight: 600; }
/* Enlaces de la lista de dependencias: color y subrayado propios, el foco lo da el anillo
   global (spec §3.4), no un color aparte. */
.enlace { color: var(--ss-primary-hover); text-decoration: underline; }
</style>
