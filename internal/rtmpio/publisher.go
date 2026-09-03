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
//
// `app` y `logApp` son dos valores distintos a propósito, y no son intercambiables:
//
//   - `app` es el protocolo. La app RTMP es el path entero de la URL del servidor, así
//     que con rtmp://host/a/b la app es "a/b". Recortarla rompería cualquier destino con
//     app anidada, como un nginx-rtmp propio. Es lo que viaja en el connect y en el tcURL.
//   - `logApp` es lo que se puede escribir en disco, y está VACÍO salvo que el path sea
//     anidado. La clave va en un campo aparte (PublisherConfig.StreamKey) y no debería
//     estar en la URL, pero si el usuario la pegó ahí, el path en un log la filtraría para
//     siempre. Con dos o más segmentos el primero es inequívocamente la app y nunca el
//     nombre del stream; con uno solo es ambiguo —`rtmp://host/live2` y
//     `rtmp://host/live_987_CLAVE` son indistinguibles desde aquí— y la §8 del spec no
//     admite un "casi nunca", así que no se loguea nada.
type target struct {
	scheme string // exactamente "rtmp" o "rtmps": go-rtmp compara con estos literales
	addr   string // host:puerto, con el puerto por defecto ya resuelto
	app    string // la app RTMP: el path completo, sin barras al principio ni al final
	logApp string // el primer segmento, solo si el path es anidado; "" si no se puede loguear
}

// parseTarget descompone una URL de destino.
//
// El esquema decide si se usa Dial o TLSDial, y go-rtmp compara contra los literales
// "rtmp" y "rtmps" respectivamente: pasar el equivocado devuelve "Unknown protocol"
// (spec §16.1).
//
// NINGÚN error de aquí reproduce la URL. Varias plataformas piden pegar la clave dentro
// de la URL, así que la URL entera —y el path, y la query— son material secreto: el error
// dice qué está mal, y como mucho con el esquema y el host por contexto (spec §8).
func parseTarget(rawURL string) (target, error) {
	// El error de url.Parse es un *url.Error que incluye la URL entera, así que no se
	// envuelve ni se reproduce: solo se dice que no se pudo interpretar.
	u, err := url.Parse(rawURL)
	if err != nil {
		return target{}, fmt.Errorf("%w: la URL del destino está mal formada", ErrUnsupportedScheme)
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

	// El path entero es la app: la URL del destino es la URL del servidor y la clave va
	// aparte.
	app := strings.Trim(u.Path, "/")
	if app == "" {
		return target{}, errors.New("la URL del destino no tiene app (la parte tras el host)")
	}

	// Y lo logueable es el primer segmento SOLO si hay más de uno. `strings.Cut` sin
	// separador devuelve la cadena entera, así que derivarlo sin más dejaba pasar el path
	// plano con la clave pegada (`rtmp://host/live_987_CLAVE`, y también el `%2f` que
	// url.Parse decodifica a un único segmento). Un path plano es ambiguo y no se loguea.
	logApp := ""
	if first, rest, nested := strings.Cut(app, "/"); nested && rest != "" {
		logApp = first
	}

	return target{
		scheme: u.Scheme,
		addr:   net.JoinHostPort(host, port),
		app:    app,
		logApp: logApp,
	}, nil
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
// TODOS sus métodos, Close incluido, deben llamarse desde UNA SOLA goroutine (la de su
// sink): ver el comentario de relay.Publisher para el porqué.
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
		log:       log.With(logAttrs(tgt)...),
	}, nil
}

