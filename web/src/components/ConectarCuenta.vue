<script setup>
import { ref, onUnmounted } from 'vue'
import { api, ApiError } from '@/api'
import { iVincular } from '@/iconos'
import AsistenteCredenciales from '@/components/AsistenteCredenciales.vue'

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
    error.value = e instanceof ApiError ? e.message : 'No se pudo iniciar la conexión'
  }
}

async function sondear() {
  try {
    const s = await api.estadoAuth(props.plataforma, inicio.value.state)
    estado.value = s.status
    if (s.status === 'done') { emit('conectada', s.account); return }
    if (s.status === 'pending') { temporizador = setTimeout(sondear, 3000); return }
    error.value = s.message || 'No se pudo completar la conexión'
  } catch (e) {
    // Un 404 aquí es que el servidor se reinició a mitad: se empieza de nuevo.
    estado.value = 'expired'
    error.value = 'La conexión se interrumpió; vuelve a empezar'
  }
}

onUnmounted(() => clearTimeout(temporizador))
</script>

<template>
  <div class="q-gutter-y-sm">
    <q-btn v-if="!mostrarAsistente && (estado === 'idle' || estado === 'expired' || estado === 'error')"
           unelevated no-caps color="primary"
           :icon="iVincular" :label="`Conectar cuenta de ${nombre || plataforma}`" @click="empezarClick" />

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
        <a href="#" class="text-caption enlace-cancelar" @click.prevent="cancelarAsistente">Cancelar</a>
      </div>
    </template>

    <!-- Kick: vuelve por el navegador. Un enlace de verdad, nunca un window.open a mano: el
         segundo lo bloquea cualquier bloqueador de ventanas emergentes. -->
    <div v-if="estado === 'pending' && inicio?.redirect_url" class="codigo-dispositivo q-pa-md rounded-borders">
      <div class="text-body2 q-mb-sm">Autoriza en una pestaña nueva:</div>
      <q-btn
        unelevated
        no-caps
        color="primary"
        :href="inicio.redirect_url"
        target="_blank"
        rel="noopener noreferrer"
        :label="`Abrir ${nombre || plataforma} para autorizar`"
      />
      <div class="text-caption text-grey-5 q-mt-sm"><q-spinner size="14px" class="q-mr-xs" />Esperando a que autorices…</div>
    </div>

    <div v-else-if="estado === 'pending' && inicio" class="codigo-dispositivo q-pa-md rounded-borders">
      <div class="text-body2">Abre <a :href="inicio.verification_uri" target="_blank" rel="noopener">{{ inicio.verification_uri.replace(/^https:\/\//, '') }}</a> y escribe este código:</div>
      <div class="codigo text-h4 q-my-sm">{{ inicio.user_code }}</div>
      <div class="text-caption text-grey-5"><q-spinner size="14px" class="q-mr-xs" />Esperando a que autorices… el código vale {{ Math.round(inicio.expires_in / 60) }} min.</div>
    </div>
    <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">{{ error }}</q-banner>
  </div>
</template>

<style scoped>
.codigo-dispositivo { background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.12); }
.enlace-cancelar { color: rgba(255,255,255,0.7); text-decoration: underline; }
.codigo { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: 0.2em; user-select: all; }
</style>
