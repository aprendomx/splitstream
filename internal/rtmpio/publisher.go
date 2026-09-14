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

// writeTimeout acota CADA escritura hacia el destino. go-rtmp v0.0.7 traía 5 s cableados
// en Stream.Write; el parche 1 de third_party/go-rtmp lo hace configurable y aquí se baja
// a 3 s (spec v1.0 §3.1).
//
// Por qué 3 y no 5: mientras una escritura está bloqueada, el sink de ese destino no puede
// hacer nada más —ni vaciar su cola, ni darse por caído, ni reconectar—, así que el plazo
// es el tiempo que tarda en detectarse una plataforma que dejó de consumir. Tres segundos
// siguen siendo holgadísimos para un enlace sano (un GOP entero cabe de sobra) y recortan
// casi a la mitad el hueco antes de reconectar.
const writeTimeout = 3 * time.Second

// stageError dice en qué paso falló Connect. Lo usa Probe para explicar al usuario si el
// problema fue la red, el TLS o el handshake. El texto del error no cambia: Error()
// delega en el error envuelto.
type stageError struct {
	stage string
	err   error
}

func (e *stageError) Error() string { return e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

// dialStage clasifica un fallo del dial. Un certificado que no verifica es "tls", un
// nombre que no resuelve es "dns", y lo demás —conexión rechazada, timeout— es "tcp".
func dialStage(err error) string {
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "tls"
	}
	var recErr tls.RecordHeaderError
	if errors.As(err, &recErr) {
		return "tls"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	return "tcp"
}

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
	// PreCommands manda releaseStream y FCPublish por el stream de control antes de
	// createStream, como FMLE. APAGADO por defecto, y el comentario de connect explica
	// por qué: enciéndelo solo si una plataforma los exige.
	PreCommands bool
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
	// preCommands se fija al construir y no cambia, así que se lee sin el candado.
	preCommands bool

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
		tgt:         tgt,
		key:         cfg.StreamKey,
		chunkSize:   size,
		log:         log.With(logAttrs(tgt)...),
		preCommands: cfg.PreCommands,
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
			}, "rtmps", p.tgt.addr, &rtmp.ConnConfig{WriteTimeout: writeTimeout})
		default:
			conn, err = rtmp.DialWithDialer(
				&net.Dialer{Deadline: deadline},
				"rtmp", p.tgt.addr, &rtmp.ConnConfig{WriteTimeout: writeTimeout})
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
		return &stageError{stage: "tcp", err: fmt.Errorf("conectar a %s: %w", p.tgt.addr, ctx.Err())}
	case r := <-results:
		if r.err != nil {
			return &stageError{stage: dialStage(r.err), err: fmt.Errorf("conectar a %s: %w", p.tgt.addr, r.err)}
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
		return &stageError{stage: "connect", err: fmt.Errorf("handshake connect con %s: %w", p.tgt.addr, err)}
	}

	// releaseStream y FCPublish: se mandan SOLO si PreCommands está encendido, y entonces
	// por el stream de control y antes de createStream, que es como los manda FMLE —el
	// cliente que las plataformas esperan—.
	//
	// La historia completa, porque el valor por defecto no es un gusto sino una cicatriz:
	//
	//   1. El spike del spec §16 dio por hecho que había que mandarlos siempre. Se
	//      mandaban MAL, y no por descuido: go-rtmp v0.0.7 no exponía su stream de
	//      control (era interno, cc.conn.streams.At(ControlStreamID)), así que el único
	//      Stream que su API pública devolvía era el de CreateStream. Iban por el stream
	//      de datos y DESPUÉS de createStream. El spec §14 anotó ese riesgo desde el
	//      principio.
	//   2. Medido el 2026-09-03 contra las plataformas de verdad: Twitch aceptaba
	//      connect, createStream y publish, y CORTABA en cuanto empezábamos a escribir.
	//      Se aisló publicando con ffmpeg a Twitch con la misma clave: ffmpeg aguantaba,
	//      nosotros no, así que el problema era nuestro. YouTube funcionaba igual con
	//      ellos que sin ellos. Se quitaron, y Twitch pasó a transmitir sin un solo
	//      descarte ni una reconexión.
	//   3. Ahora el parche 3 de third_party/go-rtmp expone el stream de control
	//      (ClientConn.ControlStream), así que ya se pueden mandar BIEN. Pero «bien» no
	//      es lo mismo que «probado»: lo único que se sabe medido es que sin ellos las
	//      dos plataformas que usamos funcionan. Por eso viven detrás de una opción
	//      apagada, para la plataforma que algún día los exija.
	//
	// Mandarlos sin esperar respuesta destapó dos fallos más de la librería, y los dos van
	// arreglados en la copia porque sin ellos esta opción no sería usable:
	//   - Parche 5: estos comandos van con TransactionID 0 y no se espera respuesta, pero
	//     hay plataformas que contestan igual. Un `_result` para una transacción que no
	//     existe hacía que go-rtmp CERRARA la conexión; ahora se ignora, pero SOLO por el
	//     stream de control, que es por donde van estos comandos. Por un stream de datos
	//     sigue cerrando: un `_error` al publish viaja igual —TransactionID 0, sin
	//     transacción— y ahí significa que la plataforma rechaza la emisión, así que
	//     cerrar es justo lo que hace falta para que el sink reconecte.
	//   - Parche 4: el tamaño de chunk nuevo lo aplica la goroutine escritora justo
	//     después de mandar el SetChunkSize, no quien lo encola. Si no, un FCPublish con
	//     una clave larga (más de 128 bytes) todavía en la cola salía troceado con un
	//     tamaño que el destino aún no conocía y el stream se desincronizaba.
	//
	// Lo que NO es fatal es el error de ESCRITURA: un destino que no espera estos comandos
	// los ignora, y quedarse sin publicar por un comando opcional sería peor que seguir.
	// Se registra a nivel debug, nunca con el nombre del stream: ESE nombre es la clave
	// (spec §8).
	if p.preCommands {
		if ctrl := conn.ControlStream(); ctrl != nil {
			for _, nombre := range []string{"releaseStream", "FCPublish"} {
				if err := p.writeCommand(ctrl, nombre); err != nil {
					p.log.Debug("comando previo rechazado", "comando", nombre, "err", err)
				}
			}
		}
	}

	// El tamaño de chunk se negocia AQUÍ y solo aquí: CreateStream manda el SetChunkSize
	// por el stream de control y, desde el parche 4 de third_party/go-rtmp, es la goroutine
	// escritora la que aplica el tamaño nuevo justo después de ponerlo en el hilo. Antes
	// había además un WriteSetChunkSize(p.chunkSize) después del publish, de cuando el
	// tamaño lo aplicaba quien encolaba y no se podía confiar en el momento: hoy es un
	// SetChunkSize repetido con el mismo valor. Que sobra no es una deducción: lo mide en
	// el cable TestFirstVideoGoesOutWithTheNegotiatedChunkSize (publisher_test.go), que
	// cuenta los bytes que salen del primer vídeo sin él.
	stream, err := conn.CreateStream(&message.NetConnectionCreateStream{}, p.chunkSize)
	if err != nil {
		return &stageError{stage: "createStream", err: fmt.Errorf("createStream con %s: %w", p.tgt.addr, err)}
	}

	if err := stream.Publish(&message.NetStreamPublish{
		PublishingName: p.key.Reveal(),
		PublishingType: "live",
	}); err != nil {
		return &stageError{stage: "publish", err: fmt.Errorf("publish en %s: %w", p.tgt.addr, err)}
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

// lastError devuelve el error con el que murió el bucle de lectura de go-rtmp, o nil si la
// conexión sigue viva. Es la única señal de que el peer colgó: go-rtmp no avisa de otra
// forma, y Publish no espera el onStatus.
func (p *Publisher) lastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return errors.New("sin conexión")
	}
	return p.conn.LastError()
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
		// FCUnpublish SÍ se manda siempre, a diferencia de releaseStream y FCPublish,
		// que son opcionales. La diferencia es cuándo: este va al CERRAR, cuando la
		// conexión se va a tirar de todas formas, así que si a la plataforma no le gusta
		// lo que ve, lo peor que puede hacer es cortar — que es justo lo que estamos
		// pidiendo.
		//
		// Por dónde sí depende: con PreCommands va por el stream de control, el mismo por
		// el que fueron releaseStream y FCPublish, que es como lo manda FMLE. Sin la
		// opción sigue yendo por el stream de datos, exactamente como hasta ahora: el
		// comportamiento por defecto no cambia ni aquí.
		destino := stream
		if p.preCommands {
			if ctrl := conn.ControlStream(); ctrl != nil {
				destino = ctrl
			}
		}
		if err := p.writeCommand(destino, "FCUnpublish"); err != nil {
			p.log.Debug("FCUnpublish falló al cerrar", "err", err)
		}
		// NO se manda deleteStream. El motivo de origen ya no vale: era la carrera de
		// datos de go-rtmp v0.0.7, cuyo `streams.At` —el que usa la goroutine de lectura
		// de la conexión— leía el mapa de streams SIN el candado que `streams.Delete` sí
		// toma. Eso lo arregla el parche 2 de third_party/go-rtmp, que pasa `At` a
		// `RLock`; hoy mandarlo no abortaría el proceso.
		//
		// Lo que queda en pie es más simple: no hace falta. Cerrar el socket termina el
		// stream igual, y FCUnpublish ya le dijo a la plataforma que suelte el slot de
		// emisión, que es el único efecto observable que nos importa. deleteStream solo
		// le pide a la otra punta que olvide un id de stream por el que ya no va a llegar
		// nada, y de paso borra ese stream del mapa de una conexión que estamos cerrando.
		//
		// Y probarlo no sería gratis: Close() se llama en CADA reconexión —con un destino
		// aleteando fueron 55 seguidas—, así que es el camino más caliente del cierre, y
		// la historia de PreCommands (arriba, en Connect) dejó claro lo que cuesta
		// mandarle a una plataforma real un comando de FMLE que nadie ha medido. Si algún
		// día una plataforma lo exige, su sitio es detrás de PreCommands, con los otros
		// tres.
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
