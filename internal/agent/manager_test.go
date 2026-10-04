package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/notify"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

var ctx = context.Background()

type testEnv struct {
	m    *Manager
	a    *app.App
	rec  *notify.Recorder
	data string
}

func newEnv(t *testing.T, store storage.Storage, opts ...func(*Options)) *testEnv {
	t.Helper()
	data := t.TempDir()
	a, err := app.Open(data, secrets.NewMemory(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
	a.LockSettle = 0
	rec := &notify.Recorder{}
	o := Options{App: a, Notifier: rec, Backoff: []time.Duration{20 * time.Millisecond, 40 * time.Millisecond}, Version: "test"}
	for _, f := range opts {
		f(&o)
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(); a.Close() })
	return &testEnv{m: m, a: a, rec: rec, data: data}
}

// newAction creates a profile and an action with one dir item holding n files.
func (e *testEnv) newAction(t *testing.T, n int) (model.Action, model.Item, string) {
	t.Helper()
	p, err := e.m.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	act, err := e.m.CreateAction(ctx, api.CreateActionRequest{ProfileID: p.ID, Action: model.Action{Name: "Editor", PropagateDeletes: true}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "snippets")
	os.MkdirAll(dir, 0o755)
	for i := 0; i < n; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("s%02d.json", i)), []byte(fmt.Sprintf("snippet %d", i)), 0o644)
	}
	it, err := e.m.AddItem(ctx, act.ID, api.AddItemRequest{Item: model.Item{RemoteName: "snippets", Type: model.ItemDir}, LocalPath: dir})
	if err != nil {
		t.Fatal(err)
	}
	return act, it, dir
}

