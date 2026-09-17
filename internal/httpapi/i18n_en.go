package httpapi

// traducciones es la tabla español → inglés de los `message` de error de la API.
//
// Se escribe a mano y se agrupa por el archivo de donde sale cada mensaje, porque quien
// añade un error quiere encontrar rápido dónde poner el suyo. Las claves con {0}, {1} son
// plantillas: el comodín casa con la parte que se conoce solo en ejecución (un nombre de
// destino, un campo, un valor) y se copia tal cual al inglés, sin traducir.
//
// Nadie tiene que acordarse de venir aquí: TestTodoMensajeDeErrorTieneTraduccion recorre
// el AST de internal/httpapi y de internal/store y falla si un mensaje no tiene entrada,
// con el volcado listo para pegar.
var traducciones = map[string]string{
	// ---- internal/store: las tres clases transversales (store/errors.go) ----
	"no encontrado":                  "not found",
	"entrada inválida":               "invalid input",
	"conflicto con el estado actual": "conflict with the current state",

	// ---- internal/store/accounts.go ----
	"cuenta no encontrada":                              "account not found",
	"token de acceso vacío":                             "empty access token",
	"estado de cuenta inválido: {0}":                    "invalid account status: {0}",
	"la cuenta es de {0} y el destino de {1}":           "the account is on {0} and the destination on {1}",
	"el destino no tiene cuenta vinculada":              "the destination has no linked account",
	"plataforma sin cuentas: {0}":                       "platform without accounts: {0}",
	"la cuenta necesita id y nombre":                    "the account needs an id and a name",
	"la cuenta necesita un token de acceso":             "the account needs an access token",
	"una app propia necesita client_id y client_secret": "your own app needs client_id and client_secret",

	// ---- internal/store/broadcasts.go ----
	"la emisión necesita destino, cuenta y plataforma": "the broadcast needs a destination, an account and a platform",
	"el destino no tiene emisión":                      "the destination has no broadcast",
	"estado de emisión inválido: {0}":                  "invalid broadcast status: {0}",

	// ---- internal/store/chat.go ----
	"mensaje de chat sin sesión o plataforma": "chat message without a session or a platform",
	"limit debe estar entre 1 y 1000":         "limit must be between 1 and 1000",

	// ---- internal/store/destinations.go ----
	"destino no encontrado":                                        "destination not found",
	"URL de destino inválida":                                      "invalid destination URL",
	"URL de destino inválida: no se pudo interpretar":              "invalid destination URL: it could not be parsed",
	"URL de destino inválida: esquema {0}, usa rtmp:// o rtmps://": "invalid destination URL: scheme {0}, use rtmp:// or rtmps://",
	"URL de destino inválida: falta el host":                       "invalid destination URL: the host is missing",
	"URL de destino inválida: falta la app tras el host":           "invalid destination URL: the app after the host is missing",
	"el nombre no puede estar vacío":                               "the name cannot be empty",
	"plataforma {0} no soportada":                                  "platform {0} is not supported",
	"la clave no puede estar vacía":                                "the stream key cannot be empty",

	// ---- internal/store/events.go, logos.go, quota.go ----
	"sesión no encontrada":            "session not found",
	"ese canal no tiene logo":         "that channel has no logo",
	"el logo llegó vacío":             "the logo arrived empty",
	"cuota: unidades y día inválidos": "quota: invalid units and day",

	// ---- internal/store/recording_settings.go, recordings.go ----
	"los minutos por segmento deben estar entre 0 y {0}": "the minutes per segment must be between 0 and {0}",
	"el tope en GB debe ser mayor que 0":                 "the cap in GB must be greater than 0",
	"los días de retención no pueden ser negativos":      "the retention days cannot be negative",
	"grabación no encontrada":                            "recording not found",
	"la grabación está en curso":                         "the recording is in progress",

	// ---- internal/store/settings.go ----
	"settings no inicializado: falta Bootstrap": "settings not initialized: Bootstrap is missing",

	// ---- internal/store/webhooks.go ----
	"webhook no encontrado":                                                  "webhook not found",
	"formato {0} no soportado: json, discord o slack":                        "format {0} is not supported: json, discord or slack",
	"nivel {0} no soportado: info, warn o error":                             "level {0} is not supported: info, warn or error",
	"URL de webhook inválida: falta el servidor":                             "invalid webhook URL: the host is missing",
	"URL de webhook inválida: usa https:// (http solo vale hacia localhost)": "invalid webhook URL: use https:// (http is only allowed for localhost)",
	"URL de webhook inválida: esquema {0}, usa https://":                     "invalid webhook URL: scheme {0}, use https://",

	// ---- auth.go ----
	"demasiados intentos; espera un momento":                            "too many attempts; wait a moment",
	"cuerpo JSON inválido":                                              "invalid JSON body",
	"no hay contraseña configurada: ejecuta `splitstream -setpassword`": "no password is set: run `splitstream -setpassword`",
	"contraseña incorrecta":                                             "wrong password",
	"sesión requerida":                                                  "a session is required",
	"cookie de sesión con formato inválido":                             "malformed session cookie",
	"firma de la cookie de sesión inválida":                             "invalid session cookie signature",
	"sesión caducada":                                                   "session expired",

	// ---- backup.go ----
	"hay una emisión en curso: el respaldo retiene la base y frenaría a los destinos; espera a terminar": "a broadcast is in progress: the backup holds the database and would slow the destinations down; wait until it ends",
	"no se pudo generar el respaldo": "the backup could not be generated",

	// ---- broadcasts.go ----
	"esta plataforma no da la clave por API":                         "this platform does not hand out the stream key over its API",
	"la plataforma no da la clave por API":                           "the platform does not hand out the stream key over its API",
	"ponle un nombre al destino":                                     "give the destination a name",
	"la plataforma no dio la clave de emisión":                       "the platform did not return the stream key",
	"este destino no tiene cuenta vinculada":                         "this destination has no linked account",
	"esta plataforma sale al aire sola":                              "this platform goes live on its own",
	"la cuenta de {0} necesita reconectarse":                         "the account {0} needs to be reconnected",
	"hablar con las plataformas no está disponible en este arranque": "talking to the platforms is not available in this build",

	// ---- chat.go ----
	"el chat no está disponible": "chat is not available",
	"limit inválido":             "invalid limit",

	// ---- destinations.go ----
	"account_id inválido":                 "invalid account_id",
	"el id debe ser un número":            "the id must be a number",
	"el destino está apagado: enciéndelo": "the destination is off: turn it on",
	"el destino no está suspendido":       "the destination is not suspended",
	"no hay emisión en curso: el destino conectará solo al empezar la siguiente": "no broadcast is in progress: the destination will connect on its own when the next one starts",

	// ---- errors.go ----
	"error interno": "internal error",

	// ---- live.go ----
	"la plataforma no permite cambiar el título":          "the platform does not allow changing the title",
	"{0} no permite cambiar el título":                    "{0} does not allow changing the title",
	"crea la emisión primero en {0}":                      "create the broadcast first on {0}",
	"la plataforma rechazó la cuenta; reconéctala":        "the platform rejected the account; reconnect it",
	"la plataforma pide esperar un momento":               "the platform asks you to wait a moment",
	"la plataforma tardó demasiado":                       "the platform took too long",
	"la plataforma respondió con un error: {0}":           "the platform answered with an error: {0}",
	"twitch no está disponible":                           "twitch is not available",
	"conecta una cuenta de Twitch para buscar categorías": "connect a Twitch account to search for categories",
	"la cuenta de Twitch necesita reconectarse":           "the Twitch account needs to be reconnected",
	"manda un título, una categoría o las dos":            "send a title, a category or both",
	"elige al menos un destino":                           "choose at least one destination",
	"como mucho 20 destinos por petición":                 "at most 20 destinations per request",

	// ---- logos.go ----
	"la imagen pesa más de 2 MB":                                     "the image is larger than 2 MB",
	"no llegó ninguna imagen en el campo «file»":                     "no image arrived in the «file» field",
	"ese archivo no es una imagen PNG o JPEG":                        "that file is not a PNG or JPEG image",
	"la imagen tiene demasiados píxeles; recórtala antes de subirla": "the image has too many pixels; crop it before uploading it",
	"no se pudo leer la imagen":                                      "the image could not be read",

	// ---- metrics.go ----
	"token de métricas inválido": "invalid metrics token",

	// ---- platforms.go ----
	"plataforma sin proveedor": "platform without a provider",
	"hay demasiadas conexiones en curso; espera a que terminen o venzan":                                              "there are too many connections in progress; wait for them to finish or expire",
	"esta plataforma necesita las credenciales de tu propia app":                                                      "this platform needs the credentials of your own app",
	"esta plataforma no tiene client_id: pon SPLITSTREAM_TWITCH_CLIENT_ID o espera a una versión con la app incluida": "this platform has no client_id: set SPLITSTREAM_TWITCH_CLIENT_ID or wait for a version with the app built in",
	"la plataforma no respondió": "the platform did not answer",
	"no se puede volver a esa dirección; abre el panel por su URL pública o desde esta misma máquina": "there is no way back to that address; open the panel through its public URL or from this same machine",
	"state inválido":                    "invalid state",
	"flujo de autorización desconocido": "unknown authorization flow",

	// ---- preview.go: el motivo con el que se cierra el WebSocket de la vista previa,
	// que el panel enseña tal cual (VistaPrevia.vue) ----
	"sin señal":          "no signal",
	"la emisión terminó": "the broadcast ended",

	// ---- camera.go: los motivos con los que se cierra el WebSocket de la cámara, que
	// el panel enseña tal cual (Camara.vue) ----
	"ya hay una emisión en curso":      "a broadcast is already in progress",
	"el primer mensaje debe ser start": "the first message must be start",
	"start mal formado":                "malformed start",
	"mensaje mal formado":              "malformed message",
	"el servidor se está apagando":     "the server is shutting down",
	"mensaje de cámara mal formado":    "malformed camera message",

	// ---- recording.go ----
	"la grabación no está en el directorio de grabaciones": "the recording is not in the recordings directory",
	"el archivo de la grabación ya no está en disco":       "the recording file is no longer on disk",
	"no se pudo borrar el archivo":                         "the file could not be deleted",

	// ---- server.go, spa.go ----
	"no existe ese endpoint":                                           "no such endpoint",
	"no existe ese archivo":                                            "no such file",
	"método no permitido en esta ruta":                                 "method not allowed on this route",
	"el panel no está compilado en este binario: ejecuta `make build`": "the panel is not built into this binary: run `make build`",

	// ---- sessions.go, status.go ----
	"{0} debe ser un número":                     "{0} must be a number",
	"{0} debe ser un número mayor o igual que 0": "{0} must be a number greater than or equal to 0",
	"limit debe ser un número":                   "limit must be a number",

	// ---- setup.go ----
	"la configuración inicial desde fuera de esta máquina necesita el código que el servicio imprime al arrancar, y este no tiene ninguno": "the initial setup from outside this machine needs the code the service prints on startup, and this one has none",
	"el código no es correcto; míralo en la consola donde arrancaste splitstream":                                                          "the code is not correct; look it up in the console where you started splitstream",
	"la contraseña necesita al menos {0} caracteres":                                                                                       "the password needs at least {0} characters",
	"este servicio ya está configurado; entra con tu contraseña":                                                                           "this service is already set up; sign in with your password",

	// ---- test_destination.go ----
	"probar destinos no está disponible en este arranque": "testing destinations is not available in this build",
	"el destino está emitiendo ahora mismo: probarlo abriría una segunda publicación con la misma clave y la plataforma cortaría la que va en vivo": "the destination is streaming right now: testing it would open a second publish with the same key and the platform would cut off the live one",

	// ---- toggleall.go ----
	"falta el campo «enabled»: hay que decir si se encienden o se apagan": "the «enabled» field is missing: you have to say whether they are turned on or off",

	// ---- webhooks.go ----
	"los webhooks no están disponibles en este arranque": "webhooks are not available in this build",
	"no se pudo entregar: {0}":                           "could not be delivered: {0}",

	// ---- live.go: el `message` de cada resultado de /api/live/title (spec §3.3) ----
	"{0} no permite cambiar el título desde aquí": "{0} does not allow changing the title from here",
	"{0} no tiene cuenta vinculada":               "{0} has no linked account",
	"{0} no permite cambiar la categoría":         "{0} does not allow changing the category",
	"sin gestor de tokens":                        "no token manager",
	"título aplicado en {0}":                      "title applied on {0}",
	"categoría aplicada en {0}":                   "category applied on {0}",
	"título y categoría aplicados en {0}":         "title and category applied on {0}",

	// ---- platforms.go: el `message` de un flujo de autorización (authStatusDTO) ----
	"el código venció; vuelve a empezar":                  "the code expired; start again",
	"no se pudo guardar la cuenta":                        "the account could not be saved",
	"rechazaste la autorización":                          "you turned down the authorization",
	"la plataforma no autorizó la conexión: {0}":          "the platform did not authorize the connection: {0}",
	"la plataforma no devolvió el código de autorización": "the platform did not return the authorization code",

	// ---- test_destination.go: el diagnóstico de «probar destino» (probeDTO) ----
	"la clave vino por API: no hay clave inválida que probar":                                                                                 "the key came from the API: there is no invalid key to test",
	"La plataforma aceptó la conexión y la mantuvo abierta. La configuración es plausible; solo emitir de verdad confirma la clave.":          "The platform accepted the connection and kept it open. The setup is plausible; only streaming for real confirms the key.",
	"La plataforma aceptó la conexión y la cerró enseguida. Casi siempre es la clave, o una emisión que ya no está abierta en la plataforma.": "The platform accepted the connection and closed it right away. It is almost always the key, or a broadcast that is no longer open on the platform.",
	"La URL del destino no vale: tiene que empezar por rtmp:// o rtmps:// y llevar servidor y aplicación.":                                    "The destination URL is not valid: it has to start with rtmp:// or rtmps:// and carry a host and an app.",
	"La plataforma rechazó el handshake en «{0}». Revisa la URL.":                                                                             "The platform rejected the handshake at «{0}». Check the URL.",
	"La prueba se canceló antes de terminar. Vuelve a intentarlo.":                                                                            "The test was cancelled before it finished. Try again.",
	"No se resuelve el nombre del servidor. Revisa la URL.":                                                                                   "The host name does not resolve. Check the URL.",
	"El certificado del servidor no es válido. Revisa que la URL sea la de la plataforma y no la de un intermediario.":                        "The server certificate is not valid. Check that the URL is the platform's and not a middleman's.",
	"No se pudo conectar con el servidor. Revisa la URL, el puerto y tu red.":                                                                 "The server could not be reached. Check the URL, the port and your network.",
}
