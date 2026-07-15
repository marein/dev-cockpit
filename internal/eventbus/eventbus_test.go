package eventbus

import "testing"

func TestPublishReachesEverySubscriber(t *testing.T) {
	b := New()
	a, cancelA := b.Subscribe()
	defer cancelA()
	c, cancelC := b.Subscribe()
	defer cancelC()

	b.Publish(Event{Type: "terminals"})

	for _, ch := range []<-chan Event{a, c} {
		select {
		case ev := <-ch:
			if ev.Type != "terminals" {
				t.Fatalf("got %q, want terminals", ev.Type)
			}
		default:
			t.Fatal("event not delivered to a subscriber")
		}
	}
}

func TestCancelStopsDelivery(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe()
	cancel()

	b.Publish(Event{Type: "terminals"})

	select {
	case ev := <-ch:
		t.Fatalf("received %q after cancel", ev.Type)
	default:
	}
}

func TestPublishDoesNotBlockOnFullSubscriber(t *testing.T) {
	b := New()
	_, cancel := b.Subscribe() // registered but never drained
	defer cancel()

	// Far more events than the buffer holds. If Publish blocked on a full
	// subscriber this would deadlock and the test would time out.
	for i := 0; i < subBuffer*4; i++ {
		b.Publish(Event{Type: "terminals"})
	}
}
