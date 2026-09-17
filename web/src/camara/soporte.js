// Detección de soporte (spec cámara §5): el primer fallo decide el aviso, en este orden,
// porque cada uno tiene una solución distinta (HTTPS, otro navegador, otra plataforma).
export const CODEC_VIDEO = 'avc1.42001f' // H.264 baseline nivel 3.1: sin B-frames, 720p30
export const CODEC_VIDEO_1080 = 'avc1.420028' // baseline nivel 4.0, para 1080p30
export const CODEC_AUDIO = 'mp4a.40.2' // AAC-LC

export async function detectarSoporte() {
  if (!window.isSecureContext) return { ok: false, motivoKey: 'camara.sin_https' }
  if (!navigator.mediaDevices?.getUserMedia || typeof VideoEncoder === 'undefined' ||
      typeof AudioEncoder === 'undefined' || typeof VideoFrame === 'undefined') {
    return { ok: false, motivoKey: 'camara.sin_apis' }
  }
  const video = await VideoEncoder.isConfigSupported({
    codec: CODEC_VIDEO, width: 1280, height: 720, bitrate: 2_500_000, framerate: 30, avc: { format: 'avc' },
  }).catch(() => null)
  if (!video?.supported) return { ok: false, motivoKey: 'camara.sin_h264' }
  const audio = await AudioEncoder.isConfigSupported({
    codec: CODEC_AUDIO, sampleRate: 48000, numberOfChannels: 2, bitrate: 128_000,
  }).catch(() => null)
  if (!audio?.supported) return { ok: false, motivoKey: 'camara.sin_aac' }
  return { ok: true, motivoKey: null }
}
