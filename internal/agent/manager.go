// Package agent runs s3-anysync in the background: one worker per action
// serialises its runs and changes, drives its state machine, reacts to
// triggers, notifies the user and publishes events. Manager implements
// api.Service for the CLI, tray, IPC server and GUI.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/notify"
)

// Autostart turns starting the agent at login on or off.
type Autostart interface {
	Enable() error
	Disable() error
	Enabled() (bool, error)
}

// Options configure a Manager.
type Options struct {
	App *app.App
	// Notifier shows desktop notifications; nil disables them.
	Notifier notify.Notifier
	// Autostart manages starting at login; nil means unsupported here.
	Autostart Autostart
	// Triggers starts file watchers, schedules and remote polls (the agent);
	// the CLI's in-process mode leaves them off.
	Triggers bool
	// Backoff is the retry schedule after failures; nil means DefaultBackoff.
	Backoff []time.Duration
	// PollInterval overrides every action's remote poll interval (tests).
	PollInterval time.Duration
	// LogLevel, if set, follows the log level setting.
	LogLevel *slog.LevelVar
	Version  string
	Log      *slog.Logger
}

// DefaultBackoff is the retry schedule after consecutive failures.
var DefaultBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}

// NotifyAfterFailures is the failure count that triggers a notification.
const NotifyAfterFailures = 3

// errShuttingDown is returned to callers waiting on a job when the agent stops.
var errShuttingDown = api.ErrUnavailable

// Manager owns the per-action workers.
type Manager struct {
	opts    Options
	app     *app.App
	log     *slog.Logger
	bus     *bus
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	workers map[string]*worker
	started bool
	// nameGen counts machine renames; workers rebuild their runner when it
	// moves past the generation theirs was built at.
	nameGen atomic.Uint64
}

var _ api.Service = (*Manager)(nil)

// New returns a Manager; call Start to begin.
func New(opts Options) (*Manager, error) {
	if opts.App == nil {
		return nil, errors.New("agent: Options.App is required")
	}
	if len(opts.Backoff) == 0 {
		opts.Backoff = DefaultBackoff
	}
	if opts.Notifier == nil {
		opts.Notifier = notify.Nop{}
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{opts: opts, app: opts.App, log: log, bus: newBus(), ctx: ctx, cancel: cancel, workers: map[string]*worker{}}, nil
}

// Start starts one worker per local action.
func (m *Manager) Start() error {
	acts, err := m.app.Actions()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return errors.New("agent: already started")
	}
	m.started = true
	for _, a := range acts {
		m.startWorkerLocked(a.ID)
	}
	return nil
}

func (m *Manager) startWorkerLocked(id string) {
	if old, ok := m.workers[id]; ok && old.ctx.Err() == nil {
		return
	}
	// Either there is none or it was cancelled (a finished leave): replace it.
	w := newWorker(m, id)
	m.workers[id] = w
	go w.loop()
}

func (m *Manager) startWorker(id string) {
	for {
		m.mu.Lock()
		old, ok := m.workers[id]
		if ok && old.ctx.Err() != nil {
			// A cancelled worker is on its way out: let it finish, then replace it.
			m.mu.Unlock()
			<-old.done
			m.mu.Lock()
			if m.workers[id] == old {
				delete(m.workers, id)
			}
		}
		if cur, ok := m.workers[id]; ok && cur.ctx.Err() != nil {
			m.mu.Unlock()
			continue
		}
		m.startWorkerLocked(id)
		m.mu.Unlock()
		return
	}
}

// Close stops every worker. A running job is interrupted through its
// context; Close waits for the workers to exit.
func (m *Manager) Close() error {
	m.cancel()
	m.mu.Lock()
	ws := make([]*worker, 0, len(m.workers))
	for _, w := range m.workers {
		ws = append(ws, w)
	}
	m.mu.Unlock()
	for _, w := range ws {
		<-w.done
	}
	return nil
}

func (m *Manager) worker(id string) (*worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workers[id]
	if !ok {
		return nil, fmt.Errorf("action %s: %w", id, api.ErrNotFound)
	}
	return w, nil
}

// forgetWorker drops w from the table if it is still the current worker of
// its action; it reports whether it did. It never waits.
func (m *Manager) forgetWorker(w *worker) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.workers[w.id] != w {
		return false
	}
	delete(m.workers, w.id)
	return true
}

func (m *Manager) backoff(failures int) time.Duration {
	i := failures - 1
	if i >= len(m.opts.Backoff) {
		i = len(m.opts.Backoff) - 1
	}
	if i < 0 {
		i = 0
	}
	return m.opts.Backoff[i]
}

func (m *Manager) notify(message string) {
	if err := m.opts.Notifier.Notify("s3-anysync", message); err != nil {
		m.log.Warn("notification failed", "err", err)
	}
}
