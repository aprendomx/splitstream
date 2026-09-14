// Package config traduce el entorno del proceso a una configuración validada.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MasterKeyLen es el tamaño exacto, en bytes, de la master key de AES-256.
const MasterKeyLen = 32

// Config es la configuración del proceso. Implementa slog.LogValuer y json.Marshaler
// para que MasterKey no pueda escaparse ni a un log ni a una respuesta de la API.
type Config struct {
	HTTPAddr  string
	RTMPAddr  string
	DBPath    string
	LogLevel  slog.Level
	MasterKey [MasterKeyLen]byte
	// MasterKeyPath es dónde se guardó la clave, cuando viene de un archivo y no del
	// entorno. Vacío si se usó SPLITSTREAM_MASTER_KEY.
	MasterKeyPath string
	// MasterKeyAutogenerada es true si esta ejecución acaba de crear la clave. Sirve para
	// avisar al usuario una sola vez de que tiene que respaldarla.
	MasterKeyAutogenerada bool
	// SecureCookies marca la cookie de sesión como Secure.
	//
	// Va en la configuración y no se deduce de la petición porque en el despliegue del
	// spec §12 el TLS lo termina un proxy y el binario solo ve HTTP: adivinarlo daría una
	// cookie sin Secure justo en producción, que es donde importa. Por defecto false, para
	// que el panel funcione en local sin TLS.
	SecureCookies bool
	// MetricsToken autoriza GET /metrics con `Authorization: Bearer`. Vacío: solo cookie
	// de sesión. Se omite en LogValue y MarshalJSON como la master key.
	MetricsToken string
	// RetentionDays es cuántos días se conservan eventos y sesiones cerradas. 0 desactiva
	// la poda por edad (pero no la poda por cantidad de RetentionMaxEvents).
	RetentionDays int
	// RetentionMaxEvents es el tope de filas en la tabla events, sin importar su edad: lo
	// que protege el disco de un destino que aletea toda la noche.
	RetentionMaxEvents int
	// RecordingsDir es donde se escriben las grabaciones. Por defecto junto a la base,
	// por la misma razón que el archivo de clave: lo que hay que respaldar o mover va
	// junto.
	RecordingsDir string
	// TLSDomain enciende el TLS integrado con Let's Encrypt para ese dominio y solo ese.
	// Vacío: el binario sirve HTTP y el TLS, si lo hay, lo termina un proxy (spec §12).
	TLSDomain string
	// TLSCacheDir es donde autocert guarda la cuenta ACME y los certificados. Junto a la
	// base por la misma razón que la clave: lo que hay que respaldar va junto, y borrarlo
	// sin querer cuesta certificados (Let's Encrypt limita a 5 por semana por dominio).
	TLSCacheDir string
	// TLSCertFile y TLSKeyFile son un certificado propio en PEM. Excluyentes con TLSDomain.
	TLSCertFile string
	TLSKeyFile  string
	// TLSRedirectAddr es el listener HTTP que atiende el reto HTTP-01 y redirige a HTTPS.
	// Solo tiene sentido con TLS; vacío lo desactiva (`none` en el entorno).
	TLSRedirectAddr string
	// SecureCookiesDesactivadas es true cuando hay TLS integrado pero la persona puso
	// SPLITSTREAM_SECURE_COOKIES=false a mano. Se respeta —quizá prueba con IP y sin
	// nombre— pero se avisa en el log al arrancar.
	SecureCookiesDesactivadas bool
	// TrustedProxies son las redes desde las que se cree X-Forwarded-For. Vacía por
	// defecto: la cabecera la puede inventar quien llega directo, y el limitador del
	// login y el «local» del asistente dejarían de significar nada.
	TrustedProxies []netip.Prefix
	// UpdateCheck consulta la última release de GitHub al arrancar y cada 24 h. Solo el
	// aviso: nunca se actualiza solo. `SPLITSTREAM_UPDATE_CHECK=false` lo apaga.
	UpdateCheck bool
	// RTMPPreCommands manda releaseStream y FCPublish por el stream de control antes de
	// createStream, como FMLE (spec v1.0 §3.3). Apagado por defecto y a propósito: se
	// midieron rompiendo Twitch cuando se mandaban por el stream de datos, y sin ellos
	// las plataformas que usamos funcionan. Solo para la plataforma que los exija.
	RTMPPreCommands bool
	// TwitchClientID: client_id propio para Twitch; vacío usa el incluido en el binario.
	// Es público por diseño, no un secreto.
	TwitchClientID string
	// RetentionMaxChat es el tope de filas en la tabla de mensajes de chat, como
	// RetentionMaxEvents pero para el chat.
	RetentionMaxChat int
	// YouTubeChatBudget son las unidades de cuota diarias que el lector de chat de
	// YouTube puede gastar antes de pausarse hasta el día siguiente (spec v0.12). No
	// limita el resto de llamadas (título, programación): solo el sondeo del chat.
	YouTubeChatBudget int
	// YouTubeQuota es la cuota diaria total que Google asigna al proyecto, puramente
	// informativa para el panel: nada en el binario la hace cumplir.
	YouTubeQuota int
}

