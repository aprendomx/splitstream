<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useQuasar } from 'quasar'
import { iCerrar, iChat, iAnterior, iSiguiente } from '@/iconos'
import { usePanel } from '@/stores/panel'
import { api } from '@/api'
import { t, formatearNumero } from '@/i18n'
import { nombrePorId, porId } from '@/plataformas'

const panel = usePanel()
const $q = useQuasar()
// Con `sesionId` el chat es de lectura: enseña lo que quedó guardado de esa sesión en vez
// de abrir el WebSocket del chat en vivo. Es el mismo render y las mismas pestañas; lo que
// cambia es de dónde salen los mensajes y que no hay nada que cerrar ni cuota que gastar.
const props = defineProps({ sesionId: { type: Number, default: 0 } })
const lectura = computed(() => props.sesionId > 0)
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

// Presupuesto de cuota del chat de YouTube (spec §6/§7.1): viene del arranque del panel,
// no de /api/platforms, porque es un límite de esta instalación y no de la plataforma.
const presupuesto = computed(() => panel.estado?.panel?.youtube_chat_budget ?? 0)
// La cuota diaria del proyecto de Google (SPLITSTREAM_YOUTUBE_QUOTA): el presupuesto del
// chat es solo una parte de ella, así que enseñarla al lado explica cuánto margen queda
// para lo demás (crear emisiones, leer el canal). 0: no se enseña.
const cuotaDiaria = computed(() => panel.estado?.panel?.youtube_quota ?? 0)
// Solo se cuenta si hay una cuenta de YouTube con cifra (null = «no aplica o no se sabe»).
const cuentaConCuota = computed(
  () => panel.cuentas.find((c) => c.platform === 'youtube' && c.quota_used_today != null) ?? null,
)
const mostrarCuota = computed(() => presupuesto.value > 0 && cuentaConCuota.value !== null)
const fraccionCuota = computed(() => {
  if (!mostrarCuota.value) return 0
  return Math.min(1, cuentaConCuota.value.quota_used_today / presupuesto.value)
})

// Página del histórico. El endpoint devuelve los mensajes en orden ascendente a partir de
// `after`, así que «cargar más» pide desde el último id que ya se tiene y los añade al
// final: se lee de arriba abajo, como se vivió.
const PAGINA_CHAT = 200
const cargandoHistorial = ref(false)
const hayMasHistorial = ref(false)

async function cargarHistorial() {
  cargandoHistorial.value = true
  try {
    const desde = mensajes.value.at(-1)?.id ?? 0
    const nuevos = await api.chatSesion(props.sesionId, desde, PAGINA_CHAT)
    mensajes.value = [...mensajes.value, ...nuevos]
    hayMasHistorial.value = nuevos.length === PAGINA_CHAT
  } catch (e) {
    $q.notify({ type: 'negative', message: e.message })
  } finally {
    cargandoHistorial.value = false
  }
}

