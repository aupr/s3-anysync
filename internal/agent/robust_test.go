package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// conflicted returns env b with an action paused on one conflict.
func conflicted(t *testing.T) (*testEnv, model.Action) {
	t.Helper()
	store := storage.NewMemory()
	a, b := newEnv(t, store), newEnv(t, store)
	act, it, dirA := a.newAction(t, 1)
	a.m.Sync(ctx, act.ID)
	dirB := b.join(t, act, it)
	b.m.Sync(ctx, act.ID)
	os.WriteFile(filepath.Join(dirA, "s00.json"), []byte("from A"), 0o644)
	a.m.Sync(ctx, act.ID)
	os.WriteFile(filepath.Join(dirB, "s00.json"), []byte("from B!"), 0o644)
	if res, err := b.m.Sync(ctx, act.ID); err != nil || res.Outcome != model.OutcomeConflict {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	return b, act
}

func historyLen(t *testing.T, e *testEnv, id string) int {
	t.Helper()
	h, err := e.m.History(ctx, id, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(h)
}

func TestBadResolveChoiceIsNotASyncFailure(t *testing.T) {
	e, act := conflicted(t)
	before := historyLen(t, e, act.ID)
	_, err := e.m.Resolve(ctx, act.ID, map[string]model.Side{"snippets/s00.json": "both"})
	if !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
	_, err = e.m.Resolve(ctx, act.ID, map[string]model.Side{})
	if !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	st := e.state(t, act.ID)
	if st.State != api.StatePausedConflict || st.Failures != 0 || !st.NextRun.IsZero() || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if n := historyLen(t, e, act.ID); n != before {
		t.Fatalf("history grew %d -> %d: a retry ran", before, n)
	}
	if msgs := e.rec.Messages(); len(msgs) != 1 {
		t.Fatalf("notifications = %v", msgs)
	}
}

func TestEmptyApproveDeletesIsNotASyncFailure(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	e.m.Sync(ctx, act.ID)
	for i := 0; i < 3; i++ {
		if _, err := e.m.ApproveDeletes(ctx, act.ID, nil); !errors.Is(err, engine.ErrInvalidInput) {
			t.Fatalf("want ErrInvalidInput, got %v", err)
		}
	}
	st := e.state(t, act.ID)
	if st.State != api.StateIdle || st.Failures != 0 || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if msgs := e.rec.Messages(); len(msgs) != 0 {
		t.Fatalf("notifications = %v", msgs)
	}
}

func TestRepeatedConflictNotifiesOnce(t *testing.T) {
	e, act := conflicted(t)
	e.m.Sync(ctx, act.ID)
	e.m.Sync(ctx, act.ID)
	if st := e.state(t, act.ID); st.State != api.StatePausedConflict {
		t.Fatalf("status = %+v", st)
	}
	if msgs := e.rec.Messages(); len(msgs) != 1 {
		t.Fatalf("notifications = %v", msgs)
	}
}

// gateStore fails or blocks Puts under current/.
type gateStore struct {
	*storage.Memory
	mu      sync.Mutex
	mode    string // "", "fail", "block"
	entered chan struct{}
}

func (g *gateStore) set(mode string) { g.mu.Lock(); g.mode = mode; g.mu.Unlock() }

func (g *gateStore) Put(c context.Context, key string, body []byte, meta map[string]string) (storage.Object, error) {
	g.mu.Lock()
	mode := g.mode
	g.mu.Unlock()
	if strings.Contains(key, "/current/") {
		switch mode {
		case "fail":
			return storage.Object{}, errors.New("network is down")
		case "block":
			select {
			case g.entered <- struct{}{}:
			default:
			}
			<-c.Done()
			return storage.Object{}, c.Err()
		}
	}
	return g.Memory.Put(c, key, body, meta)
}

func TestResumeResetsErrorState(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g, func(o *Options) { o.Backoff = []time.Duration{time.Hour} })
	act, _, _ := e.newAction(t, 1)
	g.set("fail")
	if _, err := e.m.Sync(ctx, act.ID); err == nil {
		t.Fatal("sync should fail")
	}
	if st := e.state(t, act.ID); st.Failures != 1 || st.NextRun.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	g.set("block") // the sync Resume triggers hangs, so we see Resume's own reset
	if err := e.m.Resume(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	st := e.state(t, act.ID)
	if st.Failures != 0 || !st.NextRun.IsZero() || st.LastError != "" || st.State != api.StateIdle {
		t.Fatalf("after resume = %+v", st)
	}
}

func TestCloseInterruptsRunningSync(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g)
	act, _, _ := e.newAction(t, 1)
	g.set("block")
	errc := make(chan error, 1)
	go func() { _, err := e.m.Sync(ctx, act.ID); errc <- err }()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("sync never reached the store")
	}
	closed := make(chan struct{})
	go func() { e.m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return")
	}
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("blocked sync returned no error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked caller never returned")
	}
}

