package chat_test

import (
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/chat"
	"github.com/aprendomx/splitstream/internal/platforms"
)

func mensaje(texto string) chat.Message {
	return chat.Message{
		SessionID: 1,
		ChatMessage: platforms.ChatMessage{
			Platform: platforms.Twitch, AccountID: 1, AuthorID: "u", Author: "v", Text: texto, At: time.Now(),
		},
	}
}

func TestBusPublishRepartesTest(t *testing.T) {
	b := chat.NewBus()
	ch1, release1 := b.Subscribe(4)
	defer release1()
	ch2, release2 := b.Subscribe(4)
	defer release2()

	b.Publish(mensaje("hola"))

	select {
	case m := <-ch1:
		if m.Text != "hola" {
			t.Errorf("ch1 recibió %q", m.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("ch1 no recibió el mensaje")
	}
	select {
	case m := <-ch2:
		if m.Text != "hola" {
			t.Errorf("ch2 recibió %q", m.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("ch2 no recibió el mensaje")
	}
}

func TestBusPublishNoBloqueaYCuentaDropped(t *testing.T) {
	b := chat.NewBus()
	_, release := b.Subscribe(1)
	defer release()

	// El buffer es 1: el primero entra, el segundo y el tercero se descartan porque nadie
	// lee. Publish jamás debe bloquearse esperando a un suscriptor lento.
	b.Publish(mensaje("uno"))
	b.Publish(mensaje("dos"))
	b.Publish(mensaje("tres"))

	if got := b.Dropped(); got != 2 {
		t.Errorf("Dropped = %d, quería 2", got)
	}
}

func TestBusRecentConserva50EnOrden(t *testing.T) {
	b := chat.NewBus()
	for i := 0; i < 60; i++ {
		b.Publish(mensaje(string(rune('a' + i%26))))
	}
	recent := b.Recent()
	if len(recent) != 50 {
		t.Fatalf("Recent = %d, quería 50", len(recent))
	}
	// Deben ser los últimos 50 publicados, en el mismo orden en que se publicaron: el
	// primero de la ventana es el mensaje número 10 (índice 0-based) de los 60.
	primero := mensaje(string(rune('a' + 10%26)))
	if recent[0].Text != primero.Text {
		t.Errorf("Recent[0] = %q, quería %q", recent[0].Text, primero.Text)
	}
	ultimo := mensaje(string(rune('a' + 59%26)))
	if recent[len(recent)-1].Text != ultimo.Text {
		t.Errorf("Recent[last] = %q, quería %q", recent[len(recent)-1].Text, ultimo.Text)
	}
}

func TestBusReset(t *testing.T) {
	b := chat.NewBus()
	b.Publish(mensaje("hola"))
	if len(b.Recent()) == 0 {
		t.Fatal("se esperaba al menos un mensaje antes del reset")
	}
	b.Reset()
	if got := b.Recent(); len(got) != 0 {
		t.Errorf("Recent tras Reset = %d, quería 0", len(got))
	}
}

func TestBusSubscribeReleaseEsIdempotenteYCierraElCanal(t *testing.T) {
	b := chat.NewBus()
	ch, release := b.Subscribe(1)

	release()
	release() // no debe entrar en pánico ni bloquear la segunda vez.

	_, ok := <-ch
	if ok {
		t.Error("el canal debería estar cerrado tras release")
	}

	// Publicar después de la baja no debe alcanzar al canal ya cerrado ni entrar en
	// pánico: el suscriptor ya salió del mapa.
	b.Publish(mensaje("tarde"))
}