// logAttrs son los atributos fijos del logger del publisher.
//
// No van ni la URL original, ni el path completo, ni la clave enmascarada: el spec §8 dice
// que las claves jamás aparecen en los logs, y los últimos 4 caracteres son para la
// interfaz, que es otra superficie con otro control de acceso. `destino_app` se omite
// entero cuando el path no es anidado, porque entonces podría ser la clave. No se pierde
// gran cosa: `destino_addr` ya identifica la plataforma y el destino lleva su ID numérico
// en el logger del sink.
func logAttrs(tgt target) []any {
	attrs := []any{"destino_addr", tgt.addr}
	if tgt.logApp != "" {
		attrs = append(attrs, "destino_app", tgt.logApp)
	}
	return attrs
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
		// NO fijes ConnConfig.Logger. go-rtmp loguea a nivel Info el comando de
		// publish completo, cuyo nombre de stream ES la clave. Dejándolo en nil, la
		// librería lo redirige a io.Discard y la clave no sale a ningún lado.
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
	// NO se mandan releaseStream ni FCPublish. Esto contradice lo que el spike del spec
	// §16 supuso, y la razón es una prueba contra plataformas reales.
	//
	// FMLE —el cliente que las plataformas esperan— los manda sobre el stream 0 y ANTES de
	// createStream. Nosotros solo podíamos mandarlos sobre el stream ya creado y con
	// TransactionID 0, porque go-rtmp v0.0.7 no expone su stream de control: es interno
	// (cc.conn.streams.At(ControlStreamID)) y el único Stream que su API pública devuelve
	// es el de CreateStream. El spec §14 anotó ese riesgo desde el principio.
	//
	// Medido el 2026-09-03 contra las plataformas de verdad:
	//   - Twitch aceptaba connect, createStream y publish, y CORTABA en cuanto empezábamos
	//     a escribir. Con estos dos comandos fuera, transmite sin un solo descarte ni una
	//     reconexión. Se aisló publicando con ffmpeg directamente a Twitch con la misma
	//     clave: ffmpeg aguantaba, nosotros no, así que el problema era nuestro.
	//   - YouTube funciona igual con ellos que sin ellos.
	//
	// Si algún día aparece una plataforma que SÍ los exija, mandarlos bien requiere que
	// go-rtmp exponga el stream de control, o escribirlos a más bajo nivel. Mandarlos mal
	// no es una aproximación: rompe Twitch.

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

	// Sin atributos extra: `destino_app` ya identifica el destino, y `app` es el path
	// completo, que es justo lo que no puede salir al log.
	p.log.Info("publicando en el destino")
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
		// FCUnpublish antes de deleteStream: es lo que espera el cierre ordenado del
		// spec §6.5, y varias plataformas lo usan para liberar el slot de emisión sin
		// esperar al timeout. Que falle no es motivo para no seguir cerrando.
		// FCUnpublish SÍ se sigue mandando, a diferencia de releaseStream y FCPublish,
		// que se quitaron por romper Twitch. La diferencia es cuándo: este va al CERRAR,
		// cuando la conexión se va a tirar de todas formas, así que si la plataforma no
		// le gusta lo que ve, lo peor que puede hacer es cortar — que es justo lo que
		// estamos pidiendo.
		if err := p.writeCommand(stream, "FCUnpublish"); err != nil {
			p.log.Debug("FCUnpublish falló al cerrar", "err", err)
		}
		// NO se manda deleteStream. Provoca una carrera de datos DENTRO de go-rtmp
		// v0.0.7: su `streams.Delete` toma el mutex del mapa de streams, pero su
		// `streams.At` —que usa la goroutine de lectura de la conexión— lo lee SIN
		// tomarlo (streams.go:86). Uno protege y el otro no.
		//
		// El ledger de la fase 2 dejó anotada esa carrera como riesgo; apareció bajo
		// -race el 2026-09-03. No es un problema de tests: en producción Close() se llama
		// en CADA reconexión —con un destino aleteando fueron 55 seguidas— y una escritura
		// concurrente sobre un mapa de Go puede abortar el proceso con "concurrent map
		// writes", llevándose por delante la emisión a todos los destinos.
		//
		// Cerrar el socket termina el stream igual, y FCUnpublish ya le dijo a la
		// plataforma que suelte el slot. ClientConn.Close solo cierra la conexión y no
		// toca ese mapa, así que por ahí no hay carrera.
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
