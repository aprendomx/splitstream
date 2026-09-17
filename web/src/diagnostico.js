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
//
// Los textos viven en los diccionarios (diagnostico.<estado>.titulo/detalle/consejo): esta
// función devuelve claves, no texto, para que el componente que las enseña (TarjetaDestino)
// las resuelva con t() en el idioma activo.

import { iOk, iAviso, iFallo, iNeutro, iTrabajando } from '@/iconos'
import { formatearNumero } from '@/i18n'

export const TONOS = {
  emitiendo: { color: 'positive', icono: iOk },
  atencion: { color: 'warning', icono: iAviso },
  fallo: { color: 'negative', icono: iFallo },
  // grey-5, no grey-6: es el tono que más se usa (destino apagado) y grey-6 quedaba en
  // 3,77:1 sobre la superficie oscura; grey-5 da 6,5:1.
  neutro: { color: 'grey-5', icono: iNeutro },
  trabajando: { color: 'info', icono: iTrabajando },
}

/**
 * @param destino DTO de la API
 * @param haySesion si hay alguien publicando ahora mismo
 * @returns {{tono, tituloKey, detalleKey, consejoKey, params}}
 */
export function diagnosticar(destino, haySesion) {
  if (!destino.enabled) {
    return {
      tono: 'neutro',
      tituloKey: 'diagnostico.apagado.titulo',
      detalleKey: 'diagnostico.apagado.detalle',
      consejoKey: null,
      params: {},
    }
  }

  const m = destino.metrics
  if (!haySesion || !m) {
    return {
      tono: 'neutro',
      tituloKey: 'diagnostico.espera.titulo',
      detalleKey: 'diagnostico.espera.detalle',
      consejoKey: null,
      params: {},
    }
  }

  if (m.state === 'live' && m.degraded) {
    return {
      tono: 'atencion',
      tituloKey: 'diagnostico.perdidas.titulo',
      detalleKey: 'diagnostico.perdidas.detalle',
      // El descarte por GOP solo salta cuando la cola se llena, y la cola solo se llena si
      // este destino no traga lo que le mandamos.
      consejoKey: 'diagnostico.perdidas.consejo',
      params: { n: formatearNumero(m.dropped_frames) },
    }
  }

  if (m.state === 'live') {
    return {
      tono: 'emitiendo',
      tituloKey: 'diagnostico.emitiendo.titulo',
      detalleKey: null,
      consejoKey: null,
      params: {},
    }
  }

  if (m.state === 'connecting') {
    return {
      tono: 'trabajando',
      tituloKey: 'diagnostico.conectando.titulo',
      detalleKey: null,
      consejoKey: null,
      params: {},
    }
  }

  if (m.state === 'reconnecting') {
    // Reconexiones altas con bytes enviados es el patrón del aleteo: conecta, transmite un
    // poco y lo cortan. Nos costó el cupo de streams de una cuenta de Facebook aprenderlo.
    if (m.reconnections >= 3 && m.bytes_sent > 0) {
      return {
        tono: 'fallo',
        tituloKey: 'diagnostico.corte.titulo',
        detalleKey: 'diagnostico.corte.detalle',
        consejoKey: 'diagnostico.corte.consejo',
        params: { n: formatearNumero(m.reconnections) },
      }
    }
    if (m.bytes_sent === 0) {
      return {
        tono: 'fallo',
        tituloKey: 'diagnostico.sin_envio.titulo',
        detalleKey: 'diagnostico.sin_envio.detalle',
        consejoKey: 'diagnostico.sin_envio.consejo',
        params: { n: formatearNumero(m.reconnections) },
      }
    }
    return {
      tono: 'atencion',
      tituloKey: 'diagnostico.reconectando.titulo',
      detalleKey: m.last_error ? 'diagnostico.reconectando.detalle' : null,
      consejoKey: null,
      params: {},
    }
  }

  if (m.state === 'suspended') {
    return {
      tono: 'fallo',
      tituloKey: 'diagnostico.suspendido.titulo',
      detalleKey: 'diagnostico.suspendido.detalle',
      consejoKey: 'diagnostico.suspendido.consejo',
      params: {},
    }
  }

  if (m.state === 'error') {
    return {
      tono: 'fallo',
      tituloKey: 'diagnostico.error.titulo',
      detalleKey: m.last_error ? 'diagnostico.error.detalle' : null,
      consejoKey: 'diagnostico.error.consejo',
      params: {},
    }
  }

  return { tono: 'neutro', tituloKey: 'diagnostico.inactivo.titulo', detalleKey: null, consejoKey: null, params: {} }
}

/** Formatea un bitrate para leerlo de un vistazo. */
export function bitrateLegible(bps) {
  if (!bps) return '—'
  if (bps >= 1_000_000) {
    return `${formatearNumero(bps / 1_000_000, { minimumFractionDigits: 1, maximumFractionDigits: 1 })} Mbps`
  }
  return `${formatearNumero(Math.round(bps / 1000))} kbps`
}

/** Formatea bytes acumulados. */
export function bytesLegibles(b) {
  if (!b) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++ }
  const decimales = i === 0 ? 0 : 1
  return `${formatearNumero(b, { minimumFractionDigits: decimales, maximumFractionDigits: decimales })} ${u[i]}`
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
