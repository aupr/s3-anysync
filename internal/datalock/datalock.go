// Package datalock makes one process the owner of an s3-anysync data folder.
// The agent holds the lock while it runs; the CLI takes it only when no agent
// runs, and otherwise talks to the agent over IPC.
package datalock

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// ErrBusy means another s3-anysync process owns the data folder.
var ErrBusy = errors.New("the data folder is in use by another s3-anysync process (is the agent running?)")

// Lock is a held data-folder lock.
type Lock struct {
	f *flock.Flock
}

// Acquire takes the lock on dataDir without waiting.
func Acquire(dataDir string) (*Lock, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	f := flock.New(filepath.Join(dataDir, "agent.lock"))
	ok, err := f.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrBusy
	}
	return &Lock{f: f}, nil
}

// Release gives the folder up.
func (l *Lock) Release() error { return l.f.Unlock() }
