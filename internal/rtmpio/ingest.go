package rtmpio

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/aprendomx/splitstream/internal/flv"
	"github.com/aprendomx/splitstream/internal/relay"
)

// ErrUnsupportedCodec indica que el publisher manda algo que no se puede retransmitir.
//
// Un relay puro no transcodifica, y HEVC o AV1 por enhanced-RTMP no los acepta Twitch,
// así que el fan-out sería imposible aunque se parsearan (spec §3.6).
var ErrUnsupportedCodec = errors.New("códec no soportado: configura H.264 + AAC en OBS")

// ErrBadStreamKey indica que la app o la clave del publisher no coinciden.
var ErrBadStreamKey = errors.New("app o clave de ingesta incorrectas")

// IngestHandler recibe lo que ocurre en la ingesta. Sus métodos se llaman desde la
// goroutine de la conexión, en orden.
type IngestHandler interface {
	// OnPublishStart valida al publisher. Devolver error rechaza la conexión.
	OnPublishStart(app, streamKey string) error
	// OnMessage entrega un mensaje de media ya clasificado.
	OnMessage(msg *relay.Message)
	// OnPublishEnd avisa de que el publisher se fue.
	OnPublishEnd()
}

// IngestConfig son los datos para construir el servidor de ingesta.
type IngestConfig struct {
	Addr    string
	Handler IngestHandler
	Logger  *slog.Logger
}

// Ingest es el servidor RTMP que recibe a OBS.
type Ingest struct {
	addr    string
	handler IngestHandler
	log     *slog.Logger

	mu     sync.Mutex
	srv    *rtmp.Server
	ln     net.Listener
	conns  map[net.Conn]struct{}
	closed bool
}

// NewIngest construye el servidor sin escuchar todavía.
func NewIngest(cfg IngestConfig) *Ingest {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Ingest{addr: cfg.Addr, handler: cfg.Handler, log: log}
}

// track registra una conexión aceptada. go-rtmp no lleva ese registro, y sin él su
// Server.Close() solo cierra el listener y deja publicando a quien ya estaba dentro.
func (i *Ingest) track(c net.Conn) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		// Llegó tarde: el servidor ya estaba cerrando.
		c.Close()
		return
	}
	if i.conns == nil {
		i.conns = make(map[net.Conn]struct{})
	}
	i.conns[c] = struct{}{}
}

// untrack olvida una conexión que ya terminó por su cuenta, para que el mapa no crezca
// sin límite a lo largo de una ejecución larga.
func (i *Ingest) untrack(c net.Conn) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.conns, c)
}

// ListenAndServe escucha en la dirección configurada y atiende hasta que se cierre.
func (i *Ingest) ListenAndServe() error {
	ln, err := net.Listen("tcp", i.addr)
	if err != nil {
		return fmt.Errorf("escuchar RTMP en %s: %w", i.addr, err)
	}
	return i.Serve(ln)
}

// Serve atiende sobre un listener ya abierto. Útil para tests con puerto efímero.
func (i *Ingest) Serve(ln net.Listener) error {
	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			i.track(conn)
			// NO fijes ConnConfig.Logger. go-rtmp loguea a nivel Info el comando de
			// publish completo, cuyo nombre de stream ES la clave. Dejándolo en nil, la
			// librería lo redirige a io.Discard y la clave no sale a ningún lado.
			return conn, &rtmp.ConnConfig{
				Handler: &ingestConn{
					handler: i.handler,
					log:     i.log,
					onClose: func() { i.untrack(conn) },
				},
			}
		},
	})

	i.mu.Lock()
	i.srv, i.ln = srv, ln
	i.mu.Unlock()

	i.log.Info("ingesta RTMP escuchando", "addr", ln.Addr().String())
	return srv.Serve(ln)
}

// Close deja de aceptar conexiones y cierra las que ya estaban abiertas.
//
// Lo segundo hay que hacerlo a mano: rtmp.Server.Close() de go-rtmp v0.0.7 solo cierra el
// listener y no rastrea las conexiones aceptadas, así que sin esto un OBS conectado
// seguiría publicando contra un proceso que ya se cree cerrado, y el handler nunca
// recibiría su OnPublishEnd.
func (i *Ingest) Close() error {
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return nil
	}
	i.closed = true
	srv := i.srv
	conns := make([]net.Conn, 0, len(i.conns))
	for c := range i.conns {
		conns = append(conns, c)
	}
	i.conns = nil
	i.mu.Unlock()

	var err error
	if srv != nil {
		err = srv.Close()
	}
	// Cerrar el socket hace que el bucle de lectura de go-rtmp falle, lo que dispara el
	// OnClose del handler y, con él, el OnPublishEnd que el consumidor espera.
	for _, c := range conns {
		c.Close()
	}
	return err
}

