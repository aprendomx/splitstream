# Cámara del navegador — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Una segunda fuente de ingesta: la cámara y el micrófono del dispositivo desde el que se abre el panel. El navegador codifica H.264 + AAC con WebCodecs, los manda por un WebSocket, el binario los envuelve en tags FLV y los entrega al mismo `Engine` y `Hub` que reciben a OBS. Nada aguas abajo cambia.

**Architecture:** `internal/flv` gana el inverso de `Inspect` (envolver NALUs, AAC y sus configs en cuerpos de tag, extraer el ASC de un esds, generar el `onMetaData` con go-amf0). `relay.Engine` gana `StartLocalSession()`: `OnPublishStart` sin validador de clave. `GET /api/camera/ws` en `internal/httpapi/camera.go` lee mensajes binarios (`start`, configs, frames), los convierte en `relay.Message` y garantiza `OnPublishEnd` en el `defer`. En el panel, la página `Camara.vue` captura con `getUserMedia`, codifica con `VideoEncoder`/`AudioEncoder` (audio acumulado en un `AudioWorklet`) y envía con control de `bufferedAmount`.

**Tech Stack:** Go (stdlib, `github.com/coder/websocket` ya presente, `github.com/yutopp/go-amf0` que ya está en el módulo como dependencia indirecta de go-rtmp), Vue 3 + Quasar + Pinia (ya presentes), WebCodecs, AudioWorklet y Screen Wake Lock (APIs del navegador).

**Spec:** `docs/superpowers/specs/2026-09-17-camara-navegador-design.md`

## Global Constraints

- **Cero dependencias nuevas**: ni módulos Go descargados ni paquetes npm. `go-amf0` pasa de `// indirect` a directa con `go mod tidy`; no cambia `go.sum`.
- **Fronteras de paquetes que la CI comprueba**: `internal/relay` no importa go-rtmp, database/sql, store, events ni record; `internal/httpapi` no importa go-rtmp, rtmpio, webtls ni proveedores; `internal/record` solo relay y flv. `internal/flv` importando `go-amf0` no rompe ninguna (go-amf0 no es go-rtmp).
- **Comentarios, mensajes de commit, copys de interfaz y errores en español**, con el estilo del código existente: los comentarios explican el porqué. Textos que ve el usuario en el panel siempre por `t('clave')` con la clave en `es.json` Y `en.json` (`node scripts/i18n-check.mjs` falla si falta una).
- **Tests con `-race`**: `make test` = `go test ./... -race -count=1`. Linter: `make lint`; `make vet`.
- **Rutas con método** del mux de Go 1.22, registradas SOLO en `rutas()` (`internal/httpapi/server.go`), las protegidas vía `protegida(...)`. Tras tocar una ruta o un DTO: `go test ./internal/httpapi/ -run APIContract -update` regenera `docs/api.md`, y `TestAPIContractDocIsCurrent` falla mientras esté desactualizado.
- **El payload de `relay.Message` es inmutable y compartido**: nunca escribir en él; envolver es construir un slice nuevo.
- **Mensajes de commit cortos** (una línea, `git commit -m "..."`): un hook local bloquea comandos largos con `git commit` y `--no-verify`.
- **Sin Docker en la máquina de desarrollo**: el test de integración (Task 10) se escribe con `//go:build integration`, se compila con `go vet -tags integration ./test/integration/` y lo ejecuta la CI nocturna, no el desarrollador.
- Rama de trabajo: `feat/camara-navegador` desde `main`.

---

### Task 1: Envolver NALUs y AAC en cuerpos de tag FLV

**Files:**
- Create: `internal/flv/wrap.go`
- Create: `internal/flv/wrap_test.go`

**Interfaces:**
- Consumes: `flv.InspectVideo`, `flv.InspectAudio`, `flv.ParseResolution`, `flv.CodecIDAVC`, `flv.SoundFormatAAC` (existentes).
- Produces: `func WrapVideo(nalus []byte, keyframe bool) []byte`, `func WrapVideoSeqHeader(avcC []byte) []byte`, `func WrapAudio(aac []byte) []byte`, `func WrapAudioSeqHeader(asc []byte) []byte`. Todas devuelven un slice nuevo (no comparten memoria con la entrada).

- [ ] **Step 1: Crear la rama**

```bash
git checkout -b feat/camara-navegador main
```

- [ ] **Step 2: Escribir los tests que fallan**

Crear `internal/flv/wrap_test.go` (`mustHex` ya existe en `sps_test.go`, mismo paquete `flv_test`):

```go
package flv_test

import (
	"bytes"
	"testing"

	"github.com/aprendomx/splitstream/internal/flv"
)

// Las cabeceras son bytes exactos: son lo que leen las plataformas y el grabador, y
// cualquier desviación se ve como «stream corrupto» sin más pista.
func TestWrapProducesExactHeaders(t *testing.T) {
	casos := []struct {
		nombre string
		got    []byte
		want   []byte
	}{
		{"keyframe", flv.WrapVideo([]byte{0xaa, 0xbb}, true), []byte{0x17, 0x01, 0, 0, 0, 0xaa, 0xbb}},
		{"inter", flv.WrapVideo([]byte{0xaa}, false), []byte{0x27, 0x01, 0, 0, 0, 0xaa}},
		{"avc seq header", flv.WrapVideoSeqHeader([]byte{0x01, 0x42}), []byte{0x17, 0x00, 0, 0, 0, 0x01, 0x42}},
		{"aac frame", flv.WrapAudio([]byte{0x21, 0x20}), []byte{0xaf, 0x01, 0x21, 0x20}},
		{"aac seq header", flv.WrapAudioSeqHeader([]byte{0x11, 0x90}), []byte{0xaf, 0x00, 0x11, 0x90}},
	}
	for _, c := range casos {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s = %x, quería %x", c.nombre, c.got, c.want)
		}
	}
}

// Lo que Wrap envuelve, Inspect lo desmonta con los mismos campos que el relay usa para
// decidir: el círculo se cierra.
func TestWrapRoundTripsThroughInspect(t *testing.T) {
	v, err := flv.InspectVideo(flv.WrapVideo([]byte{0x00}, true))
	if err != nil || !v.IsKeyframe || v.IsSequenceHeader || v.CodecID != flv.CodecIDAVC || v.IsEnhanced {
		t.Errorf("keyframe envuelto se inspecciona como %+v (err %v)", v, err)
	}
	v, err = flv.InspectVideo(flv.WrapVideo([]byte{0x00}, false))
	if err != nil || v.IsKeyframe || v.IsSequenceHeader {
		t.Errorf("inter envuelto se inspecciona como %+v (err %v)", v, err)
	}
	v, err = flv.InspectVideo(flv.WrapVideoSeqHeader([]byte{0x01}))
	if err != nil || !v.IsSequenceHeader || !v.IsKeyframe {
		t.Errorf("seq header envuelto se inspecciona como %+v (err %v)", v, err)
	}
	a, err := flv.InspectAudio(flv.WrapAudio([]byte{0x21}))
	if err != nil || a.IsSequenceHeader || a.SoundFormat != flv.SoundFormatAAC {
		t.Errorf("frame AAC envuelto se inspecciona como %+v (err %v)", a, err)
	}
	a, err = flv.InspectAudio(flv.WrapAudioSeqHeader([]byte{0x11, 0x90}))
	if err != nil || !a.IsSequenceHeader {
		t.Errorf("seq header AAC envuelto se inspecciona como %+v (err %v)", a, err)
	}
}

// El avcC que entrega VideoEncoder lleva el SPS dentro, y el motor saca de ahí la
// resolución que enseña el panel: un sequence header envuelto tiene que servirle a
// ParseResolution tal cual.
func TestWrapVideoSeqHeaderKeepsTheResolutionReadable(t *testing.T) {
	sps := mustHex(t, "6742c01fda014016ec0440000003004000000f03c60ca8")
	avcC := []byte{0x01, sps[1], sps[2], sps[3], 0xFF, 0xE1, byte(len(sps) >> 8), byte(len(sps))}
	avcC = append(avcC, sps...)
	w, h, err := flv.ParseResolution(flv.WrapVideoSeqHeader(avcC))
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	if w != 1280 || h != 720 {
		t.Errorf("resolución = %dx%d, quería 1280x720", w, h)
	}
}

// El resultado no comparte memoria con la entrada: el payload de relay.Message es
// inmutable y se reparte entre todos los sinks, y el buffer de lectura del WebSocket
// puede reutilizarse.
func TestWrapCopiesItsInput(t *testing.T) {
	in := []byte{0xaa, 0xbb}
	out := flv.WrapVideo(in, true)
	in[0] = 0x00
	if out[5] != 0xaa {
		t.Error("WrapVideo devolvió un slice que comparte memoria con la entrada")
	}
}
```

- [ ] **Step 3: Comprobar que fallan**

Run: `go test ./internal/flv/ -run 'TestWrap' -v`
Expected: FAIL de compilación con `undefined: flv.WrapVideo` (y los demás).

- [ ] **Step 4: Implementar**

Crear `internal/flv/wrap.go`:

```go
package flv

// Este archivo es el inverso de inspect.go: escribe las cabeceras de tag que Inspect*
// lee. Existe porque, cuando el publisher es el navegador (spec cámara §4), lo que llega
// son NALUs y frames AAC desnudos, y el hub —y todo lo que hay detrás: sinks, grabación,
// vista previa— espera cuerpos de tag FLV como los que manda OBS.

const (
	// Primer byte de un tag de vídeo AVC clásico: frameType en el nibble alto (1 =
	// keyframe, 2 = inter) y codecID en el bajo (7 = AVC).
	videoHeaderKeyframe byte = 0x17
	videoHeaderInter    byte = 0x27

	// AVCPacketType: 0 = sequence header (avcC), 1 = NALUs.
	avcPacketSeqHeader byte = 0x00
	avcPacketNALU      byte = 0x01

	// Primer byte de un tag de audio AAC: soundFormat 10 en el nibble alto y, en el
	// bajo, tasa 3 (44 kHz), tamaño 1 (16 bits) y tipo 1 (estéreo). Para AAC la
	// especificación FLV fija esos tres campos SIEMPRE así, sean cuales sean la tasa y
	// los canales reales: esos van dentro del AudioSpecificConfig, que es lo que leen
	// las plataformas.
	audioHeaderAAC byte = 0xAF

	// AACPacketType: 0 = sequence header (AudioSpecificConfig), 1 = frame AAC crudo.
	aacPacketSeqHeader byte = 0x00
	aacPacketRaw       byte = 0x01
)

// WrapVideo envuelve NALUs en formato AVCC (longitud prefijada de 4 bytes) en el cuerpo
// de un tag de vídeo. El composition time va a 0: los codificadores de navegador en modo
// realtime no producen B-frames, y sin B-frames la marca de presentación coincide con la
// de decodificación.
func WrapVideo(nalus []byte, keyframe bool) []byte {
	h := videoHeaderInter
	if keyframe {
		h = videoHeaderKeyframe
	}
	out := make([]byte, 0, 5+len(nalus))
	out = append(out, h, avcPacketNALU, 0, 0, 0)
	return append(out, nalus...)
}

// WrapVideoSeqHeader envuelve un AVCDecoderConfigurationRecord (avcC) en el cuerpo de un
// AVC sequence header. Va marcado como keyframe, igual que lo manda OBS, y es lo que
// InspectVideo espera de un sequence header.
func WrapVideoSeqHeader(avcC []byte) []byte {
	out := make([]byte, 0, 5+len(avcC))
	out = append(out, videoHeaderKeyframe, avcPacketSeqHeader, 0, 0, 0)
	return append(out, avcC...)
}

// WrapAudio envuelve un frame AAC crudo (sin cabecera ADTS) en el cuerpo de un tag de
// audio.
func WrapAudio(aac []byte) []byte {
	out := make([]byte, 0, 2+len(aac))
	out = append(out, audioHeaderAAC, aacPacketRaw)
	return append(out, aac...)
}

// WrapAudioSeqHeader envuelve un AudioSpecificConfig en el cuerpo de un AAC sequence
// header.
func WrapAudioSeqHeader(asc []byte) []byte {
	out := make([]byte, 0, 2+len(asc))
	out = append(out, audioHeaderAAC, aacPacketSeqHeader)
	return append(out, asc...)
}
```

- [ ] **Step 5: Comprobar que pasan**

Run: `go test ./internal/flv/ -race -count=1`
Expected: PASS (los tests nuevos y los de siempre).

- [ ] **Step 6: Commit**

```bash
git add internal/flv/wrap.go internal/flv/wrap_test.go
git commit -m "feat(flv): envolver NALUs y AAC en cuerpos de tag, inverso de Inspect"
```

---

### Task 2: Extraer el AudioSpecificConfig de un esds

**Files:**
- Create: `internal/flv/esds.go`
- Create: `internal/flv/esds_test.go`

**Interfaces:**
- Consumes: `flv.ErrEmptyPayload` (existente).
- Produces: `func AudioSpecificConfig(description []byte) ([]byte, error)` y `var ErrMalformedESDS`. Devuelve la entrada tal cual si no empieza por `0x03`; si es un ES_Descriptor, devuelve el DecoderSpecificInfo (tag `0x05`).

- [ ] **Step 1: Escribir los tests que fallan**

Crear `internal/flv/esds_test.go`:

