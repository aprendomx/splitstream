package config_test

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"

	"github.com/aprendomx/splitstream/internal/config"
)

// 32 bytes de 0x01..0x20, codificados en base64 estándar.
func testKeyB64() string {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(k[:])
}

func lookup(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

func TestLoadFromAppliesDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(),
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, quería \":8080\"", cfg.HTTPAddr)
	}
	if cfg.RTMPAddr != ":1935" {
		t.Errorf("RTMPAddr = %q, quería \":1935\"", cfg.RTMPAddr)
	}
	if cfg.DBPath != "splitstream.db" {
		t.Errorf("DBPath = %q, quería \"splitstream.db\"", cfg.DBPath)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, quería info", cfg.LogLevel)
	}
	if cfg.MasterKey[0] != 1 || cfg.MasterKey[31] != 32 {
		t.Errorf("MasterKey mal decodificada: %v", cfg.MasterKey)
	}
}

func TestLoadFromOverridesDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(),
		"SPLITSTREAM_HTTP_ADDR":  "127.0.0.1:9000",
		"SPLITSTREAM_RTMP_ADDR":  "0.0.0.0:1936",
		"SPLITSTREAM_DB_PATH":    "/var/lib/splitstream/db.sqlite",
		"SPLITSTREAM_LOG_LEVEL":  "debug",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.RTMPAddr != "0.0.0.0:1936" {
		t.Errorf("RTMPAddr = %q", cfg.RTMPAddr)
	}
	if cfg.DBPath != "/var/lib/splitstream/db.sqlite" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, quería debug", cfg.LogLevel)
	}
}

func TestLoadFromRequiresMasterKey(t *testing.T) {
	_, err := config.LoadFrom(lookup(map[string]string{}))
	if err == nil {
		t.Fatal("quería error cuando falta SPLITSTREAM_MASTER_KEY")
	}
	if !strings.Contains(err.Error(), "SPLITSTREAM_MASTER_KEY") {
		t.Errorf("el error debería nombrar la variable: %v", err)
	}
}

func TestLoadFromRejectsWrongKeyLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": short,
	}))
	if err == nil {
		t.Fatal("quería error con una clave de 16 bytes")
	}
}

func TestLoadFromRejectsInvalidBase64(t *testing.T) {
	_, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": "no-es-base64-!!!",
	}))
	if err == nil {
		t.Fatal("quería error con base64 inválido")
	}
}

func TestLoadFromRejectsUnknownLogLevel(t *testing.T) {
	_, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": testKeyB64(),
		"SPLITSTREAM_LOG_LEVEL":  "verboso",
	}))
	if err == nil {
		t.Fatal("quería error con un nivel de log desconocido")
	}
}

// El error de una master key inválida no puede reproducir su valor.
func TestLoadFromErrorDoesNotLeakKey(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": secret,
	}))
	if err == nil {
		t.Fatal("quería error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("el error filtró la clave: %v", err)
	}
}

func TestConfigLogValueOmitsMasterKey(t *testing.T) {
	key := testKeyB64()
	cfg, err := config.LoadFrom(lookup(map[string]string{
		"SPLITSTREAM_MASTER_KEY": key,
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("arranque", "config", cfg)

	out := buf.String()
	if strings.Contains(out, key) {
		t.Errorf("el log filtró la master key en base64: %s", out)
	}
	if strings.Contains(out, "AQIDBA") { // prefijo base64 de 0x01020304
		t.Errorf("el log filtró bytes de la master key: %s", out)
	}
	if !strings.Contains(out, ":8080") {
		t.Errorf("el log debería incluir los campos no secretos: %s", out)
	}
}