function conectar() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/api/chat/ws`)
  // Una conexión que abre bien reinicia el backoff aunque el chat esté en silencio.
  ws.onopen = () => { reintento = 0 }
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

// La cuota gastada viaja en /api/accounts, no en el estado que empuja el WebSocket: sin
// refrescarla, la barra se quedaba con la foto del momento en que se abrió el chat y no
// se movía en toda la emisión. Un minuto basta: la cuota se gasta de cinco en cinco
// unidades por sondeo.
const REFRESCO_CUOTA = 60_000
let refresco = null

onMounted(() => {
  if (lectura.value) {
    cargarHistorial()
    return
  }
  conectar()
  panel.cargarCuentas()
  refresco = setInterval(() => panel.cargarCuentas(), REFRESCO_CUOTA)
})
onUnmounted(() => {
  clearTimeout(temporizador)
  clearInterval(refresco)
  if (ws) { ws.onclose = null; ws.close() }
})
</script>

<template>
  <q-card flat bordered class="chat column no-wrap q-mb-md">
    <!-- Cabecera: pestañas por plataforma (la de "todos" incluida) y, en vivo, el cierre. -->
    <div class="cabecera-tarjeta row items-center no-wrap">
      <q-tabs v-model="pestaña" dense no-caps narrow-indicator class="col pestanas"
              :left-icon="iAnterior" :right-icon="iSiguiente">
        <q-tab name="todos" :label="t('chat.todos')" />
        <q-tab v-for="p in plataformas" :key="p" :name="p" :icon="porId(p).icono" :label="nombrePorId(p)" />
      </q-tabs>
      <q-btn v-if="!lectura" flat round dense :icon="iCerrar" class="cerrar-btn" :aria-label="t('chat.cerrar_chat')" @click="emit('cerrar')" />
    </div>
    <div v-if="mostrarCuota && !lectura" class="cuota q-px-sm q-pt-xs">
      <q-linear-progress
        class="barra-cuota"
        :value="fraccionCuota"
        :color="fraccionCuota > 0.8 ? 'warning' : 'primary'"
        size="4px"
        rounded
      />
      <div class="ss-t-12 ss-subtle ss-tabular texto-cuota">
        {{ t('chat.cuota_texto', { usados: formatearNumero(cuentaConCuota.quota_used_today), presupuesto: formatearNumero(presupuesto) }) }}<template v-if="cuotaDiaria > 0"> {{ t('chat.cuota_diaria_texto', { cuota: formatearNumero(cuotaDiaria) }) }}</template>
        · {{ t('chat.cuota_pausa', { presupuesto: formatearNumero(presupuesto) }) }}
      </div>
    </div>
    <div ref="lista" class="col scroll mensajes q-px-sm q-pb-sm" :aria-live="lectura ? 'off' : 'polite'">
      <div v-if="!visibles.length && !cargandoHistorial" class="vacio ss-t-14 ss-muted text-center column items-center q-pa-md">
        <q-icon :name="iChat" size="28px" class="q-mb-sm" aria-hidden="true" />
        <div>{{ lectura ? t('chat.sin_guardado') : t('chat.vacio') }}</div>
      </div>
      <div v-for="(m, i) in visibles" :key="m.message_id || i" class="mensaje ss-t-14">
        <span class="autor" :style="{ color: m.color || 'inherit' }">{{ m.author }}</span>
        <span v-if="m.badges?.some((b) => b.startsWith('moderator'))" class="insignia">{{ t('chat.insignia_mod') }}</span>:
        <span class="texto">{{ m.text }}</span>
      </div>
      <div v-if="lectura && hayMasHistorial" class="text-center q-py-sm">
        <q-btn flat dense no-caps :label="t('chat.cargar_mas')" :loading="cargandoHistorial" @click="cargarHistorial" />
      </div>
    </div>
  </q-card>
</template>

<style scoped>
.chat { height: 360px; }
/* Patrón de cabecera de tarjeta (spec v1.1 §3.3), repetido a propósito en cada componente. */
.cabecera-tarjeta {
  padding: var(--ss-space-3) var(--ss-space-4);
  border-bottom: 1px solid var(--ss-border);
}
/* Las pestañas son nodos internos de Quasar: hace falta :deep() para llegar a su alto. */
.cabecera-tarjeta :deep(.q-tab) { min-height: 44px; }
.cerrar-btn { width: 44px; height: 44px; }
.barra-cuota :deep(.q-linear-progress__track) { background: var(--ss-surface-2); }
.texto-cuota { text-align: right; margin-top: var(--ss-space-1); }
.mensajes { overflow-anchor: none; }
.mensaje { padding: 2px 0; word-break: break-word; }
.autor { font-weight: 600; }
.insignia {
  font-size: 12px;
  font-weight: 500;
  padding: 1px 6px;
  margin-left: 4px;
  border: 1px solid var(--ss-border);
  border-radius: 999px;
  color: var(--ss-fg-muted);
  vertical-align: middle;
}
</style>
