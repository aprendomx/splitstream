// Cliente de la API. Fino a propósito: la validación vive en el backend, que ya devuelve
// mensajes escritos para personas ("la clave no puede estar vacía", "URL de destino
// inválida: falta el host"). Duplicarlos aquí crearía dos fuentes de verdad que se
// desincronizarían a la primera.

/** Error de la API, con el `code` del spec §9 para que quien llame pueda decidir. */
export class ApiError extends Error {
  constructor(status, code, message) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function pedir(metodo, ruta, cuerpo) {
  const opciones = {
    method: metodo,
    // La sesión es una cookie httpOnly: nunca la tocamos desde JS, solo hay que
    // asegurarse de que viaja.
    credentials: 'same-origin',
    headers: {},
  }
  if (cuerpo !== undefined) {
    opciones.headers['Content-Type'] = 'application/json'
    opciones.body = JSON.stringify(cuerpo)
  }

  let res
  try {
    res = await fetch(ruta, opciones)
  } catch (e) {
    // Fallo de red: el servidor no responde. Es distinto de un error de la API y merece
    // otro mensaje, porque la acción del usuario es otra.
    throw new ApiError(0, 'network', 'No se pudo contactar con el servidor')
  }

  if (res.status === 204) return null

  const texto = await res.text()
  let datos = null
  if (texto) {
    try {
      datos = JSON.parse(texto)
    } catch {
      throw new ApiError(res.status, 'internal', 'El servidor devolvió una respuesta ilegible')
    }
  }

  if (!res.ok) {
    const e = datos?.error
    throw new ApiError(res.status, e?.code ?? 'internal', e?.message ?? `Error ${res.status}`)
  }
  return datos
}

// subirArchivo va aparte de pedir() porque un multipart no lleva Content-Type propio: lo
// pone el navegador con el boundary que él mismo genera. Fijarlo a mano rompe la petición.
async function subirArchivo(ruta, archivo) {
  const cuerpo = new FormData()
  cuerpo.append('file', archivo)

  let res
  try {
    res = await fetch(ruta, { method: 'PUT', credentials: 'same-origin', body: cuerpo })
  } catch {
    throw new ApiError(0, 'network', 'No se pudo contactar con el servidor')
  }

  const texto = await res.text()
  let datos = null
  if (texto) {
    try {
      datos = JSON.parse(texto)
    } catch {
      throw new ApiError(res.status, 'internal', 'El servidor devolvió una respuesta ilegible')
    }
  }
  if (!res.ok) {
    const e = datos?.error
    throw new ApiError(res.status, e?.code ?? 'internal', e?.message ?? `Error ${res.status}`)
  }
  return datos
}

export const api = {
  // Configuración inicial. Es pública por definición: existe justo cuando todavía no hay
  // contraseña con la que autenticarse.
  estadoSetup: () => pedir('GET', '/api/setup'),
  configurar: (password, codigo) => pedir('POST', '/api/setup', { password, codigo }),

  login: (password) => pedir('POST', '/api/auth/login', { password }),
  logout: () => pedir('POST', '/api/auth/logout'),

  estado: () => pedir('GET', '/api/status'),
  eventos: (limit = 50) => pedir('GET', `/api/events?limit=${limit}`),

  ingesta: () => pedir('GET', '/api/ingest'),
  rotarClave: (desconectarAhora) =>
    pedir('POST', '/api/ingest/rotate-key', { disconnect_now: desconectarAhora }),

  destinos: () => pedir('GET', '/api/destinations'),
  crearDestino: (d) => pedir('POST', '/api/destinations', d),
  editarDestino: (id, patch) => pedir('PATCH', `/api/destinations/${id}`, patch),
  borrarDestino: (id) => pedir('DELETE', `/api/destinations/${id}`),
  alternarDestino: (id) => pedir('POST', `/api/destinations/${id}/toggle`),
  reordenarDestinos: (ids) => pedir('POST', '/api/destinations/reorder', { ids }),
  revelarClave: (id) => pedir('GET', `/api/destinations/${id}/key`),

  // El interruptor maestro manda el estado deseado, no una orden de invertir: si unos
  // canales están encendidos y otros no, invertir dejaría la mitad al revés de lo que el
  // usuario acaba de pulsar.
  alternarTodos: (enabled) => pedir('POST', '/api/destinations/toggle-all', { enabled }),

  subirLogo: (id, archivo) => subirArchivo(`/api/destinations/${id}/logo`, archivo),
  quitarLogo: (id) => pedir('DELETE', `/api/destinations/${id}/logo`),
  // La URL lleva la versión del logo para que al cambiarlo el navegador no siga
  // enseñando el anterior desde su caché.
  urlLogo: (d) => `/api/destinations/${d.id}/logo?v=${d.logo_etag}`,
}
