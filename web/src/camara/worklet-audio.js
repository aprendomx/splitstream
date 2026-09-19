// Acumula las muestras que llegan de 128 en 128 hasta completar un frame AAC (1024 por
// canal) y las manda al hilo principal en formato f32-planar, que es lo que AudioData
// espera. Vive en el hilo de audio: aquí no hay AudioEncoder ni DOM.
const FRAME = 1024

class Acumulador extends AudioWorkletProcessor {
  constructor() {
    super()
    this.buf = null
    this.lleno = 0
  }

  process(inputs) {
    const entrada = inputs[0]
    if (!entrada || !entrada.length) return true
    const canales = entrada.length
    if (!this.buf || this.buf.length !== canales) {
      this.buf = Array.from({ length: canales }, () => new Float32Array(FRAME))
      this.lleno = 0
    }
    // El bloque se consume a trozos en vez de copiarse entero: el quantum es 128 hoy, pero
    // la spec permite otros, y uno mayor que el hueco libre haría que set() lanzara
    // RangeError; uno que cruce el borde de un frame perdería el resto del bloque.
    const n = entrada[0].length
    for (let pos = 0; pos < n; ) {
      const trozo = Math.min(n - pos, FRAME - this.lleno)
      for (let c = 0; c < canales; c++) this.buf[c].set(entrada[c].subarray(pos, pos + trozo), this.lleno)
      this.lleno += trozo
      pos += trozo
      if (this.lleno === FRAME) {
        const planar = new Float32Array(FRAME * canales)
        for (let c = 0; c < canales; c++) planar.set(this.buf[c], c * FRAME)
        this.port.postMessage({ planar, canales, frames: FRAME }, [planar.buffer])
        this.lleno = 0
      }
    }
    return true
  }
}

registerProcessor('acumulador', Acumulador)
