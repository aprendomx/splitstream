package relay

import (
	"sync"
	"testing"
	"time"
)

func tapVideo(ts uint32, key bool) *Message {
	return &Message{Kind: KindVideo, Timestamp: ts, IsKeyframe: key, Payload: []byte{0x17, 0x01}}
}

// Los sequence headers reales llegan con el bit de keyframe puesto (0x17), y es lo que
// InspectVideo marca; el test los fabrica igual para no probar contra un caso irreal.
func tapSeqHeader() *Message {
	return &Message{Kind: KindVideo, IsSeqHeader: true, IsKeyframe: true, Payload: []byte{0x17, 0x00}}
}

func recvTap(t *testing.T, ch <-chan *Message) *Message {
	t.Helper()
	select {
	case m, ok := <-ch:
		if !ok {
			t.Fatal("el canal del tap se cerró antes de tiempo")
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("el tap no entregó nada en 2 s")
	}
	return nil
}

func expectNothing(t *testing.T, ch <-chan *Message) {
	t.Helper()
	select {
	case m, ok := <-ch:
		if !ok {
			t.Fatal("el canal del tap se cerró antes de tiempo")
		}
		t.Fatalf("el tap entregó un mensaje que no debía: ts=%d key=%v seq=%v",
			m.Timestamp, m.IsKeyframe, m.IsSeqHeader)
	default:
	}
}

// Un decodificador no puede arrancar en mitad de un GOP: lo primero que sale de un tap
// es siempre un keyframe.
func TestTapStartsAtAKeyframe(t *testing.T) {
	hub := NewHub(nil)
	ch, release := hub.Tap()
	defer release()

	hub.Publish(tapVideo(1, false))
	hub.Publish(tapVideo(2, true))
	hub.Publish(tapVideo(3, false))

	got := recvTap(t, ch)
	if !got.IsKeyframe || got.Timestamp != 2 {
		t.Fatalf("el primer mensaje fue ts=%d key=%v; quería el keyframe ts=2", got.Timestamp, got.IsKeyframe)
	}
	if got := recvTap(t, ch); got.Timestamp != 3 {
		t.Fatalf("tras el keyframe llegó ts=%d, quería 3", got.Timestamp)
	}
}

// La vista es silenciada por diseño: el audio ni entra al tap. El meta tampoco: el
// cliente no lo necesita. Los sequence headers de vídeo pasan SIEMPRE, incluso mientras
// el tap espera keyframe, porque llevan la config de una renegociación.
func TestTapIgnoresAudioAndMetaButPassesVideoSeqHeaders(t *testing.T) {
	hub := NewHub(nil)
	ch, release := hub.Tap()
	defer release()

	hub.Publish(&Message{Kind: KindAudio, Payload: []byte{0xaf, 0x01}})
	hub.Publish(&Message{Kind: KindMeta, Payload: []byte{0x02}})
	hub.Publish(tapSeqHeader())

	if got := recvTap(t, ch); !got.IsSeqHeader {
		t.Fatalf("quería el sequence header, llegó otra cosa: %+v", got)
	}
	expectNothing(t, ch)
}

// Con el buffer lleno se descarta, y tras el descarte no sale nada hasta el siguiente
// keyframe: entregar un delta con su pasado descartado le daría al decodificador un GOP
// roto.
func TestTapDropsAndResumesAtAKeyframe(t *testing.T) {
	hub := NewHub(nil)
	ch, release := hub.Tap()
	defer release()

	hub.Publish(tapVideo(0, true))
	for i := 1; i <= tapBuffer+10; i++ { // desborda el buffer a propósito
		hub.Publish(tapVideo(uint32(i), false))
	}

	// Se drena todo lo que el buffer retuvo.
	for {
		select {
		case <-ch:
			continue
		default:
		}
		break
	}

	hub.Publish(tapVideo(100, false)) // el tap está esperando keyframe: no debe salir
	expectNothing(t, ch)

	hub.Publish(tapVideo(101, true))
	if got := recvTap(t, ch); !got.IsKeyframe || got.Timestamp != 101 {
		t.Fatalf("tras el descarte llegó ts=%d key=%v; quería el keyframe 101", got.Timestamp, got.IsKeyframe)
	}
}

// El fin de la sesión cierra los taps, y release es idempotente y convive con Close:
// cerrar dos veces un canal es un panic, así que esto protege el apagado.
func TestHubCloseClosesTapsAndReleaseIsIdempotent(t *testing.T) {
	hub := NewHub(nil)
	ch, release := hub.Tap()

	hub.Close()
	if _, ok := <-ch; ok {
		t.Fatal("Close no cerró el canal del tap")
	}
	release()
	release() // segunda llamada: no debe hacer nada, y menos panic

	ch2, release2 := hub.Tap()
	release2()
	if _, ok := <-ch2; ok {
		t.Fatal("release no cerró el canal del tap")
	}
	hub.Close() // el tap ya liberado no debe hacer panic aquí
}

// Publish jamás bloquea por un tap, y abrir/soltar taps mientras se publica no puede
// tener carreras: este test existe sobre todo para el detector de -race.
func TestTapsDoNotBlockOrRacePublish(t *testing.T) {
	hub := NewHub(nil)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			hub.Publish(tapVideo(uint32(i), i%30 == 0))
		}
	}()

	for i := 0; i < 50; i++ {
		ch, release := hub.Tap()
		var drena sync.WaitGroup
		drena.Add(1)
		go func() {
			defer drena.Done()
			for range ch { //nolint:revive // drenar hasta que release cierre el canal
			}
		}()
		time.Sleep(time.Millisecond)
		release()
		drena.Wait()
	}

	close(stop)
	wg.Wait()
	hub.Close()
}
