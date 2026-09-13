import { defineStore } from 'pinia'
import { Notify } from 'quasar'
import { api, ApiError } from '@/api'
import { t } from '@/i18n'

// El estado del panel se alimenta del WebSocket, que empuja el mismo statusDTO que
// devuelve GET /api/status (spec §10). El snapshot inicial viene del GET para que la
// interfaz pinte algo sin esperar a que el WS conecte.
export const usePanel = defineStore('panel', {
  state: () => ({
    autenticado: false,
    // Primer arranque: mientras no haya contraseña, el panel enseña el asistente.
    necesitaSetup: false,
    pideCodigo: false,
    esLocal: true,
    cargando: true,
    estado: null,      // statusDTO
    plataformas: [],   // platformDTO[]: capacidades y si hay client_id configurado
    cuentas: [],       // accountDTO[]: cuentas conectadas (own_app, quota_used_today…)
    errorConexion: null,
    ws: null,
    reintentoWs: 0,
    ultimoEventoAvisado: null,
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
    eventosRecientes: (s) => s.estado?.recent_events ?? [],
    grabacion: (s) => s.estado?.recording ?? null,
    actualizacion: (s) => s.estado?.update ?? null,
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
        this.marcarVistos(this.estado.recent_events ?? [])
        this.autenticado = true
        this.errorConexion = null
        await this.cargarPlataformas()
        // Al recargar la página la cookie sigue siendo válida, así que se entra por aquí
        // y no por entrar(). Sin esto el panel se quedaba con la foto del GET inicial y
        // no volvía a actualizarse nunca.
        this.conectarWs()
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          this.autenticado = false
          // Un 401 puede significar "hay que entrar" o "esto no está configurado
          // todavía". Solo el segundo lleva al asistente.
          await this.comprobarSetup()
        } else {
          this.errorConexion = e.message
        }
      } finally {
        this.cargando = false
      }
    },

    /** Catálogo de plataformas con proveedor: capacidades y si hay client_id configurado. */
    async cargarPlataformas() {
      try { this.plataformas = await api.plataformas() } catch { this.plataformas = [] }
    },

    /** Cuentas conectadas (Twitch, YouTube, Kick): own_app y quota_used_today (YouTube). */
    async cargarCuentas() {
      try { this.cuentas = await api.cuentas() } catch { this.cuentas = [] }
    },

    async comprobarSetup() {
      try {
        const s = await api.estadoSetup()
        this.necesitaSetup = s.necesario
        this.pideCodigo = s.pide_codigo
        this.esLocal = s.local
      } catch {
        // Si esto falla, se enseña el login: es el camino conservador.
        this.necesitaSetup = false
      }
    },

    /** Tras el asistente ya hay sesión: el backend deja la cookie puesta. */
    async trasSetup() {
      this.necesitaSetup = false
      await this.cargar()
    },

    /** El primer estado no avisa: son eventos de antes de abrir el panel. */
    marcarVistos(eventos) {
      this.ultimoEventoAvisado = eventos[0]?.id ?? 0
    },

    /**
     * Un aviso por cada evento de nivel error que no se había visto. Los warn no avisan:
     * un destino reconectando durante una emisión larga produciría una notificación cada
     * pocos segundos, y eso es ruido que acaba en "cerrar sin leer".
     */
    avisarErroresNuevos(eventos) {
      if (this.ultimoEventoAvisado === null) { this.marcarVistos(eventos); return }
      const nuevos = eventos.filter((e) => e.id > this.ultimoEventoAvisado)
      if (!nuevos.length) return
      this.ultimoEventoAvisado = nuevos[0].id
      for (const e of nuevos.filter((e) => e.level === 'error').reverse()) {
        Notify.create({ type: 'negative', message: e.message, timeout: 8000, actions: [{ label: t('comun.cerrar'), color: 'white' }] })
      }
    },

    conectarWs() {
      if (this.ws) return
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const ws = new WebSocket(`${proto}://${location.host}/ws`)
      this.ws = ws

      ws.onmessage = (ev) => {
        try {
          const nuevo = JSON.parse(ev.data)
          this.avisarErroresNuevos(nuevo.recent_events ?? [])
          this.estado = nuevo
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
