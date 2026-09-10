package events_test

import (
	"sync"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/events"
	"github.com/aprendomx/splitstream/internal/store"
)

func TestEverySubscriberReceivesTheEvent(t *testing.T) {
	bus := events.NewBus()
	a, releaseA := bus.Subscribe(4)
	defer releaseA()
	b, releaseB := bus.Subscribe(4)
	defer releaseB()

	bus.Publish(store.Event{ID: 1, Kind: "x"})

	for nombre, ch := range map[string]<-chan store.Event{"a": a, "b": b} {
		select {
		case ev := <-ch:
			if ev.ID != 1 {
				t.Errorf("%s recibió el evento %d, quería 1", nombre, ev.ID)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s no recibió nada", nombre)
		}
	}
}

// Es la propiedad que justifica el bus: LogEvent lo llaman las goroutines de los sinks, y
// un consumidor lento —un webhook contra un servidor caído— no puede frenarlas.
func TestPublishNeverBlocksOnASlowSubscriber(t *testing.T) {
	bus := events.NewBus()
	_, release := bus.Subscribe(2) // nadie lee
	defer release()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			bus.Publish(store.Event{ID: int64(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish se bloqueó con un suscriptor que no lee")
	}
	// 2 cupieron en el buffer; los otros 98 se perdieron y se contaron.
	if got := bus.Dropped(); got != 98 {
		t.Errorf("Dropped = %d, quería 98", got)
	}
}

func TestReleaseIsIdempotentAndClosesTheChannel(t *testing.T) {
	bus := events.NewBus()
	ch, release := bus.Subscribe(1)
	release()
	release()
	if _, ok := <-ch; ok {
		t.Error("el canal siguió abierto tras release")
	}
	bus.Publish(store.Event{ID: 1}) // no debe entrar en pánico escribiendo en un canal cerrado
}

func TestPublishAndReleaseAreSafeConcurrently(t *testing.T) {
	bus := events.NewBus()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, release := bus.Subscribe(1)
			release()
		}()
		go func() {
			defer wg.Done()
			bus.Publish(store.Event{ID: 1})
		}()
	}
	wg.Wait()
}
