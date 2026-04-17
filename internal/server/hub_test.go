package server

import (
	"sync"
	"testing"
	"time"
)

func TestHub_BroadcastFanOut(t *testing.T) {
	hub := NewHub()
	chA, unsubA := hub.Subscribe()
	chB, unsubB := hub.Subscribe()
	defer unsubA()
	defer unsubB()

	hub.Publish("test", 42)

	for _, ch := range []<-chan Event{chA, chB} {
		select {
		case e := <-ch:
			if e.Kind != "test" || e.Payload != 42 {
				t.Fatalf("unexpected event: %+v", e)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive broadcast")
		}
	}
}

func TestHub_UnsubscribeClosesChannel(t *testing.T) {
	hub := NewHub()
	ch, unsub := hub.Subscribe()

	unsub()

	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after unsubscribe")
	}
	if hub.SubscriberCount() != 0 {
		t.Fatalf("SubscriberCount = %d, want 0", hub.SubscriberCount())
	}
	// Double-unsubscribe must not panic.
	unsub()
}

func TestHub_SlowSubscriberDoesNotBlockProducer(t *testing.T) {
	hub := NewHub()
	_, unsub := hub.Subscribe() // never drained
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBufferSize*4; i++ {
			hub.Publish("flood", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}

func TestHub_ConcurrentPublishSubscribe(t *testing.T) {
	hub := NewHub()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// A handful of subscribers churning through subscribe/unsubscribe.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ch, unsub := hub.Subscribe()
				go func() {
					for range ch {
					}
				}()
				time.Sleep(time.Millisecond)
				unsub()
			}
		}()
	}

	// A publisher broadcasting flat-out.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			hub.Publish("k", i)
		}
	}()

	time.Sleep(30 * time.Millisecond)
	close(stop)
	wg.Wait()
}