// LogValue implementa slog.LogValuer. Omite MasterKey deliberadamente. Receptor por
// valor a propósito: con receptor puntero, un Config logueado por valor (no *Config)
// queda fuera del method set y slog vuelca el struct entero, incluida la master key.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("http_addr", c.HTTPAddr),
		slog.String("rtmp_addr", c.RTMPAddr),
		slog.Bool("secure_cookies", c.SecureCookies),
		slog.String("db_path", c.DBPath),
		slog.String("log_level", c.LogLevel.String()),
		slog.Int("retention_days", c.RetentionDays),
		slog.Int("retention_max_events", c.RetentionMaxEvents),
		slog.String("recordings_dir", c.RecordingsDir),
		slog.String("tls_domain", c.TLSDomain),
		slog.String("tls_cert_file", c.TLSCertFile),
		slog.String("tls_redirect_addr", c.TLSRedirectAddr),
		slog.String("trusted_proxies", prefijos(c.TrustedProxies)),
		slog.Bool("update_check", c.UpdateCheck),
		slog.Bool("rtmp_precommands", c.RTMPPreCommands),
		slog.String("twitch_client_id", c.TwitchClientID),
		slog.Int("retention_max_chat", c.RetentionMaxChat),
		slog.Int("youtube_chat_budget", c.YouTubeChatBudget),
		slog.Int("youtube_quota", c.YouTubeQuota),
	)
}

// MarshalJSON implementa json.Marshaler. Omite MasterKey igual que LogValue: sin este
// método, json.Marshal vuelca el array de 32 bytes entero. Hoy nadie serializa un Config,
// pero la fase 4 es precisamente una API JSON y el fallo llegaría con ella.
//
// Receptor por valor por el mismo motivo que LogValue, y no es teórico: en la fase 1 este
// mismo enmascarado se declaró sobre puntero y un Config logueado por valor volcó la
// clave. Con receptor por valor el método está tanto en Config como en *Config.
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		HTTPAddr string `json:"http_addr"`
		RTMPAddr string `json:"rtmp_addr"`
		DBPath   string `json:"db_path"`
		LogLevel string `json:"log_level"`
	}{
		HTTPAddr: c.HTTPAddr,
		RTMPAddr: c.RTMPAddr,
		DBPath:   c.DBPath,
		LogLevel: c.LogLevel.String(),
	})
}

// TLS dice si el binario termina TLS él mismo, sea con Let's Encrypt o con certificado
// propio. Es lo que decide el puerto por defecto, la cookie Secure y el listener de
// redirección.
func (c *Config) TLS() bool { return c.TLSDomain != "" || c.TLSCertFile != "" }

// Load lee la configuración del entorno del proceso.
func Load() (*Config, error) {
	return LoadFrom(os.LookupEnv)
}

// KeyPathFor devuelve dónde vive el archivo de clave de una base dada: al lado y con el
// mismo nombre, cambiando la extensión. Junto a la base y no en otro sitio porque los dos
// archivos se respaldan y se mueven juntos; separarlos garantizaba que alguien copiara solo
// uno y perdiera el otro.
func KeyPathFor(dbPath string) string {
	ext := filepath.Ext(dbPath)
	return strings.TrimSuffix(dbPath, ext) + ".key"
}

