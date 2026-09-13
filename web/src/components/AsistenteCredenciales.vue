<script setup>
import { ref, computed } from 'vue'
import { useQuasar } from 'quasar'
import { usePanel } from '@/stores/panel'
import { iCopiar, iVer, iOcultar, iAviso } from '@/iconos'
import { t } from '@/i18n'

// Guía paso a paso para crear la app propia que YouTube y Kick exigen (spec §3.3). Las
// credenciales solo viven aquí mientras la persona las pega: al pulsar «Continuar» se
// mandan hacia arriba y se olvidan (nunca a localStorage, nunca a un log).
const props = defineProps({
  plataforma: { type: String, required: true }, // youtube | kick
  origin: { type: String, required: true },
})
const emit = defineEmits(['listo'])

const $q = useQuasar()
const panel = usePanel()

const paso = ref(1)
const clientId = ref('')
const clientSecret = ref('')
const verSecreto = ref(false)
const porQue = ref(false)

const redirectUrl = computed(() => `${props.origin}/api/platforms/kick/callback`)
// `public_url_ok` lo decide el servidor (platformDTO): es él quien sabe si termina TLS y
// con qué URL pública. Recalcularlo aquí con `tls` y `public_url` duplicaba esa regla y se
// desviaría en cuanto el servidor la cambiara.
const hayUrlPublica = computed(() => Boolean(panel.plataformas.find((p) => p.id === 'kick')?.public_url_ok))
const webhookUrl = computed(() => `${panel.estado?.panel?.public_url ?? ''}/api/platforms/kick/webhook`)

const puedeContinuar = computed(() => clientId.value.trim() && clientSecret.value.trim())

/**
 * Copia al portapapeles con el mismo respaldo que el resto del panel: navigator.clipboard
 * solo existe en contexto seguro (HTTPS o localhost), y en la red local por IP no lo hay.
 */
async function copiar(texto) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(texto)
    } else {
      const ta = document.createElement('textarea')
      ta.value = texto
      ta.setAttribute('readonly', '')
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      document.body.removeChild(ta)
      if (!ok) throw new Error('execCommand devolvió false')
    }
    $q.notify({ type: 'positive', message: t('asistente_credenciales.copiado') })
  } catch {
    $q.notify({ type: 'warning', message: t('asistente_credenciales.no_se_pudo_copiar') })
  }
}

function continuar() {
  const credenciales = { client_id: clientId.value.trim(), client_secret: clientSecret.value.trim() }
  // Se limpian en el acto: de aquí solo salen hacia el evento, nunca se quedan en pantalla
  // ni en memoria más de lo necesario.
  clientId.value = ''
  clientSecret.value = ''
  emit('listo', credenciales)
}
</script>