```go
package flv_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/aprendomx/splitstream/internal/flv"
)

// Lo que Safari 26 entregó en decoderConfig.description durante el spike del
// 2026-09-17: un ES_Descriptor completo con el ASC (11 90 = AAC-LC, 48 kHz, estéreo)
// dentro, como DecoderSpecificInfo. Chrome, en cambio, entrega los 2 bytes desnudos.
var esdsSafari = []byte{
	0x03, 0x80, 0x80, 0x80, 0x22, 0x00, 0x00, 0x00,
	0x04, 0x80, 0x80, 0x80, 0x14, 0x40, 0x14, 0x00, 0x18, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x05, 0x80, 0x80, 0x80, 0x02, 0x11, 0x90,
	0x06, 0x80, 0x80, 0x80, 0x01, 0x02,
}

func TestAudioSpecificConfigPassesABareASCThrough(t *testing.T) {
	got, err := flv.AudioSpecificConfig([]byte{0x11, 0x90})
	if err != nil {
		t.Fatalf("AudioSpecificConfig: %v", err)
	}
	if !bytes.Equal(got, []byte{0x11, 0x90}) {
		t.Errorf("ASC = %x, quería 1190", got)
	}
}

func TestAudioSpecificConfigExtractsFromSafariESDS(t *testing.T) {
	got, err := flv.AudioSpecificConfig(esdsSafari)
	if err != nil {
		t.Fatalf("AudioSpecificConfig: %v", err)
	}
	if !bytes.Equal(got, []byte{0x11, 0x90}) {
		t.Errorf("ASC = %x, quería 1190", got)
	}
}

// La longitud «base 128» del esds puede venir en 1 byte (0x22) o en 4 (80 80 80 22): el
// parser tiene que aceptar las dos, porque cada muxer usa una.
func TestAudioSpecificConfigAcceptsShortLengths(t *testing.T) {
	corto := []byte{
		0x03, 0x19, 0x00, 0x00, 0x00,
		0x04, 0x11, 0x40, 0x14, 0x00, 0x18, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x05, 0x02, 0x11, 0x90,
		0x06, 0x01, 0x02,
	}
	got, err := flv.AudioSpecificConfig(corto)
	if err != nil {
		t.Fatalf("AudioSpecificConfig: %v", err)
	}
	if !bytes.Equal(got, []byte{0x11, 0x90}) {
		t.Errorf("ASC = %x, quería 1190", got)
	}
}

func TestAudioSpecificConfigRejectsTruncatedESDS(t *testing.T) {
	for corte := 1; corte < 32; corte++ {
		if _, err := flv.AudioSpecificConfig(esdsSafari[:corte]); !errors.Is(err, flv.ErrMalformedESDS) {
			t.Errorf("esds cortado en %d bytes: err = %v, quería ErrMalformedESDS", corte, err)
		}
	}
}

func TestAudioSpecificConfigRejectsEmpty(t *testing.T) {
	if _, err := flv.AudioSpecificConfig(nil); !errors.Is(err, flv.ErrEmptyPayload) {
		t.Errorf("err = %v, quería ErrEmptyPayload", err)
	}
}
```

- [ ] **Step 2: Comprobar que fallan**

Run: `go test ./internal/flv/ -run 'TestAudioSpecificConfig' -v`
Expected: FAIL de compilación con `undefined: flv.AudioSpecificConfig`.

- [ ] **Step 3: Implementar**

Crear `internal/flv/esds.go`:

```go
package flv

import "errors"

// ErrMalformedESDS indica que lo que llegó empieza como un ES_Descriptor pero no se
// puede recorrer hasta el AudioSpecificConfig.
var ErrMalformedESDS = errors.New("ES_Descriptor malformado")

// Etiquetas de los descriptores de ISO 14496-1 §7.2.6 que hay que atravesar.
const (
	esDescrTag             byte = 0x03
	decoderConfigDescrTag  byte = 0x04
	decoderSpecificInfoTag byte = 0x05
)

// AudioSpecificConfig devuelve el ASC que va dentro de un AAC sequence header a partir
// de lo que AudioEncoder entrega en decoderConfig.description.
//
// Chrome entrega el ASC desnudo (2 bytes para AAC-LC). Safari entrega un ES_Descriptor
// completo —lo que iría en una caja esds de MP4— con el ASC dentro como
// DecoderSpecificInfo. Se distingue por el primer byte: un ASC de AAC-LC empieza por el
// audioObjectType 2 en sus 5 bits altos (0x10–0x17), nunca por 0x03.
func AudioSpecificConfig(description []byte) ([]byte, error) {
	if len(description) == 0 {
		return nil, ErrEmptyPayload
	}
	if description[0] != esDescrTag {
		return description, nil
	}

	es, err := leerDescriptor(description, esDescrTag)
	if err != nil {
		return nil, err
	}
	// ES_ID (2 bytes) y flags (1). Los campos opcionales que anuncian los flags no los
	// produce ningún navegador, pero se saltan igual: cuesta tres ifs.
	if len(es) < 3 {
		return nil, ErrMalformedESDS
	}
	flags := es[2]
	es = es[3:]
	if flags&0x80 != 0 { // streamDependenceFlag: dependsOn_ES_ID (2 bytes)
		if len(es) < 2 {
			return nil, ErrMalformedESDS
		}
		es = es[2:]
	}
	if flags&0x40 != 0 { // URL_Flag: URLlength (1 byte) + URL
		if len(es) < 1 || len(es) < 1+int(es[0]) {
			return nil, ErrMalformedESDS
		}
		es = es[1+int(es[0]):]
	}
	if flags&0x20 != 0 { // OCRstreamFlag: OCR_ES_Id (2 bytes)
		if len(es) < 2 {
			return nil, ErrMalformedESDS
		}
		es = es[2:]
	}

	dc, err := leerDescriptor(es, decoderConfigDescrTag)
	if err != nil {
		return nil, err
	}
	// objectTypeIndication (1), streamType/upStream/reserved (1), bufferSizeDB (3),
	// maxBitrate (4), avgBitrate (4): 13 bytes antes del DecoderSpecificInfo.
	if len(dc) < 13 {
		return nil, ErrMalformedESDS
	}
	asc, err := leerDescriptor(dc[13:], decoderSpecificInfoTag)
	if err != nil {
		return nil, err
	}
	if len(asc) < 2 {
		return nil, ErrMalformedESDS
	}
	return asc, nil
}

// leerDescriptor comprueba la etiqueta, lee la longitud «base 128 extensible» (de 1 a 4
// bytes, el bit alto de cada uno dice si sigue otro) y devuelve el cuerpo.
func leerDescriptor(b []byte, tag byte) ([]byte, error) {
	if len(b) < 2 || b[0] != tag {
		return nil, ErrMalformedESDS
	}
	size, n := 0, 1
	for i := 0; i < 4; i++ {
		if n >= len(b) {
			return nil, ErrMalformedESDS
		}
		c := b[n]
		n++
		size = size<<7 | int(c&0x7f)
		if c&0x80 == 0 {
			break
		}
	}
	if size > len(b)-n {
		return nil, ErrMalformedESDS
	}
	return b[n : n+size], nil
}
```

- [ ] **Step 4: Comprobar que pasan**

Run: `go test ./internal/flv/ -race -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/flv/esds.go internal/flv/esds_test.go
git commit -m "feat(flv): AudioSpecificConfig desde el ASC desnudo o el esds de Safari"
```

---

### Task 3: Generar el onMetaData con go-amf0

**Files:**
- Create: `internal/flv/metadata.go`
- Create: `internal/flv/metadata_test.go`
- Modify: `go.mod` (go-amf0 pasa a directa; lo hace `go mod tidy`)

**Interfaces:**
- Produces: `type Meta struct { Width, Height int; Framerate, VideoBitrateKbps, AudioBitrateKbps float64; AudioSampleRate int; Stereo bool; Encoder string }` y `func OnMetaData(m Meta) ([]byte, error)`. El payload es lo que `rtmpio.Publisher.WriteMeta` envuelve en `@setDataFrame` y lo que `relay.Message{Kind: KindMeta}` transporta: la cadena AMF0 `"onMetaData"` seguida de un ECMA array.

- [ ] **Step 1: Escribir el test que falla**

Crear `internal/flv/metadata_test.go`:

```go
package flv_test

import (
	"bytes"
	"testing"

	"github.com/yutopp/go-amf0"

	"github.com/aprendomx/splitstream/internal/flv"
)

// El onMetaData es declarativo (spec base §3.8), pero las plataformas lo leen y algunas
// rechazan el stream si falta: se comprueba campo a campo decodificando con la misma
// librería AMF0 que usa go-rtmp.
func TestOnMetaDataEncodesWhatThePlatformsRead(t *testing.T) {
	payload, err := flv.OnMetaData(flv.Meta{
		Width: 1280, Height: 720, Framerate: 30,
		VideoBitrateKbps: 2500, AudioBitrateKbps: 128, AudioSampleRate: 48000,
		Stereo: true, Encoder: "splitstream-camera/test",
	})
	if err != nil {
		t.Fatalf("OnMetaData: %v", err)
	}

	dec := amf0.NewDecoder(bytes.NewReader(payload))
	var nombre string
	if err := dec.Decode(&nombre); err != nil {
		t.Fatalf("decodificar el nombre: %v", err)
	}
	if nombre != "onMetaData" {
		t.Fatalf("nombre = %q, quería onMetaData", nombre)
	}
	var campos amf0.ECMAArray
	if err := dec.Decode(&campos); err != nil {
		t.Fatalf("decodificar el ECMA array: %v", err)
	}

	quiere := map[string]interface{}{
		"width": float64(1280), "height": float64(720), "framerate": float64(30),
		"videocodecid": float64(7), "videodatarate": float64(2500),
		"audiocodecid": float64(10), "audiodatarate": float64(128),
		"audiosamplerate": float64(48000), "audiosamplesize": float64(16),
		"stereo": true, "encoder": "splitstream-camera/test",
	}
	for k, v := range quiere {
		if campos[k] != v {
			t.Errorf("%s = %v (%T), quería %v", k, campos[k], campos[k], v)
		}
	}
	if len(campos) != len(quiere) {
		t.Errorf("el array tiene %d campos, quería %d: %v", len(campos), len(quiere), campos)
	}
}
```

- [ ] **Step 2: Comprobar que falla**

Run: `go test ./internal/flv/ -run TestOnMetaData -v`
Expected: FAIL de compilación con `undefined: flv.OnMetaData` (y posiblemente «no required module provides package go-amf0» hasta el tidy del paso 3).

- [ ] **Step 3: Implementar**

Crear `internal/flv/metadata.go`:

```go
package flv

import (
	"bytes"
	"fmt"

	"github.com/yutopp/go-amf0"
)

// Meta son los campos del onMetaData que Splitstream declara cuando el publisher es el
// navegador. Con OBS no hace falta: OBS manda el suyo y el relay lo reenvía tal cual.
type Meta struct {
	Width, Height    int
	Framerate        float64
	VideoBitrateKbps float64
	AudioBitrateKbps float64
	AudioSampleRate  int
	Stereo           bool
	// Encoder es el nombre que las plataformas enseñan como «software de emisión».
	Encoder string
}

// OnMetaData codifica en AMF0 el cuerpo de un @setDataFrame: la cadena "onMetaData" y un
// ECMA array con los campos. Es exactamente el payload que OnSetDataFrame recibe de OBS
// (go-rtmp entrega los bytes que siguen a "@setDataFrame"), así que Publisher.WriteMeta
// y el grabador lo tratan igual que al de OBS sin tocarse.
//
// Los números van como float64 porque AMF0 solo tiene un tipo numérico (Number, IEEE
// 754); los codecid 7 y 10 son los mismos que Inspect* reconoce.
func OnMetaData(m Meta) ([]byte, error) {
	var buf bytes.Buffer
	enc := amf0.NewEncoder(&buf)
	if err := enc.Encode("onMetaData"); err != nil {
		return nil, fmt.Errorf("codificar onMetaData: %w", err)
	}
	campos := amf0.ECMAArray{
		"width":           float64(m.Width),
		"height":          float64(m.Height),
		"framerate":       m.Framerate,
		"videocodecid":    float64(CodecIDAVC),
		"videodatarate":   m.VideoBitrateKbps,
		"audiocodecid":    float64(SoundFormatAAC),
		"audiodatarate":   m.AudioBitrateKbps,
		"audiosamplerate": float64(m.AudioSampleRate),
		"audiosamplesize": float64(16),
		"stereo":          m.Stereo,
		"encoder":         m.Encoder,
	}
	if err := enc.Encode(campos); err != nil {
		return nil, fmt.Errorf("codificar los campos de onMetaData: %w", err)
	}
	return buf.Bytes(), nil
}
```

Después:

```bash
go mod tidy
git diff go.mod go.sum
```

Expected: `go.mod` mueve `github.com/yutopp/go-amf0 v0.1.0` del bloque `// indirect` al bloque directo; `go.sum` no cambia. Si `go.sum` cambiara o apareciera un módulo nuevo, parar: viola la restricción global.

- [ ] **Step 4: Comprobar que pasa**

Run: `go test ./internal/flv/ -race -count=1 && go vet ./internal/flv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/flv/metadata.go internal/flv/metadata_test.go go.mod
git commit -m "feat(flv): onMetaData en AMF0 para el publisher del navegador"
```

---

### Task 4: `Engine.StartLocalSession` y el origen de la sesión

**Files:**
- Modify: `internal/relay/engine.go` (`Engine` struct, `OnPublishStart`, `LiveSession`, `Session`, `OnPublishEnd`)
- Modify: `internal/relay/engine_test.go` (añadir tests al final)

**Interfaces:**
- Consumes: `Engine`, `fakeStore` del test (existente en `engine_test.go`).
- Produces: `const SourceRTMP = "rtmp"`, `const SourceBrowser = "browser"`, `func (e *Engine) StartLocalSession() error`, campo `LiveSession.Source string`. `StartLocalSession` devuelve `ErrSessionInProgress` si hay sesión; si no, abre sesión en el store, arranca los sinks del proveedor y loguea `publisher_connected`. `OnMessage` y `OnPublishEnd` no cambian.

- [ ] **Step 1: Escribir los tests que fallan**

Añadir al final de `internal/relay/engine_test.go`:

```go
// La cámara del navegador no pasa por el validador: quien llega ya se autenticó con la
// cookie del panel, y la clave de ingesta es cosa de RTMP (spec cámara §4). Todo lo
// demás —sesión en el store, sinks, evento, cierre— es idéntico a OBS.
func TestEngineStartLocalSessionSkipsTheValidator(t *testing.T) {
	st := &fakeStore{}
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: st})
	e.SetValidator(func(string, string) error { return errors.New("nadie pasa por aquí") })
	var provisto int64
	e.SetSinkProvider(func(id int64) ([]*Sink, error) { provisto = id; return nil, nil })

	if err := e.StartLocalSession(); err != nil {
		t.Fatalf("StartLocalSession: %v", err)
	}
	ses := e.Session()
	if ses.ID == 0 || ses.Source != SourceBrowser {
		t.Fatalf("Session() = %+v, quería una sesión con Source browser", ses)
	}
	if provisto != ses.ID {
		t.Errorf("el proveedor de sinks recibió la sesión %d, quería %d", provisto, ses.ID)
	}
	st.mu.Lock()
	eventos := append([]EngineEvent(nil), st.events...)
	st.mu.Unlock()
	if len(eventos) != 1 || eventos[0].Kind != "publisher_connected" {
		t.Errorf("eventos = %+v, quería solo publisher_connected", eventos)
	}

	e.OnPublishEnd()
	if e.SessionID() != 0 {
		t.Error("OnPublishEnd no cerró la sesión local")
	}
	st.mu.Lock()
	ended := st.ended
	st.mu.Unlock()
	if ended != 1 {
		t.Errorf("FinishSession se llamó %d veces, quería 1", ended)
	}
}

// OBS y la cámara comparten el motor: una excluye a la otra, en los dos sentidos, y al
// cerrar la que estaba la otra vuelve a poder entrar.
func TestEngineLocalAndRTMPSessionsExcludeEachOther(t *testing.T) {
	st := &fakeStore{}
	h := NewHub(nil)
	defer h.Close()
	e := NewEngine(EngineConfig{Hub: h, Store: st})
	e.SetValidator(func(string, string) error { return nil })

	if err := e.StartLocalSession(); err != nil {
		t.Fatalf("StartLocalSession: %v", err)
	}
	if err := e.OnPublishStart("live", "ok"); !errors.Is(err, ErrSessionInProgress) {
		t.Errorf("OnPublishStart con la cámara en el aire = %v, quería ErrSessionInProgress", err)
	}
	e.OnPublishEnd()

	if err := e.OnPublishStart("live", "ok"); err != nil {
		t.Fatalf("OnPublishStart tras cerrar la local: %v", err)
	}
	if got := e.Session().Source; got != SourceRTMP {
		t.Errorf("Source = %q, quería %q", got, SourceRTMP)
	}
	if err := e.StartLocalSession(); !errors.Is(err, ErrSessionInProgress) {
		t.Errorf("StartLocalSession con OBS en el aire = %v, quería ErrSessionInProgress", err)
	}
	e.OnPublishEnd()

	if err := e.StartLocalSession(); err != nil {
		t.Fatalf("StartLocalSession tras cerrar la RTMP: %v", err)
	}
	e.OnPublishEnd()
}
```

- [ ] **Step 2: Comprobar que fallan**

Run: `go test ./internal/relay/ -run 'TestEngineStartLocal|TestEngineLocalAndRTMP' -v`
Expected: FAIL de compilación: `undefined: SourceBrowser`, `e.StartLocalSession undefined`.

- [ ] **Step 3: Implementar**

En `internal/relay/engine.go`:

1. Debajo de `ErrSessionInProgress`, añadir:

```go
// Origen de la sesión en curso: de dónde entran los mensajes. El panel lo enseña; no se
// persiste (spec cámara §10).
const (
	SourceRTMP    = "rtmp"    // OBS o cualquier publisher RTMP
	SourceBrowser = "browser" // la cámara del navegador por /api/camera/ws
)
```

2. En el struct `Engine`, debajo de `sessionID int64`, añadir `sessionSource string`.

3. Sustituir `OnPublishStart` entero por:

```go
// OnPublishStart valida al publisher RTMP y abre la sesión.
func (e *Engine) OnPublishStart(app, streamKey string) error {
	e.mu.Lock()
	if e.sessionID != 0 {
		e.mu.Unlock()
		return ErrSessionInProgress
	}
	validate := e.validate
	e.mu.Unlock()

	if err := validate(app, streamKey); err != nil {
		return err
	}
	return e.startSession(SourceRTMP, app, "el publisher conectó")
}

// StartLocalSession abre una sesión para la cámara del navegador (spec cámara §4). No
// pasa por el validador: quien llega aquí ya se autenticó con la cookie del panel, y la
// clave de ingesta es cosa de RTMP. Todo lo demás es idéntico a OnPublishStart, y la
// sesión se cierra con el mismo OnPublishEnd.
func (e *Engine) StartLocalSession() error {
	return e.startSession(SourceBrowser, "browser", "la cámara del navegador conectó")
}

// startSession es lo común a las dos entradas: abre la sesión en la base, arranca los
// sinks del proveedor y deja constancia. Vuelve a comprobar que no haya sesión porque
// OnPublishStart soltó el mutex para validar.
func (e *Engine) startSession(source, app, mensaje string) error {
	e.mu.Lock()
	if e.sessionID != 0 {
		e.mu.Unlock()
		return ErrSessionInProgress
	}
	provider := e.newSinks
	e.mu.Unlock()

	ctx := context.Background()
	id, err := e.store.StartSession(ctx)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.sessionID = id
	e.sessionSource = source
	e.sessionWidth, e.sessionHeight = 0, 0
	e.sessionBytes = 0
	e.sessionStarted = time.Now()
	e.mu.Unlock()

	// Los destinos se conectan al empezar la sesión, no al arrancar el proceso.
	sinks, err := provider(id)
	if err != nil {
		// Un fallo construyendo destinos no debe rechazar al publisher: es preferible
		// ingestar sin retransmitir que cortarle la transmisión al usuario.
		e.log.Error("no se pudieron construir los destinos de la sesión", "err", err)
	}
	for _, s := range sinks {
		e.AddSink(s)
	}

	e.logEvent(ctx, &id, nil, "info", "publisher_connected", mensaje)
	e.log.Info("sesión iniciada", "sesion_id", id, "app", app, "origen", source)
	return nil
}
```

4. En `LiveSession`, añadir el campo con su comentario:

```go
	// Source es SourceRTMP o SourceBrowser: el panel enseña de dónde viene lo que está
	// en el aire y deshabilita la otra entrada.
	Source string
```

5. En `Session()`, al construir `out`, añadir `Source: e.sessionSource,`.

6. En `OnPublishEnd`, en el bloque final `e.mu.Lock(); e.sessionID = 0; e.mu.Unlock()`, añadir `e.sessionSource = ""` junto a `e.sessionID = 0`.

- [ ] **Step 4: Comprobar que pasan**

Run: `go test ./internal/relay/ -race -count=1 && GOMAXPROCS=2 go test ./internal/relay/ -race -count=1 -run Engine`
Expected: PASS en las dos corridas. `go build ./...` también debe pasar: `cmd/splitstream` y `test/integration` no usan la firma que cambió.

- [ ] **Step 5: Commit**

```bash
git add internal/relay/engine.go internal/relay/engine_test.go
git commit -m "feat(relay): StartLocalSession sin validador y origen de la sesión"
```

---

### Task 5: La API conoce el origen y el fake del motor sabe ser publisher

**Files:**
- Modify: `internal/httpapi/server.go` (interfaz `EngineView`)
- Modify: `internal/httpapi/destinations_test.go` (`fakeEngine`)
- Modify: `internal/httpapi/dto.go` (`sessionDTO`)
- Modify: `internal/httpapi/status.go` (mapeo de la sesión)
- Modify: `internal/httpapi/status_test.go` (test nuevo)
- Modify: `docs/api.md` (regenerado)

**Interfaces:**
- Consumes: `relay.SourceBrowser`, `relay.LiveSession.Source`, `(*relay.Engine).StartLocalSession/OnMessage/OnPublishEnd` (Task 4).
- Produces: `EngineView` con `StartLocalSession() error`, `OnMessage(msg *relay.Message)`, `OnPublishEnd()`; `sessionDTO.Source string \`json:"source"\``; en `fakeEngine`: `setStartErr(err error)`, `sesionesLocales() int`, `terminadas() int`, `mensajes() []*relay.Message`.

- [ ] **Step 1: Escribir el test que falla**

Añadir al final de `internal/httpapi/status_test.go`:

```go
// El panel deshabilita «Emitir» en la página de cámara cuando lo que hay en el aire es
// OBS, y al revés: para eso necesita saber el origen, no solo que hay sesión.
func TestStatusReportsTheSessionSource(t *testing.T) {
	srv, _, eng, _, cookies := newDestServer(t)
	eng.setSesion(relay.LiveSession{ID: 7, StartedAt: time.Now(), Source: relay.SourceBrowser})

	st := decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if !st.Session.Live || st.Session.Source != "browser" {
		t.Errorf("session = %+v, quería live con source browser", st.Session)
	}

	eng.setSesion(relay.LiveSession{})
	st = decodeStatus(t, do(t, srv, cookies, http.MethodGet, "/api/status", ""))
	if st.Session.Source != "" {
		t.Errorf("sin sesión, source = %q, quería vacío", st.Session.Source)
	}
}
```

(Si `status_test.go` no importa ya `time` o `relay`, añadirlos.)

- [ ] **Step 2: Comprobar que falla**

Run: `go test ./internal/httpapi/ -run TestStatusReportsTheSessionSource -v`
Expected: FAIL de compilación: `st.Session.Source undefined`.

- [ ] **Step 3: Implementar**

1. `internal/httpapi/dto.go`, en `sessionDTO`, añadir tras `BitrateBPS`:

```go
	// Source dice de dónde viene la sesión: "rtmp" (OBS) o "browser" (la cámara del
	// panel). Vacío cuando Live es false.
	Source string `json:"source"`
```

2. `internal/httpapi/status.go`, dentro del `if ses := s.engine.Session(); ses.ID != 0 {`, tras `out.Session.ID = ses.ID`, añadir `out.Session.Source = ses.Source`.

3. `internal/httpapi/server.go`, en `EngineView`, tras `VideoConfig() []byte`, añadir:

```go

	// StartLocalSession, OnMessage y OnPublishEnd son la ingesta de la cámara del
	// navegador (spec cámara §4): aquí la API ES el publisher. Son los mismos métodos
	// con los que rtmpio alimenta al motor, a través de una interfaz que no lo importa.
	StartLocalSession() error
	OnMessage(msg *relay.Message)
	OnPublishEnd()
```

4. `internal/httpapi/destinations_test.go`, en `fakeEngine` añadir los campos:

```go
	startErr   error
	locales    int
	acabadas   int
	recibidos  []*relay.Message
```

y, tras `liberados()`, los métodos:

```go
// StartLocalSession simula la ingesta de la cámara (spec cámara §4): abre una sesión
// con id fijo, o falla con lo que el test haya fijado en setStartErr.
func (f *fakeEngine) StartLocalSession() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.locales++
	f.sesion = relay.LiveSession{ID: 42, StartedAt: time.Now(), Source: relay.SourceBrowser}
	return nil
}

func (f *fakeEngine) OnMessage(m *relay.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recibidos = append(f.recibidos, m)
}

func (f *fakeEngine) OnPublishEnd() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acabadas++
	f.sesion = relay.LiveSession{}
}

func (f *fakeEngine) setStartErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startErr = err
}

func (f *fakeEngine) sesionesLocales() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.locales
}

func (f *fakeEngine) terminadas() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acabadas
}

// mensajes devuelve una copia de lo que llegó por OnMessage, en orden.
func (f *fakeEngine) mensajes() []*relay.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*relay.Message(nil), f.recibidos...)
}
```

5. Regenerar el contrato:

```bash
go test ./internal/httpapi/ -run APIContract -update
git diff docs/api.md
```

Expected: `docs/api.md` gana la fila `source` en `sessionDTO`.

- [ ] **Step 4: Comprobar que pasa**

Run: `go test ./internal/httpapi/ -race -count=1 && go build ./...`
Expected: PASS. `cmd/splitstream` compila porque `*relay.Engine` ya cumple los tres métodos nuevos.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi docs/api.md
git commit -m "feat(api): la sesión dice su origen y el motor expone la ingesta local"
```

---

### Task 6: `GET /api/camera/ws`

**Files:**
- Create: `internal/httpapi/camera.go`
- Create: `internal/httpapi/camera_ws_test.go`
- Modify: `internal/httpapi/server.go` (tabla `rutas()`)
- Modify: `docs/api.md` (regenerado)

**Interfaces:**
- Consumes: `flv.WrapVideo`, `flv.WrapVideoSeqHeader`, `flv.WrapAudio`, `flv.WrapAudioSeqHeader`, `flv.AudioSpecificConfig`, `flv.OnMetaData`, `flv.Meta` (Tasks 1–3); `EngineView.StartLocalSession/OnMessage/OnPublishEnd/Session` (Task 5); `idiomaDe`, `traducir`, `wsWriteTimeout`, `requireSession` (existentes); `s.version`.
- Produces: ruta `GET /api/camera/ws`; constantes `cameraMsgStart=0x00, cameraMsgVideoConfig=0x01, cameraMsgVideoFrame=0x02, cameraMsgAudioConfig=0x03, cameraMsgAudioFrame=0x04`; códigos `cameraCloseBusy=4002, cameraCloseProtocol=4003, cameraCloseShutdown=4004`; el servidor confirma el arranque con el mensaje de texto `{"session_id":N}`.

- [ ] **Step 1: Escribir los tests que fallan**

Crear `internal/httpapi/camera_ws_test.go`:

```go
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/yutopp/go-amf0"

	"github.com/aprendomx/splitstream/internal/relay"
)

func cameraServer(t *testing.T) (*fakeEngine, string, []*http.Cookie) {
	t.Helper()
	srv, _, eng, _, cookies := newDestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return eng, "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/camera/ws", cookies
}

const startValido = `{"width":1280,"height":720,"framerate":30,"video_bitrate":2500000,"audio_bitrate":128000,"sample_rate":48000,"channels":2}`

func enviarBinario(t *testing.T, ctx context.Context, conn *websocket.Conn, tipo byte, cuerpo []byte) {
	t.Helper()
	escritura, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := conn.Write(escritura, websocket.MessageBinary, append([]byte{tipo}, cuerpo...)); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// arrancar manda start y espera la confirmación con el id de sesión.
func arrancar(t *testing.T, ctx context.Context, conn *websocket.Conn) int64 {
	t.Helper()
	enviarBinario(t, ctx, conn, cameraMsgStart, []byte(startValido))
	leer, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tipo, data, err := conn.Read(leer)
	if err != nil {
		t.Fatalf("Read de la confirmación: %v", err)
	}
	if tipo != websocket.MessageText {
		t.Fatalf("la confirmación llegó como %v, quería texto", tipo)
	}
	var conf struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(data, &conf); err != nil {
		t.Fatalf("confirmación ilegible %q: %v", data, err)
	}
	return conf.SessionID
}

func esperarCierre(t *testing.T, ctx context.Context, conn *websocket.Conn) websocket.CloseError {
	t.Helper()
	leer, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, _, err := conn.Read(leer)
	var cierre websocket.CloseError
	if !errors.As(err, &cierre) {
		t.Fatalf("se esperaba un cierre con código, llegó %v", err)
	}
	return cierre
}

func esperarTerminadas(t *testing.T, eng *fakeEngine, n int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if eng.terminadas() >= n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("OnPublishEnd se llamó %d veces, quería %d", eng.terminadas(), n)
}

// El handshake lleva la cookie: sin sesión del panel no hay upgrade.
func TestCameraRequiresASession(t *testing.T) {
	_, url, _ := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, resp, err := dialWS(ctx, url, nil)
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("la cámara aceptó una conexión sin sesión")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("código = %d, quería 401", resp.StatusCode)
	}
}

