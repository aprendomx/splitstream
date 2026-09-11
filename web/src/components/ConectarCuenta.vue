<script setup>
import { ref, onUnmounted } from 'vue'
import { api, ApiError } from '@/api'
import { iVincular } from '@/iconos'

const props = defineProps({ plataforma: { type: String, required: true }, nombre: { type: String, default: '' } })
const emit = defineEmits(['conectada'])

const inicio = ref(null)   // {state, verification_uri, user_code, expires_in}
const estado = ref('idle') // idle | pending | done | expired | error
const error = ref(null)
let temporizador = null

// El servidor sondea a Twitch; el panel solo pregunta cada 3 s por el estado. Cerrar el
// diálogo no cancela nada: la persona puede estar autorizando desde el móvil.
async function empezar() {
  error.value = null
  try {
    inicio.value = await api.iniciarAuth(props.plataforma)
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
    <q-btn v-if="estado === 'idle' || estado === 'expired' || estado === 'error'" unelevated no-caps color="primary"
           :icon="iVincular" :label="`Conectar cuenta de ${nombre || plataforma}`" @click="empezar" />
    <div v-if="estado === 'pending' && inicio" class="codigo-dispositivo q-pa-md rounded-borders">
      <div class="text-body2">Abre <a :href="inicio.verification_uri" target="_blank" rel="noopener">{{ inicio.verification_uri.replace(/^https:\/\//, '') }}</a> y escribe este código:</div>
      <div class="codigo text-h4 q-my-sm">{{ inicio.user_code }}</div>
      <div class="text-caption text-grey-5"><q-spinner size="14px" class="q-mr-xs" />Esperando a que autorices… el código vale {{ Math.round(inicio.expires_in / 60) }} min.</div>
    </div>
    <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">{{ error }}</q-banner>
  </div>
</template>

<style scoped>
.codigo-dispositivo { background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.12); }
.codigo { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; letter-spacing: 0.2em; user-select: all; }
</style>
