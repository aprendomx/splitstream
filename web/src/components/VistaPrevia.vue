<script setup>
import { onBeforeUnmount, ref } from 'vue'
import { t } from '@/i18n'
import { iVer, iCerrar } from '@/iconos'

// La vista previa silenciada: solo vídeo, decodificado con WebCodecs y pintado en un
// canvas. Sin librerías — VideoDecoder y canvas son APIs del navegador, y el servidor
// manda los NALUs H.264 tal cual salen de OBS.
//
// Todo el ciclo de vida vive aquí: abrir el WebSocket, decodificar, pintar y limpiar. El
// padre solo decide cuándo existe el componente (v-if) y escucha 'cerrar'.
const emit = defineEmits(['cerrar'])

const lienzo = ref(null)
// Se guarda la CLAVE, no el texto ya traducido: así, si el idioma cambia mientras se
// enseña, la plantilla lo resuelve de nuevo con t() en vez de quedarse con el texto viejo.
const avisoKey = ref('vista_previa.conectando')

let ws = null
let decoder = null
// Tras (re)configurar, no se le da nada al decodificador hasta el primer keyframe: un
// delta sin su pasado es un error de decodificación seguro.
let esperandoKeyframe = true
let cerrado = false
// Generación de la config en curso: crece en cada llamada a configurar() y permite que un
// await que resuelve tarde (o fuera de orden frente a una config más nueva) se descarte
// sin tocar el decodificador actual.
let generacion = 0

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
  avisoKey.value = null
}

async function configurar(avcc) {
  // Renegociación (spec §7): el decodificador viejo se cierra AQUÍ, antes de cualquier
  // await, no después de isConfigSupported. Si se cerrara después, el keyframe de la
  // época nueva —que el servidor manda pegado a la config— podría llegarle al
  // decodificador viejo mientras el await sigue en vuelo y reventarlo con un error de
  // decodificación en vez de recuperarse. Los frames que lleguen mientras tanto se
  // descartan solos en decodificar() (decoder es null). La generación, además, evita que
  // dos configs seguidas resuelvan al revés: si esta ya no es la más reciente cuando
  // vuelve el await, no toca el decodificador actual.
  const mia = ++generacion
  if (decoder && decoder.state !== 'closed') decoder.close()
  decoder = null
  esperandoKeyframe = true

  // El string de códec sale del propio avcC: perfil, flags de compatibilidad y nivel
  // son sus bytes 1 a 3. El servidor no parsea nada a propósito.
  const codec = 'avc1.' + [...avcc.subarray(1, 4)]
    .map((b) => b.toString(16).padStart(2, '0')).join('')
  const config = { codec, description: avcc, optimizeForLatency: true }

  if (typeof VideoDecoder === 'undefined') {
    cerrar(t('vista_previa.sin_soporte'))
    return
  }
  const soporte = await VideoDecoder.isConfigSupported(config).catch(() => null)
  if (cerrado || mia !== generacion) return
  if (!soporte?.supported) {
    cerrar(t('vista_previa.sin_soporte'))
    return
  }

  decoder = new VideoDecoder({
    output: pintar,
    error: () => cerrar(t('vista_previa.fallo_decodificar')),
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
  // ev.reason viene ya en el idioma que se pidió en el handshake (el servidor lo negocia
  // con Accept-Language antes de aceptar el WebSocket), así que se enseña tal cual. El
  // respaldo de aquí abajo, para un cierre sin motivo, sí pasa por t().
  ws.onclose = (ev) => { ws = null; cerrar(ev.reason || t('vista_previa.se_corto')) }
}

conectar()
onBeforeUnmount(() => cerrar())
</script>

<template>
  <q-card flat bordered class="q-mb-md">
    <!-- Cabecera: el botón «Vista previa» del panel no lleva icono propio hoy, así que se
         usa uno que representa lo mismo (ver/observar) en vez de inventar un nombre que
         iconos.js no exporta. -->
    <div class="cabecera-tarjeta row items-center no-wrap">
      <q-icon :name="iVer" size="20px" class="q-mr-sm" aria-hidden="true" />
      <span class="ss-t-16 titulo-texto">{{ t('vista_previa.encabezado') }}</span>
      <q-space />
      <q-btn flat round dense :icon="iCerrar" class="cerrar-btn" :aria-label="t('comun.cerrar')" @click="cerrar()" />
    </div>
    <!-- El recuadro reserva 16:9 desde el primer render, antes de que llegue ningún
         fotograma: así no salta el layout cuando aparece el vídeo. -->
    <div class="cuadro">
      <canvas ref="lienzo" class="lienzo" />
      <q-skeleton v-if="avisoKey" type="rect" class="esqueleto" aria-busy="true" />
      <div v-if="avisoKey" class="aviso ss-t-14 ss-muted">{{ t(avisoKey) }}</div>
    </div>
  </q-card>
</template>

<style scoped>
/* Patrón de cabecera de tarjeta (spec v1.1 §3.3), repetido a propósito en cada componente. */
.cabecera-tarjeta {
  padding: var(--ss-space-3) var(--ss-space-4);
  border-bottom: 1px solid var(--ss-border);
}
.titulo-texto { font-weight: 600; }
.cerrar-btn { width: 44px; height: 44px; }
.cuadro {
  position: relative;
  aspect-ratio: 16 / 9;
  /* Negro intencional: es el fondo de "sin señal" de cualquier pantalla de vídeo, y aquí
     además reserva el hueco 16:9 completo mientras no hay fotograma que pintar. Es el
     único color literal permitido en este componente. */
  background: #000;
}
.lienzo { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; display: block; }
.esqueleto { position: absolute; inset: 0; width: 100%; height: 100%; }
.aviso {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: var(--ss-space-4);
}
</style>