// start es siempre lo primero: un frame antes de start no abre sesión y cierra con 4003.
func TestCameraRejectsWhenStartIsNotFirst(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	enviarBinario(t, ctx, conn, cameraMsgVideoConfig, []byte{0x01, 0x42, 0x00, 0x1f, 0xff, 0xe1, 0x00})
	if got := esperarCierre(t, ctx, conn); got.Code != cameraCloseProtocol {
		t.Errorf("código de cierre = %d, quería %d", got.Code, cameraCloseProtocol)
	}
	if eng.sesionesLocales() != 0 {
		t.Error("se abrió una sesión sin start")
	}
}

// Con OBS en el aire, la cámara no entra: 4002 con motivo en el idioma del panel.
func TestCameraClosesBusyWhenASessionIsLive(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	eng.setStartErr(relay.ErrSessionInProgress)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialPreview(ctx, url, cookies, "en")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	enviarBinario(t, ctx, conn, cameraMsgStart, []byte(startValido))
	got := esperarCierre(t, ctx, conn)
	if got.Code != cameraCloseBusy {
		t.Errorf("código de cierre = %d, quería %d", got.Code, cameraCloseBusy)
	}
	if got.Reason != "a broadcast is already in progress" {
		t.Errorf("motivo = %q, quería el texto en inglés", got.Reason)
	}
	if eng.terminadas() != 0 {
		t.Error("OnPublishEnd se llamó sin que hubiera sesión que cerrar")
	}
}

// start abre la sesión, publica el onMetaData construido con lo que declaró el cliente y
// confirma con el id.
func TestCameraStartsSessionAndPublishesMeta(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	if id := arrancar(t, ctx, conn); id != 42 {
		t.Errorf("session_id = %d, quería 42 (el del fake)", id)
	}
	if eng.sesionesLocales() != 1 {
		t.Fatalf("sesiones locales = %d, quería 1", eng.sesionesLocales())
	}
	msgs := eng.mensajes()
	if len(msgs) != 1 || msgs[0].Kind != relay.KindMeta {
		t.Fatalf("mensajes = %+v, quería solo el onMetaData", msgs)
	}
	dec := amf0.NewDecoder(bytes.NewReader(msgs[0].Payload))
	var nombre string
	var campos amf0.ECMAArray
	if err := dec.Decode(&nombre); err != nil || nombre != "onMetaData" {
		t.Fatalf("el meta no empieza por onMetaData: %q, %v", nombre, err)
	}
	if err := dec.Decode(&campos); err != nil {
		t.Fatalf("decodificar campos: %v", err)
	}
	if campos["width"] != float64(1280) || campos["audiosamplerate"] != float64(48000) || campos["stereo"] != true {
		t.Errorf("campos = %v", campos)
	}
}

// Cada mensaje binario se convierte en el relay.Message que el hub espera: mismos
// flags que Inspect* sacaría del tag equivalente de OBS, timestamps intactos, y el ASC
// extraído aunque llegue dentro de un esds.
func TestCameraWrapsFramesIntoRelayMessages(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	arrancar(t, ctx, conn)

	avcc := []byte{0x01, 0x42, 0x00, 0x1f, 0xff, 0xe1, 0x00}
	esds := []byte{
		0x03, 0x80, 0x80, 0x80, 0x22, 0x00, 0x00, 0x00,
		0x04, 0x80, 0x80, 0x80, 0x14, 0x40, 0x14, 0x00, 0x18, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x05, 0x80, 0x80, 0x80, 0x02, 0x11, 0x90,
		0x06, 0x80, 0x80, 0x80, 0x01, 0x02,
	}
	enviarBinario(t, ctx, conn, cameraMsgVideoConfig, avcc)
	enviarBinario(t, ctx, conn, cameraMsgVideoFrame, []byte{0x01, 0x00, 0x00, 0x01, 0x02, 0xaa, 0xbb})
	enviarBinario(t, ctx, conn, cameraMsgVideoFrame, []byte{0x00, 0x00, 0x00, 0x01, 0x23, 0xcc})
	enviarBinario(t, ctx, conn, cameraMsgAudioConfig, esds)
	enviarBinario(t, ctx, conn, cameraMsgAudioFrame, []byte{0x00, 0x00, 0x01, 0x10, 0x21, 0x20})

	var msgs []*relay.Message
	for i := 0; i < 50; i++ {
		if msgs = eng.mensajes(); len(msgs) >= 6 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(msgs) != 6 {
		t.Fatalf("llegaron %d mensajes, quería 6 (meta + 5)", len(msgs))
	}
	casos := []struct {
		nombre string
		got    *relay.Message
		want   relay.Message
	}{
		{"video config", msgs[1], relay.Message{Kind: relay.KindVideo, IsSeqHeader: true, IsKeyframe: true,
			Payload: append([]byte{0x17, 0x00, 0, 0, 0}, avcc...)}},
		{"keyframe", msgs[2], relay.Message{Kind: relay.KindVideo, Timestamp: 0x0102, IsKeyframe: true,
			Payload: []byte{0x17, 0x01, 0, 0, 0, 0xaa, 0xbb}}},
		{"inter", msgs[3], relay.Message{Kind: relay.KindVideo, Timestamp: 0x0123,
			Payload: []byte{0x27, 0x01, 0, 0, 0, 0xcc}}},
		{"audio config", msgs[4], relay.Message{Kind: relay.KindAudio, IsSeqHeader: true,
			Payload: []byte{0xaf, 0x00, 0x11, 0x90}}},
		{"audio frame", msgs[5], relay.Message{Kind: relay.KindAudio, Timestamp: 0x0110,
			Payload: []byte{0xaf, 0x01, 0x21, 0x20}}},
	}
	for _, c := range casos {
		g, w := c.got, c.want
		if g.Kind != w.Kind || g.Timestamp != w.Timestamp || g.IsKeyframe != w.IsKeyframe ||
			g.IsSeqHeader != w.IsSeqHeader || !bytes.Equal(g.Payload, w.Payload) {
			t.Errorf("%s = %+v (payload %x), quería %+v (payload %x)", c.nombre, g, g.Payload, w, w.Payload)
		}
	}
}

// Un mensaje que no se puede envolver cierra con 4003 y termina la sesión: seguir
// aceptando tras un frame corrupto mandaría basura a las plataformas.
func TestCameraClosesOnMalformedMessage(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	arrancar(t, ctx, conn)

	enviarBinario(t, ctx, conn, cameraMsgVideoFrame, []byte{0x01, 0x00}) // sin timestamp completo
	if got := esperarCierre(t, ctx, conn); got.Code != cameraCloseProtocol {
		t.Errorf("código de cierre = %d, quería %d", got.Code, cameraCloseProtocol)
	}
	esperarTerminadas(t, eng, 1)
}

// Cuando el cliente se va —con cierre limpio o sin él— la sesión se cierra en el motor:
// es lo que apaga los sinks y lo que WaitIdle necesita para el apagado limpio.
func TestCameraEndsTheSessionWhenTheClientLeaves(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	arrancar(t, ctx, conn)
	conn.Close(websocket.StatusNormalClosure, "")
	esperarTerminadas(t, eng, 1)

	conn2, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial 2: %v", err)
	}
	arrancar(t, ctx, conn2)
	conn2.CloseNow() // sin trama de cierre: el socket muere sin más
	esperarTerminadas(t, eng, 2)
}

// Un keyframe 1080p pasa de los 32 KiB que la librería acepta por defecto: el límite
// tiene que ser mayor. Y uno mayor que el límite cierra la sesión en vez de colgarla.
func TestCameraAcceptsBigFramesAndClosesOnHugeOnes(t *testing.T) {
	eng, url, cookies := cameraServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, _, err := dialWS(ctx, url, cookies)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()
	arrancar(t, ctx, conn)

	grande := append([]byte{0x01, 0, 0, 0, 0}, make([]byte, 512*1024)...)
	enviarBinario(t, ctx, conn, cameraMsgVideoFrame, grande)
	for i := 0; i < 50 && len(eng.mensajes()) < 2; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	// El payload envuelto mide lo mismo que el mensaje sin su byte de tipo: 5 de cabecera
	// FLV en lugar de los 5 de flags + timestamp.
	if msgs := eng.mensajes(); len(msgs) != 2 || len(msgs[1].Payload) != len(grande) {
		t.Fatalf("un frame de 512 KiB no llegó entero: %d mensajes", len(msgs))
	}

	enorme := append([]byte{0x01, 0, 0, 0, 0}, make([]byte, cameraReadLimit+1)...)
	escritura, cancelEscritura := context.WithTimeout(ctx, 8*time.Second)
	_ = conn.Write(escritura, websocket.MessageBinary, append([]byte{cameraMsgVideoFrame}, enorme...))
	cancelEscritura()
	esperarTerminadas(t, eng, 1)
}
```

- [ ] **Step 2: Comprobar que fallan**

Run: `go test ./internal/httpapi/ -run TestCamera -v`
Expected: FAIL de compilación: `undefined: cameraMsgStart` (y demás constantes).

- [ ] **Step 3: Implementar el handler**

Crear `internal/httpapi/camera.go`:

```go
package httpapi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/aprendomx/splitstream/internal/flv"
	"github.com/aprendomx/splitstream/internal/relay"
)

// El protocolo de la cámara del navegador (spec cámara §3): mensajes binarios del
// cliente con 1 byte de tipo. Es el espejo de la vista previa: los frames de vídeo
// llevan el mismo formato ([flags][ts][NALUs AVCC]) en sentido contrario, y aquí se les
// pone la cabecera FLV que allí se recorta.
const (
	cameraMsgStart       byte = 0x00 // JSON con lo que el cliente va a mandar
	cameraMsgVideoConfig byte = 0x01 // avcC tal cual sale de VideoEncoder
	cameraMsgVideoFrame  byte = 0x02 // [1 byte flags: bit 0 keyframe][4 bytes ts ms][NALUs]
	cameraMsgAudioConfig byte = 0x03 // AudioSpecificConfig desnudo o dentro de un esds
	cameraMsgAudioFrame  byte = 0x04 // [4 bytes ts ms][AAC crudo]

	// cameraReadLimit acota cada mensaje. El de la librería son 32 KiB, y un keyframe
	// 1080p lo pasa de sobra; 4 MiB da margen a un keyframe de 4,5 Mbps con creces.
	cameraReadLimit = 4 << 20

	// cameraStartWait es cuánto se espera al start: un cliente que abre el socket y no
	// habla no retiene nada.
	cameraStartWait = 5 * time.Second

	// cameraReadTimeout acota la espera entre mensajes. Un teléfono que se queda sin red
	// no siempre manda la trama de cierre, y sin plazo la sesión quedaría abierta hasta
	// que el TCP se rinda, con los destinos colgando de ella.
	cameraReadTimeout = 10 * time.Second
)

// Códigos de cierre de aplicación: el cliente enseña el motivo tal cual.
const (
	cameraCloseBusy     websocket.StatusCode = 4002 // ya hay una emisión en curso
	cameraCloseProtocol websocket.StatusCode = 4003 // primer mensaje no fue start, o mensaje mal formado
	cameraCloseShutdown websocket.StatusCode = 4004 // el servidor se está apagando
)

var errCameraProtocol = errors.New("mensaje de cámara mal formado")

// cameraStart es lo que el cliente declara antes de mandar media. Va al onMetaData,
// que es declarativo: la resolución real la saca el motor del SPS (spec base §3.8).
type cameraStart struct {
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Framerate    float64 `json:"framerate"`
	VideoBitrate int     `json:"video_bitrate"` // bps
	AudioBitrate int     `json:"audio_bitrate"` // bps
	SampleRate   int     `json:"sample_rate"`
	Channels     int     `json:"channels"`
}

func (c cameraStart) valida() bool {
	return c.Width >= 16 && c.Width <= 4096 && c.Height >= 16 && c.Height <= 4096 &&
		c.Framerate > 0 && c.Framerate <= 120 && c.VideoBitrate > 0 && c.AudioBitrate > 0 &&
		c.SampleRate > 0 && (c.Channels == 1 || c.Channels == 2)
}

