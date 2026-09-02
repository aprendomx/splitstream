// Package rtmpio conecta el motor de relay con la red: el servidor de ingesta y el
// cliente que publica hacia las plataformas.
package rtmpio

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yutopp/go-rtmp"
	"github.com/yutopp/go-rtmp/message"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/relay"
)

// ErrUnsupportedScheme indica que la URL del destino no es rtmp:// ni rtmps://.
var ErrUnsupportedScheme = errors.New("esquema no soportado: usa rtmp:// o rtmps://")

// DefaultChunkSize es el tamaño de chunk que se negocia con el destino. Subirlo desde los
// 128 por defecto reduce el overhead de cabeceras; el spike lo verificó (spec §16.3).
const DefaultChunkSize = 4096

// Identificadores de chunk stream. Separar audio y video es la convención habitual.
const (
	csCommand = 3
	csAudio   = 4
	csVideo   = 5
)

// connectTimeout acota Connect para todo el que llame, incluso si pasa un contexto sin
// deadline propio (el camino real: cmd/splitstream/main.go pasa el contexto de señales,
// que es cancelable pero no tiene plazo). Connect deriva su propio contexto con este
// timeout en vez de confiar en el ajeno.
const connectTimeout = 15 * time.Second

// target es una URL de destino ya descompuesta en lo que necesita go-rtmp.
type target struct {
	scheme string // exactamente "rtmp" o "rtmps": go-rtmp compara con estos literales
	addr   string // host:puerto, con el puerto por defecto ya resuelto
	app    string // la app RTMP, sin barras al principio ni al final
}

// parseTarget descompone una URL de destino.
//
// El esquema decide si se usa Dial o TLSDial, y go-rtmp compara contra los literales
// "rtmp" y "rtmps" respectivamente: pasar el equivocado devuelve "Unknown protocol"
// (spec §16.1).
func parseTarget(rawURL string) (target, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return target{}, fmt.Errorf("%w: %s", ErrUnsupportedScheme, rawURL)
	}

	var defaultPort string
	switch u.Scheme {
	case "rtmp":
		defaultPort = "1935"
	case "rtmps":
		defaultPort = "443"
	default:
		return target{}, fmt.Errorf("%w: %q", ErrUnsupportedScheme, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return target{}, errors.New("la URL del destino no tiene host")
	}
	port := u.Port()
	if port == "" {
		port = defaultPort
	}

	app := strings.Trim(u.Path, "/")
	if app == "" {
		return target{}, errors.New("la URL del destino no tiene app (la parte tras el host)")
	}

	return target{scheme: u.Scheme, addr: net.JoinHostPort(host, port), app: app}, nil
}

// PublisherConfig son los datos para construir un Publisher.
type PublisherConfig struct {
	URL       string
	StreamKey crypto.Secret
	ChunkSize uint32
	Logger    *slog.Logger
}

// Publisher publica hacia una plataforma. Implementa relay.Publisher.
//
// Lo usa una sola goroutine (la de su sink), así que no es seguro para uso concurrente,
// salvo Close, que sí puede llamarse desde otra.
type Publisher struct {
	tgt       target
	key       crypto.Secret
	chunkSize uint32
	log       *slog.Logger

	mu     sync.Mutex
	conn   *rtmp.ClientConn
	stream *rtmp.Stream
	closed bool
}