// DisconnectPublisher corta las publicaciones en curso SIN dejar de escuchar, y devuelve
// cuántas conexiones cerró.
//
// Existe para la rotación de la clave de ingesta con `disconnect_now` (spec §9): hay que
// echar a quien está publicando con la clave vieja y dejar el servidor listo para que
// vuelva a entrar con la nueva. Close() no sirve, porque cierra también el listener y
// entonces recuperar la ingesta exigiría reiniciar el proceso — justo lo que la rotación
// quiere evitar.
//
// Reutiliza el registro de conexiones que Close ya necesita, por la misma razón: go-rtmp
// v0.0.7 no las rastrea.
func (i *Ingest) DisconnectPublisher() int {
	i.mu.Lock()
	conns := make([]net.Conn, 0, len(i.conns))
	for c := range i.conns {
		conns = append(conns, c)
	}
	// El mapa se vacía pero NO se marca closed: el servidor sigue aceptando conexiones
	// nuevas, que es la diferencia entera con Close.
	i.conns = nil
	i.mu.Unlock()

	// Cerrar el socket hace fallar el bucle de lectura de go-rtmp, lo que dispara el
	// OnClose del handler y con él el OnPublishEnd que el motor espera para cerrar la
	// sesión en la base.
	for _, c := range conns {
		c.Close()
	}
	return len(conns)
}

// ingestConn atiende una conexión de publisher.
type ingestConn struct {
	rtmp.DefaultHandler
	handler IngestHandler
	log     *slog.Logger
	onClose func()

	app        string
	publishing bool
}

func (c *ingestConn) OnConnect(timestamp uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	c.app = cmd.Command.App
	return nil
}

func (c *ingestConn) OnPublish(ctx *rtmp.StreamContext, timestamp uint32, cmd *rtmpmsg.NetStreamPublish) error {
	// El error no revela cuál de las dos partes falló, para no ayudar a adivinar.
	if err := c.handler.OnPublishStart(c.app, cmd.PublishingName); err != nil {
		c.log.Warn("publisher rechazado", "app", c.app, "err", err)
		return err
	}
	c.publishing = true
	c.log.Info("publisher aceptado", "app", c.app)
	return nil
}

func (c *ingestConn) OnAudio(timestamp uint32, payload io.Reader) error {
	data, err := io.ReadAll(payload)
	if err != nil {
		return fmt.Errorf("leer tag de audio: %w", err)
	}
	msg, err := classifyAudio(timestamp, data)
	if err != nil {
		return err
	}
	c.handler.OnMessage(msg)
	return nil
}

func (c *ingestConn) OnVideo(timestamp uint32, payload io.Reader) error {
	data, err := io.ReadAll(payload)
	if err != nil {
		return fmt.Errorf("leer tag de video: %w", err)
	}
	msg, err := classifyVideo(timestamp, data)
	if err != nil {
		return err
	}
	c.handler.OnMessage(msg)
	return nil
}

func (c *ingestConn) OnSetDataFrame(timestamp uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	payload := make([]byte, len(data.Payload))
	copy(payload, data.Payload)
	c.handler.OnMessage(&relay.Message{
		Kind:      relay.KindMeta,
		Timestamp: timestamp,
		Payload:   payload,
	})
	return nil
}

func (c *ingestConn) OnClose() {
	if c.publishing {
		c.publishing = false
		c.handler.OnPublishEnd()
	}
	c.log.Info("publisher desconectado", "app", c.app)
	if c.onClose != nil {
		c.onClose()
	}
}

// classifyVideo convierte un tag de video en un relay.Message, rechazando lo que no se
// puede retransmitir. El payload se copia porque go-rtmp reutiliza sus buffers.
func classifyVideo(timestamp uint32, data []byte) (*relay.Message, error) {
	info, err := flv.InspectVideo(data)
	if err != nil {
		return nil, err
	}
	if info.IsEnhanced {
		return nil, fmt.Errorf("%w (enhanced-RTMP: HEVC o AV1)", ErrUnsupportedCodec)
	}
	if info.CodecID != flv.CodecIDAVC {
		return nil, fmt.Errorf("%w (codecID de video %d, se esperaba %d = H.264)",
			ErrUnsupportedCodec, info.CodecID, flv.CodecIDAVC)
	}

	payload := make([]byte, len(data))
	copy(payload, data)
	return &relay.Message{
		Kind:        relay.KindVideo,
		Timestamp:   timestamp,
		Payload:     payload,
		IsKeyframe:  info.IsKeyframe,
		IsSeqHeader: info.IsSequenceHeader,
	}, nil
}

// classifyAudio convierte un tag de audio en un relay.Message.
func classifyAudio(timestamp uint32, data []byte) (*relay.Message, error) {
	info, err := flv.InspectAudio(data)
	if err != nil {
		return nil, err
	}
	if info.SoundFormat != flv.SoundFormatAAC {
		return nil, fmt.Errorf("%w (soundFormat de audio %d, se esperaba %d = AAC)",
			ErrUnsupportedCodec, info.SoundFormat, flv.SoundFormatAAC)
	}

	payload := make([]byte, len(data))
	copy(payload, data)
	return &relay.Message{
		Kind:        relay.KindAudio,
		Timestamp:   timestamp,
		Payload:     payload,
		IsSeqHeader: info.IsSequenceHeader,
	}, nil
}
