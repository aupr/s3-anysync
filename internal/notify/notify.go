// Package notify shows desktop notifications.
package notify

import (
	"sync"

	"github.com/gen2brain/beeep"
)

// Notifier shows a notification to the user.
type Notifier interface {
	Notify(title, message string) error
}

var nameOnce sync.Once

// Desktop uses the OS notification center.
type Desktop struct{}

// NewDesktop returns the OS notifier.
func NewDesktop() Desktop {
	nameOnce.Do(func() { beeep.AppName = "s3-anysync" })
	return Desktop{}
}

func (Desktop) Notify(title, message string) error { return beeep.Notify(title, message, "") }

// Nop discards notifications.
type Nop struct{}

func (Nop) Notify(string, string) error { return nil }

// Recorder keeps notifications in memory (for tests).
type Recorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *Recorder) Notify(title, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, title+": "+message)
	return nil
}

// Messages returns the notifications so far as "title: message".
func (r *Recorder) Messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.msgs...)
}