// NewPublisher valida la URL y construye el publisher sin conectar todavía.
func NewPublisher(cfg PublisherConfig) (*Publisher, error) {
	tgt, err := parseTarget(cfg.URL)
	if err != nil {
		return nil, err
	}
	size := cfg.ChunkSize
	if size == 0 {
		size = DefaultChunkSize
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Publisher{
		tgt:       tgt,
		key:       cfg.StreamKey,
		chunkSize: size,
		// La clave va como crypto.Secret y se loguea enmascarada.
		log: log.With("destino_url", cfg.URL, "clave", cfg.StreamKey),
	}, nil
}

// Connect abre la conexión y deja el stream listo para recibir media.
func (p *Publisher) Connect(ctx context.Context) error {
	// connectTimeout acota Connect para TODO el que llame, incluso si pasa un contexto
	// sin deadline: derivamos el nuestro en vez de confiar en el ajeno. Sin esto, la
	// rama ctx.Done() del select de abajo nunca dispararía con un context.Background()
	// y un destino que acepta el TCP y luego se calla colgaría para siempre.
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	deadline, _ := ctx.Deadline()

	type dialResult struct {
		conn *rtmp.ClientConn
		err  error
	}
	// Con buffer: si abandonamos la espera, la goroutine puede escribir y terminar en
	// vez de quedarse bloqueada para siempre.
	results := make(chan dialResult, 1)

	go func() {
		var (
			conn *rtmp.ClientConn
			err  error
		)
		switch p.tgt.scheme {
		case "rtmps":
			conn, err = rtmp.DialWithTLSDialer(&tls.Dialer{
				NetDialer: &net.Dialer{Deadline: deadline},
				Config: &tls.Config{
					ServerName: hostOf(p.tgt.addr),
					MinVersion: tls.VersionTLS12,
				},
			}, "rtmps", p.tgt.addr, &rtmp.ConnConfig{})
		default:
			conn, err = rtmp.DialWithDialer(
				&net.Dialer{Deadline: deadline},
				"rtmp", p.tgt.addr, &rtmp.ConnConfig{})
		}
		results <- dialResult{conn: conn, err: err}
	}()

	// LIMITACIÓN CONOCIDA: el deadline del dialer acota la conexión TCP, no los reads del
	// handshake RTMP que go-rtmp hace después. Connect siempre retorna acotado, porque
	// deriva su propio contexto con connectTimeout — pero la goroutine que quedó haciendo
	// el dial sobrevive hasta que el peer cierre o el sistema mate el socket. Eliminar eso
	// exigiría que go-rtmp expusiera un constructor a partir de un net.Conn ya abierto,
	// para fijarle un SetDeadline; la v0.0.7 no lo expone.
	var conn *rtmp.ClientConn
	select {
	case <-ctx.Done():
		// Se abandona la espera, pero no la limpieza: cuando la goroutine termine por su
		// cuenta, se cierra la conexión que haya podido abrir.
		go func() {
			if r := <-results; r.conn != nil {
				r.conn.Close()
			}
		}()
		return fmt.Errorf("conectar a %s: %w", p.tgt.addr, ctx.Err())
	case r := <-results:
		if r.err != nil {
			return fmt.Errorf("conectar a %s: %w", p.tgt.addr, r.err)
		}
		conn = r.conn
	}

	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()

	tcURL := fmt.Sprintf("%s://%s/%s", p.tgt.scheme, p.tgt.addr, p.tgt.app)
	if err := conn.Connect(&message.NetConnectionConnect{
		Command: message.NetConnectionConnectCommand{
			App:      p.tgt.app,
			Type:     "nonprivate",
			FlashVer: "FMLE/3.0 (compatible; Splitstream)",
			TCURL:    tcURL,
		},
	}); err != nil {
		return fmt.Errorf("handshake connect con %s: %w", p.tgt.addr, err)
	}

	stream, err := conn.CreateStream(&message.NetConnectionCreateStream{}, p.chunkSize)
	if err != nil {
		return fmt.Errorf("createStream con %s: %w", p.tgt.addr, err)
	}

	// Algunas plataformas exigen releaseStream y FCPublish antes de publish. go-rtmp no
	// tiene helper para ellos, pero Stream.Write acepta un CommandMessage (spec §16).
	// Un rechazo aquí no es fatal: los destinos que no los esperan simplemente los ignoran.
	for _, cmd := range []string{"releaseStream", "FCPublish"} {
		if err := p.writeCommand(stream, cmd); err != nil {
			p.log.Debug("el destino no aceptó el comando previo", "comando", cmd, "err", err)
		}
	}

	if err := stream.Publish(&message.NetStreamPublish{
		PublishingName: p.key.Reveal(),
		PublishingType: "live",
	}); err != nil {
		return fmt.Errorf("publish en %s: %w", p.tgt.addr, err)
	}

	if err := stream.WriteSetChunkSize(p.chunkSize); err != nil {
		p.log.Debug("no se pudo fijar el chunk size", "err", err)
	}

	p.mu.Lock()
	p.stream = stream
	p.mu.Unlock()

	p.log.Info("publicando en el destino", "app", p.tgt.app)
	return nil
}

// writeCommand manda un comando AMF0 con objeto nulo y el nombre del stream, que es la
// forma de releaseStream y FCPublish.
func (p *Publisher) writeCommand(stream *rtmp.Stream, name string) error {
	buf := new(bytes.Buffer)
	enc := message.NewAMFEncoder(buf, message.EncodingTypeAMF0)
	if err := enc.Encode(nil); err != nil {
		return err
	}
	if err := enc.Encode(p.key.Reveal()); err != nil {
		return err
	}
	return stream.Write(csCommand, 0, &message.CommandMessage{
		CommandName:   name,
		TransactionID: 0,
		Encoding:      message.EncodingTypeAMF0,
		Body:          buf,
	})
}

// WriteMeta envía el onMetaData envuelto en @setDataFrame, sin el cual las plataformas lo
// ignoran y algunas rechazan el stream (spec §3.5).
func (p *Publisher) WriteMeta(ts uint32, payload []byte) error {
	stream, err := p.liveStream()
	if err != nil {
		return err
	}
	return stream.Write(csAudio, ts, &message.DataMessage{
		Name:     "@setDataFrame",
		Encoding: message.EncodingTypeAMF0,
		Body:     bytes.NewReader(payload),
	})
}

// WriteAudio envía un tag de audio tal cual.
func (p *Publisher) WriteAudio(ts uint32, payload []byte) error {
	stream, err := p.liveStream()
	if err != nil {
		return err
	}
	return stream.Write(csAudio, ts, &message.AudioMessage{Payload: bytes.NewReader(payload)})
}

// WriteVideo envía un tag de video tal cual.
func (p *Publisher) WriteVideo(ts uint32, payload []byte) error {
	stream, err := p.liveStream()
	if err != nil {
		return err
	}
	return stream.Write(csVideo, ts, &message.VideoMessage{Payload: bytes.NewReader(payload)})
}

func (p *Publisher) liveStream() (*rtmp.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("el publisher está cerrado")
	}
	if p.stream == nil {
		return nil, errors.New("el publisher no está conectado")
	}
	return p.stream, nil
}

// Close cierra el stream y la conexión. Es idempotente y tolera que Connect nunca se
// haya llamado o haya fallado a medias.
func (p *Publisher) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	conn, stream := p.conn, p.stream
	p.conn, p.stream = nil, nil
	p.mu.Unlock()

	if conn != nil && stream != nil {
		if err := conn.DeleteStream(&message.NetStreamDeleteStream{StreamID: stream.StreamID()}); err != nil {
			p.log.Debug("deleteStream falló al cerrar", "err", err)
		}
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// Comprobación en tiempo de compilación de que *Publisher cumple el contrato del relay.
var _ relay.Publisher = (*Publisher)(nil)
