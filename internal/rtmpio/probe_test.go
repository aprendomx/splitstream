package rtmpio

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/probe"
)

// sondaHandler es un servidor RTMP cuyo comportamiento decide cada test.
type sondaHandler struct {
	rtmp.DefaultHandler
	conn       net.Conn
	rechazar   bool          // OnConnect devuelve error
	cerrarTras time.Duration // >0: tras aceptar publish, cierra el socket
}

func (h *sondaHandler) OnConnect(ts uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	if h.rechazar {
		return errors.New("rechazado")
	}
	return nil
}

func (h *sondaHandler) OnPublish(_ *rtmp.StreamContext, ts uint32, cmd *rtmpmsg.NetStreamPublish) error {
	if h.cerrarTras > 0 {
		time.AfterFunc(h.cerrarTras, func() { h.conn.Close() })
	}
	return nil
}

// servidorDeSonda levanta el servidor sobre un puerto efímero y devuelve host:puerto.
func servidorDeSonda(t *testing.T, ajustar func(*sondaHandler)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(c net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			h := &sondaHandler{conn: c}
			if ajustar != nil {
				ajustar(h)
			}
			return c, &rtmp.ConnConfig{Handler: h}
		},
	})
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func sondear(t *testing.T, url string, grace time.Duration) probe.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return Probe(ctx, PublisherConfig{URL: url, StreamKey: crypto.Secret("clave-de-prueba")}, grace)
}

func TestProbePlausibleAgainstAServerThatKeepsTheStream(t *testing.T) {
	addr := servidorDeSonda(t, nil)
	res := sondear(t, "rtmp://"+addr+"/live", 300*time.Millisecond)
	if res.Outcome != probe.Plausible {
		t.Fatalf("outcome = %v (etapa %s, err %v), quería plausible", res.Outcome, res.Stage, res.Err)
	}
	if res.Stage != "grace" {
		t.Errorf("stage = %q, quería grace", res.Stage)
	}
}

// Lo que hace Twitch con una clave mala: acepta publish y corta. La sonda lo ve porque el
// bucle de lectura de go-rtmp muere y ClientConn.LastError deja de ser nil.
func TestProbeClosedEarlyWhenTheServerHangsUp(t *testing.T) {
	addr := servidorDeSonda(t, func(h *sondaHandler) { h.cerrarTras = 100 * time.Millisecond })
	res := sondear(t, "rtmp://"+addr+"/live", 1500*time.Millisecond)
	if res.Outcome != probe.ClosedEarly {
		t.Fatalf("outcome = %v (etapa %s, err %v), quería closed_early", res.Outcome, res.Stage, res.Err)
	}
}

// Un connect rechazado llega al cliente como ConnectRejectedError: la sonda lo distingue
// de un cierre.
func TestProbeRejectedWhenConnectIsRefused(t *testing.T) {
	addr := servidorDeSonda(t, func(h *sondaHandler) { h.rechazar = true })
	res := sondear(t, "rtmp://"+addr+"/live", 300*time.Millisecond)
	if res.Outcome != probe.Rejected {
		t.Fatalf("outcome = %v (etapa %s, err %v), quería rejected", res.Outcome, res.Stage, res.Err)
	}
	if res.Stage != "connect" {
		t.Errorf("stage = %q, quería connect", res.Stage)
	}
}

func TestProbeUnreachableOnAClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // el puerto queda cerrado

	res := sondear(t, "rtmp://"+addr+"/live", 300*time.Millisecond)
	if res.Outcome != probe.Unreachable || res.Stage != "tcp" {
		t.Fatalf("outcome = %v etapa %q, quería unreachable/tcp (err %v)", res.Outcome, res.Stage, res.Err)
	}
}

// Un certificado que no verifica es la etapa tls, y la sonda no lo acepta: la
// verificación por defecto se mantiene (spec base §16).
func TestProbeUnreachableOnABadCertificate(t *testing.T) {
	cert := certificadoAutofirmado(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(io.Discard, c); c.Close() }()
		}
	}()

	res := sondear(t, "rtmps://"+ln.Addr().String()+"/live", 300*time.Millisecond)
	if res.Outcome != probe.Unreachable || res.Stage != "tls" {
		t.Fatalf("outcome = %v etapa %q, quería unreachable/tls (err %v)", res.Outcome, res.Stage, res.Err)
	}
}

func TestProbeRejectsABadURLWithoutLeakingIt(t *testing.T) {
	const key = "CLAVESECRETA"
	res := sondear(t, "http://example.com/live/"+key, 300*time.Millisecond)
	if res.Outcome != probe.Rejected || res.Stage != "url" {
		t.Fatalf("outcome = %v etapa %q, quería rejected/url", res.Outcome, res.Stage)
	}
	if res.Err != nil && strings.Contains(res.Err.Error(), key) {
		t.Errorf("el error de la sonda lleva la clave: %v", res.Err)
	}
}

func certificadoAutofirmado(t *testing.T) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}
