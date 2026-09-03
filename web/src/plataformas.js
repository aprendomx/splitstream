// Catálogo de plataformas.
//
// Elegir plataforma precarga su URL y el usuario solo pega la clave. Eso elimina de raíz
// la clase entera de error "URL mal escrita": el backend la rechazaría con un mensaje
// correcto, pero lo mejor es que no llegue a ocurrir.
//
// Las URL salen de haberlas probado contra las plataformas de verdad el 2026-09-03, no de
// documentación: YouTube y Twitch por RTMP, Facebook por RTMPS —retiró el RTMP plano—.

import { iYoutube, iTwitch, iFacebook, iKick, iX, iTiktok, iServidor } from '@/iconos'

export const PLATAFORMAS = [
  {
    id: 'youtube',
    nombre: 'YouTube',
    url: 'rtmp://a.rtmp.youtube.com/live2',
    donde: 'YouTube Studio → Crear → Emitir en directo',
    icono: iYoutube,
    color: '#ff0033',
  },
  {
    id: 'twitch',
    nombre: 'Twitch',
    url: 'rtmp://live.twitch.tv/app',
    donde: 'Creator Dashboard → Configuración → Transmisión',
    icono: iTwitch,
    color: '#9146ff',
    // Twitch corta lo que pase de 6 Mbps.
    avisoBitrate: 6_000_000,
  },
  {
    id: 'facebook',
    nombre: 'Facebook',
    url: 'rtmps://live-api-s.facebook.com:443/rtmp/',
    donde: 'Live Producer → Usar clave de transmisión',
    icono: iFacebook,
    color: '#0866ff',
  },
  {
    id: 'kick',
    nombre: 'Kick',
    url: 'rtmps://fa723fc1b171.global-contribute.live-video.net/app',
    donde: 'Creator Dashboard → Configuración de stream',
    icono: iKick,
    color: '#53fc18',
  },
  {
    id: 'x',
    nombre: 'X',
    url: 'rtmps://va.pscp.tv:443/x',
    donde: 'Media Studio → Producer',
    icono: iX,
    color: '#e7e9ea',
  },
  {
    id: 'tiktok',
    nombre: 'TikTok',
    // TikTok es la excepción: emite servidor Y clave por emisión, así que no hay URL que
    // precargar. La interfaz pide las dos cosas en lugar de fingir que es como las demás.
    url: null,
    donde: 'TikTok Live Studio, o Live Center → Transmitir con software',
    icono: iTiktok,
    color: '#25f4ee',
    nota: 'TikTok da un servidor distinto en cada emisión, así que hay que pegar los dos campos.',
  },
  {
    id: 'custom',
    nombre: 'Otro (RTMP/RTMPS)',
    url: null,
    donde: 'Cualquier servidor RTMP o RTMPS',
    icono: iServidor,
    color: '#94a3b8',
  },
]

export const porId = (id) => PLATAFORMAS.find((p) => p.id === id) ?? PLATAFORMAS.at(-1)

/** Las que no traen URL fija piden servidor además de clave. */
export const pideServidor = (id) => porId(id).url === null
