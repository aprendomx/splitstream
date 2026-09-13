// Catálogo de plataformas.
//
// Elegir plataforma precarga su URL y el usuario solo pega la clave. Eso elimina de raíz
// la clase entera de error "URL mal escrita": el backend la rechazaría con un mensaje
// correcto, pero lo mejor es que no llegue a ocurrir.
//
// Las URL salen de haberlas probado contra las plataformas de verdad el 2026-09-03, no de
// documentación: YouTube y Twitch por RTMP, Facebook por RTMPS —retiró el RTMP plano—.

import { iYoutube, iTwitch, iFacebook, iKick, iX, iTiktok, iServidor } from '@/iconos'
import { t } from '@/i18n'

export const PLATAFORMAS = [
  {
    id: 'youtube',
    nombre: 'YouTube',
    url: 'rtmp://a.rtmp.youtube.com/live2',
    dondeKey: 'plataformas.youtube.donde',
    icono: iYoutube,
    color: '#ff0033',
  },
  {
    id: 'twitch',
    nombre: 'Twitch',
    url: 'rtmp://live.twitch.tv/app',
    dondeKey: 'plataformas.twitch.donde',
    icono: iTwitch,
    color: '#9146ff',
    // Twitch corta lo que pase de 6 Mbps.
    avisoBitrate: 6_000_000,
  },
  {
    id: 'facebook',
    nombre: 'Facebook',
    url: 'rtmps://live-api-s.facebook.com:443/rtmp/',
    dondeKey: 'plataformas.facebook.donde',
    icono: iFacebook,
    color: '#0866ff',
  },
  {
    id: 'kick',
    nombre: 'Kick',
    url: 'rtmps://fa723fc1b171.global-contribute.live-video.net/app',
    dondeKey: 'plataformas.kick.donde',
    icono: iKick,
    color: '#53fc18',
  },
  {
    id: 'x',
    nombre: 'X',
    url: 'rtmps://va.pscp.tv:443/x',
    dondeKey: 'plataformas.x.donde',
    icono: iX,
    color: '#e7e9ea',
  },
  {
    id: 'tiktok',
    nombre: 'TikTok',
    // TikTok es la excepción: emite servidor Y clave por emisión, así que no hay URL que
    // precargar. La interfaz pide las dos cosas en lugar de fingir que es como las demás.
    url: null,
    dondeKey: 'plataformas.tiktok.donde',
    icono: iTiktok,
    color: '#25f4ee',
    notaKey: 'plataformas.tiktok.nota',
  },
  {
    id: 'custom',
    // Las seis de arriba son marcas y se escriben igual en cualquier idioma; «Otro» no es
    // una marca sino una descripción, así que va por la tabla de traducciones.
    nombreKey: 'plataformas.custom.nombre',
    // Sin dondeKey a propósito: no hay una plataforma real cuyo menú describir, es
    // cualquier servidor RTMP/RTMPS propio del usuario.
    url: null,
    icono: iServidor,
    color: '#94a3b8',
  },
]

export const porId = (id) => PLATAFORMAS.find((p) => p.id === id) ?? PLATAFORMAS.at(-1)

/** El nombre que se le enseña a una persona: la marca tal cual, o la clave traducida. */
export const nombreDe = (p) => (p?.nombreKey ? t(p.nombreKey) : (p?.nombre ?? ''))

/**
 * El nombre a partir del id que manda el servidor (el chat, los eventos de una sesión).
 * Un id que no está en el catálogo sale tal cual: decir «Otro» de algo que tiene nombre
 * propio sería peor que enseñar el id.
 */
export const nombrePorId = (id) => {
  const p = PLATAFORMAS.find((x) => x.id === id)
  return p ? nombreDe(p) : String(id ?? '')
}

/** Las que no traen URL fija piden servidor además de clave. */
export const pideServidor = (id) => porId(id).url === null
