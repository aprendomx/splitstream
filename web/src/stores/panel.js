import { defineStore } from 'pinia'
import { api, ApiError } from '@/api'

// El estado del panel se alimenta del WebSocket, que empuja el mismo statusDTO que
// devuelve GET /api/status (spec §10). El snapshot inicial viene del GET para que la
// interfaz pinte algo sin esperar a que el WS conecte.
export const usePanel = defineStore('panel', {
  state: () => ({
    autenticado: false,
    cargando: true,
    estado: null,      // statusDTO
    eventos: [],
    errorConexion: null,
    ws: null,
    reintentoWs: 0,
  }),

  getters: {
    haySesion: (s) => Boolean(s.estado?.session?.live),
    sesion: (s) => s.estado?.session ?? { live: false },
    ingesta: (s) => s.estado?.ingest ?? null,
    destinos: (s) => s.estado?.destinations ?? [],
    resolucion: (s) => {
      const ses = s.estado?.session
      // width y height llegan null hasta el primer sequence header, que es cosa de un
      // segundo. Es "todavía no se sabe", no un error.
      return ses?.width && ses?.height ? `${ses.width}×${ses.height}` : null
    },
  },

  actions: {
    async entrar(password) {
      await api.login(password)
      this.autenticado = true
      await this.cargar()
      this.conectarWs()
    },

    async salir() {
      this.desconectarWs()
      try { await api.logout() } finally {
        this.autenticado = false
        this.estado = null
      }
    },

    /** Primer snapshot. Si da 401, es que no hay sesión y toca login. */
    async cargar() {
      this.cargando = true
      try {
        this.estado = await api.estado()
        this.autenticado = true
        this.errorConexion = null
        this.refrescarEventos()
        // Al recargar la página la cookie sigue siendo válida, así que se entra por aquí
        // y no por entrar(). Sin esto el panel se quedaba con la foto del GET inicial y
        // no volvía a actualizarse nunca.
        this.conectarWs()
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          this.autenticado = false
        } else {
          this.errorConexion = e.message
        }
      } finally {
        this.cargando = false
      }
    },

    async refrescarEventos() {
      try { this.eventos = await api.eventos(50) } catch { /* el log no es crítico */ }
    },

    conectarWs() {
      if (this.ws) return
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const ws = new WebSocket(`${proto}://${location.host}/ws`)
      this.ws = ws

      ws.onmessage = (ev) => {
        try {
          this.estado = JSON.parse(ev.data)
          this.errorConexion = null
          this.reintentoWs = 0
        } catch { /* un mensaje ilegible no debe tirar el panel */ }
      }

      // El servidor no reintenta: la reconexión es del cliente (spec §10). Backoff con
      // tope, para no martillear al servidor si es él quien está caído — la misma lección
      // que nos costó el cupo de Facebook, un piso más arriba.
      ws.onclose = () => {
        this.ws = null
        if (!this.autenticado) return
        const espera = Math.min(1000 * 2 ** this.reintentoWs, 30000)
        this.reintentoWs++
        setTimeout(() => this.conectarWs(), espera)
      }
      ws.onerror = () => ws.close()
    },

    desconectarWs() {
      const ws = this.ws
      this.ws = null
      if (ws) { ws.onclose = null; ws.close() }
    },
  },
})
