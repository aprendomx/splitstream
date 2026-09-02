// Package config traduce el entorno del proceso a una configuración validada.
package config

import (
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
}

// LogValue implementa slog.LogValuer. Omite MasterKey deliberadamente. Receptor por
// valor a propósito: con receptor puntero, un Config logueado por valor (no *Config)
// queda fuera del method set y slog vuelca el struct entero, incluida la master key.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("http_addr", c.HTTPAddr),
		slog.String("rtmp_addr", c.RTMPAddr),
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
	}

	level, err := parseLevel(get("SPLITSTREAM_LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

	raw, ok := lookup("SPLITSTREAM_MASTER_KEY")
	if !ok || raw == "" {
		return nil, fmt.Errorf("falta SPLITSTREAM_MASTER_KEY: genera una con `splitstream -genkey`")
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
