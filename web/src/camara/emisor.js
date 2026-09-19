import { CODEC_VIDEO, CODEC_VIDEO_1080, CODEC_AUDIO } from './soporte'

// Emisor: captura → WebCodecs → WebSocket /api/camera/ws (spec cámara §3 y §5).
//
// Un solo reloj para audio y vídeo: performance.now() al pulsar «Emitir» es el cero, y
// cada frame y cada bloque de audio llevan microsegundos desde ahí; al enviar se pasan a
// milisegundos, que es lo que RTMP usa. El servidor no corrige nada, igual que con OBS.
export const CALIDADES = {
  '720p': { width: 1280, height: 720, videoBitrate: 2_500_000, codec: CODEC_VIDEO },
  '1080p': { width: 1920, height: 1080, videoBitrate: 4_500_000, codec: CODEC_VIDEO_1080 },
}
const FPS = 30
const KEYFRAME_US = 2_000_000 // un keyframe cada 2 s: las plataformas piden GOP ≤ 4 s
// El nodo del worklet se fuerza a dos canales (channelCountMode: 'explicit'), así que hoy
// siempre se usa la entrada 2: la de 1 se conserva por si vuelve el mono.
const AUDIO_BITRATE = { 1: 96_000, 2: 128_000 }
const COLA_MAX = 2 // frames pendientes en el codificador antes de descartar la captura

const MSG_START = 0x00
const MSG_VIDEO_CONFIG = 0x01
const MSG_VIDEO_FRAME = 0x02
const MSG_AUDIO_CONFIG = 0x03
const MSG_AUDIO_FRAME = 0x04

function bytesDe(d) {
  return ArrayBuffer.isView(d) ? new Uint8Array(d.buffer, d.byteOffset, d.byteLength) : new Uint8Array(d)
}

export class Emisor {
  constructor({ video, stream, calidad, onFin }) {
    this.video = video
    this.stream = stream
    this.calidad = CALIDADES[calidad] ?? CALIDADES['720p']
    this.onFin = onFin
    this.ws = null
    // Rechazo pendiente del handshake en curso (si lo hay): limpiar() lo usa para no dejar
    // iniciar() colgado si parar() llega mientras se espera al servidor.
    this.rechazarPendiente = null
    this.videoEnc = null
    this.audioEnc = null
    this.audioCtx = null
    this.origen = 0
    this.ultimoKey = -KEYFRAME_US
    this.forzarKey = false
    // Control de subida (spec §5): por encima de 1 s de bitrate en el buffer del socket
    // se descartan deltas hasta el siguiente keyframe; por encima de 5 s sostenidos se
    // para. El audio nunca se descarta: es pequeño y su hueco se nota más.
    this.umbral1s = this.calidad.videoBitrate / 8
    this.umbral5s = this.umbral1s * 5
    this.descartando = false
    this.terminado = false
    this.rvfc = 0
    this.silencio = null
    // Tamaño con el que se configuró el codificador; se compara con el de cada fotograma.
    this.ancho = 0
    this.alto = 0
    // true mientras ancho/alto sean los nominales de la calidad porque el <video> todavía
    // no tenía metadatos al arrancar: el primer fotograma real dirá el tamaño de verdad.
    this.tamanoNominal = false
  }

  async iniciar() {
    // Instancia de un solo uso: una vez terminada (por parar() o por un fallo) no arranca de nuevo.
    if (this.terminado) throw new Error('')
    try {
      await this.arrancar()
    } catch (e) {
      // Cualquier fallo antes de emitir deja todo como estaba: sin AudioContext abierto (Chrome
      // limita cuántos puede tener una página), sin micrófono retenido y sin socket huérfano.
      this.limpiar()
      throw e
    }
  }

