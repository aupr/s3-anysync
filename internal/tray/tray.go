//go:build cgo || windows

package tray

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/niceit/s3-anysync/internal/api"
)

// Available reports whether this build has a real tray.
func Available() bool { return true }

// Run shows the tray and blocks until Quit is picked or ctx ends. On macOS
// it must be called from the main goroutine (the process's main thread).
func Run(ctx context.Context, svc api.Service, o Options) error {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	t := &tray{svc: svc, o: o, actions: map[string]*actionMenu{}}
	systray.Run(func() { t.ready(ctx) }, func() {})
	return nil
}

type actionMenu struct {
	title, sync, snap, pause *systray.MenuItem
	paused                   bool
	stop                     chan struct{}
}

type tray struct {
	svc     api.Service
	o       Options
	mu      sync.Mutex
	status  *systray.MenuItem
	parent  *systray.MenuItem
	actions map[string]*actionMenu
	icon    iconKind
	iconSet bool
}

func (t *tray) ready(ctx context.Context) {
	systray.SetIcon(iconPNG(iconIdle))
	systray.SetTooltip("s3-anysync")
	t.status = systray.AddMenuItem("s3-anysync", "")
	t.status.Disable()
	var open *systray.MenuItem
	if t.o.OpenGUI != nil {
		open = systray.AddMenuItem("Open s3-anysync", "Open the settings window")
	}
	systray.AddSeparator()
	syncAll := systray.AddMenuItem("Sync all now", "")
	snapAll := systray.AddMenuItem("Snapshot all", "")
	pauseAll := systray.AddMenuItem("Pause all", "")
	resumeAll := systray.AddMenuItem("Resume all", "")
	systray.AddSeparator()
	t.parent = systray.AddMenuItem("Actions", "")
	t.parent.Disable() // enabled by refresh once there is an action
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit s3-anysync", "Stop the background agent")

	t.refresh(ctx)
	go t.watch(ctx)

	var openC <-chan struct{}
	if open != nil {
		openC = open.ClickedCh
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				systray.Quit()
				return
			case <-quit.ClickedCh:
				if t.o.OnQuit != nil {
					t.o.OnQuit()
				}
				systray.Quit()
				return
			case <-openC:
				if err := t.o.OpenGUI(); err != nil {
					t.o.Log.Warn("open window", "err", err)
				}
			case <-syncAll.ClickedCh:
				go t.forAll(ctx, func(id string) error { _, err := t.svc.Sync(ctx, id); return err })
			case <-snapAll.ClickedCh:
				go t.forAll(ctx, func(id string) error { _, err := t.svc.Snapshot(ctx, id); return err })
			case <-pauseAll.ClickedCh:
				go t.forAll(ctx, func(id string) error { return t.svc.Pause(ctx, id) })
			case <-resumeAll.ClickedCh:
				go t.forAll(ctx, func(id string) error { return t.svc.Resume(ctx, id) })
			}
		}
	}()
}

func (t *tray) forAll(ctx context.Context, f func(id string) error) {
	st, err := t.svc.Status(ctx)
	if err != nil {
		t.o.Log.Warn("tray status", "err", err)
		return
	}
	for _, a := range st.Actions {
		if err := f(a.Action.ID); err != nil {
			t.o.Log.Info("tray action", "action", a.Action.ID, "err", err)
		}
	}
}

// watch refreshes the menu on events, at most every 250ms.
func (t *tray) watch(ctx context.Context) {
	ch, err := t.svc.Subscribe(ctx)
	if err != nil {
		t.o.Log.Warn("tray subscribe", "err", err)
		return
	}
	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if pending == nil {
				pending = time.After(250 * time.Millisecond)
			}
		case <-pending:
			pending = nil
			t.refresh(ctx)
		}
	}
}

func (t *tray) refresh(ctx context.Context) {
	st, err := t.svc.Status(ctx)
	if err != nil {
		t.o.Log.Warn("tray status", "err", err)
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if k := iconFor(st); !t.iconSet || k != t.icon {
		systray.SetIcon(iconPNG(k))
		t.icon, t.iconSet = k, true
	}
	tip := tooltip(st)
	systray.SetTooltip(tip)
	t.status.SetTitle(tip)
	seen := map[string]bool{}
	for _, a := range st.Actions {
		id := a.Action.ID
		seen[id] = true
		m, ok := t.actions[id]
		if !ok {
			m = t.addAction(ctx, id)
			t.actions[id] = m
		}
		m.title.SetTitle(actionTitle(a))
		m.paused = a.State == api.StatePausedUser
		if m.paused {
			m.pause.SetTitle("Resume")
		} else {
			m.pause.SetTitle("Pause")
		}
	}
	for id, m := range t.actions {
		if !seen[id] {
			close(m.stop)
			m.sync.Remove()
			m.snap.Remove()
			m.pause.Remove()
			m.title.Remove()
			delete(t.actions, id)
		}
	}
	if len(t.actions) == 0 {
		t.parent.Disable()
	} else {
		t.parent.Enable()
	}
}

func (t *tray) addAction(ctx context.Context, id string) *actionMenu {
	m := &actionMenu{stop: make(chan struct{})}
	m.title = t.parent.AddSubMenuItem(id, "")
	m.sync = m.title.AddSubMenuItem("Sync now", "")
	m.snap = m.title.AddSubMenuItem("Snapshot now", "")
	m.pause = m.title.AddSubMenuItem("Pause", "")
	run := func(what string, f func() error) {
		if err := f(); err != nil {
			t.o.Log.Info("tray action", "action", id, "op", what, "err", err)
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stop:
				return
			case <-m.sync.ClickedCh:
				go run("sync", func() error { _, err := t.svc.Sync(ctx, id); return err })
			case <-m.snap.ClickedCh:
				go run("snapshot", func() error { _, err := t.svc.Snapshot(ctx, id); return err })
			case <-m.pause.ClickedCh:
				t.mu.Lock()
				paused := m.paused
				t.mu.Unlock()
				if paused {
					go run("resume", func() error { return t.svc.Resume(ctx, id) })
				} else {
					go run("pause", func() error { return t.svc.Pause(ctx, id) })
				}
			}
		}
	}()
	return m
}