// claveDelArchivo lee la clave del archivo, o la crea si no existe.
//
// Devuelve también si acaba de crearla, para que el binario pueda avisar una sola vez.
func claveDelArchivo(ruta string) (string, bool, error) {
	datos, err := os.ReadFile(ruta)
	if err == nil {
		clave := strings.TrimSpace(string(datos))
		if clave == "" {
			return "", false, fmt.Errorf("el archivo de clave %s está vacío: bórralo para "+
				"generar una nueva, pero perderás las claves de tus destinos", ruta)
		}
		return clave, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", false, fmt.Errorf("leer el archivo de clave %s: %w", ruta, err)
	}

	// No existe: se genera una.
	buf := make([]byte, MasterKeyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("generar la clave maestra: %w", err)
	}
	clave := base64.StdEncoding.EncodeToString(buf)

	if dir := filepath.Dir(ruta); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", false, fmt.Errorf("crear el directorio de %s: %w", ruta, err)
		}
	}
	f, err := crearArchivoDeClave(ruta)
	if err != nil {
		return "", false, fmt.Errorf("crear el archivo de clave %s: %w", ruta, err)
	}
	// Aquí NO vale el `defer f.Close()` de siempre. Este archivo es lo que hace legibles
	// las claves de TODOS los destinos: si se queda a medias no hay de dónde recuperarlo.
	// Y Close puede fallar mucho después del WriteString —con el disco lleno, o con un
	// error de escritura que el núcleo difiere hasta el cierre—, así que tirar su error
	// era devolver como guardada una clave que no llegó entera al disco.
	//
	// Si algo falla se borra el archivo a medio escribir: el arranque siguiente genera
	// otro en vez de leer basura y dar por buena una clave que no es.
	if err := escribirClave(f, clave); err != nil {
		if rerr := os.Remove(ruta); rerr != nil {
			// No se pudo ni borrar el archivo a medias. Hay que decirlo en el mismo error:
			// si queda ahí, el arranque siguiente lo lee y da por buena una clave que no
			// lo es, y para entonces ya no hay nada que enseñe qué pasó.
			return "", false, fmt.Errorf("escribir el archivo de clave %s: %w "+
				"(y quedó a medias: bórralo a mano, no se pudo borrar aquí: %v)", ruta, err, rerr)
		}
		return "", false, fmt.Errorf("escribir el archivo de clave %s: %w", ruta, err)
	}
	return clave, true, nil
}

