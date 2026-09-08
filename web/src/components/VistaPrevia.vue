<script setup>
import { onBeforeUnmount, ref } from 'vue'

// La vista previa silenciada: solo vídeo, decodificado con WebCodecs y pintado en un
// canvas. Sin librerías — VideoDecoder y canvas son APIs del navegador, y el servidor
// manda los NALUs H.264 tal cual salen de OBS.
//
// Todo el ciclo de vida vive aquí: abrir el WebSocket, decodificar, pintar y limpiar. El
// padre solo decide cuándo existe el componente (v-if) y escucha 'cerrar'.
const emit = defineEmits(['cerrar'])

const lienzo = ref(null)
const aviso = ref('Conectando…')

let ws = null
let decoder = null
// Tras (re)configurar, no se le da nada al decodificador hasta el primer keyframe: un
// delta sin su pasado es un error de decodificación seguro.
let esperandoKeyframe = true
let cerrado = false

function cerrar(motivo = null) {
  if (cerrado) return
  cerrado = true
  if (ws) { ws.onclose = null; ws.close(); ws = null }
  if (decoder && decoder.state !== 'closed') decoder.close()
  decoder = null
  emit('cerrar', motivo)
}

function pintar(frame) {
  const canvas = lienzo.value
  if (!canvas) { frame.close(); return }
  if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
    canvas.width = frame.displayWidth
    canvas.height = frame.displayHeight
  }
  canvas.getContext('2d').drawImage(frame, 0, 0)
  // Obligatorio: cada VideoFrame retiene memoria de GPU hasta que se cierra.
  frame.close()
  aviso.value = null
}

async function configurar(avcc) {
  // El string de códec sale del propio avcC: perfil, flags de compatibilidad y nivel
  // son sus bytes 1 a 3. El servidor no parsea nada a propósito.
  const codec = 'avc1.' + [...avcc.subarray(1, 4)]
    .map((b) => b.toString(16).padStart(2, '0')).join('')
  const config = { codec, description: avcc, optimizeForLatency: true }

  if (typeof VideoDecoder === 'undefined') {
    cerrar('Tu navegador no soporta la vista previa')
    return
  }
  const soporte = await VideoDecoder.isConfigSupported(config).catch(() => null)
  if (!soporte?.supported) {
    cerrar('Tu navegador no soporta la vista previa')
    return
  }
  if (cerrado) return

  // Una config a mitad significa que el publisher renegoció: el decodificador anterior
  // ya no vale y el que viene arranca en el siguiente keyframe.
  if (decoder && decoder.state !== 'closed') decoder.close()
  decoder = new VideoDecoder({
    output: pintar,
    error: () => cerrar('La vista previa falló al decodificar'),
  })
  decoder.configure(config)
  esperandoKeyframe = true
}

function decodificar(b) {
  if (!decoder || decoder.state !== 'configured') return
  const esKeyframe = (b[1] & 0x01) === 0x01
  if (esperandoKeyframe && !esKeyframe) return
  esperandoKeyframe = false
  const ts = new DataView(b.buffer, b.byteOffset + 2, 4).getUint32(0)
  decoder.decode(new EncodedVideoChunk({
    type: esKeyframe ? 'key' : 'delta',
    timestamp: ts * 1000, // WebCodecs cuenta en microsegundos; el servidor manda ms
    data: b.subarray(6),
  }))
}

function conectar() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/api/preview/ws`)
  ws.binaryType = 'arraybuffer'
  ws.onmessage = (ev) => {
    const b = new Uint8Array(ev.data)
    if (b.length < 1) return
    if (b[0] === 0x01 && b.length > 4) configurar(b.subarray(1))
    else if (b[0] === 0x02 && b.length > 6) decodificar(b)
  }
  // Sin reconexión, a propósito: la vista es bajo demanda y gasta subida del servidor.
  // El servidor cierra con motivo («sin señal», «la emisión terminó») y ese texto es lo
  // que se le enseña al usuario, que decide si reabrir.
  ws.onclose = (ev) => { ws = null; cerrar(ev.reason || 'La vista previa se cortó') }
}

conectar()
onBeforeUnmount(() => cerrar())
</script>

<template>
  <q-card flat bordered class="q-mb-md">
    <q-card-section class="row items-center q-py-xs">
      <div class="text-caption text-grey-5">Vista previa · sin sonido</div>
      <q-space />
      <q-btn flat dense no-caps size="sm" label="Cerrar" @click="cerrar()" />
    </q-card-section>
    <q-separator />
    <q-card-section class="q-pa-none cuadro">
      <canvas ref="lienzo" class="lienzo" />
      <div v-if="aviso" class="text-caption text-grey-5 q-pa-md">{{ aviso }}</div>
    </q-card-section>
  </q-card>
</template>

<style scoped>
.cuadro { background: #000; text-align: center; }
.lienzo { max-width: 100%; height: auto; display: block; margin: 0 auto; }
</style>