  async arrancar() {
    // 1. Audio primero: hace falta saber cuántos canales entrega el micrófono antes de
    // declarar el start, y eso solo lo dice el primer bloque del worklet.
    const canales = await this.prepararAudio()
    if (this.terminado) throw new Error('')
    const sampleRate = this.audioCtx.sampleRate

    // La cámara no siempre entrega el tamaño pedido (una webcam 4:3 da 960×720 aunque se
    // pida 720p): el codificador y el onMetaData declaran lo que de verdad llega.
    const width = (this.video.videoWidth || this.calidad.width) & ~1
    const height = (this.video.videoHeight || this.calidad.height) & ~1
    // Si el <video> aún no había cargado metadatos, lo de arriba son los nominales de la
    // calidad, no lo que entrega la cámara: queda anotado para adoptarlo al primer frame.
    this.tamanoNominal = !this.video.videoWidth || !this.video.videoHeight

    // 2. WebSocket y start.
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    this.ws = new WebSocket(`${proto}://${location.host}/api/camera/ws`)
    this.ws.binaryType = 'arraybuffer'
    await new Promise((resolver, rechazar) => {
      this.rechazarPendiente = rechazar
      this.ws.onopen = resolver
      this.ws.onerror = () => rechazar(new Error('ws'))
      this.ws.onclose = (ev) => rechazar(new Error(ev.reason || 'ws'))
    })
    this.rechazarPendiente = null
    if (this.terminado) throw new Error('')
    const start = {
      width, height, framerate: FPS,
      video_bitrate: this.calidad.videoBitrate, audio_bitrate: AUDIO_BITRATE[canales] ?? 128_000,
      sample_rate: sampleRate, channels: canales,
    }
    this.ws.send(this.mensaje(MSG_START, new TextEncoder().encode(JSON.stringify(start))))
    await new Promise((resolver, rechazar) => {
      this.rechazarPendiente = rechazar
      this.ws.onmessage = (ev) => { if (typeof ev.data === 'string') resolver() }
      this.ws.onclose = (ev) => rechazar(new Error(ev.reason || 'ws'))
    })
    this.rechazarPendiente = null
    if (this.terminado) throw new Error('')
    // A partir de aquí el servidor solo habla para cerrar, y ese motivo es para el usuario.
    this.ws.onmessage = null
    this.ws.onclose = (ev) => this.terminar(ev.reason || '')

    // 3. Codificadores y captura.
    this.origen = performance.now()
    this.ancho = width
    this.alto = height
    this.configurarVideo(width, height)
    this.configurarAudio(canales, sampleRate)
    this.capturarVideo()
  }

  async prepararAudio() {
    this.audioCtx = new AudioContext({ sampleRate: 48000 })
    await this.audioCtx.audioWorklet.addModule(new URL('./worklet-audio.js', import.meta.url))
    const fuente = this.audioCtx.createMediaStreamSource(this.stream)
    this.nodo = new AudioWorkletNode(this.audioCtx, 'acumulador', {
      numberOfInputs: 1, numberOfOutputs: 1, outputChannelCount: [1],
      channelCount: 2, channelCountMode: 'explicit', channelInterpretation: 'speakers',
    })
    fuente.connect(this.nodo)
    // El grafo de Web Audio solo procesa lo que cuelga del destino: un nodo sin salida puede
    // no ejecutarse nunca (WebKit). Se conecta a través de una ganancia a cero: el
    // procesador no escribe en su salida, así que no suena nada.
    this.silencio = new GainNode(this.audioCtx, { gain: 0 })
    this.nodo.connect(this.silencio).connect(this.audioCtx.destination)
    // Safari arranca el AudioContext suspendido hasta un gesto; el gesto fue pulsar
    // «Emitir», así que resume() aquí funciona.
    await this.audioCtx.resume()
    return new Promise((resolver, rechazar) => {
      // Sin bloque en 3 s no hay audio que emitir: micrófono muerto, pista sin datos o
      // worklet que el navegador no llegó a ejecutar. Colgarse aquí dejaría «Emitir» sin salida.
      const plazo = setTimeout(() => rechazar(new Error('sin_audio')), 3000)
      this.nodo.port.onmessage = ({ data }) => {
        clearTimeout(plazo)
        // Hasta que haya codificador (después del start) los bloques se tiran.
        if (this.audioEnc) this.codificarAudio(data)
        resolver(data.canales)
      }
    })
  }