// crearArchivoDeClave crea el archivo de clave, que tiene que no existir.
//
// 0600 y O_EXCL: solo el dueño puede leerla, y si otro proceso la creó entre el ReadFile de
// claveDelArchivo y esta línea, se falla en vez de pisarla. Pisarla dejaría las claves de
// los destinos ilegibles para siempre.
//
// Es una variable, y no una llamada directa a os.OpenFile, por una sola razón: el camino
// que BORRA el archivo a medias cuando la escritura falla no se puede probar de otra forma.
// Con un archivo de verdad ni WriteString ni Sync fallan a voluntad, y el propio O_EXCL
// descarta los trucos de dejar una tubería o un enlace en el sitio —entonces lo que falla es
// la creación, que es el otro camino y ya tiene su test—. El test interno la sustituye por
// una que crea el archivo igual pero devuelve el extremo de una tubería sin lector. Fuera
// del test nadie la toca.
var crearArchivoDeClave = func(ruta string) (*os.File, error) {
	return os.OpenFile(ruta, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// escribirClave escribe la clave y cierra el archivo, y devuelve el PRIMER error de los
// tres pasos. Sync antes de Close porque un Close limpio no promete que los bytes estén
// en el disco, solo que el descriptor se soltó sin quejas. Es el mismo patrón que
// FLVWriter.closeSegment.
func escribirClave(f *os.File, clave string) error {
	_, err := f.WriteString(clave + "\n")
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// LoadFrom lee la configuración de una función de consulta arbitraria, para poder
// testear sin tocar el entorno del proceso.
func LoadFrom(lookup func(string) (string, bool)) (*Config, error) {
	var err error
	var level slog.Level

	get := func(name, def string) string {
		if v, ok := lookup(name); ok && v != "" {
			return v
		}
		return def
	}

	// El TLS se lee antes que el resto porque cambia dos valores por defecto: el puerto
	// (:443 en vez de :8080) y la cookie Secure.
	tlsDomain := strings.TrimSpace(get("SPLITSTREAM_TLS_DOMAIN", ""))
	tlsCert := get("SPLITSTREAM_TLS_CERT_FILE", "")
	tlsKey := get("SPLITSTREAM_TLS_KEY_FILE", "")
	if err := validarTLS(tlsDomain, tlsCert, tlsKey); err != nil {
		return nil, err
	}
	conTLS := tlsDomain != "" || tlsCert != ""

	httpDef := ":8080"
	if conTLS {
		httpDef = ":443"
	}

	cfg := &Config{
		HTTPAddr: get("SPLITSTREAM_HTTP_ADDR", httpDef),
		RTMPAddr: get("SPLITSTREAM_RTMP_ADDR", ":1935"),
		DBPath:   get("SPLITSTREAM_DB_PATH", "splitstream.db"),

		MetricsToken: get("SPLITSTREAM_METRICS_TOKEN", ""),
		TLSDomain:    tlsDomain,
		TLSCertFile:  tlsCert,
		TLSKeyFile:   tlsKey,
		UpdateCheck:  get("SPLITSTREAM_UPDATE_CHECK", "true") != "false",
	}
	cfg.RecordingsDir = get("SPLITSTREAM_RECORDINGS_DIR", filepath.Join(filepath.Dir(cfg.DBPath), "recordings"))
	cfg.TLSCacheDir = get("SPLITSTREAM_TLS_CACHE_DIR", filepath.Join(filepath.Dir(cfg.DBPath), "tls-cache"))
	if conTLS {
		if v := get("SPLITSTREAM_TLS_REDIRECT_ADDR", ":80"); v != "none" {
			cfg.TLSRedirectAddr = v
		}
	}

	// Secure por defecto solo cuando el propio binario termina TLS; con proxy delante no
	// se puede adivinar y sigue siendo una decisión de quien despliega. Un `false`
	// explícito con TLS se respeta y se marca para avisar.
	if v, ok := lookup("SPLITSTREAM_SECURE_COOKIES"); ok && v != "" {
		cfg.SecureCookies = v == "true"
		cfg.SecureCookiesDesactivadas = conTLS && !cfg.SecureCookies
	} else {
		cfg.SecureCookies = conTLS
	}

	if cfg.TrustedProxies, err = parseProxies(get("SPLITSTREAM_TRUSTED_PROXIES", "")); err != nil {
		return nil, err
	}

	level, err = parseLevel(get("SPLITSTREAM_LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

	if cfg.RetentionDays, err = parseNonNegative(get("SPLITSTREAM_RETENTION_DAYS", "90"), "SPLITSTREAM_RETENTION_DAYS"); err != nil {
		return nil, err
	}
	if cfg.RetentionMaxEvents, err = parseNonNegative(get("SPLITSTREAM_RETENTION_MAX_EVENTS", "50000"), "SPLITSTREAM_RETENTION_MAX_EVENTS"); err != nil {
		return nil, err
	}
	if cfg.RetentionMaxChat, err = parseNonNegative(get("SPLITSTREAM_RETENTION_MAX_CHAT", "200000"), "SPLITSTREAM_RETENTION_MAX_CHAT"); err != nil {
		return nil, err
	}
	cfg.RTMPPreCommands = parseBool(get("SPLITSTREAM_RTMP_PRECOMMANDS", ""))
	cfg.TwitchClientID = strings.TrimSpace(get("SPLITSTREAM_TWITCH_CLIENT_ID", ""))
	if cfg.YouTubeChatBudget, err = parseNonNegative(get("SPLITSTREAM_YOUTUBE_CHAT_BUDGET", "6000"), "SPLITSTREAM_YOUTUBE_CHAT_BUDGET"); err != nil {
		return nil, err
	}
	if cfg.YouTubeQuota, err = parseNonNegative(get("SPLITSTREAM_YOUTUBE_QUOTA", "10000"), "SPLITSTREAM_YOUTUBE_QUOTA"); err != nil {
		return nil, err
	}

	raw, ok := lookup("SPLITSTREAM_MASTER_KEY")
	if !ok || raw == "" {
		// Sin variable de entorno: se busca el archivo de clave junto a la base, y si no
		// existe se crea. Es lo que permite abrir el programa con doble clic desde el
		// Finder o el Explorador, donde no hay variables de entorno que valgan.
		//
		// La variable manda SIEMPRE cuando está: el camino del servidor no cambia.
		raw, cfg.MasterKeyAutogenerada, err = claveDelArchivo(KeyPathFor(cfg.DBPath))
		if err != nil {
			return nil, err
		}
		cfg.MasterKeyPath = KeyPathFor(cfg.DBPath)
	}
	// Los mensajes de error de aquí abajo nunca incluyen `raw` ni los bytes decodificados.
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("SPLITSTREAM_MASTER_KEY no es base64 estándar válido")
	}
	if len(decoded) != MasterKeyLen {
		return nil, fmt.Errorf("SPLITSTREAM_MASTER_KEY debe decodificar a %d bytes, decodificó a %d", MasterKeyLen, len(decoded))
	}
	copy(cfg.MasterKey[:], decoded)

	return cfg, nil
}

// prefijos imprime la lista para el log; vacía se ve como "" y no como "[]".
func prefijos(ps []netip.Prefix) string {
	partes := make([]string, len(ps))
	for i, p := range ps {
		partes[i] = p.String()
	}
	return strings.Join(partes, ",")
}

// validarTLS rechaza las combinaciones que no pueden querer decir nada: o Let's Encrypt
// o certificado propio, y el certificado siempre con su clave. El dominio va sin
// esquema ni puerto porque autocert lo compara con el SNI tal cual.
func validarTLS(dominio, cert, key string) error {
	if dominio != "" && (cert != "" || key != "") {
		return errors.New("SPLITSTREAM_TLS_DOMAIN y SPLITSTREAM_TLS_CERT_FILE/SPLITSTREAM_TLS_KEY_FILE son excluyentes: " +
			"o Let's Encrypt o certificado propio")
	}
	if (cert == "") != (key == "") {
		return errors.New("SPLITSTREAM_TLS_CERT_FILE y SPLITSTREAM_TLS_KEY_FILE van juntas")
	}
	if dominio != "" && (strings.ContainsAny(dominio, " /:@") || !strings.Contains(dominio, ".")) {
		return fmt.Errorf("SPLITSTREAM_TLS_DOMAIN inválido %q: solo el nombre, sin esquema ni puerto "+
			"(por ejemplo relay.ejemplo.com)", dominio)
	}
	return nil
}

// parseProxies lee una lista separada por comas de CIDR o IP sueltas. Una IP suelta es
// su /32 (o /128): es lo que quiere decir quien escribe "127.0.0.1".
func parseProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, campo := range strings.Split(s, ",") {
		campo = strings.TrimSpace(campo)
		if campo == "" {
			continue
		}
		if p, err := netip.ParsePrefix(campo); err == nil {
			out = append(out, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(campo); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		return nil, fmt.Errorf("SPLITSTREAM_TRUSTED_PROXIES: %q no es una IP ni un CIDR", campo)
	}
	return out, nil
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("SPLITSTREAM_LOG_LEVEL inválido %q: usa debug, info, warn o error", s)
	}
}

// parseBool interpreta una variable de encendido/apagado que está APAGADA por defecto:
// solo `true` la enciende, igual que SPLITSTREAM_SECURE_COOKIES. Una sola forma de
// escribirla es una menos que documentar y que explicar cuando alguien pone `1` y no pasa
// nada.
//
// No devuelve error, también como las demás booleanas: en este archivo solo fallan al
// arrancar las variables cuyo valor no se puede adivinar (una red, un nivel de log, un
// entero). Para un interruptor apagado por defecto, un valor ilegible significa lo mismo
// que no ponerlo.
func parseBool(s string) bool {
	return s == "true"
}

// parseNonNegative interpreta s como un entero >= 0, para las variables de retención.
func parseNonNegative(s, name string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s inválido %q: usa un entero mayor o igual que 0", name, s)
	}
	return n, nil
}