<template>
  <div class="asistente-credenciales q-gutter-y-md">
    <q-stepper v-model="paso" vertical flat bordered color="primary" animated class="rounded-borders">
      <template v-if="plataforma === 'youtube'">
        <q-step :name="1" :title="t('asistente_credenciales.youtube.paso1.titulo')" :done="paso > 1">
          <p class="text-body2">
            {{ t('asistente_credenciales.youtube.paso1.texto_pre') }}
            <a href="https://console.cloud.google.com/projectcreate" target="_blank" rel="noopener noreferrer">Google Cloud Console</a>
            {{ t('asistente_credenciales.youtube.paso1.texto_post') }}
          </p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" :label="t('asistente_credenciales.siguiente')" @click="paso = 2" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="2" :title="t('asistente_credenciales.youtube.paso2.titulo')" :done="paso > 2">
          <p class="text-body2">
            {{ t('asistente_credenciales.youtube.paso2.texto_pre') }}
            <b>YouTube Data API v3</b> {{ t('asistente_credenciales.youtube.paso2.texto_post') }}
          </p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" :label="t('asistente_credenciales.siguiente')" @click="paso = 3" />
            <q-btn flat no-caps :label="t('asistente_credenciales.atras')" class="q-ml-sm" @click="paso = 1" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="3" :title="t('asistente_credenciales.youtube.paso3.titulo')" :done="paso > 3">
          <p class="text-body2">
            {{ t('asistente_credenciales.youtube.paso3.texto_pre') }}
            <b>{{ t('asistente_credenciales.youtube.paso3.tipo') }}</b> {{ t('asistente_credenciales.youtube.paso3.texto_post') }}
          </p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" :label="t('asistente_credenciales.siguiente')" @click="paso = 4" />
            <q-btn flat no-caps :label="t('asistente_credenciales.atras')" class="q-ml-sm" @click="paso = 2" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="4" :title="t('asistente_credenciales.youtube.paso4.titulo')" :done="paso > 4">
          <p class="text-body2">
            {{ t('asistente_credenciales.youtube.paso4.texto_pre') }}
            <b>«TVs and Limited Input devices»</b> {{ t('asistente_credenciales.youtube.paso4.texto_post1') }}
            <code>client_id</code> {{ t('asistente_credenciales.youtube.paso4.texto_post2') }} <code>client_secret</code>
            {{ t('asistente_credenciales.youtube.paso4.texto_post3') }}
          </p>
          <q-banner dense class="bg-grey-9 text-grey-3 rounded-borders">
            <template #avatar><q-icon :name="iAviso" color="warning" /></template>
            {{ t('asistente_credenciales.youtube.paso4.aviso_testing') }}
          </q-banner>
          <p class="text-caption text-grey-5">
            {{ t('asistente_credenciales.guia_capturas') }}
            <a href="https://github.com/aprendomx/splitstream/blob/main/docs/youtube-credenciales.md" target="_blank" rel="noopener noreferrer">docs/youtube-credenciales.md</a>
          </p>
        </q-step>
      </template>

      <template v-else>
        <q-step :name="1" :title="t('asistente_credenciales.kick.paso1.titulo')" :done="paso > 1">
          <p class="text-body2">
            {{ t('asistente_credenciales.kick.paso1.texto') }}
          </p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" :label="t('asistente_credenciales.siguiente')" @click="paso = 2" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="2" :title="t('asistente_credenciales.kick.paso2.titulo')" :done="paso > 2">
          <p class="text-body2">{{ t('asistente_credenciales.kick.paso2.texto') }}</p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" :label="t('asistente_credenciales.siguiente')" @click="paso = 3" />
            <q-btn flat no-caps :label="t('asistente_credenciales.atras')" class="q-ml-sm" @click="paso = 1" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="3" :title="t('asistente_credenciales.kick.paso3.titulo')" :done="paso > 3">
          <p class="text-body2">
            {{ t('asistente_credenciales.kick.paso3.texto_pre') }}
            <b>{{ t('asistente_credenciales.kick.paso3.enfasis') }}</b>{{ t('asistente_credenciales.kick.paso3.texto_post') }}
          </p>
          <div class="row items-center no-wrap q-gutter-sm campo-copiable">
            <div class="col campo-mono">{{ redirectUrl }}</div>
            <q-btn flat round dense :icon="iCopiar" :aria-label="t('asistente_credenciales.kick.paso3.copiar_url')" @click="copiar(redirectUrl)" />
          </div>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" :label="t('asistente_credenciales.siguiente')" @click="paso = 4" />
            <q-btn flat no-caps :label="t('asistente_credenciales.atras')" class="q-ml-sm" @click="paso = 2" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="4" :title="t('asistente_credenciales.kick.paso4.titulo')" :done="paso > 4">
          <template v-if="hayUrlPublica">
            <p class="text-body2">
              {{ t('asistente_credenciales.kick.paso4.texto_url_publica') }}
            </p>
            <div class="row items-center no-wrap q-gutter-sm campo-copiable">
              <div class="col campo-mono">{{ webhookUrl }}</div>
              <q-btn flat round dense :icon="iCopiar" :aria-label="t('asistente_credenciales.kick.paso4.copiar_webhook')" @click="copiar(webhookUrl)" />
            </div>
          </template>
          <q-banner v-else dense class="bg-grey-9 text-grey-3 rounded-borders">
            <template #avatar><q-icon :name="iAviso" color="warning" /></template>
            {{ t('asistente_credenciales.kick.paso4.sin_url_publica') }}
          </q-banner>
          <p class="text-body2 q-mt-sm">
            {{ t('asistente_credenciales.kick.paso4.texto_final_pre') }}
            <code>client_id</code> {{ t('asistente_credenciales.kick.paso4.texto_final_mid') }} <code>client_secret</code>
            {{ t('asistente_credenciales.kick.paso4.texto_final_post') }}
          </p>
          <p class="text-caption text-grey-5">
            {{ t('asistente_credenciales.guia_capturas') }}
            <a href="https://github.com/aprendomx/splitstream/blob/main/docs/kick-credenciales.md" target="_blank" rel="noopener noreferrer">docs/kick-credenciales.md</a>
          </p>
        </q-step>
      </template>
    </q-stepper>

    <div>
      <a href="#" class="text-caption enlace-por-que" @click.prevent="porQue = !porQue">{{ t('asistente_credenciales.por_que_pregunta') }}</a>
      <p v-if="porQue" class="text-body2 text-grey-5 q-mt-xs">
        <template v-if="plataforma === 'youtube'">
          {{ t('asistente_credenciales.por_que_youtube') }}
        </template>
        <template v-else>
          {{ t('asistente_credenciales.por_que_kick') }}
        </template>
      </p>
    </div>

    <q-input
      v-model="clientId"
      :label="t('asistente_credenciales.client_id')"
      outlined
      dense
      autocapitalize="off"
      autocorrect="off"
      spellcheck="false"
      autocomplete="off"
    />
    <q-input
      v-model="clientSecret"
      :type="verSecreto ? 'text' : 'password'"
      :label="t('asistente_credenciales.client_secret')"
      outlined
      dense
      autocapitalize="off"
      autocorrect="off"
      spellcheck="false"
      autocomplete="off"
    >
      <template #append>
        <q-btn
          flat
          round
          dense
          :icon="verSecreto ? iOcultar : iVer"
          :aria-label="verSecreto ? t('comun.ocultar') : t('comun.mostrar')"
          @click="verSecreto = !verSecreto"
        />
      </template>
    </q-input>

    <q-btn unelevated no-caps color="primary" :label="t('asistente_credenciales.continuar')" :disable="!puedeContinuar" @click="continuar" />
  </div>
</template>

<style scoped>
.campo-copiable { max-width: 100%; }
.campo-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  word-break: break-all;
  color: rgba(255, 255, 255, 0.85);
}
</style>