// join makes machine e join act (created on another env) and map its item to a new folder.
func (e *testEnv) join(t *testing.T, act model.Action, it model.Item) string {
	t.Helper()
	p, err := e.m.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.JoinAction(ctx, api.JoinActionRequest{ProfileID: p.ID, ActionID: act.ID}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "snippets")
	if err := e.m.MapItem(ctx, act.ID, it.ID, dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (e *testEnv) state(t *testing.T, id string) api.ActionStatus {
	t.Helper()
	st, err := e.m.Action(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSyncThroughManager(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 2)
	res, err := e.m.Sync(ctx, act.ID)
	if err != nil || res.Uploaded != 2 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	st := e.state(t, act.ID)
	if st.State != api.StateIdle || st.LastOutcome != model.OutcomeOK || st.LastRun.IsZero() || st.ProfileID == "" {
		t.Fatalf("status = %+v", st)
	}
	if h, err := e.m.History(ctx, act.ID, 5); err != nil || len(h) == 0 {
		t.Fatalf("history = %v, %v", h, err)
	}
	s, err := e.m.Status(ctx)
	if err != nil || s.Version != "test" || len(s.Actions) != 1 || s.MachineID == "" {
		t.Fatalf("Status = %+v, %v", s, err)
	}
	if _, err := e.m.Sync(ctx, "nope"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("unknown action: %v", err)
	}
}

func TestConflictPausesUntilResolved(t *testing.T) {
	store := storage.NewMemory()
	a, b := newEnv(t, store), newEnv(t, store)
	act, it, dirA := a.newAction(t, 1)
	a.m.Sync(ctx, act.ID)
	dirB := b.join(t, act, it)
	b.m.Sync(ctx, act.ID)

	os.WriteFile(filepath.Join(dirA, "s00.json"), []byte("from A"), 0o644)
	a.m.Sync(ctx, act.ID)
	os.WriteFile(filepath.Join(dirB, "s00.json"), []byte("from B!"), 0o644)
	res, err := b.m.Sync(ctx, act.ID)
	if err != nil || res.Outcome != model.OutcomeConflict {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	st := b.state(t, act.ID)
	if st.State != api.StatePausedConflict || st.Conflicts != 1 {
		t.Fatalf("status = %+v", st)
	}
	if msgs := b.rec.Messages(); len(msgs) != 1 || !strings.Contains(msgs[0], "conflict") {
		t.Fatalf("notifications = %v", msgs)
	}
	if d, err := b.m.ConflictDetail(ctx, act.ID, "snippets/s00.json"); err != nil || d.LocalText != "from B!" {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	res, err = b.m.Resolve(ctx, act.ID, map[string]model.Side{"snippets/s00.json": model.SideLocal})
	if err != nil || res.Uploaded != 1 {
		t.Fatalf("resolve = %+v, %v", res, err)
	}
	if st := b.state(t, act.ID); st.State != api.StateIdle || st.Conflicts != 0 {
		t.Fatalf("after resolve = %+v", st)
	}
}

func TestDeleteGuardPausesUntilApproved(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, dir := e.newAction(t, 8)
	e.m.Sync(ctx, act.ID)
	for i := 0; i < 5; i++ {
		os.Remove(filepath.Join(dir, fmt.Sprintf("s%02d.json", i)))
	}
	res, err := e.m.Sync(ctx, act.ID)
	if err != nil || res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	if st := e.state(t, act.ID); st.State != api.StatePausedDeletes || st.PendingDeletes != 5 {
		t.Fatalf("status = %+v", st)
	}
	p, err := e.m.PendingDeletes(ctx, act.ID)
	if err != nil || p == nil || len(p.Paths) != 5 {
		t.Fatalf("pending = %+v, %v", p, err)
	}
	if msgs := e.rec.Messages(); len(msgs) != 1 || !strings.Contains(msgs[0], "approval") {
		t.Fatalf("notifications = %v", msgs)
	}
	// Approving a list the user did not review approves nothing.
	res, err = e.m.ApproveDeletes(ctx, act.ID, p.Paths[:2])
	if err != nil || res.Outcome != model.OutcomeDeletesPending || res.DeletedRemote != 0 {
		t.Fatalf("approve of a different list = %+v, %v", res, err)
	}
	res, err = e.m.ApproveDeletes(ctx, act.ID, p.Paths)
	if err != nil || res.DeletedRemote != 5 {
		t.Fatalf("approve = %+v, %v", res, err)
	}
	if st := e.state(t, act.ID); st.State != api.StateIdle {
		t.Fatalf("after approve = %+v", st)
	}
}

// flakyStore fails the next n Puts under current/.
type flakyStore struct {
	*storage.Memory
	mu   sync.Mutex
	fail int
}

func (f *flakyStore) Put(c context.Context, key string, body []byte, meta map[string]string) (storage.Object, error) {
	f.mu.Lock()
	failing := f.fail > 0 && strings.Contains(key, "/current/")
	if failing {
		f.fail--
	}
	f.mu.Unlock()
	if failing {
		return storage.Object{}, errors.New("network is down")
	}
	return f.Memory.Put(c, key, body, meta)
}

func TestTransientErrorsRetryWithBackoff(t *testing.T) {
	store := &flakyStore{Memory: storage.NewMemory()}
	e := newEnv(t, store)
	act, _, _ := e.newAction(t, 1)
	store.mu.Lock()
	store.fail = 3
	store.mu.Unlock()
	if _, err := e.m.Sync(ctx, act.ID); err == nil {
		t.Fatal("first sync should fail")
	}
	st := e.state(t, act.ID)
	if st.State != api.StateError || st.Failures != 1 || st.NextRun.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	waitFor(t, "automatic retries to succeed", func() bool { return e.state(t, act.ID).State == api.StateIdle })
	msgs := e.rec.Messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "failed 3 times") {
		t.Fatalf("notifications = %v", msgs)
	}
	if st := e.state(t, act.ID); st.Failures != 0 || st.LastError != "" {
		t.Fatalf("after recovery = %+v", st)
	}
}

func TestPermanentErrorIsNotRetried(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, dir := e.newAction(t, 1)
	e.m.Sync(ctx, act.ID)
	os.RemoveAll(dir)
	if _, err := e.m.Sync(ctx, act.ID); !errors.Is(err, engine.ErrRootMissing) {
		t.Fatalf("want ErrRootMissing, got %v", err)
	}
	st := e.state(t, act.ID)
	if st.State != api.StateError || !st.NextRun.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	before, _ := e.m.History(ctx, act.ID, 100)
	time.Sleep(150 * time.Millisecond)
	after, _ := e.m.History(ctx, act.ID, 100)
	if len(after) != len(before) {
		t.Fatal("a permanent error was retried")
	}
	if msgs := e.rec.Messages(); len(msgs) != 1 || !strings.Contains(msgs[0], "needs attention") {
		t.Fatalf("notifications = %v", msgs)
	}
}

func TestPauseResumeSurvivesRestart(t *testing.T) {
	store := storage.NewMemory()
	e := newEnv(t, store)
	act, _, _ := e.newAction(t, 1)
	if err := e.m.Pause(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if st := e.state(t, act.ID); st.State != api.StatePausedUser {
		t.Fatalf("status = %+v", st)
	}
	if res, err := e.m.Sync(ctx, act.ID); err != nil || res.Uploaded != 1 {
		t.Fatalf("a manual sync must run while paused: %+v, %v", res, err)
	}
	if st := e.state(t, act.ID); st.State != api.StatePausedUser {
		t.Fatalf("manual sync left the paused state: %+v", st)
	}
	e.m.Close()
	m2, err := New(Options{App: e.a})
	if err != nil {
		t.Fatal(err)
	}
	m2.Start()
	defer m2.Close()
	st, _ := m2.Action(ctx, act.ID)
	if st.State != api.StatePausedUser {
		t.Fatalf("after restart = %+v", st)
	}
	if err := m2.Resume(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := m2.Action(ctx, act.ID); st.State != api.StateIdle {
		t.Fatalf("after resume = %+v", st)
	}
}

func TestLeaveActionStopsItsWorker(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	if err := e.m.LeaveAction(ctx, act.ID, false); err != nil {
		t.Fatal(err)
	}
	if acts, _ := e.m.Actions(ctx); len(acts) != 0 {
		t.Fatalf("actions = %+v", acts)
	}
	if _, err := e.m.Sync(ctx, act.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("sync after leave: %v", err)
	}
}

func TestSubscribeReceivesStateAndResults(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	sctx, cancel := context.WithCancel(ctx)
	ch, err := e.m.Subscribe(sctx)
	if err != nil {
		t.Fatal(err)
	}
	go e.m.Sync(ctx, act.ID)
	var sawRunning, sawResult bool
	timeout := time.After(5 * time.Second)
	for !(sawRunning && sawResult) {
		select {
		case ev := <-ch:
			if ev.Type == api.EventState && ev.Status != nil && ev.Status.Running {
				sawRunning = true
			}
			if ev.Type == api.EventResult && ev.Result != nil && ev.Result.Uploaded == 1 {
				sawResult = true
			}
		case <-timeout:
			t.Fatalf("events: running=%v result=%v", sawRunning, sawResult)
		}
	}
	cancel()
	for range ch { // closed after cancel
	}
}

func TestRulesAndItemsThroughManager(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, it, _ := e.newAction(t, 1)
	rules := act.Rules()
	rules.Name = "Editor 2"
	if updated, err := e.m.UpdateRules(ctx, act.ID, rules); err != nil || updated.Name != "Editor 2" {
		t.Fatalf("UpdateRules = %+v, %v", updated, err)
	}
	if err := e.m.SetMachineDirection(ctx, act.ID, model.LocalToCloud); err != nil {
		t.Fatal(err)
	}
	if err := e.m.UnmapItem(ctx, act.ID, it.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.m.MapItem(ctx, act.ID, it.ID, filepath.Join(t.TempDir(), "x")); err != nil {
		t.Fatal(err)
	}
	if err := e.m.RemoveItem(ctx, act.ID, it.ID); err != nil {
		t.Fatal(err)
	}
	st := e.state(t, act.ID)
	if st.Action.Name != "Editor 2" || len(st.Action.Items) != 0 || st.Action.DirectionFor(e.a.MachineID) != model.LocalToCloud {
		t.Fatalf("action = %+v", st.Action)
	}
}

type fakeAutostart struct{ on bool }

func (f *fakeAutostart) Enable() error          { f.on = true; return nil }
func (f *fakeAutostart) Disable() error         { f.on = false; return nil }
func (f *fakeAutostart) Enabled() (bool, error) { return f.on, nil }

func TestSettings(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	s, err := e.m.Settings(ctx)
	if err != nil || s.AutostartSupported || s.LogLevel != "info" {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	s.MachineName = "Work Mac"
	s.LogLevel = "debug"
	if s, err = e.m.UpdateSettings(ctx, s); err != nil || s.MachineName != "Work Mac" || s.LogLevel != "debug" {
		t.Fatalf("updated = %+v, %v", s, err)
	}
	s.Autostart = true
	if _, err := e.m.UpdateSettings(ctx, s); !errors.Is(err, api.ErrUnsupported) {
		t.Fatalf("autostart without support: %v", err)
	}

	auto := &fakeAutostart{}
	lv := new(slog.LevelVar)
	e2 := newEnv(t, storage.NewMemory(), func(o *Options) { o.Autostart = auto; o.LogLevel = lv })
	s2, _ := e2.m.Settings(ctx)
	s2.Autostart = true
	s2.LogLevel = "warn"
	if s2, err = e2.m.UpdateSettings(ctx, s2); err != nil || !s2.Autostart || !auto.on || lv.Level() != slog.LevelWarn {
		t.Fatalf("settings = %+v, %v, level %v", s2, err, lv.Level())
	}
}
