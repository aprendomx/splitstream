<script setup>
import { ref, computed } from 'vue'
import { useQuasar } from 'quasar'
import { usePanel } from '@/stores/panel'
import { iCopiar, iVer, iOcultar, iAviso } from '@/iconos'

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
const hayUrlPublica = computed(() => Boolean(panel.estado?.panel?.tls && panel.estado?.panel?.public_url))
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
    $q.notify({ type: 'positive', message: 'Copiado' })
  } catch {
    $q.notify({ type: 'warning', message: 'Tu navegador no deja copiar aquí. Selecciona el texto y cópialo a mano.' })
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
        <q-step :name="1" title="Crea un proyecto en Google Cloud" :done="paso > 1">
          <p class="text-body2">
            Entra a <a href="https://console.cloud.google.com/projectcreate" target="_blank" rel="noopener noreferrer">Google Cloud Console</a>
            y crea un proyecto nuevo (puede ser solo para esto).
          </p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" label="Siguiente" @click="paso = 2" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="2" title="Habilita la YouTube Data API v3" :done="paso > 2">
          <p class="text-body2">
            Con el proyecto abierto, ve a «APIs y servicios → Biblioteca», busca
            <b>YouTube Data API v3</b> y pulsa «Habilitar».
          </p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" label="Siguiente" @click="paso = 3" />
            <q-btn flat no-caps label="Atrás" class="q-ml-sm" @click="paso = 1" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="3" title="Configura la pantalla de consentimiento" :done="paso > 3">
          <p class="text-body2">
            En «APIs y servicios → Pantalla de consentimiento de OAuth», elige tipo
            <b>Externo</b> y, en la pestaña de usuarios de prueba, añade tu propia cuenta de
            Google.
          </p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" label="Siguiente" @click="paso = 4" />
            <q-btn flat no-caps label="Atrás" class="q-ml-sm" @click="paso = 2" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="4" title="Crea la credencial" :done="paso > 4">
          <p class="text-body2">
            En «APIs y servicios → Credenciales → Crear credenciales → ID de cliente de
            OAuth», elige el tipo <b>«TVs and Limited Input devices»</b> y copia el
            <code>client_id</code> y el <code>client_secret</code> que te da.
          </p>
          <q-banner dense class="bg-grey-9 text-grey-3 rounded-borders">
            <template #avatar><q-icon :name="iAviso" color="warning" /></template>
            Mientras el proyecto esté en «Testing», Google caduca la autorización a los 7
            días y hay que reconectar la cuenta. Publicar la app («In production») lo evita,
            a cambio de una pantalla de «app no verificada» que solo ve tu propia cuenta.
          </q-banner>
          <p class="text-caption text-grey-5">
            Guía con capturas:
            <a href="https://github.com/aprendomx/splitstream/blob/main/docs/youtube-credenciales.md" target="_blank" rel="noopener noreferrer">docs/youtube-credenciales.md</a>
          </p>
        </q-step>
      </template>

      <template v-else>
        <q-step :name="1" title="Abre los ajustes de desarrollador de Kick" :done="paso > 1">
          <p class="text-body2">
            En Kick, ve a Ajustes → Developer. Necesitas la verificación en dos pasos (2FA)
            activada en tu cuenta para entrar aquí.
          </p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" label="Siguiente" @click="paso = 2" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="2" title="Crea una app nueva" :done="paso > 2">
          <p class="text-body2">Pulsa «Crear app» (o el botón equivalente) para empezar.</p>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" label="Siguiente" @click="paso = 3" />
            <q-btn flat no-caps label="Atrás" class="q-ml-sm" @click="paso = 1" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="3" title="Redirect URL" :done="paso > 3">
          <p class="text-body2">
            Pega esta dirección <b>exactamente así</b>, sin cambiar nada, como Redirect URL
            de la app:
          </p>
          <div class="row items-center no-wrap q-gutter-sm campo-copiable">
            <div class="col campo-mono">{{ redirectUrl }}</div>
            <q-btn flat round dense :icon="iCopiar" aria-label="Copiar la Redirect URL" @click="copiar(redirectUrl)" />
          </div>
          <q-stepper-navigation>
            <q-btn unelevated no-caps color="primary" label="Siguiente" @click="paso = 4" />
            <q-btn flat no-caps label="Atrás" class="q-ml-sm" @click="paso = 2" />
          </q-stepper-navigation>
        </q-step>
        <q-step :name="4" title="Webhooks del chat (opcional)" :done="paso > 4">
          <template v-if="hayUrlPublica">
            <p class="text-body2">
              Si quieres el chat de Kick en el panel, activa «Enable Webhooks» y pega esta
              dirección:
            </p>
            <div class="row items-center no-wrap q-gutter-sm campo-copiable">
              <div class="col campo-mono">{{ webhookUrl }}</div>
              <q-btn flat round dense :icon="iCopiar" aria-label="Copiar la URL del webhook" @click="copiar(webhookUrl)" />
            </div>
          </template>
          <q-banner v-else dense class="bg-grey-9 text-grey-3 rounded-borders">
            <template #avatar><q-icon :name="iAviso" color="warning" /></template>
            Sin URL pública el chat de Kick no está disponible. Puedes seguir sin esto: la
            retransmisión funciona igual, solo faltará el chat.
          </q-banner>
          <p class="text-body2 q-mt-sm">
            Por último, copia el <code>client_id</code> y el <code>client_secret</code> de
            la app.
          </p>
          <p class="text-caption text-grey-5">
            Guía con capturas:
            <a href="https://github.com/aprendomx/splitstream/blob/main/docs/kick-credenciales.md" target="_blank" rel="noopener noreferrer">docs/kick-credenciales.md</a>
          </p>
        </q-step>
      </template>
    </q-stepper>

    <div>
      <a href="#" class="text-caption enlace-por-que" @click.prevent="porQue = !porQue">¿Por qué me piden esto?</a>
      <p v-if="porQue" class="text-body2 text-grey-5 q-mt-xs">
        <template v-if="plataforma === 'youtube'">
          YouTube reparte la cuota de su API por proyecto, no por usuario: si Splitstream
          usara una app compartida entre todo el mundo, cualquiera podría agotarla y
          dejarte sin poder crear emisiones. Con tu propio proyecto, la cuota es solo tuya.
        </template>
        <template v-else>
          Kick no admite apps públicas compartidas entre distintas instalaciones: cada
          integración necesita su propia app registrada. No hay forma de rodear esto.
        </template>
      </p>
    </div>

    <q-input
      v-model="clientId"
      label="Client ID"
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
      label="Client secret"
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
          :aria-label="verSecreto ? 'Ocultar' : 'Mostrar'"
          @click="verSecreto = !verSecreto"
        />
      </template>
    </q-input>

    <q-btn unelevated no-caps color="primary" label="Continuar" :disable="!puedeContinuar" @click="continuar" />
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