  // Las opciones del codificador en un solo sitio: la configuración inicial y la
  // reconfiguración al adoptar el tamaño real tienen que ser idénticas salvo el tamaño.
  configVideo(width, height) {
    return {
      codec: this.calidad.codec, width, height, bitrate: this.calidad.videoBitrate, framerate: FPS,
      avc: { format: 'avc' }, latencyMode: 'realtime',
    }
  }

  configurarVideo(width, height) {
    this.videoEnc = new VideoEncoder({
      output: (chunk, meta) => {
        if (meta?.decoderConfig?.description) {
          this.enviar(this.mensaje(MSG_VIDEO_CONFIG, bytesDe(meta.decoderConfig.description)), false)
        }
        const esKey = chunk.type === 'key'
        const buf = new Uint8Array(6 + chunk.byteLength)
        buf[0] = MSG_VIDEO_FRAME
        buf[1] = esKey ? 0x01 : 0x00
        new DataView(buf.buffer).setUint32(2, Math.round(chunk.timestamp / 1000))
        chunk.copyTo(buf.subarray(6))
        this.enviar(buf, true, esKey)
      },
      error: () => this.terminar('fallo_codificar'),
    })
    this.videoEnc.configure(this.configVideo(width, height))
  }

  configurarAudio(canales, sampleRate) {
    this.muestras = 0
    this.audioBase = null
    this.audioEnc = new AudioEncoder({
      output: (chunk, meta) => {
        if (meta?.decoderConfig?.description) {
          this.enviar(this.mensaje(MSG_AUDIO_CONFIG, bytesDe(meta.decoderConfig.description)), false)
        }
        const buf = new Uint8Array(5 + chunk.byteLength)
        buf[0] = MSG_AUDIO_FRAME
        new DataView(buf.buffer).setUint32(1, Math.round(chunk.timestamp / 1000))
        chunk.copyTo(buf.subarray(5))
        this.enviar(buf, false)
      },
      error: () => this.terminar('fallo_codificar'),
    })
    this.audioEnc.configure({ codec: CODEC_AUDIO, sampleRate, numberOfChannels: canales, bitrate: AUDIO_BITRATE[canales] ?? 128_000 })
  }

  codificarAudio({ planar, canales, frames }) {
    if (!this.audioEnc || this.audioEnc.state !== 'configured') return
    // El primer bloque ancla el reloj de audio al común; los siguientes se cuentan por
    // muestras, que es más estable que la hora de llegada de cada mensaje del worklet.
    if (this.audioBase === null) this.audioBase = (performance.now() - this.origen) * 1000
    const timestamp = Math.round(this.audioBase + this.muestras * 1e6 / this.audioCtx.sampleRate)
    this.muestras += frames
    const datos = new AudioData({
      format: 'f32-planar', sampleRate: this.audioCtx.sampleRate, numberOfFrames: frames,
      numberOfChannels: canales, timestamp, data: planar,
    })
    try {
      this.audioEnc.encode(datos)
    } finally {
      datos.close()
    }
  }