// handleCameraWS es la ingesta de la cámara del navegador (spec cámara §4): la API es el
// publisher. La sesión y el Origin se comprueban igual que en handleWS; llegar aquí ya
// implica cookie válida.
func (s *Server) handleCameraWS(w http.ResponseWriter, r *http.Request) {
	// El idioma se negocia ANTES del Accept, como en la vista previa: el motivo de cierre
	// es texto para personas.
	lang := idiomaDe(w)
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.Warn("no se pudo abrir el WebSocket de la cámara", "err", err)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(cameraReadLimit)
	ctx := r.Context()

	// 1. start, o nada.
	leer, cancel := context.WithTimeout(ctx, cameraStartWait)
	tipo, data, err := conn.Read(leer)
	cancel()
	if err != nil {
		return
	}
	if tipo != websocket.MessageBinary || len(data) < 1 || data[0] != cameraMsgStart {
		conn.Close(cameraCloseProtocol, traducir(lang, "el primer mensaje debe ser start"))
		return
	}
	var start cameraStart
	if err := json.Unmarshal(data[1:], &start); err != nil || !start.valida() {
		conn.Close(cameraCloseProtocol, traducir(lang, "start mal formado"))
		return
	}

	// 2. La sesión. Sin validador de clave: la cookie ya autenticó.
	if s.engine == nil {
		conn.Close(websocket.StatusInternalError, "sin motor")
		return
	}
	if err := s.engine.StartLocalSession(); err != nil {
		if errors.Is(err, relay.ErrSessionInProgress) {
			conn.Close(cameraCloseBusy, traducir(lang, "ya hay una emisión en curso"))
			return
		}
		s.logger.Error("no se pudo abrir la sesión de la cámara", "err", err)
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	// Pase lo que pase a partir de aquí —cierre del cliente, plazo vencido, mensaje
	// ilegal, apagado— la sesión se cierra: es lo que apaga los sinks y lo que WaitIdle
	// necesita para que el apagado sea limpio.
	defer s.engine.OnPublishEnd()

	// 3. El onMetaData y la confirmación.
	meta, err := flv.OnMetaData(flv.Meta{
		Width: start.Width, Height: start.Height, Framerate: start.Framerate,
		VideoBitrateKbps: float64(start.VideoBitrate) / 1000,
		AudioBitrateKbps: float64(start.AudioBitrate) / 1000,
		AudioSampleRate:  start.SampleRate, Stereo: start.Channels == 2,
		Encoder: "splitstream-camera/" + s.version,
	})
	if err != nil {
		s.logger.Error("no se pudo construir el onMetaData de la cámara", "err", err)
		conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	s.engine.OnMessage(&relay.Message{Kind: relay.KindMeta, Payload: meta})

	escritura, cancelEscritura := context.WithTimeout(ctx, wsWriteTimeout)
	err = wsjson.Write(escritura, conn, map[string]int64{"session_id": s.engine.Session().ID})
	cancelEscritura()
	if err != nil {
		return
	}
	s.logger.Info("cámara del navegador aceptada", "sesion_id", s.engine.Session().ID)

	// 4. Media hasta que el cliente se vaya.
	for {
		leer, cancel := context.WithTimeout(ctx, cameraReadTimeout)
		tipo, data, err := conn.Read(leer)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				conn.Close(cameraCloseShutdown, traducir(lang, "el servidor se está apagando"))
			}
			s.logger.Info("cámara del navegador desconectada", "motivo", err)
			return
		}
		msg, err := cameraMessage(tipo, data)
		if err != nil {
			conn.Close(cameraCloseProtocol, traducir(lang, "mensaje mal formado"))
			return
		}
		s.engine.OnMessage(msg)
	}
}

// cameraMessage convierte un mensaje binario en el relay.Message que el hub espera. Los
// Wrap* copian el cuerpo, así que el payload no comparte memoria con el buffer del
// WebSocket (relay.Message exige payload inmutable).
func cameraMessage(tipo websocket.MessageType, data []byte) (*relay.Message, error) {
	if tipo != websocket.MessageBinary || len(data) < 2 {
		return nil, errCameraProtocol
	}
	switch data[0] {
	case cameraMsgVideoConfig:
		// avcC mínimo: versión, perfil, compat, nivel, lengthSize, numSPS, numPPS.
		if len(data) < 1+7 {
			return nil, errCameraProtocol
		}
		return &relay.Message{Kind: relay.KindVideo, IsSeqHeader: true, IsKeyframe: true,
			Payload: flv.WrapVideoSeqHeader(data[1:])}, nil
	case cameraMsgVideoFrame:
		if len(data) < 1+5+1 {
			return nil, errCameraProtocol
		}
		key := data[1]&0x01 == 0x01
		return &relay.Message{Kind: relay.KindVideo, Timestamp: binary.BigEndian.Uint32(data[2:6]),
			IsKeyframe: key, Payload: flv.WrapVideo(data[6:], key)}, nil
	case cameraMsgAudioConfig:
		asc, err := flv.AudioSpecificConfig(data[1:])
		if err != nil {
			return nil, err
		}
		return &relay.Message{Kind: relay.KindAudio, IsSeqHeader: true,
			Payload: flv.WrapAudioSeqHeader(asc)}, nil
	case cameraMsgAudioFrame:
		if len(data) < 1+4+1 {
			return nil, errCameraProtocol
		}
		return &relay.Message{Kind: relay.KindAudio, Timestamp: binary.BigEndian.Uint32(data[1:5]),
			Payload: flv.WrapAudio(data[5:])}, nil
	default:
		return nil, errCameraProtocol
	}
}
```

Registrar la ruta en `rutas()` de `internal/httpapi/server.go`, justo debajo de la de `/api/preview/ws`:

```go
		protegida("GET", "/api/camera/ws", s.handleCameraWS, "ws", "Canal WebSocket por el que el navegador publica su cámara como fuente de la emisión"),
```

Añadir las traducciones al inglés en `internal/httpapi/i18n_en.go`, en el mapa `traducciones`, justo debajo del bloque `// ---- preview.go: ...` (que termina en `"la emisión terminó": "the broadcast ended",`):

```go

	// ---- camera.go: los motivos con los que se cierra el WebSocket de la cámara, que
	// el panel enseña tal cual (Camara.vue) ----
	"ya hay una emisión en curso":      "a broadcast is already in progress",
	"el primer mensaje debe ser start": "the first message must be start",
	"start mal formado":                "malformed start",
	"mensaje mal formado":              "malformed message",
	"el servidor se está apagando":     "the server is shutting down",
```

`TestTodoMensajeDeErrorTieneTraduccion` recorre el AST del paquete y falla si a alguna cadena pasada a `traducir` le falta entrada: correrá en el Step 4.

Regenerar el contrato:

```bash
go test ./internal/httpapi/ -run APIContract -update
```

- [ ] **Step 4: Comprobar que pasan**

Run: `go test ./internal/httpapi/ -race -count=1 && go vet ./... && make lint`
Expected: PASS y linter limpio. Si el linter se queja de `append` sobre `data` en los tests (gocritic `appendAssign`), ajustar sin cambiar la semántica.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi docs/api.md
git commit -m "feat(api): GET /api/camera/ws, la cámara del navegador como publisher"
```

---

### Task 7: Página «Cámara» en el panel: soporte, dispositivos y vista local

**Files:**
- Modify: `web/src/iconos.js` (dos iconos)
- Modify: `web/src/router.js` (ruta `/camara`)
- Modify: `web/src/App.vue` (pestaña)
- Modify: `web/src/i18n/es.json`, `web/src/i18n/en.json`
- Create: `web/src/camara/soporte.js`
- Create: `web/src/pages/Camara.vue`

**Interfaces:**
- Consumes: `usePanel` (`haySesion`, `sesion`, `destinos`), `ChipEstado`, `diagnosticar`, `t`.
- Produces: `detectarSoporte(): Promise<{ok: boolean, motivoKey: string|null}>`, `CODEC_VIDEO = 'avc1.42001f'`, `CODEC_AUDIO = 'mp4a.40.2'`; la página con selectores y `<video>` local. El botón «Emitir» existe pero en esta tarea solo enseña un aviso; Task 9 lo conecta.

No hay tests unitarios de JS en el proyecto: la verificación es `node scripts/i18n-check.mjs`, `npm run build` y abrir la página.

- [ ] **Step 1: Iconos, ruta y pestaña**

`web/src/iconos.js`: dentro del `export { ... }`, añadir dos líneas (orden alfabético no importa; junto a `mdiEye as iVer`):

```js
  mdiVideo as iCamara,
  mdiMicrophone as iMicrofono,
```

`web/src/router.js`: tras la ruta `panel`, añadir:

```js
    { path: '/camara', name: 'camara', component: () => import('@/pages/Camara.vue') },
```

`web/src/App.vue`: en el `import { ... } from '@/iconos'` de la primera línea, añadir `iCamara`. En el `<q-tabs>`, tras la `q-tab name="panel"`, añadir:

```vue
          <q-tab name="camara" :icon="iCamara"
                 :label="$q.screen.width >= 480 ? t('app.camara') : undefined"
                 :aria-label="t('app.camara')" @click="router.push({ name: 'camara' })" />
```

y actualizar el comentario de arriba («Las cuatro pestañas» → «Las cinco pestañas»).

- [ ] **Step 2: Claves de idioma**

Añadir a `web/src/i18n/es.json` (respetando el orden alfabético del archivo):

```json
  "app.camara": "Cámara",
  "camara.aviso_salir": "Si sales de la página, la emisión se corta.",
  "camara.calidad": "Calidad",
  "camara.calidad_1080": "1080p · 4,5 Mbps",
  "camara.calidad_720": "720p · 2,5 Mbps",
  "camara.camara": "Cámara",
  "camara.comprobando": "Comprobando el navegador…",
  "camara.conectando": "Conectando…",
  "camara.consejo_orientacion": "Gira el teléfono antes de emitir: cambiar la orientación a mitad corta la emisión.",
  "camara.destinos": "Destinos",
  "camara.dispositivo_sin_nombre": "Dispositivo {n}",
  "camara.emitir": "Emitir",
  "camara.en_vivo": "Emitiendo desde este dispositivo",
  "camara.fallo_codificar": "El codificador falló. Prueba con 720p.",
  "camara.intro": "La cámara y el micrófono de este dispositivo salen hacia tus destinos, sin OBS.",
  "camara.limites": "Necesita HTTPS (o localhost), Chrome, Edge o Safari —no Firefox ni Chrome en Linux— y la página en primer plano.",
  "camara.microfono": "Micrófono",
  "camara.ocupado": "Ya hay una emisión en curso. Para OBS, o la otra cámara, para emitir desde aquí.",
  "camara.parada_dispositivo": "La emisión se paró porque la cámara o el micrófono cambiaron.",
  "camara.parada_oculta": "La emisión se paró al salir de la página: el navegador suspende la cámara en segundo plano.",
  "camara.parar": "Parar",
  "camara.permiso": "Permite el acceso a la cámara y al micrófono para continuar.",
  "camara.permiso_denegado": "Sin cámara y micrófono no se puede emitir. Da permiso en el navegador y recarga la página.",
  "camara.se_corto": "La emisión se cortó.",
  "camara.sin_aac": "Tu navegador no codifica AAC, que las plataformas exigen. Funciona en Chrome, Edge y Safari, salvo en Linux.",
  "camara.sin_apis": "Tu navegador no puede emitir: le faltan la cámara o los codificadores de vídeo.",
  "camara.sin_destinos": "No hay destinos encendidos: se emite, pero no llega a ninguna plataforma.",
  "camara.sin_h264": "Tu navegador no codifica H.264, que es lo que aceptan las plataformas.",
  "camara.sin_https": "La cámara necesita HTTPS. Abre el panel por https:// (TLS integrado, proxy con certificado o túnel) o desde localhost.",
  "camara.subida_insuficiente": "Tu conexión no da para este bitrate. Prueba con 720p.",
  "camara.titulo": "Emitir desde este dispositivo",
```

Y a `web/src/i18n/en.json`:

```json
  "app.camara": "Camera",
  "camara.aviso_salir": "If you leave the page, the broadcast stops.",
  "camara.calidad": "Quality",
  "camara.calidad_1080": "1080p · 4.5 Mbps",
  "camara.calidad_720": "720p · 2.5 Mbps",
  "camara.camara": "Camera",
  "camara.comprobando": "Checking the browser…",
  "camara.conectando": "Connecting…",
  "camara.consejo_orientacion": "Rotate the phone before going live: changing orientation mid-stream stops the broadcast.",
  "camara.destinos": "Destinations",
  "camara.dispositivo_sin_nombre": "Device {n}",
  "camara.emitir": "Go live",
  "camara.en_vivo": "Live from this device",
  "camara.fallo_codificar": "The encoder failed. Try 720p.",
  "camara.intro": "This device's camera and microphone go out to your destinations, no OBS needed.",
  "camara.limites": "Needs HTTPS (or localhost), Chrome, Edge or Safari —not Firefox nor Chrome on Linux— and the page in the foreground.",
  "camara.microfono": "Microphone",
  "camara.ocupado": "A broadcast is already in progress. Stop OBS, or the other camera, to go live from here.",
  "camara.parada_dispositivo": "The broadcast stopped because the camera or microphone changed.",
  "camara.parada_oculta": "The broadcast stopped when you left the page: browsers suspend the camera in the background.",
  "camara.parar": "Stop",
  "camara.permiso": "Allow access to the camera and microphone to continue.",
  "camara.permiso_denegado": "Without camera and microphone there is nothing to broadcast. Grant permission in the browser and reload.",
  "camara.se_corto": "The broadcast was cut off.",
  "camara.sin_aac": "Your browser can't encode AAC, which the platforms require. It works in Chrome, Edge and Safari, except on Linux.",
  "camara.sin_apis": "Your browser can't broadcast: it lacks the camera or the video encoders.",
  "camara.sin_destinos": "No destinations are on: you are broadcasting, but it reaches no platform.",
  "camara.sin_h264": "Your browser can't encode H.264, which is what the platforms accept.",
  "camara.sin_https": "The camera needs HTTPS. Open the panel over https:// (built-in TLS, a proxy with a certificate or a tunnel) or from localhost.",
  "camara.subida_insuficiente": "Your connection can't sustain this bitrate. Try 720p.",
  "camara.titulo": "Go live from this device",
```

- [ ] **Step 3: Detección de soporte**

Crear `web/src/camara/soporte.js`:

```js
// Detección de soporte (spec cámara §5): el primer fallo decide el aviso, en este orden,
// porque cada uno tiene una solución distinta (HTTPS, otro navegador, otra plataforma).
export const CODEC_VIDEO = 'avc1.42001f' // H.264 baseline nivel 3.1: sin B-frames, 720p30
export const CODEC_VIDEO_1080 = 'avc1.420028' // baseline nivel 4.0, para 1080p30
export const CODEC_AUDIO = 'mp4a.40.2' // AAC-LC

export async function detectarSoporte() {
  if (!window.isSecureContext) return { ok: false, motivoKey: 'camara.sin_https' }
  if (!navigator.mediaDevices?.getUserMedia || typeof VideoEncoder === 'undefined' ||
      typeof AudioEncoder === 'undefined' || typeof VideoFrame === 'undefined') {
    return { ok: false, motivoKey: 'camara.sin_apis' }
  }
  const video = await VideoEncoder.isConfigSupported({
    codec: CODEC_VIDEO, width: 1280, height: 720, bitrate: 2_500_000, framerate: 30, avc: { format: 'avc' },
  }).catch(() => null)
  if (!video?.supported) return { ok: false, motivoKey: 'camara.sin_h264' }
  const audio = await AudioEncoder.isConfigSupported({
    codec: CODEC_AUDIO, sampleRate: 48000, numberOfChannels: 2, bitrate: 128_000,
  }).catch(() => null)
  if (!audio?.supported) return { ok: false, motivoKey: 'camara.sin_aac' }
  return { ok: true, motivoKey: null }
}
```

- [ ] **Step 4: La página**

Crear `web/src/pages/Camara.vue`:

```vue
<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useQuasar } from 'quasar'
import { usePanel } from '@/stores/panel'
import { t } from '@/i18n'
import { iCamara, iMicrofono, iSenal } from '@/iconos'
import ChipEstado from '@/components/ChipEstado.vue'
import { diagnosticar } from '@/diagnostico'
import { detectarSoporte } from '@/camara/soporte'

