// Package config traduce el entorno del proceso a una configuración validada.
package config

import (
	"crypto/rand"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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
	// 0600 y O_EXCL: solo el dueño puede leerla, y si otro proceso la creó entre el
	// ReadFile de arriba y esta línea, se falla en vez de pisarla. Pisarla dejaría las
	// claves de los destinos ilegibles para siempre.
	f, err := os.OpenFile(ruta, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", false, fmt.Errorf("crear el archivo de clave %s: %w", ruta, err)
	}
	defer f.Close()
	if _, err := f.WriteString(clave + "\n"); err != nil {
		return "", false, fmt.Errorf("escribir el archivo de clave %s: %w", ruta, err)
	}
	return clave, true, nil
}

// LoadFrom lee la configuración de una función de consulta arbitraria, para poder
// testear sin tocar el entorno del proceso.
func LoadFrom(lookup func(string) (string, bool)) (*Config, error) {
	get := func(name, def string) string {
		if v, ok := lookup(name); ok && v != "" {
			return v
		}
		return def
	}

	cfg := &Config{
		HTTPAddr: get("SPLITSTREAM_HTTP_ADDR", ":8080"),
		RTMPAddr: get("SPLITSTREAM_RTMP_ADDR", ":1935"),
		DBPath:   get("SPLITSTREAM_DB_PATH", "splitstream.db"),

		SecureCookies: get("SPLITSTREAM_SECURE_COOKIES", "false") == "true",
	}

	level, err := parseLevel(get("SPLITSTREAM_LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

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
