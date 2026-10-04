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
	e, act, _ := conflictedIn(t)
	return e, act
}

// conflictedIn is conflicted that also returns the local item folder of env b.
func conflictedIn(t *testing.T) (*testEnv, model.Action, string) {
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
	return b, act, dirB
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
	_, err := e.m.Resolve(ctx, act.ID, api.ResolveRequest{Choices: map[string]model.Side{"snippets/s00.json": "both"}, Reviewed: reviewedOf(t, e, act.ID)})
	if !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
	_, err = e.m.Resolve(ctx, act.ID, api.ResolveRequest{Choices: map[string]model.Side{}, Reviewed: reviewedOf(t, e, act.ID)})
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
	release chan struct{} // for mode "hold"
	onPanic func()        // run just before a "panic" Put panics
}

// hold parks any storage call while mode is "hold", ignoring contexts, so a
// test can keep a job running past its caller's deadline.
func (g *gateStore) hold() {
	g.mu.Lock()
	mode := g.mode
	g.mu.Unlock()
	if mode != "hold" {
		return
	}
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
}

func (g *gateStore) Get(c context.Context, key string) ([]byte, storage.Object, error) {
	g.hold()
	return g.Memory.Get(c, key)
}

func (g *gateStore) PutIfAbsent(c context.Context, key string, body []byte) (storage.Object, error) {
	g.hold()
	return g.Memory.PutIfAbsent(c, key, body)
}