func TestUserPauseSuppressesRetryTimer(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g)
	act, _, _ := e.newAction(t, 1)
	if err := e.m.Pause(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	g.set("fail")
	if _, err := e.m.Sync(ctx, act.ID); err == nil {
		t.Fatal("sync should fail")
	}
	st := e.state(t, act.ID)
	if st.State != api.StatePausedUser || !st.NextRun.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	w, _ := e.m.worker(act.ID)
	w.do(ctx, func(context.Context) (any, error) {
		if w.retry != nil {
			t.Error("retry timer armed while paused")
		}
		return nil, nil
	})
}

func TestLeaveActionCancelsQueuedJobs(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	w, _ := e.m.worker(act.ID)
	started, release := make(chan struct{}), make(chan struct{})
	go w.do(ctx, func(context.Context) (any, error) { close(started); <-release; return nil, nil })
	<-started
	leave := make(chan error, 1)
	go func() { leave <- e.m.LeaveAction(ctx, act.ID, false) }()
	waitFor(t, "leave queued", func() bool { return len(w.jobs) == 1 })
	syncErr := make(chan error, 1)
	go func() { _, err := e.m.Sync(ctx, act.ID); syncErr <- err }()
	waitFor(t, "sync queued", func() bool { return len(w.jobs) == 2 })
	close(release)
	if err := <-leave; err != nil {
		t.Fatal(err)
	}
	if err := <-syncErr; !errors.Is(err, errShuttingDown) && !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("queued sync: %v", err)
	}
	if h, err := e.a.History(act.ID, 10); err == nil && len(h) != 0 {
		t.Fatalf("history after leave: %v", h)
	}
}

func TestCancelledCallerJobDoesNotRun(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	w, _ := e.m.worker(act.ID)
	started, release := make(chan struct{}), make(chan struct{})
	go w.do(ctx, func(context.Context) (any, error) { close(started); <-release; return nil, nil })
	<-started
	cctx, cancel := context.WithCancel(ctx)
	ran := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, err := w.do(cctx, func(context.Context) (any, error) { ran <- struct{}{}; return nil, nil })
		done <- err
	}()
	waitFor(t, "job queued", func() bool { return len(w.jobs) == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	close(release)
	if _, err := w.do(ctx, func(context.Context) (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
		t.Fatal("a job whose caller gave up was executed")
	default:
	}
}

func TestPanicInJobIsRecovered(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	w, _ := e.m.worker(act.ID)
	_, err := w.do(ctx, func(context.Context) (any, error) { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "internal error") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if res, err := e.m.Sync(ctx, act.ID); err != nil || res.Uploaded != 1 {
		t.Fatalf("sync after panic = %+v, %v", res, err)
	}
}

func TestSnapshotConflictDoesNotOverrideUserPause(t *testing.T) {
	e, act := conflicted(t)
	if err := e.m.Pause(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Snapshot(ctx, act.ID); !errors.Is(err, engine.ErrConflictPending) {
		t.Fatalf("snapshot: %v", err)
	}
	if st := e.state(t, act.ID); st.State != api.StatePausedUser {
		t.Fatalf("status = %+v", st)
	}
}

func TestUpdateSettingsValidatesBeforeApplying(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	s, _ := e.m.Settings(ctx)
	old := s
	s.MachineName = "Renamed"
	s.LogLevel = "debug"
	s.Autostart = true
	if _, err := e.m.UpdateSettings(ctx, s); !errors.Is(err, api.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if cur, _ := e.m.Settings(ctx); cur.MachineName != old.MachineName || cur.LogLevel != old.LogLevel {
		t.Fatalf("settings changed by a failing request: %+v", cur)
	}
	s.Autostart = false
	s.LogLevel = "loud"
	if _, err := e.m.UpdateSettings(ctx, s); !errors.Is(err, api.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if cur, _ := e.m.Settings(ctx); cur.MachineName != old.MachineName {
		t.Fatalf("name changed by a failing request: %+v", cur)
	}
	s.LogLevel = "debug"
	act, _, _ := e.newAction(t, 1)
	w, _ := e.m.worker(act.ID)
	e.m.Sync(ctx, act.ID) // caches a runner
	if got, err := e.m.UpdateSettings(ctx, s); err != nil || got.MachineName != "Renamed" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	w.do(ctx, func(context.Context) (any, error) {
		if w.runner != nil {
			t.Error("runner not reset after rename")
		}
		return nil, nil
	})
}