// La cámara del navegador como fuente (spec cámara §5). Esta página es dueña del stream
// de captura y de su ciclo de vida; codificar y enviar es cosa de Emisor (camara/emisor.js).
const $q = useQuasar()
const panel = usePanel()

const soporte = ref(null)       // null = comprobando; {ok, motivoKey}
const permisoKey = ref(null)    // aviso mientras no hay stream
const camaras = ref([])
const microfonos = ref([])
const camaraId = ref(null)
const microfonoId = ref(null)
const calidad = ref('720p')
const videoEl = ref(null)
const emitiendo = ref(false)
const conectando = ref(false)
const motivoFin = ref(null)     // texto ya traducido del último corte, o null
// El stream no es reactivo a propósito (Vue envolvería cada pista en un proxy); hayStream
// es la señal que la plantilla mira.
let stream = null
const hayStream = ref(false)
let calidadAbierta = null

// Se deshabilita cuando hay sesión de OTRA fuente: OBS o una cámara en otro dispositivo.
// Cuando la sesión es la nuestra, el botón es «Parar».
const ocupado = computed(() => panel.haySesion && !emitiendo.value)
const listo = computed(() => soporte.value?.ok && hayStream.value && !ocupado.value)

const opcionesCalidad = computed(() => [
  { label: t('camara.calidad_720'), value: '720p' },
  { label: t('camara.calidad_1080'), value: '1080p' },
])
function nombre(d, i) {
  return d.label || t('camara.dispositivo_sin_nombre', { n: i + 1 })
}

async function pararStream() {
  if (stream) { for (const pista of stream.getTracks()) pista.stop() }
  stream = null
  hayStream.value = false
  if (videoEl.value) videoEl.value.srcObject = null
}

// Dice si lo que está abierto ya es lo que piden los selectores: evita reabrir la cámara
// cuando abrirCamara() rellena los ids con los del stream recién abierto.
function yaAbierto() {
  if (!stream || calidadAbierta !== calidad.value) return false
  const cam = stream.getVideoTracks()[0]?.getSettings().deviceId
  const mic = stream.getAudioTracks()[0]?.getSettings().deviceId
  return cam === camaraId.value && mic === microfonoId.value
}

// Abre la cámara y el micrófono elegidos. Sin ids (primera vez) deja que el navegador
// elija: en el móvil, la frontal, que es la que quiere quien se emite a sí mismo.
async function abrirCamara() {
  await pararStream()
  const alto = calidad.value === '1080p' ? 1080 : 720
  const restricciones = {
    video: camaraId.value
      ? { deviceId: { exact: camaraId.value }, height: { ideal: alto }, frameRate: { ideal: 30 } }
      : { facingMode: 'user', height: { ideal: alto }, frameRate: { ideal: 30 } },
    audio: microfonoId.value
      ? { deviceId: { exact: microfonoId.value }, echoCancellation: true, noiseSuppression: true }
      : { echoCancellation: true, noiseSuppression: true },
  }
  try {
    stream = await navigator.mediaDevices.getUserMedia(restricciones)
    permisoKey.value = null
  } catch {
    permisoKey.value = 'camara.permiso_denegado'
    return
  }
  videoEl.value.srcObject = stream
  calidadAbierta = calidad.value
  hayStream.value = true
  // Las etiquetas de enumerateDevices solo se rellenan tras conceder permiso.
  const dispositivos = await navigator.mediaDevices.enumerateDevices()
  camaras.value = dispositivos.filter((d) => d.kind === 'videoinput')
  microfonos.value = dispositivos.filter((d) => d.kind === 'audioinput')
  camaraId.value = stream.getVideoTracks()[0]?.getSettings().deviceId ?? camaraId.value
  microfonoId.value = stream.getAudioTracks()[0]?.getSettings().deviceId ?? microfonoId.value
}

onMounted(async () => {
  soporte.value = await detectarSoporte()
  if (!soporte.value.ok) return
  permisoKey.value = 'camara.permiso'
  await abrirCamara()
})
onBeforeUnmount(() => { pararStream() })

// Cambiar de dispositivo o de calidad reabre la captura; mientras se emite los controles
// están deshabilitados, así que aquí nunca hay emisión en curso.
watch([camaraId, microfonoId, calidad], () => { if (stream && !emitiendo.value && !yaAbierto()) abrirCamara() })

function emitir() {
  $q.notify({ type: 'info', message: t('camara.conectando') })
}
function parar() {}
</script>

<template>
  <q-page class="q-pa-md q-pb-xl">
    <div class="pagina">
      <div class="row items-center q-mb-md">
        <div class="ss-t-22">{{ t('camara.titulo') }}</div>
        <q-space />
        <ChipEstado v-if="emitiendo" tono="emitiendo" :icono="iSenal" pulso tam="lg" anuncia :texto="t('camara.en_vivo')" />
      </div>
      <p class="ss-t-14 ss-muted">{{ t('camara.intro') }}</p>

      <q-banner v-if="soporte === null" class="q-mb-md" aria-busy="true">{{ t('camara.comprobando') }}</q-banner>
      <q-banner v-else-if="!soporte.ok" class="q-mb-md bg-warning text-dark" role="alert">{{ t(soporte.motivoKey) }}</q-banner>

      <template v-else>
        <q-card flat bordered class="q-mb-md">
          <div class="cuadro">
            <!-- muted y playsinline: sin ellos iOS no reproduce la vista local ni la deja
                 en la página; el sonido lo oye la plataforma, no quien emite. -->
            <video ref="videoEl" class="video" autoplay muted playsinline />
            <div v-if="permisoKey" class="aviso ss-t-14 ss-muted">{{ t(permisoKey) }}</div>
          </div>
        </q-card>

        <q-card flat bordered class="q-pa-md q-mb-md">
          <div class="row q-col-gutter-md">
            <div class="col-12 col-sm-6">
              <q-select v-model="camaraId" :options="camaras.map((d, i) => ({ label: nombre(d, i), value: d.deviceId }))"
                        emit-value map-options outlined dense :label="t('camara.camara')" :disable="emitiendo">
                <template #prepend><q-icon :name="iCamara" /></template>
              </q-select>
            </div>
            <div class="col-12 col-sm-6">
              <q-select v-model="microfonoId" :options="microfonos.map((d, i) => ({ label: nombre(d, i), value: d.deviceId }))"
                        emit-value map-options outlined dense :label="t('camara.microfono')" :disable="emitiendo">
                <template #prepend><q-icon :name="iMicrofono" /></template>
              </q-select>
            </div>
            <div class="col-12">
              <q-btn-toggle v-model="calidad" :options="opcionesCalidad" no-caps outline toggle-color="primary"
                            :disable="emitiendo" :aria-label="t('camara.calidad')" />
            </div>
          </div>
          <p class="ss-t-14 ss-muted q-mt-md q-mb-none">{{ t('camara.consejo_orientacion') }}</p>
        </q-card>

        <q-banner v-if="ocupado" class="q-mb-md" role="status">{{ t('camara.ocupado') }}</q-banner>
        <q-banner v-if="motivoFin" class="q-mb-md bg-warning text-dark" role="alert">{{ motivoFin }}</q-banner>

        <div class="row items-center q-gutter-sm q-mb-lg">
          <q-btn v-if="!emitiendo" unelevated no-caps color="primary" size="lg" :label="t('camara.emitir')"
                 :disable="!listo" :loading="conectando" @click="emitir" />
          <q-btn v-else unelevated no-caps color="negative" size="lg" :label="t('camara.parar')" @click="parar" />
        </div>

        <div class="ss-t-16 q-mb-sm">{{ t('camara.destinos') }}</div>
        <p v-if="!panel.destinos.some((d) => d.enabled)" class="ss-t-14 ss-muted">{{ t('camara.sin_destinos') }}</p>
        <div v-else class="row q-gutter-sm">
          <ChipEstado v-for="d in panel.destinos.filter((x) => x.enabled)" :key="d.id"
                      :tono="diagnosticar(d, panel.haySesion).tono" :texto="d.name" />
        </div>

        <p class="ss-t-14 ss-muted q-mt-lg">{{ t('camara.limites') }}</p>
      </template>
    </div>
  </q-page>
</template>

<style scoped>
.pagina { max-width: 960px; margin: 0 auto; padding: var(--ss-space-5) var(--ss-space-4); }
.cuadro {
  position: relative;
  aspect-ratio: 16 / 9;
  /* Negro: el fondo de «sin señal» de cualquier pantalla de vídeo, como en VistaPrevia. */
  background: #000;
}
.video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; display: block; }
.aviso {
  position: absolute; inset: 0;
  display: flex; align-items: center; justify-content: center; text-align: center;
  padding: var(--ss-space-4);
}
</style>
```

- [ ] **Step 5: Comprobar**

```bash
cd web && node scripts/i18n-check.mjs && npm run build
```

Expected: paridad OK y build sin errores. Luego, con el binario y el panel en desarrollo:

```bash
cd web && npm run dev      # en otra terminal: SPLITSTREAM_HTTP_ADDR=:8099 go run ./cmd/splitstream
```

Abrir `http://localhost:5173/camara` (localhost es contexto seguro): la página pide permiso, enseña la cámara en el recuadro y lista los dispositivos con nombre. Cambiar de cámara reabre la vista. Con Chrome a 375 px (iframe de QA), las cinco pestañas caben con solo icono.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat(panel): página Cámara con detección de soporte y vista local"
```

---

### Task 8: `Emisor`: captura, codificación y envío

**Files:**
- Create: `web/src/camara/emisor.js`
- Create: `web/src/camara/worklet-audio.js`

**Interfaces:**
- Consumes: `CODEC_VIDEO`, `CODEC_VIDEO_1080`, `CODEC_AUDIO` (Task 7); el protocolo de Task 6.
- Produces: `class Emisor { constructor({ video, stream, calidad, onFin }); async iniciar(); parar(motivo) }` y `CALIDADES`. `iniciar()` resuelve cuando el servidor confirmó `session_id`; rechaza con `Error` si el WebSocket cerró antes (con `error.message` = motivo del servidor) o si la captura falló. `onFin(motivoTexto|null)` se llama exactamente una vez cuando la emisión termina por cualquier causa que no sea `parar()` del usuario (`null` solo si fue `parar()`).

- [ ] **Step 1: El worklet**

Crear `web/src/camara/worklet-audio.js`:

```js
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
    const n = entrada[0].length
    for (let c = 0; c < canales; c++) this.buf[c].set(entrada[c], this.lleno)
    this.lleno += n
    if (this.lleno >= FRAME) {
      const planar = new Float32Array(FRAME * canales)
      for (let c = 0; c < canales; c++) planar.set(this.buf[c], c * FRAME)
      this.port.postMessage({ planar, canales, frames: FRAME }, [planar.buffer])
      this.lleno = 0
    }
    return true
  }
}

registerProcessor('acumulador', Acumulador)
```

- [ ] **Step 2: El emisor**

Crear `web/src/camara/emisor.js`:

```js
import { CODEC_VIDEO, CODEC_VIDEO_1080, CODEC_AUDIO } from './soporte'

// Emisor: captura → WebCodecs → WebSocket /api/camera/ws (spec cámara §3 y §5).
//
// Un solo reloj para audio y vídeo: performance.now() al pulsar «Emitir» es el cero, y
// cada frame y cada bloque de audio llevan microsegundos desde ahí; al enviar se pasan a
// milisegundos, que es lo que RTMP usa. El servidor no corrige nada, igual que con OBS.
export const CALIDADES = {
  '720p': { width: 1280, height: 720, videoBitrate: 2_500_000, codec: CODEC_VIDEO },
  '1080p': { width: 1920, height: 1080, videoBitrate: 4_500_000, codec: CODEC_VIDEO_1080 },
}
const FPS = 30
const KEYFRAME_US = 2_000_000 // un keyframe cada 2 s: las plataformas piden GOP ≤ 4 s
const AUDIO_BITRATE = { 1: 96_000, 2: 128_000 }
const COLA_MAX = 2 // frames pendientes en el codificador antes de descartar la captura

const MSG_START = 0x00
const MSG_VIDEO_CONFIG = 0x01
const MSG_VIDEO_FRAME = 0x02
const MSG_AUDIO_CONFIG = 0x03
const MSG_AUDIO_FRAME = 0x04

export class Emisor {
  constructor({ video, stream, calidad, onFin }) {
    this.video = video
    this.stream = stream
    this.calidad = CALIDADES[calidad] ?? CALIDADES['720p']
    this.onFin = onFin
    this.ws = null
    this.videoEnc = null
    this.audioEnc = null
    this.audioCtx = null
    this.origen = 0
    this.ultimoKey = -KEYFRAME_US
    this.forzarKey = false
    // Control de subida (spec §5): por encima de 1 s de bitrate en el buffer del socket
    // se descartan deltas hasta el siguiente keyframe; por encima de 5 s sostenidos se
    // para. El audio nunca se descarta: es pequeño y su hueco se nota más.
    this.umbral1s = this.calidad.videoBitrate / 8
    this.umbral5s = this.umbral1s * 5
    this.descartando = false
    this.terminado = false
    this.rvfc = 0
  }

