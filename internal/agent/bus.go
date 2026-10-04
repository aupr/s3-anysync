package agent

import (
	"context"
	"sync"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
)

// bus fans events out to subscribers. A subscriber that falls behind loses
// events rather than blocking the agent.
type bus struct {
	mu   sync.Mutex
	subs map[chan api.Event]struct{}
}

func newBus() *bus { return &bus{subs: map[chan api.Event]struct{}{}} }

func (b *bus) publish(e api.Event) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// subscribe returns a channel closed when ctx or stop ends.
func (b *bus) subscribe(ctx context.Context, stop <-chan struct{}) <-chan api.Event {
	ch := make(chan api.Event, 128)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		b.mu.Lock()
		delete(b.subs, ch)
		close(ch)
		b.mu.Unlock()
	}()
	return ch
}
