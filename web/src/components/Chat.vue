<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { iCerrar } from '@/iconos'

const emit = defineEmits(['cerrar'])
const mensajes = ref([])
const pestaña = ref('todos')
const lista = ref(null)
let ws = null
let reintento = 0
let temporizador = null
const TOPE = 500

const plataformas = computed(() => [...new Set(mensajes.value.map((m) => m.platform))])
const visibles = computed(() => (pestaña.value === 'todos' ? mensajes.value : mensajes.value.filter((m) => m.platform === pestaña.value)))

function conectar() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/api/chat/ws`)
  ws.onmessage = async (ev) => {
    try {
      mensajes.value.push(JSON.parse(ev.data))
      if (mensajes.value.length > TOPE) mensajes.value.splice(0, mensajes.value.length - TOPE)
      reintento = 0
      await nextTick()
      // Pegado al final, como cualquier chat; si la persona subió a leer, no se la mueve.
      const el = lista.value
      if (el && el.scrollHeight - el.scrollTop - el.clientHeight < 80) el.scrollTop = el.scrollHeight
    } catch { /* un mensaje ilegible no tira el chat */ }
  }
  ws.onclose = () => {
    ws = null
    const espera = Math.min(1000 * 2 ** reintento, 30000)
    reintento++
    temporizador = setTimeout(conectar, espera)
  }
  ws.onerror = () => ws?.close()
}

onMounted(conectar)
onUnmounted(() => { clearTimeout(temporizador); if (ws) { ws.onclose = null; ws.close() } })
</script>

<template>
  <q-card flat bordered class="chat column no-wrap q-mb-md">
    <div class="row items-center q-px-sm q-pt-xs">
      <q-tabs v-model="pestaña" dense no-caps class="col">
        <q-tab name="todos" label="Todos" />
        <q-tab v-for="p in plataformas" :key="p" :name="p" :label="p" />
      </q-tabs>
      <q-btn flat round dense :icon="iCerrar" aria-label="Cerrar el chat" @click="emit('cerrar')" />
    </div>
    <div ref="lista" class="col scroll mensajes q-px-sm q-pb-sm" aria-live="polite">
      <div v-if="!visibles.length" class="text-caption text-grey-6 q-pa-md text-center">Aquí aparecerá el chat cuando llegue.</div>
      <div v-for="(m, i) in visibles" :key="m.message_id || i" class="mensaje">
        <span class="autor" :style="{ color: m.color || 'inherit' }">{{ m.author }}</span>
        <span v-if="m.badges?.some((b) => b.startsWith('moderator'))" class="insignia">mod</span>:
        <span class="texto">{{ m.text }}</span>
      </div>
    </div>
  </q-card>
</template>

<style scoped>
.chat { height: 360px; }
.mensajes { font-size: 13px; line-height: 1.4; overflow-anchor: none; }
.mensaje { padding: 2px 0; word-break: break-word; }
.autor { font-weight: 600; }
.insignia { font-size: 10px; padding: 0 4px; margin-left: 4px; border-radius: 3px; background: rgba(255,255,255,0.12); vertical-align: middle; }
</style>