  // Un VideoFrame por cada fotograma que pinta el <video>. Es el camino que funciona en
  // Chrome, Safari y Firefox; MediaStreamTrackProcessor no está en Window en Safari.
  capturarVideo() {
    const paso = () => {
      if (this.terminado) return
      this.rvfc = this.video.requestVideoFrameCallback(paso)
      if (!this.videoEnc || this.videoEnc.state !== 'configured') return
      // Si el codificador va por detrás, se salta este fotograma: encolar más solo
      // añade latencia y acaba en un error de memoria en móviles.
      if (this.videoEnc.encodeQueueSize > COLA_MAX) return
      // El tamaño se compara redondeado a par, igual que se guardó: una captura impar
      // (1281 de ancho) no es un cambio de dispositivo, es el mismo fotograma de siempre.
      const w = this.video.videoWidth & ~1
      const h = this.video.videoHeight & ~1
      if (this.video.videoWidth && this.video.videoHeight && (w !== this.ancho || h !== this.alto)) {
        if (this.tamanoNominal) {
          // Arrancamos con el tamaño nominal de la calidad porque el <video> no tenía
          // metadatos: este primer fotograma es el que dice el real (una webcam 4:3 da
          // 960×720 aunque se pida 720p). No es motivo para cortar; lo correcto es adoptarlo
          // reconfigurando el codificador, que WebCodecs permite en caliente. El siguiente
          // fotograma tiene que ser clave: el avcC cambia y un delta contra el anterior no
          // vale. El onMetaData ya declaró el nominal, pero es declarativo y las plataformas
          // leen el tamaño real del SPS (spec base §3.8), así que el servidor no toca nada.
          this.videoEnc.configure(this.configVideo(w, h))
          this.forzarKey = true
          this.ancho = w
          this.alto = h
          this.tamanoNominal = false
        } else {
          // Girar el teléfono o cambiar de cámara sí cambia el tamaño de la captura, y el
          // codificador quedó configurado con el anterior: WebCodecs escalaría en silencio en
          // vez de fallar, y la emisión seguiría con la imagen deformada. El spec §6 y los
          // manuales prometen una parada limpia con motivo, así que se para.
          this.terminar('parada_dispositivo')
          return
        }
      }
      const timestamp = Math.round((performance.now() - this.origen) * 1000)
      const keyFrame = this.forzarKey || timestamp - this.ultimoKey >= KEYFRAME_US
      if (keyFrame) { this.ultimoKey = timestamp; this.forzarKey = false }
      let frame = null
      try {
        frame = new VideoFrame(this.video, { timestamp })
        this.videoEnc.encode(frame, { keyFrame })
      } catch {
        this.terminar('fallo_codificar')
        return
      } finally {
        frame?.close()
      }
    }
    this.rvfc = this.video.requestVideoFrameCallback(paso)
  }

  mensaje(tipo, cuerpo) {
    const buf = new Uint8Array(1 + cuerpo.byteLength)
    buf[0] = tipo
    buf.set(cuerpo, 1)
    return buf
  }

  enviar(buf, esVideo, esKey = false) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
    const pendiente = this.ws.bufferedAmount
    if (pendiente > this.umbral5s) {
      this.terminar('subida_insuficiente')
      return
    }
    if (esVideo) {
      if (pendiente > this.umbral1s && !esKey) {
        // Se descarta hasta el siguiente keyframe, que se fuerza: un delta sin su pasado
        // es un GOP roto en la plataforma.
        this.descartando = true
        this.forzarKey = true
        return
      }
      if (esKey) this.descartando = false
      if (this.descartando) return
    }
    this.ws.send(buf)
  }

  // Parar por decisión del usuario: sin motivo, y sin onFin.
  parar() {
    this.limpiar()
  }

  // Terminar por cualquier otra causa: el motivo va a la página. Los motivos propios
  // viajan como clave corta (la página los traduce); los del servidor ya vienen en el
  // idioma del panel, negociado en el handshake.
  terminar(motivo) {
    if (this.terminado) return
    this.limpiar()
    this.onFin?.(motivo)
  }

  limpiar() {
    if (this.terminado) return
    this.terminado = true
    if (this.rvfc) this.video.cancelVideoFrameCallback(this.rvfc)
    // Se cierra sin flush() a propósito: parar tiene que ser inmediato, y flush() esperaría
    // a que se vacíe la cola del codificador. Lo que se pierde son los últimos fotogramas,
    // que ya nadie va a ver.
    if (this.videoEnc && this.videoEnc.state !== 'closed') this.videoEnc.close()
    if (this.audioEnc && this.audioEnc.state !== 'closed') this.audioEnc.close()
    this.videoEnc = this.audioEnc = null
    if (this.nodo) { this.nodo.port.onmessage = null; this.nodo.disconnect() }
    if (this.silencio) this.silencio.disconnect()
    if (this.audioCtx) this.audioCtx.close().catch(() => {})
    // Si limpiar() llega durante el handshake, quita ws.onclose antes de cerrar: sin esto,
    // el Promise pendiente de iniciar() nunca se resolvería ni se rechazaría.
    if (this.rechazarPendiente) { this.rechazarPendiente(new Error('')); this.rechazarPendiente = null }
    // Las pistas de captura NO se paran aquí: son de la página, que las abrió y sigue
    // enseñando la vista previa local después de parar la emisión.
    if (this.ws) {
      this.ws.onclose = null
      if (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING) this.ws.close(1000)
      this.ws = null
    }
  }
}