func (g *gateStore) PutIfMatch(c context.Context, key string, body []byte, etag string) (storage.Object, error) {
	g.hold()
	return g.Memory.PutIfMatch(c, key, body, etag)
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
		case "panic":
			if g.onPanic != nil {
				g.onPanic()
			}
			panic("storage exploded")
		case "block":
			select {
			case g.entered <- struct{}{}:
			default:
			}
			<-c.Done()
			return storage.Object{}, c.Err()
		}
	}
	g.hold()
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
	if _, err := w.do(ctx, func(context.Context) (any, error) {
		if w.retry != nil {
			t.Error("retry timer armed while paused")
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
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
	var oldRunner *engine.Runner
	if _, err := w.do(ctx, func(context.Context) (any, error) { oldRunner = w.runner; return nil, nil }); err != nil || oldRunner == nil {
		t.Fatalf("no cached runner: %v", err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	jobDone := make(chan struct{})
	go func() {
		w.do(ctx, func(context.Context) (any, error) { close(started); <-release; return nil, nil })
		close(jobDone)
	}()
	<-started
	upd := make(chan error, 1)
	go func() {
		got, err := e.m.UpdateSettings(ctx, s)
		if err == nil && got.MachineName != "Renamed" {
			err = errors.New("name not applied: " + got.MachineName)
		}
		upd <- err
	}()
	select {
	case err := <-upd:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("UpdateSettings blocked behind a busy worker")
	}
	close(release)
	<-jobDone
	if _, err := e.m.Sync(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.do(ctx, func(context.Context) (any, error) {
		if w.runner == nil || w.runner == oldRunner || w.gen != e.m.nameGen.Load() {
			t.Error("runner not rebuilt after the rename")
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeaveWithExpiredCallerContextDoesNotLeaveDeadWorker(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	e := newEnv(t, g)
	act, _, _ := e.newAction(t, 1)
	g.set("hold")
	cctx, cancel := context.WithCancel(ctx)
	leave := make(chan error, 1)
	go func() { leave <- e.m.LeaveAction(cctx, act.ID, false) }()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("leave never reached storage")
	}
	cancel() // the caller gives up while the leave is running
	if err := <-leave; !errors.Is(err, context.Canceled) {
		t.Fatalf("leave err = %v", err)
	}
	g.set("")
	close(g.release) // the leave finishes and cancels the worker after the caller left
	waitFor(t, "action gone", func() bool { _, err := e.a.DB.GetAction(act.ID); return err != nil })
	// The same action id can be joined again and works.
	p, _ := e.m.Profiles(ctx)
	if _, err := e.m.JoinAction(ctx, api.JoinActionRequest{ProfileID: p[0].ID, ActionID: act.ID}); err != nil {
		t.Fatalf("join after leave: %v", err)
	}
	if _, err := e.m.Sync(ctx, act.ID); err != nil {
		t.Fatalf("sync after rejoin: %v", err)
	}
}

func TestCancelledWorkerRunsNoSync(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	w, _ := e.m.worker(act.ID)
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	ch, _ := e.m.Subscribe(sctx)
	w.cancel()
	<-w.done
	w.autoSync(ctx, "wake")
	w.autoSync(ctx, "retry")
	select {
	case ev := <-ch:
		t.Fatalf("event after cancel: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
	if n := historyLen(t, e, act.ID); n != 0 {
		t.Fatalf("history = %d", n)
	}
}

func TestPanicInSyncIsRecordedAsFailure(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g, func(o *Options) { o.Backoff = []time.Duration{time.Hour} })
	act, _, dir := e.newAction(t, 1)
	w, _ := e.m.worker(act.ID)
	e.m.Sync(ctx, act.ID) // cache a runner
	os.WriteFile(filepath.Join(dir, "s00.json"), []byte("changed"), 0o644)
	g.set("panic")
	w.trigger("wake") // an automatic sync that panics
	waitFor(t, "panic recorded", func() bool { st := e.state(t, act.ID); return st.State == api.StateError && st.Failures >= 1 })
	if _, err := w.do(ctx, func(context.Context) (any, error) {
		if w.runner != nil {
			t.Error("runner kept after panic")
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	st := e.state(t, act.ID)
	if !strings.Contains(st.LastError, "internal error") || st.NextRun.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	g.set("")
	if _, err := e.m.Sync(ctx, act.ID); err != nil {
		t.Fatalf("sync after panic: %v", err)
	}
	if st := e.state(t, act.ID); st.State != api.StateIdle || st.Failures != 0 {
		t.Fatalf("status = %+v", st)
	}
}

func TestPauseClearsRetryTimerAndNextRun(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g, func(o *Options) { o.Backoff = []time.Duration{150 * time.Millisecond} })
	act, _, _ := e.newAction(t, 1)
	g.set("fail")
	if _, err := e.m.Sync(ctx, act.ID); err == nil {
		t.Fatal("sync should fail")
	}
	if st := e.state(t, act.ID); st.NextRun.IsZero() {
		t.Fatalf("no retry armed: %+v", st)
	}
	if err := e.m.Pause(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if st := e.state(t, act.ID); st.State != api.StatePausedUser || !st.NextRun.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	before := historyLen(t, e, act.ID)
	time.Sleep(400 * time.Millisecond)
	if n := historyLen(t, e, act.ID); n != before {
		t.Fatalf("a retry ran while paused (%d -> %d)", before, n)
	}
	w, _ := e.m.worker(act.ID)
	if _, err := w.do(ctx, func(context.Context) (any, error) {
		if w.retry != nil {
			t.Error("retry timer still armed")
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDoReturnsWhenCallerGivesUpDuringShutdown(t *testing.T) {
	wctx, wcancel := context.WithCancel(ctx)
	w := &worker{ctx: wctx, cancel: wcancel, jobs: make(chan *job, 1), done: make(chan struct{})} // loop never runs
	cctx, ccancel := context.WithCancel(ctx)
	res := make(chan error, 1)
	go func() { _, err := w.do(cctx, func(context.Context) (any, error) { return nil, nil }); res <- err }()
	waitFor(t, "job queued", func() bool { return len(w.jobs) == 1 })
	wcancel()
	time.Sleep(50 * time.Millisecond)
	ccancel()
	select {
	case err := <-res:
		if err == nil {
			t.Fatal("want an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("do ignored the caller's context while waiting for shutdown")
	}
}

func TestShuttingDownIsUnavailable(t *testing.T) {
	if !errors.Is(errShuttingDown, api.ErrUnavailable) || api.Code(errShuttingDown) != "unavailable" {
		t.Fatalf("errShuttingDown = %v (%s)", errShuttingDown, api.Code(errShuttingDown))
	}
}

func TestItemContainingDataFolderIsRefusedPermanently(t *testing.T) {
	store := storage.NewMemory()
	e := newEnv(t, store)
	act, it, _ := e.newAction(t, 1)
	// Bypass the app-level validation: an item mapped to the data folder, as
	// an older version or a hand-edited database could have it.
	local, err := e.a.Action(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range local.Items {
		if local.Items[i].ID == it.ID {
			local.Items[i].LocalPaths[e.a.MachineID] = e.data
		}
	}
	if err := e.a.DB.PutAction(local); err != nil {
		t.Fatal(err)
	}
	before := len(store.Keys())
	if _, err := e.m.Sync(ctx, act.ID); !errors.Is(err, engine.ErrProtectedPath) {
		t.Fatalf("want ErrProtectedPath, got %v", err)
	}
	st := e.state(t, act.ID)
	if st.State != api.StateError || !st.NextRun.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	for _, k := range store.Keys() {
		if strings.Contains(k, "state.db") || strings.Contains(k, "ipc.token") || strings.Contains(k, "agent.lock") {
			t.Fatalf("the agent's own file was uploaded: %s", k)
		}
	}
	if after := len(store.Keys()); after > before+1 {
		t.Fatalf("objects were written: %d -> %d", before, after)
	}
}

func TestCancelSyncIsNotAFailure(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g)
	act, _, _ := e.newAction(t, 3)
	if err := e.m.CancelSync(ctx, act.ID); err != nil {
		t.Fatalf("cancel while idle: %v", err)
	}
	if err := e.m.CancelSync(ctx, "nope"); api.Code(err) != "not-found" {
		t.Fatalf("cancel unknown: %v", err)
	}
	sub, cancelSub := context.WithCancel(ctx)
	defer cancelSub()
	events, _ := e.m.Subscribe(sub)

	g.set("block")
	errc := make(chan error, 1)
	go func() { _, err := e.m.Sync(ctx, act.ID); errc <- err }()
	<-g.entered
	if err := e.m.CancelSync(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, api.ErrCancelled) {
			t.Fatalf("sync err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the sync")
	}
	st := e.state(t, act.ID)
	if st.State != api.StateIdle || st.Running || st.Failures != 0 || st.LastOutcome != model.OutcomeCancelled || st.LastError != "" {
		t.Fatalf("after cancel = %+v", st)
	}
	if msgs := e.rec.Messages(); len(msgs) != 0 {
		t.Fatalf("notified: %v", msgs)
	}
	sawCancelled := false
	for !sawCancelled {
		select {
		case ev := <-events:
			sawCancelled = ev.Type == api.EventResult && ev.Result != nil && ev.Result.Outcome == model.OutcomeCancelled
		case <-time.After(5 * time.Second):
			t.Fatal("no cancelled result event")
		}
	}

	// Nothing was left half-done: the lock is free and the next run converges.
	g.set("")
	res, err := e.m.Sync(ctx, act.ID)
	if err != nil || res.Uploaded != 3 {
		t.Fatalf("sync after cancel = %+v, %v", res, err)
	}
	res, err = e.m.Sync(ctx, act.ID)
	if err != nil || res.Outcome != model.OutcomeOK || res.Uploaded+res.Downloaded+res.DeletedLocal+res.DeletedRemote != 0 {
		t.Fatalf("third sync = %+v, %v", res, err)
	}
}

// A cancel that finds nothing running must not leak into the next job.
func TestCancelSyncWhenIdleDoesNotCancelNextJob(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 2)
	for i := 0; i < 3; i++ {
		if err := e.m.CancelSync(ctx, act.ID); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.m.Sync(ctx, act.ID)
	if err != nil || res.Uploaded != 2 {
		t.Fatalf("sync after idle cancels = %+v, %v", res, err)
	}
	// A cancel after the job finished is also a no-op.
	if err := e.m.CancelSync(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if st := e.state(t, act.ID); st.LastOutcome != model.OutcomeOK {
		t.Fatalf("after late cancel = %+v", st)
	}
}

func reviewedOf(t *testing.T, e *testEnv, id string) map[string]api.ConflictVersion {
	t.Helper()
	list, err := e.m.Conflicts(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]api.ConflictVersion{}
	for _, c := range list {
		var v api.ConflictVersion
		if c.Local != nil {
			v.LocalHash = c.Local.Hash
		}
		if c.Remote != nil {
			v.RemoteHash = c.Remote.Hash
		}
		out[c.Path] = v
	}
	return out
}

func TestStaleResolveIsNotASyncFailure(t *testing.T) {
	e, act, dir := conflictedIn(t)
	old := reviewedOf(t, e, act.ID)
	// the conflict is saved again with new local content
	if err := os.WriteFile(filepath.Join(dir, "s00.json"), []byte("edited again"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := e.m.Sync(ctx, act.ID); err != nil || res.Outcome != model.OutcomeConflict {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	before := historyLen(t, e, act.ID)
	_, err := e.m.Resolve(ctx, act.ID, api.ResolveRequest{
		Choices:  map[string]model.Side{"snippets/s00.json": model.SideLocal},
		Reviewed: old,
	})
	if !errors.Is(err, engine.ErrConflictChanged) {
		t.Fatalf("want ErrConflictChanged, got %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	st := e.state(t, act.ID)
	if st.State != api.StatePausedConflict || st.Conflicts != 1 || st.Failures != 0 || !st.NextRun.IsZero() || st.LastError != "" {
		t.Fatalf("status = %+v", st)
	}
	if n := historyLen(t, e, act.ID); n != before {
		t.Fatalf("history grew %d -> %d: a retry ran", before, n)
	}
	res, err := e.m.Resolve(ctx, act.ID, api.ResolveRequest{
		Choices:  map[string]model.Side{"snippets/s00.json": model.SideLocal},
		Reviewed: reviewedOf(t, e, act.ID),
	})
	if err != nil || res.Outcome != model.OutcomeOK {
		t.Fatalf("fresh resolve = %+v, %v", res, err)
	}
}
