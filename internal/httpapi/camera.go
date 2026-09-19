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

// valida acota lo que el cliente declara. El sample rate se acota al rango que AAC-LC
// admite (8000–96000 Hz): fuera de ahí no hay índice en la tabla del AudioSpecificConfig,
// así que un valor cualquiera acabaría en un onMetaData que miente y en un ASC imposible.
func (c cameraStart) valida() bool {
	return c.Width >= 16 && c.Width <= 4096 && c.Height >= 16 && c.Height <= 4096 &&
		c.Framerate > 0 && c.Framerate <= 120 && c.VideoBitrate > 0 && c.AudioBitrate > 0 &&
		c.SampleRate >= 8000 && c.SampleRate <= 96000 && (c.Channels == 1 || c.Channels == 2)
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

	// El WebSocket queda secuestrado en cuanto Accept lo hijack-ea: net/http deja de
	// vigilarlo, así que ni http.Server.Shutdown ni la cancelación del contexto de la
	// petición llegan aquí solos. s.cameraCtx es el gancho que DisconnectCameras acciona
	// desde main.go (el equivalente de Ingest.Close() para RTMP) para sacar al publisher
	// ANTES de que el apagado ordenado espere WaitIdle.
	//
	// Esto llama a conn.Close directamente, NO cancela el contexto que usan los Read de
	// abajo: coder/websocket trata cualquier Done() en el contexto de Read como un plazo
	// vencido y cierra el socket en crudo sin trama de cierre (setupReadTimeout), que es
	// justo lo que un cierre con código y motivo NO es. Cerrando aquí, el Read en curso
	// se desbloquea solo, con la trama de cierre ya en camino.
	stop := context.AfterFunc(s.cameraCtx, func() {
		conn.Close(cameraCloseShutdown, traducir(lang, "el servidor se está apagando"))
	})
	defer stop()

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
	// El motivo de cierre lo LEE una persona en el teléfono: va traducido y siempre el
	// mismo. El err de verdad se queda en el log del servidor, que es donde sirve; mandarlo
	// por el socket enseñaría entrañas (ruta de la base, dirección del destino) a quien solo
	// necesita saber que no se pudo empezar.
	if s.engine == nil {
		s.logger.Error("la cámara del navegador llegó sin motor configurado")
		conn.Close(websocket.StatusInternalError, traducir(lang, "no se pudo abrir la sesión de la cámara"))
		return
	}
	if err := s.engine.StartLocalSession(); err != nil {
		if errors.Is(err, relay.ErrSessionInProgress) {
			conn.Close(cameraCloseBusy, traducir(lang, "ya hay una emisión en curso"))
			return
		}
		s.logger.Error("no se pudo abrir la sesión de la cámara", "err", err)
		conn.Close(websocket.StatusInternalError, traducir(lang, "no se pudo abrir la sesión de la cámara"))
		return
	}
	// Pase lo que pase a partir de aquí —cierre del cliente, plazo vencido, mensaje
	// ilegal, o DisconnectCameras cerrando la conexión con el código 4004 en el apagado
	// ordenado— la sesión se cierra: es lo que apaga los sinks y lo que WaitIdle necesita
	// para que el apagado sea limpio.
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
		conn.Close(websocket.StatusInternalError, traducir(lang, "no se pudo abrir la sesión de la cámara"))
		return
	}
	s.engine.OnMessage(&relay.Message{Kind: relay.KindMeta, Payload: meta})

	// Un solo Session() para la confirmación y para el log: son la misma sesión, y
	// preguntarlo dos veces permitiría que el log nombrara otra si la primera se cerró
	// entre medias.
	sesionID := s.engine.Session().ID
	escritura, cancelEscritura := context.WithTimeout(ctx, wsWriteTimeout)
	err = wsjson.Write(escritura, conn, map[string]int64{"session_id": sesionID})
	cancelEscritura()
	if err != nil {
		return
	}
	s.logger.Info("cámara del navegador aceptada", "sesion_id", sesionID)

	// 4. Media hasta que el cliente se vaya.
	for {
		leer, cancel := context.WithTimeout(ctx, cameraReadTimeout)
		tipo, data, err := conn.Read(leer)
		cancel()
		if err != nil {
			// Si fue DisconnectCameras, el AfterFunc de más arriba ya mandó el 4004: no
			// hay que cerrar dos veces, solo dejar constancia y volver.
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
