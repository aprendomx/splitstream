<script setup>
import { computed } from 'vue'
import { iBroadcast } from '@/iconos'
import { usePanel } from '@/stores/panel'

const panel = usePanel()
const version = computed(() => panel.estado?.version || 'dev')

const REPOSITORIO = 'https://github.com/aprendomx/splitstream'

// Licencias leídas de los propios paquetes, no supuestas.
const MOTOR = [
  { n: 'go-rtmp', l: 'Boost Software License 1.0', u: 'https://github.com/yutopp/go-rtmp',
    q: 'Cliente y servidor RTMP. Es lo que habla con OBS y con las plataformas.' },
  { n: 'modernc.org/sqlite', l: 'BSD-3-Clause', u: 'https://gitlab.com/cznic/sqlite',
    q: 'SQLite reescrito en Go puro. Es lo que permite que un solo binario funcione en cinco plataformas sin compilador de C.' },
  { n: 'coder/websocket', l: 'ISC', u: 'https://github.com/coder/websocket',
    q: 'El WebSocket que empuja el estado al panel cada segundo.' },
  { n: 'golang.org/x/crypto', l: 'BSD-3-Clause', u: 'https://pkg.go.dev/golang.org/x/crypto',
    q: 'argon2id para la contraseña del panel.' },
  { n: 'golang.org/x/time', l: 'BSD-3-Clause', u: 'https://pkg.go.dev/golang.org/x/time',
    q: 'El limitador de intentos de acceso.' },
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
    q: 'Los servidores RTMP falsos contra los que corren los tests de integración.' },
  { n: 'FFmpeg', l: 'LGPL-2.1 / GPL-2.0', u: 'https://ffmpeg.org',
    q: 'Publica el patrón de prueba en los tests, y sirve para aislar si un fallo es nuestro o de la plataforma.' },
]
</script>

<template>
  <q-page class="q-pa-md q-pb-xl creditos">
    <div class="contenido">
      <div class="text-center q-mb-lg">
        <q-icon :name="iBroadcast" size="40px" class="text-primary" />
        <div class="text-h5 q-mt-sm">Splitstream</div>
        <div class="text-caption text-grey-5">versión {{ version }}</div>
        <div class="text-body2 text-grey-4 q-mt-sm">
          Retransmisión RTMP self-hosted. Recibe un stream desde OBS y lo reenvía
          simultáneamente a varias plataformas, sin transcodificar.
        </div>
        <q-btn
          flat no-caps color="primary" class="q-mt-sm"
          label="Ver el proyecto en GitHub"
          type="a" :href="REPOSITORIO" target="_blank" rel="noopener noreferrer"
        />
      </div>

      <q-card flat bordered class="q-mb-md">
        <q-card-section>
          <div class="text-subtitle1">Licencia</div>
          <p class="text-body2 text-grey-4 q-mt-sm q-mb-none">
            Splitstream se publica bajo la licencia <strong>MIT</strong>: puedes usarlo,
            modificarlo y distribuirlo, también con fines comerciales, conservando el aviso
            de copyright. Se ofrece sin garantía de ningún tipo.
          </p>
        </q-card-section>
      </q-card>

      <q-card flat bordered class="q-mb-md">
        <q-card-section>
          <div class="text-subtitle1">Gracias</div>
          <p class="text-body2 text-grey-4 q-mt-sm q-mb-none">
            Esto existe gracias al trabajo de otras personas, publicado libremente. Lo que
            sigue no es un requisito legal: es de dónde viene lo que estás usando.
          </p>
        </q-card-section>
      </q-card>

      <div class="text-subtitle2 text-grey-5 q-mb-sm">El motor</div>
      <q-list bordered separator class="rounded-borders q-mb-md">
        <q-item v-for="d in MOTOR" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section>
            <q-item-label>{{ d.n }}</q-item-label>
            <q-item-label caption class="porque">{{ d.q }}</q-item-label>
          </q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-6" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <div class="text-subtitle2 text-grey-5 q-mb-sm">El panel</div>
      <q-list bordered separator class="rounded-borders q-mb-md">
        <q-item v-for="d in PANEL" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section>{{ d.n }}</q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-6" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <div class="text-subtitle2 text-grey-5 q-mb-sm">Para construirlo y probarlo</div>
      <q-list bordered separator class="rounded-borders q-mb-md">
        <q-item v-for="d in HERRAMIENTAS" :key="d.n" clickable tag="a" :href="d.u"
                target="_blank" rel="noopener noreferrer">
          <q-item-section>
            <q-item-label>{{ d.n }}</q-item-label>
            <q-item-label caption class="porque">{{ d.q }}</q-item-label>
          </q-item-section>
          <q-item-section side>
            <q-badge outline color="grey-6" :label="d.l" />
          </q-item-section>
        </q-item>
      </q-list>

      <p class="text-caption text-grey-6 text-center q-mt-lg">
        Las marcas de YouTube, Twitch, Facebook, Kick, X y TikTok pertenecen a sus
        respectivos dueños. Splitstream no está afiliado a ninguna de ellas.
      </p>
    </div>
  </q-page>
</template>

<style scoped>
.contenido { max-width: 720px; margin: 0 auto; }
.porque { line-height: 1.45; }
</style>