  async iniciar() {
    // 1. Audio primero: hace falta saber cuántos canales entrega el micrófono antes de
    // declarar el start, y eso solo lo dice el primer bloque del worklet.
    const canales = await this.prepararAudio()
    const sampleRate = this.audioCtx.sampleRate

    // 2. WebSocket y start.
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    this.ws = new WebSocket(`${proto}://${location.host}/api/camera/ws`)
    this.ws.binaryType = 'arraybuffer'
    await new Promise((resolver, rechazar) => {
      this.ws.onopen = resolver
      this.ws.onerror = () => rechazar(new Error('ws'))
      this.ws.onclose = (ev) => rechazar(new Error(ev.reason || 'ws'))
    })
    const start = {
      width: this.calidad.width, height: this.calidad.height, framerate: FPS,
      video_bitrate: this.calidad.videoBitrate, audio_bitrate: AUDIO_BITRATE[canales],
      sample_rate: sampleRate, channels: canales,
    }
    this.ws.send(this.mensaje(MSG_START, new TextEncoder().encode(JSON.stringify(start))))
    await new Promise((resolver, rechazar) => {
      this.ws.onmessage = (ev) => { if (typeof ev.data === 'string') resolver() }
      this.ws.onclose = (ev) => rechazar(new Error(ev.reason || 'ws'))
    })
    // A partir de aquí el servidor solo habla para cerrar, y ese motivo es para el usuario.
    this.ws.onmessage = null
    this.ws.onclose = (ev) => this.terminar(ev.reason || '')

    // 3. Codificadores y captura.
    this.origen = performance.now()
    this.configurarVideo(this.calidad)
    this.configurarAudio(canales, sampleRate)
    this.capturarVideo()
  }

  async prepararAudio() {
    this.audioCtx = new AudioContext({ sampleRate: 48000 })
    await this.audioCtx.audioWorklet.addModule(new URL('./worklet-audio.js', import.meta.url))
    const fuente = this.audioCtx.createMediaStreamSource(this.stream)
    this.nodo = new AudioWorkletNode(this.audioCtx, 'acumulador', { numberOfInputs: 1, numberOfOutputs: 0 })
    fuente.connect(this.nodo)
    // Safari arranca el AudioContext suspendido hasta un gesto; el gesto fue pulsar
    // «Emitir», así que resume() aquí funciona.
    await this.audioCtx.resume()
    return new Promise((resolver) => {
      this.nodo.port.onmessage = ({ data }) => {
        // Hasta que haya codificador (después del start) los bloques se tiran.
        if (this.audioEnc) this.codificarAudio(data)
        resolver(data.canales)
      }
    })
  }

  configurarVideo({ width, height, videoBitrate, codec }) {
    this.videoEnc = new VideoEncoder({
      output: (chunk, meta) => {
        if (meta?.decoderConfig?.description) {
          this.enviar(this.mensaje(MSG_VIDEO_CONFIG, new Uint8Array(meta.decoderConfig.description)), false)
        }
        const esKey = chunk.type === 'key'
        const buf = new Uint8Array(6 + chunk.byteLength)
        buf[0] = MSG_VIDEO_FRAME
        buf[1] = esKey ? 0x01 : 0x00
        new DataView(buf.buffer).setUint32(2, Math.round(chunk.timestamp / 1000))
        chunk.copyTo(buf.subarray(6))
        this.enviar(buf, true, esKey)
      },
      error: () => this.terminar('fallo_codificar'),
    })
    this.videoEnc.configure({
      codec, width, height, bitrate: videoBitrate, framerate: FPS,
      avc: { format: 'avc' }, latencyMode: 'realtime',
    })
  }

  configurarAudio(canales, sampleRate) {
    this.muestras = 0
    this.audioBase = null
    this.audioEnc = new AudioEncoder({
      output: (chunk, meta) => {
        if (meta?.decoderConfig?.description) {
          this.enviar(this.mensaje(MSG_AUDIO_CONFIG, new Uint8Array(meta.decoderConfig.description)), false)
        }
        const buf = new Uint8Array(5 + chunk.byteLength)
        buf[0] = MSG_AUDIO_FRAME
        new DataView(buf.buffer).setUint32(1, Math.round(chunk.timestamp / 1000))
        chunk.copyTo(buf.subarray(5))
        this.enviar(buf, false)
      },
      error: () => this.terminar('fallo_codificar'),
    })
    this.audioEnc.configure({ codec: CODEC_AUDIO, sampleRate, numberOfChannels: canales, bitrate: AUDIO_BITRATE[canales] })
  }

  codificarAudio({ planar, canales, frames }) {
    if (!this.audioEnc || this.audioEnc.state !== 'configured') return
    // El primer bloque ancla el reloj de audio al común; los siguientes se cuentan por
    // muestras, que es más estable que la hora de llegada de cada mensaje del worklet.
    if (this.audioBase === null) this.audioBase = (performance.now() - this.origen) * 1000
    const timestamp = Math.round(this.audioBase + this.muestras * 1e6 / this.audioCtx.sampleRate)
    this.muestras += frames
    const datos = new AudioData({
      format: 'f32-planar', sampleRate: this.audioCtx.sampleRate, numberOfFrames: frames,
      numberOfChannels: canales, timestamp, data: planar,
    })
    this.audioEnc.encode(datos)
    datos.close()
  }

  // Un VideoFrame por cada fotograma que pinta el <video>. Es el camino que funciona en
  // Chrome, Safari y Firefox; MediaStreamTrackProcessor no está en Window en Safari.
  capturarVideo() {
    const paso = () => {
      if (this.terminado) return
      this.rvfc = this.video.requestVideoFrameCallback(paso)
      if (!this.videoEnc || this.videoEnc.state !== 'configured') return
      // Si el codificador va por detrás, se salta este fotograma: encolar más solo
      // añade latencia y acaba en un error de memoria en móviles.
      if (this.videoEnc.encodeQueueSize > COLA_MAX) return
      const timestamp = Math.round((performance.now() - this.origen) * 1000)
      const keyFrame = this.forzarKey || timestamp - this.ultimoKey >= KEYFRAME_US
      if (keyFrame) { this.ultimoKey = timestamp; this.forzarKey = false }
      const frame = new VideoFrame(this.video, { timestamp })
      try {
        this.videoEnc.encode(frame, { keyFrame })
      } finally {
        frame.close()
      }
    }
    this.rvfc = this.video.requestVideoFrameCallback(paso)
  }

  mensaje(tipo, cuerpo) {
    const buf = new Uint8Array(1 + cuerpo.byteLength)
    buf[0] = tipo
    buf.set(cuerpo, 1)
    return buf
  }

  enviar(buf, esVideo, esKey = false) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return
    const pendiente = this.ws.bufferedAmount
    if (pendiente > this.umbral5s) {
      this.terminar('subida_insuficiente')
      return
    }
    if (esVideo) {
      if (pendiente > this.umbral1s && !esKey) {
        // Se descarta hasta el siguiente keyframe, que se fuerza: un delta sin su pasado
        // es un GOP roto en la plataforma.
        this.descartando = true
        this.forzarKey = true
        return
      }
      if (esKey) this.descartando = false
      if (this.descartando) return
    }
    this.ws.send(buf)
  }

  // Parar por decisión del usuario: sin motivo, y sin onFin.
  parar() {
    this.limpiar()
  }

  // Terminar por cualquier otra causa: el motivo va a la página. Los motivos propios
  // viajan como clave corta (la página los traduce); los del servidor ya vienen en el
  // idioma del panel, negociado en el handshake.
  terminar(motivo) {
    if (this.terminado) return
    this.limpiar()
    this.onFin?.(motivo)
  }

  limpiar() {
    if (this.terminado) return
    this.terminado = true
    if (this.rvfc) this.video.cancelVideoFrameCallback(this.rvfc)
    if (this.videoEnc && this.videoEnc.state !== 'closed') this.videoEnc.close()
    if (this.audioEnc && this.audioEnc.state !== 'closed') this.audioEnc.close()
    this.videoEnc = this.audioEnc = null
    if (this.nodo) { this.nodo.port.onmessage = null; this.nodo.disconnect() }
    if (this.audioCtx) this.audioCtx.close().catch(() => {})
    if (this.ws) {
      this.ws.onclose = null
      if (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING) this.ws.close(1000)
      this.ws = null
    }
  }
}
```

- [ ] **Step 3: Comprobar que compila**

```bash
cd web && npm run build
```

Expected: build sin errores y un asset `worklet-audio-*.js` aparte en `dist/spa/assets` (Vite separa lo que se referencia con `new URL(..., import.meta.url)`).

- [ ] **Step 4: Commit**

```bash
git add web/src/camara
git commit -m "feat(panel): Emisor con WebCodecs, worklet de audio y control de subida"
```

---

### Task 9: Conectar el emisor a la página: emitir, parar, wake lock y visibilidad

**Files:**
- Modify: `web/src/pages/Camara.vue`

**Interfaces:**
- Consumes: `Emisor` (Task 8), `panel.sesion.source`.
- Produces: la página completa. `onFin` con clave corta (`fallo_codificar`, `subida_insuficiente`) se traduce con `t('camara.' + clave)`; con texto del servidor se enseña tal cual; vacío → `t('camara.se_corto')`.

- [ ] **Step 1: Script**

En `web/src/pages/Camara.vue`, en el `import` de iconos ya está todo. Añadir tras `import { detectarSoporte } from '@/camara/soporte'`:

```js
import { Emisor } from '@/camara/emisor'
```

Sustituir las dos funciones vacías del final del script

```js
function emitir() {
  $q.notify({ type: 'info', message: t('camara.conectando') })
}
function parar() {}
```

por:

```js
let emisor = null
let wakeLock = null

function textoFin(motivo) {
  if (!motivo) return t('camara.se_corto')
  return motivo.includes(' ') ? motivo : t('camara.' + motivo)
}

async function pedirWakeLock() {
  try { wakeLock = await navigator.wakeLock?.request('screen') } catch { wakeLock = null }
}
function soltarWakeLock() {
  wakeLock?.release().catch(() => {})
  wakeLock = null
}

async function emitir() {
  if (!listo.value) return
  motivoFin.value = null
  conectando.value = true
  const nuevo = new Emisor({
    video: videoEl.value, stream, calidad: calidad.value,
    onFin: (motivo) => {
      emitiendo.value = false
      soltarWakeLock()
      motivoFin.value = textoFin(motivo)
      emisor = null
    },
  })
  try {
    await nuevo.iniciar()
    emisor = nuevo
    emitiendo.value = true
    await pedirWakeLock()
  } catch (e) {
    nuevo.parar()
    motivoFin.value = textoFin(e.message === 'ws' ? '' : e.message)
  } finally {
    conectando.value = false
  }
}

function parar() {
  emisor?.parar()
  emisor = null
  emitiendo.value = false
  soltarWakeLock()
}

// Página oculta = cámara suspendida en el móvil (spec §1.4): se para y se dice por qué,
// en vez de dejar una emisión medio viva que la plataforma corta un minuto después.
function alCambiarVisibilidad() {
  if (document.hidden && emitiendo.value) {
    parar()
    motivoFin.value = t('camara.parada_oculta')
  }
}
// Si la cámara o el micrófono desaparecen (cable, otro app que los toma), no hay nada
// que codificar: se para con motivo.
function alTerminarPista() {
  if (emitiendo.value) {
    parar()
    motivoFin.value = t('camara.parada_dispositivo')
  }
}
function antesDeSalir(ev) {
  if (!emitiendo.value) return
  ev.preventDefault()
  ev.returnValue = t('camara.aviso_salir')
}

onMounted(() => {
  document.addEventListener('visibilitychange', alCambiarVisibilidad)
  window.addEventListener('beforeunload', antesDeSalir)
})
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', alCambiarVisibilidad)
  window.removeEventListener('beforeunload', antesDeSalir)
  parar()
})
```

En `abrirCamara()`, justo después de `videoEl.value.srcObject = stream`, añadir:

```js
  for (const pista of stream.getTracks()) pista.addEventListener('ended', alTerminarPista)
```

- [ ] **Step 2: Comprobar en el navegador**

```bash
cd web && node scripts/i18n-check.mjs && npm run build
```

Expected: paridad OK, build OK. Luego con `npm run dev` + binario en `:8099` y al menos un destino de prueba (un mediamtx local, o un destino real apagado para ver solo la sesión):

1. Abrir `http://localhost:5173/camara`, pulsar «Emitir»: el chip «Emitiendo desde este dispositivo» aparece, y en la pestaña Panel la sesión sale en vivo con resolución 1280×720 y bitrate ≈ 2,6 Mbps al cabo de unos segundos.
2. «Vista previa» desde el Panel en otra pestaña enseña la cámara.
3. Con la grabación encendida en Ajustes, aparece un archivo en Grabaciones; `ffprobe` sobre él dice `h264` y `aac`.
4. «Parar»: la sesión cierra en el Panel y el historial la lista.
5. Emitir, cambiar a otra pestaña del navegador (no otra ruta del panel): la emisión se para con el aviso de página oculta.
6. Con OBS emitiendo, la página enseña «Ya hay una emisión en curso» y el botón deshabilitado.
7. Con Firefox: el aviso de AAC y sin botón.
8. Abrir por `http://<ip-lan>:5173/camara`: aviso de HTTPS.

Cualquier fallo que aparezca aquí y no en los tests es de esta tarea: arreglarlo antes del commit.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/Camara.vue
git commit -m "feat(panel): emitir y parar desde la página Cámara, wake lock y visibilidad"
```

---

### Task 10: Test de integración: la cámara es «otro OBS» para lo que hay detrás

**Files:**
- Create: `test/integration/camera_test.go`

**Interfaces:**
- Consumes: `httpapi.New`, `httpapi.Config`, `relay.NewHub/NewEngine/NewSink`, `rtmpio.NewPublisher`, `store.Open/Bootstrap/SetPasswordHash`, `crypto.HashPassword`, `adapter` y `sinkA` (existentes en `relay_test.go`), `probeStream` (en `fanout_test.go`).

- [ ] **Step 1: Escribir el test**

Crear `test/integration/camera_test.go`:

```go
//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/httpapi"
	"github.com/aprendomx/splitstream/internal/relay"
	"github.com/aprendomx/splitstream/internal/rtmpio"
	"github.com/aprendomx/splitstream/internal/store"
)

// tagFLV es un tag de media leído de un archivo FLV: lo justo para hacer de navegador.
type tagFLV struct {
	tipo byte // 8 audio, 9 vídeo
	ts   uint32
	body []byte
}

// leerFLV recorre un archivo FLV (cabecera de 9 bytes, y por cada tag 11 bytes de cabecera,
// el cuerpo y 4 bytes de PreviousTagSize) y devuelve los tags de audio y vídeo.
func leerFLV(t *testing.T, path string) []tagFLV {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leer %s: %v", path, err)
	}
	if len(b) < 13 || string(b[:3]) != "FLV" {
		t.Fatalf("%s no es un FLV", path)
	}
	var tags []tagFLV
	for pos := 13; pos+11 <= len(b); {
		tipo := b[pos]
		size := int(b[pos+1])<<16 | int(b[pos+2])<<8 | int(b[pos+3])
		ts := uint32(b[pos+4])<<16 | uint32(b[pos+5])<<8 | uint32(b[pos+6]) | uint32(b[pos+7])<<24
		pos += 11
		if pos+size > len(b) {
			break
		}
		if tipo == 8 || tipo == 9 {
			tags = append(tags, tagFLV{tipo: tipo, ts: ts, body: b[pos : pos+size]})
		}
		pos += size + 4
	}
	return tags
}

