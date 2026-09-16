<script setup>
import { ref, onUnmounted } from 'vue'
import { api, ApiError } from '@/api'
import { iVincular } from '@/iconos'
import AsistenteCredenciales from '@/components/AsistenteCredenciales.vue'
import { t } from '@/i18n'

const props = defineProps({
  plataforma: { type: String, required: true },
  nombre: { type: String, default: '' },
  // De platformDTO.capabilities.requires_own_app: YouTube y Kick exigen credenciales de
  // una app propia antes de poder empezar el flujo. Twitch no.
  requiereApp: { type: Boolean, default: false },
})
const emit = defineEmits(['conectada'])

// El origen se lee AQUÍ, no en la plantilla: dentro de `<template>` una expresión se
// compila contra el contexto del componente (`_ctx.location`), que no existe, y abrir el
// asistente reventaba con un TypeError. Los globales del navegador van siempre en el
// `<script setup>`.
const origen = location.origin

const mostrarAsistente = ref(false)
const inicio = ref(null)   // {state, verification_uri, user_code, redirect_url, expires_in}
const estado = ref('idle') // idle | pending | done | expired | error
const error = ref(null)
let temporizador = null

function empezarClick() {
  if (props.requiereApp) {
    mostrarAsistente.value = true
    return
  }
  empezar()
}

// Volver al botón de conectar sin cerrar el diálogo ni empezar ningún flujo.
function cancelarAsistente() {
  mostrarAsistente.value = false
  error.value = null
}

// El servidor sondea a Twitch (o espera el redirect de Kick); el panel solo pregunta cada
// 3 s por el estado. Cerrar el diálogo no cancela nada: la persona puede estar autorizando
// desde el móvil.
//
// `credenciales` es lo que emite el asistente ({client_id, client_secret}): se manda tal
// cual en el cuerpo y no se guarda en ningún ref propio de este componente más allá de
// esta llamada.
async function empezar(credenciales) {
  error.value = null
  mostrarAsistente.value = false
  try {
    const cuerpo = credenciales ? { ...credenciales, origin: location.origin } : undefined
    inicio.value = await api.iniciarAuth(props.plataforma, cuerpo)
    estado.value = 'pending'
    sondear()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : t('conectar.error_iniciar')
  }
}

async function sondear() {
  try {
    const s = await api.estadoAuth(props.plataforma, inicio.value.state)
    estado.value = s.status
    if (s.status === 'done') { emit('conectada', s.account); return }
    if (s.status === 'pending') { temporizador = setTimeout(sondear, 3000); return }
    error.value = s.message || t('conectar.error_completar')
  } catch (e) {
    // Un 404 aquí es que el servidor se reinició a mitad: se empieza de nuevo.
    estado.value = 'expired'
    error.value = t('conectar.error_interrumpida')
  }
}

onUnmounted(() => clearTimeout(temporizador))
</script>

<template>
  <div class="q-gutter-y-sm">
    <q-btn v-if="!mostrarAsistente && (estado === 'idle' || estado === 'expired' || estado === 'error')"
           unelevated no-caps color="primary"
           :icon="iVincular" :label="t('conectar.conectar_cuenta', { nombre: nombre || plataforma })" @click="empezarClick" />

    <template v-if="mostrarAsistente">
      <AsistenteCredenciales
        :plataforma="plataforma"
        :origin="origen"
        @listo="empezar"
      />
      <!-- Cancelar cierra SOLO el asistente: el diálogo que envuelve a este componente
           sigue abierto, que es lo que espera quien se arrepiente a mitad de pegar las
           credenciales y quiere volver al botón de conectar. -->
      <div>
        <q-btn flat no-caps :label="t('comun.cancelar')" @click="cancelarAsistente" />
      </div>
    </template>

    <!-- Kick: vuelve por el navegador. Un enlace de verdad, nunca un window.open a mano: el
         segundo lo bloquea cualquier bloqueador de ventanas emergentes. -->
    <div v-if="estado === 'pending' && inicio?.redirect_url" class="codigo-dispositivo">
      <p class="ss-t-14">{{ t('conectar.autoriza_pestana') }}</p>
      <q-btn
        unelevated
        no-caps
        color="primary"
        :href="inicio.redirect_url"
        target="_blank"
        rel="noopener noreferrer"
        :label="t('conectar.abrir_para_autorizar', { nombre: nombre || plataforma })"
      />
      <p class="ss-t-14 ss-muted q-mt-sm" role="status" aria-live="polite"><q-spinner size="14px" class="q-mr-xs" />{{ t('conectar.esperando') }}</p>
    </div>

    <div v-else-if="estado === 'pending' && inicio" class="codigo-dispositivo">
      <p class="ss-t-14">{{ t('conectar.abre_pre') }} <a :href="inicio.verification_uri" target="_blank" rel="noopener">{{ inicio.verification_uri.replace(/^https:\/\//, '') }}</a> {{ t('conectar.abre_post') }}</p>
      <div class="caja-codigo ss-surface-2">
        <div class="codigo ss-t-28 ss-mono ss-tabular">{{ inicio.user_code }}</div>
      </div>
      <p class="ss-t-14 ss-muted" role="status" aria-live="polite"><q-spinner size="14px" class="q-mr-xs" />{{ t('conectar.esperando_codigo', { mins: Math.round(inicio.expires_in / 60) }) }}</p>
    </div>
    <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">{{ error }}</q-banner>
  </div>
</template>

<style scoped>
.caja-codigo {
  padding: var(--ss-space-4) var(--ss-space-5);
  border: 1px solid var(--ss-border);
  text-align: center;
  margin: var(--ss-space-3) 0;
}
.codigo {
  letter-spacing: 0.08em;
  user-select: all;
}
</style>
