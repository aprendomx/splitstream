// Traduce el estado de un destino a algo que una persona pueda accionar.
//
// Esto existe por lo que pasó el 2026-09-03 probando contra plataformas reales. Un destino
// roto se ve así por dentro:
//
//   Failed to wait chunk writer: write tcp 192.168.3.63:57572->35.55.34.1:1935: broken pipe
//
// Con eso el usuario no puede hacer nada. Peor: los tres fallos que encontramos aquel día
// —Twitch rechazando el handshake, Facebook con el cupo lleno, y nuestro propio bucle de
// reintentos— producían el MISMO texto. El estado y los contadores sí los distinguen, y de
// ahí sale el diagnóstico.

import { iOk, iAviso, iFallo, iNeutro, iTrabajando } from '@/iconos'

export const TONOS = {
  emitiendo: { color: 'positive', icono: iOk },
  atencion: { color: 'warning', icono: iAviso },
  fallo: { color: 'negative', icono: iFallo },
  neutro: { color: 'grey-6', icono: iNeutro },
  trabajando: { color: 'info', icono: iTrabajando },
}

/**
 * @param destino DTO de la API
 * @param haySesion si hay alguien publicando ahora mismo
 * @returns {{tono, titulo, detalle, consejo}}
 */
export function diagnosticar(destino, haySesion) {
  if (!destino.enabled) {
    return {
      tono: 'neutro',
      titulo: 'Apagado',
      detalle: 'No recibe nada mientras esté apagado.',
      consejo: null,
    }
  }

  const m = destino.metrics
  if (!haySesion || !m) {
    return {
      tono: 'neutro',
      titulo: 'En espera',
      detalle: 'Conectará en cuanto empieces a transmitir desde OBS.',
      consejo: null,
    }
  }

  if (m.state === 'live' && m.degraded) {
    return {
      tono: 'atencion',
      titulo: 'Emitiendo con pérdidas',
      detalle: `Está descartando vídeo para no atrasarse (${m.dropped_frames.toLocaleString('es')} fotogramas).`,
      // El descarte por GOP solo salta cuando la cola se llena, y la cola solo se llena si
      // este destino no traga lo que le mandamos.
      consejo: 'Tu subida no da abasto para todos los destinos, o esta plataforma va lenta. Baja el bitrate en OBS o apaga un destino.',
    }
  }

  if (m.state === 'live') {
    return {
      tono: 'emitiendo',
      titulo: 'Emitiendo',
      detalle: null,
      consejo: null,
    }
  }

  if (m.state === 'connecting') {
    return { tono: 'trabajando', titulo: 'Conectando…', detalle: null, consejo: null }
  }

  if (m.state === 'reconnecting') {
    // Reconexiones altas con bytes enviados es el patrón del aleteo: conecta, transmite un
    // poco y lo cortan. Nos costó el cupo de streams de una cuenta de Facebook aprenderlo.
    if (m.reconnections >= 3 && m.bytes_sent > 0) {
      return {
        tono: 'fallo',
        titulo: 'Conecta y se corta',
        detalle: `${m.reconnections} reconexiones. La plataforma acepta la conexión y la cierra enseguida.`,
        consejo: 'Suele ser que la emisión ya no está abierta en la plataforma, que la clave caducó, o que alcanzaste su límite de emisiones activas.',
      }
    }
    if (m.bytes_sent === 0) {
      return {
        tono: 'fallo',
        titulo: 'No llega a transmitir',
        detalle: `${m.reconnections} intentos sin conseguir enviar nada.`,
        consejo: 'Revisa la clave: es lo que falla casi siempre. Si la acabas de pegar, comprueba que la copiaste entera.',
      }
    }
    return {
      tono: 'atencion',
      titulo: 'Reconectando…',
      detalle: m.last_error ? 'Se cayó la conexión y está volviendo a intentarlo.' : null,
      consejo: null,
    }
  }

  if (m.state === 'error') {
    return {
      tono: 'fallo',
      titulo: 'Error',
      detalle: m.last_error ? 'No se pudo conectar con la plataforma.' : null,
      consejo: 'Revisa la URL y la clave del destino.',
    }
  }

  return { tono: 'neutro', titulo: 'Inactivo', detalle: null, consejo: null }
}

/** Formatea un bitrate para leerlo de un vistazo. */
export function bitrateLegible(bps) {
  if (!bps) return '—'
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(1)} Mbps`
  return `${Math.round(bps / 1000)} kbps`
}

/** Formatea bytes acumulados. */
export function bytesLegibles(b) {
  if (!b) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++ }
  return `${b.toFixed(i === 0 ? 0 : 1)} ${u[i]}`
}

/** Duración en formato h:mm:ss, para el tiempo emitiendo. */
export function duracionLegible(segundos) {
  if (!segundos) return '0:00'
  const h = Math.floor(segundos / 3600)
  const m = Math.floor((segundos % 3600) / 60)
  const s = Math.floor(segundos % 60)
  return h > 0
    ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
    : `${m}:${String(s).padStart(2, '0')}`
}
