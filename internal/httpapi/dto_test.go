package httpapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/store"
)

// TestMetricsDTOCoversEveryEngineField es el test que sostiene la decisión del spec §15.2.
//
// Duplicar los campos del motor en un DTO solo es seguro si alguien vigila la copia. Este
// test recorre relay.Metrics por reflexión y falla si aparece un campo que nadie mapeó:
// añadir una métrica al motor obliga entonces a decidir su nombre público, en vez de que
// desaparezca en silencio del WebSocket.
func TestMetricsDTOCoversEveryEngineField(t *testing.T) {
	mapeados := map[string]bool{
		"State":          true,
		"Degraded":       true,
		"BytesSent":      true,
		"BitrateBPS":     true,
		"DroppedFrames":  true,
		"Uptime":         true,
		"Reconnections":  true,
		"LastError":      true,
		"QueuedBytes":    true,
		"QueuedMessages": true,
	}

	rt := reflect.TypeOf(relay.Metrics{})
	for i := 0; i < rt.NumField(); i++ {
		nombre := rt.Field(i).Name
		if !mapeados[nombre] {
			t.Errorf("relay.Metrics.%s no está mapeado en metricsDTO.\n"+
				"Si has añadido un campo al motor, decide su nombre público en dto.go y "+
				"añádelo a este mapa. Si de verdad no debe salir por la API, añádelo "+
				"igualmente aquí con un comentario que diga por qué.", nombre)
		}
	}

	// Y al revés: un nombre en el mapa que ya no exista en el motor es basura acumulada.
	for nombre := range mapeados {
		if _, ok := rt.FieldByName(nombre); !ok {
			t.Errorf("%q está en el mapa pero ya no existe en relay.Metrics", nombre)
		}
	}
}

// TestNewMetricsDTOCopiesTheValues: que los campos estén mapeados no significa que se
// copien bien. Valores distintos en cada campo para que un cruce se note.
func TestNewMetricsDTOCopiesTheValues(t *testing.T) {
	m := relay.Metrics{
		State: "live", Degraded: true,
		BytesSent: 1_000, BitrateBPS: 2_000, DroppedFrames: 3,
		Uptime: 90 * time.Second, Reconnections: 4,
		LastError: "se cayó la red", QueuedBytes: 5, QueuedMessages: 6,
	}

	got := newMetricsDTO(m)

	if got.State != "live" || !got.Degraded {
		t.Errorf("estado mal copiado: %+v", got)
	}
	if got.BytesSent != 1000 || got.BitrateBPS != 2000 || got.DroppedFrames != 3 {
		t.Errorf("contadores mal copiados: %+v", got)
	}
	if got.Reconnections != 4 || got.LastError != "se cayó la red" {
		t.Errorf("reconexión mal copiada: %+v", got)
	}
	if got.QueuedBytes != 5 || got.QueuedMessages != 6 {
		t.Errorf("cola mal copiada: %+v", got)
	}
	// Uptime sale en segundos: un time.Duration serializa como nanosegundos en un int64,
	// que para el frontend es un número enorme y sin unidad.
	if got.UptimeSeconds != 90 {
		t.Errorf("UptimeSeconds = %d, quería 90", got.UptimeSeconds)
	}
}

// TestDestinationDTONeverCarriesThePlainKey es la propiedad del spec §8 en la frontera de
// serialización: el listado enseña la máscara, nunca la clave.
func TestDestinationDTONeverCarriesThePlainKey(t *testing.T) {
	d := store.Destination{
		ID: 1, Name: "yt", Platform: store.PlatformYouTube,
		RTMPURL: "rtmp://a.rtmp.youtube.com/live2",
		KeyMask: "••••abcd", Enabled: true, SortOrder: 0,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	blob, err := json.Marshal(newDestinationDTO(d, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var suelto map[string]any
	if err := json.Unmarshal(blob, &suelto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, prohibido := range []string{"key", "stream_key", "stream_key_encrypted"} {
		if _, ok := suelto[prohibido]; ok {
			t.Errorf("el DTO expone el campo %q", prohibido)
		}
	}
	if suelto["key_mask"] != "••••abcd" {
		t.Errorf("key_mask = %v", suelto["key_mask"])
	}
}

// TestDestinationDTOMetricsAreNilWhenNotStreaming: la UI tiene que poder distinguir "sin
// métricas" de "métricas en cero", o enseñará "0 kbps" para un destino apagado y parecerá
// que va mal.
func TestDestinationDTOMetricsAreNilWhenNotStreaming(t *testing.T) {
	d := store.Destination{ID: 1, Name: "yt", Platform: store.PlatformCustom}

	blob, err := json.Marshal(newDestinationDTO(d, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var suelto map[string]any
	if err := json.Unmarshal(blob, &suelto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := suelto["metrics"]; !ok || v != nil {
		t.Errorf("metrics = %v (presente=%v), quería null", v, ok)
	}

	// Y con métricas, no es null.
	m := relay.Metrics{State: "live", BytesSent: 10}
	blob, err = json.Marshal(newDestinationDTO(d, &m))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(blob, &suelto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if suelto["metrics"] == nil {
		t.Error("metrics es null habiendo métricas")
	}
}

// TestNewEventDTOKeepsTheOptionalIDs: un evento del sistema no tiene sesión ni destino, y
// el frontend distingue eso de un cero por el null.
func TestNewEventDTOKeepsTheOptionalIDs(t *testing.T) {
	blob, err := json.Marshal(newEventDTO(store.Event{
		ID: 1, Level: store.LevelInfo, Kind: "arranque", Message: "hola",
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var suelto map[string]any
	if err := json.Unmarshal(blob, &suelto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"session_id", "destination_id"} {
		if v, ok := suelto[k]; !ok || v != nil {
			t.Errorf("%s = %v (presente=%v), quería null", k, v, ok)
		}
	}

	id := int64(7)
	dto := newEventDTO(store.Event{ID: 1, SessionID: &id, DestinationID: &id,
		Level: store.LevelWarn, Kind: "caida", Message: "adiós"})
	if dto.SessionID == nil || *dto.SessionID != 7 {
		t.Errorf("SessionID = %v", dto.SessionID)
	}
	if dto.DestinationID == nil || *dto.DestinationID != 7 {
		t.Errorf("DestinationID = %v", dto.DestinationID)
	}
	if dto.Level != "warn" {
		t.Errorf("Level = %q", dto.Level)
	}
}

// TestDTOFieldNamesAreSnakeCase: el frontend de la fase 5 va a depender de estos nombres,
// así que conviene que sean consistentes desde el principio.
func TestDTOFieldNamesAreSnakeCase(t *testing.T) {
	tipos := []any{metricsDTO{}, destinationDTO{}, eventDTO{}, sessionDTO{}, ingestDTO{}, statusDTO{}}

	for _, v := range tipos {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			tag := rt.Field(i).Tag.Get("json")
			if tag == "" {
				t.Errorf("%s.%s no tiene tag json", rt.Name(), rt.Field(i).Name)
				continue
			}
			nombre, _, _ := strings.Cut(tag, ",")
			if nombre != strings.ToLower(nombre) {
				t.Errorf("%s.%s usa %q; los nombres van en snake_case", rt.Name(), rt.Field(i).Name, nombre)
			}
			if strings.Contains(nombre, "-") {
				t.Errorf("%s.%s usa %q; con guiones, no con guiones bajos", rt.Name(), rt.Field(i).Name, nombre)
			}
		}
	}
}