// TestCameraEndToEnd hace de navegador: manda por /api/camera/ws lo que WebCodecs
// entregaría —avcC, NALUs AVCC, ASC y AAC crudo, sacados de un FLV que genera ffmpeg— y
// comprueba que un sink RTMP real recibe un stream decodificable con vídeo y audio.
func TestCameraEndToEnd(t *testing.T) {
	requireTool(t, "ffmpeg")
	requireTool(t, "ffprobe")
	requireSink(t, "localhost:19351")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// El «navegador»: un FLV corto con H.264 baseline sin B-frames y AAC.
	src := filepath.Join(t.TempDir(), "src.flv")
	gen := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "8", "-c:v", "libx264", "-preset", "ultrafast", "-profile:v", "baseline", "-bf", "0",
		"-pix_fmt", "yuv420p", "-g", "30", "-b:v", "800k",
		"-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2", "-y", "-f", "flv", src)
	if b, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg no pudo generar el FLV: %v\n%s", err, b)
	}
	tags := leerFLV(t, src)
	if len(tags) == 0 {
		t.Fatal("el FLV generado no tiene tags de media")
	}

	// Servidor: base, motor, un sink al mediamtx de pruebas y la API con el endpoint.
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { time.Sleep(300 * time.Millisecond); db.Close() }()
	cipher := testCipher(t)
	if err := db.Bootstrap(ctx, cipher); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	hash, err := crypto.HashPassword("secreta-de-prueba")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := db.SetPasswordHash(ctx, hash); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}

	streamName := fmt.Sprintf("cam%d", time.Now().UnixNano())
	hub := relay.NewHub(nil)
	defer hub.Close()
	engine := relay.NewEngine(relay.EngineConfig{Hub: hub, Store: adapter{db}})
	engine.SetSinkProvider(func(int64) ([]*relay.Sink, error) {
		pub, err := rtmpio.NewPublisher(rtmpio.PublisherConfig{URL: sinkA, StreamKey: crypto.Secret(streamName)})
		if err != nil {
			return nil, err
		}
		return []*relay.Sink{relay.NewSink(relay.SinkConfig{ID: 1, Name: "sink-a", Pub: pub})}, nil
	})

	var master [32]byte
	copy(master[:], bytes.Repeat([]byte{7}, 32))
	api, err := httpapi.New(httpapi.Config{DB: db, Cipher: cipher, Engine: engine, MasterKey: master})
	if err != nil {
		t.Fatalf("httpapi.New: %v", err)
	}
	ts := httptest.NewServer(api.Handler())
	defer ts.Close()

	// Sesión del panel: la cámara se autentica con la cookie, no con la clave RTMP.
	resp, err := http.Post(ts.URL+"/api/auth/login", "application/json", strings.NewReader(`{"password":"secreta-de-prueba"}`))
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login: %v (código %v)", err, resp)
	}
	h := http.Header{}
	var partes []string
	for _, c := range resp.Cookies() {
		partes = append(partes, c.Name+"="+c.Value)
	}
	h.Set("Cookie", strings.Join(partes, "; "))
	resp.Body.Close()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/camera/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.CloseNow()

	start, _ := json.Marshal(map[string]any{
		"width": 640, "height": 360, "framerate": 30, "video_bitrate": 800000,
		"audio_bitrate": 128000, "sample_rate": 48000, "channels": 2,
	})
	if err := conn.Write(ctx, websocket.MessageBinary, append([]byte{0x00}, start...)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, data, err := conn.Read(ctx); err != nil || !strings.Contains(string(data), "session_id") {
		t.Fatalf("confirmación: %v %q", err, data)
	}

	// Reproducir los tags a su ritmo en una goroutine, quitándoles la cabecera FLV: lo
	// que queda es lo que WebCodecs entrega. Los fallos del emisor salen por el canal
	// (t.Fatalf no se puede llamar fuera de la goroutine del test).
	errEnvio := make(chan error, 1)
	go func() {
		inicio := time.Now()
		for _, tg := range tags {
			var msg []byte
			switch {
			case tg.tipo == 9 && tg.body[1] == 0x00:
				msg = append([]byte{0x01}, tg.body[5:]...)
			case tg.tipo == 9:
				flags := byte(0)
				if tg.body[0]>>4 == 1 {
					flags = 1
				}
				msg = append([]byte{0x02, flags, 0, 0, 0, 0}, tg.body[5:]...)
				binary.BigEndian.PutUint32(msg[2:6], tg.ts)
			case tg.tipo == 8 && tg.body[1] == 0x00:
				msg = append([]byte{0x03}, tg.body[2:]...)
			default:
				msg = append([]byte{0x04, 0, 0, 0, 0}, tg.body[2:]...)
				binary.BigEndian.PutUint32(msg[1:5], tg.ts)
			}
			if espera := time.Duration(tg.ts)*time.Millisecond - time.Since(inicio); espera > 0 {
				time.Sleep(espera)
			}
			if err := conn.Write(ctx, websocket.MessageBinary, msg); err != nil {
				errEnvio <- fmt.Errorf("enviar tag ts=%d: %w", tg.ts, err)
				return
			}
		}
		errEnvio <- nil
	}()

	// A los 4 s de emisión, leer del sink mientras sigue llegando media.
	time.Sleep(4 * time.Second)
	got := probeStream(t, ctx, fmt.Sprintf("%s/%s", sinkA, streamName), filepath.Join(t.TempDir(), "out.flv"))
	t.Logf("ffprobe del stream retransmitido:\n%s", got)
	for _, want := range []string{"codec_name=h264", "codec_name=aac"} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en la salida del sink:\n%s", want, got)
		}
	}
	if ses := engine.Session(); ses.ID == 0 || ses.Source != relay.SourceBrowser || ses.Width != 640 {
		t.Errorf("sesión = %+v, quería browser 640x360 en vivo", ses)
	}

	if err := <-errEnvio; err != nil {
		t.Fatal(err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(time.Second)
	if engine.SessionID() != 0 {
		t.Error("la sesión no se cerró al cerrar el WebSocket")
	}
}
```

- [ ] **Step 2: Comprobar que compila y que corre donde puede**

```bash
go vet -tags integration ./test/integration/
```

Expected: sin errores. Sin Docker en local, el test se salta (`requireSink` hace `t.Skip`); lo ejecuta el job `integration` de la CI (`make test-integration`). Si hay Docker: `make sinks-up && go test -tags integration ./test/integration/ -run TestCameraEndToEnd -v -count=1`.

- [ ] **Step 3: Commit**

```bash
git add test/integration/camera_test.go
git commit -m "test(integration): la cámara del navegador llega a un sink RTMP real"
```

---

### Task 11: Documentación

**Files:**
- Modify: `README.md`, `README.es.md`
- Modify: `docs/manual-de-usuario.md`
- Modify: `docs/comparativa.md`, `docs/comparison.md`

- [ ] **Step 1: README en inglés**

En `README.md`, borrar la subsección `### Streaming from the browser camera (planned)` entera de `## Scope`, y en el párrafo de Scope cambiar la primera frase `Restreaming and local recording.` por `Restreaming from OBS or from the browser camera, and local recording.`.

Insertar, justo antes de `## With Docker`, la sección:

```markdown
### Streaming from your phone

Open the panel on the phone or laptop, go to **Camera**, allow the camera and microphone,
pick the quality and press **Go live**. The browser encodes H.264 and AAC with WebCodecs
and sends them to the binary, which forwards them exactly as it forwards OBS: still no
transcoding, and recording, preview and metrics work the same. OBS and the camera take
turns: while one is live the other is refused.

What the browsers impose, and Splitstream cannot change:

- **Needs HTTPS.** Browsers only expose the camera and the encoders on a secure origin.
  Opening the panel over plain `http://` at a LAN address gives no camera; only
  `localhost` is exempt. Use the built-in TLS (`SPLITSTREAM_TLS_DOMAIN`), a proxy with a
  certificate, or a tunnel that terminates HTTPS (Tailscale with `tailscale cert`,
  Cloudflare Tunnel).
- **Chrome, Edge or Safari.** Firefox and Chrome on Linux encode H.264 but have no AAC
  encoder, and the platforms accept nothing else. iOS needs Safari 26 or later.
- **Keep the page in front.** Locking the screen or switching apps suspends the camera;
  the broadcast stops and the panel says why.
- **Pick orientation and quality before going live.** Changing them mid-stream stops it.
- **Not a webcam plugged into the server**, and not WebRTC/WHIP: both would need a
  capture or transcoding stack inside the binary.
```

- [ ] **Step 2: README en español**

En `README.es.md`, borrar la subsección `### Emitir desde la cámara del navegador (planeado)` de `## Alcance`, y en su párrafo cambiar `Retransmisión y grabación local.` por `Retransmisión desde OBS o desde la cámara del navegador, y grabación local.`.

Insertar, justo antes de `## Con Docker`:

```markdown
### Emitir desde el teléfono

Abre el panel en el teléfono o el portátil, entra en **Cámara**, permite la cámara y el
micrófono, elige la calidad y pulsa **Emitir**. El navegador codifica H.264 y AAC con
WebCodecs y se los manda al binario, que los reenvía exactamente igual que a OBS: sigue
sin transcodificar, y la grabación, la vista previa y las métricas funcionan igual. OBS y
la cámara se turnan: mientras una emite, la otra es rechazada.

Lo que imponen los navegadores, y Splitstream no puede cambiar:

- **Necesita HTTPS.** Los navegadores solo exponen la cámara y los codificadores en un
  origen seguro. Abrir el panel por `http://` en una dirección de la LAN no da cámara;
  solo `localhost` se libra. Usa el TLS integrado (`SPLITSTREAM_TLS_DOMAIN`), un proxy
  con certificado o un túnel que termine HTTPS (Tailscale con `tailscale cert`,
  Cloudflare Tunnel).
- **Chrome, Edge o Safari.** Firefox y Chrome en Linux codifican H.264 pero no tienen
  codificador AAC, y las plataformas no aceptan otra cosa. iOS necesita Safari 26 o
  posterior.
- **La página, delante.** Bloquear la pantalla o cambiar de app suspende la cámara; la
  emisión se para y el panel dice por qué.
- **Orientación y calidad se eligen antes de emitir.** Cambiarlas a mitad la corta.
- **No es una webcam enchufada al servidor**, ni WebRTC/WHIP: las dos exigirían meter
  captura o transcodificación en el binario.
```

- [ ] **Step 3: Manual y comparativa**

En `docs/manual-de-usuario.md`, tras la subsección `### Vista previa` (dentro de «3. Empieza a emitir»), añadir:

```markdown
### Emitir desde el teléfono, sin OBS

En la pestaña **Cámara** del panel eliges cámara, micrófono y calidad (720p o 1080p) y
pulsas **Emitir**. Lo que ves en el recuadro es lo que sale hacia tus canales; los chips
de abajo dicen cómo va cada uno. **Parar** cierra la sesión como si desconectaras OBS.

Tres cosas que conviene saber antes:

- El panel tiene que abrirse por **HTTPS** (o en `localhost`). Por `http://` en la IP de
  la red local el navegador no da acceso a la cámara, y la página te lo dice.
- Funciona en **Chrome, Edge y Safari** (iOS 26 o posterior). En Firefox y en Chrome
  para Linux no: les falta el codificador de audio que exigen las plataformas.
- **Deja la página delante.** Si bloqueas el teléfono o cambias de app, el navegador
  suspende la cámara y la emisión se para. Gira el teléfono y elige la calidad antes de
  pulsar «Emitir»: cambiarlas a mitad también la corta.

Si OBS está emitiendo, el botón queda deshabilitado con el aviso «Ya hay una emisión en
curso», y al revés: mientras la cámara emite, OBS es rechazado.
```

En `docs/comparativa.md` y `docs/comparison.md`, en la fila **Splitstream**, la celda «Relay a varios destinos» pasa a `Sí: N destinos RTMP/RTMPS a la vez, sin transcodificar; desde OBS o desde la cámara del navegador` (y en inglés `Yes: N RTMP/RTMPS destinations at once, no transcoding; from OBS or from the browser camera`).

- [ ] **Step 4: Comprobar y commit**

```bash
grep -n "planned\|planeado" README.md README.es.md   # no debe salir nada
git add README.md README.es.md docs/manual-de-usuario.md docs/comparativa.md docs/comparison.md
git commit -m "docs: emitir desde el teléfono en README, manual y comparativa"
```

---

### Task 12: Verificación final y cierre de la rama

**Files:** ninguno nuevo.

- [ ] **Step 1: Toda la suite, como en la CI**

```bash
make vet && make lint && make test
cd web && node scripts/i18n-check.mjs && npm run build && cd ..
go vet -tags integration ./test/integration/
go test ./internal/httpapi/ -run APIContract -count=1
CGO_ENABLED=0 go build -o /dev/null ./cmd/splitstream
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/splitstream
go list -deps ./internal/httpapi | grep -E 'go-rtmp|internal/rtmpio' && echo "FRONTERA ROTA" || echo "fronteras OK"
```

Expected: todo en verde y «fronteras OK». Cualquier fallo se arregla en la tarea a la que pertenece antes de seguir.

- [ ] **Step 2: Prueba manual con TLS y un teléfono**

Con el binario compilado (`make build`) y `SPLITSTREAM_TLS_DOMAIN` o un túnel HTTPS, abrir el panel en un iPhone (Safari 26+) y en un Android (Chrome) y emitir contra un destino real (YouTube o Twitch) durante cinco minutos mirando la plataforma: vídeo y audio en sincronía, sin reconexiones en el panel. Anotar en el ledger de la implementación lo que se vio, como en las fases anteriores.

- [ ] **Step 3: Cerrar la rama**

Invocar `superpowers:finishing-a-development-branch` para integrar `feat/camara-navegador` en `main` según el flujo del proyecto (PR con la descripción del cambio y la referencia al spec).
