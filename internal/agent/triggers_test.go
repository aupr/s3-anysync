package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/schedule"
	"github.com/niceit/s3-anysync/internal/storage"
)

func withTriggers(o *Options) {
	o.Triggers = true
	o.PollInterval = 100 * time.Millisecond
}

// newTriggeredAction is newAction with the given rules applied.
func (e *testEnv) newTriggeredAction(t *testing.T, n int, set func(*model.Rules)) (model.Action, model.Item, string) {
	t.Helper()
	act, it, dir := e.newAction(t, n)
	rules := act.Rules()
	set(&rules)
	updated, err := e.m.UpdateRules(ctx, act.ID, rules)
	if err != nil {
		t.Fatal(err)
	}
	return updated, it, dir
}

func onChange(r *model.Rules) {
	r.Triggers.OnChange = true
	r.Triggers.DebounceSeconds = 1
}

func TestChangesSyncAutomaticallyBetweenMachines(t *testing.T) {
	store := storage.NewMemory()
	a, b := newEnv(t, store, withTriggers), newEnv(t, store, withTriggers)
	act, it, dirA := a.newTriggeredAction(t, 1, onChange)
	waitFor(t, "A's first automatic sync", func() bool { return a.state(t, act.ID).LastOutcome == model.OutcomeOK })
	dirB := b.join(t, act, it)
	waitFor(t, "B downloads the file", func() bool {
		data, err := os.ReadFile(filepath.Join(dirB, "s00.json"))
		return err == nil && string(data) == "snippet 0"
	})

	os.WriteFile(filepath.Join(dirA, "s00.json"), []byte("edited on A"), 0o644)
	waitFor(t, "the edit to reach B", func() bool {
		data, _ := os.ReadFile(filepath.Join(dirB, "s00.json"))
		return string(data) == "edited on A"
	})
}

func TestScheduleTriggersSyncs(t *testing.T) {
	// Production rejects schedules faster than schedule.MinInterval (one
	// minute); this test needs a one-second schedule.
	old := schedule.MinInterval
	schedule.MinInterval = 0
	t.Cleanup(func() { schedule.MinInterval = old })
	e := newEnv(t, storage.NewMemory(), withTriggers)
	act, _, _ := e.newTriggeredAction(t, 1, func(r *model.Rules) {
		r.Triggers.Schedule = model.Schedule{Kind: model.ScheduleCron, Cron: "@every 1s"}
	})
	// One sync follows the rule change itself; more must come from the schedule.
	waitFor(t, "repeated scheduled syncs", func() bool {
		h, _ := e.m.History(ctx, act.ID, 100)
		return len(h) >= 3
	})
	if st := e.state(t, act.ID); st.NextRun.IsZero() {
		t.Fatalf("next run not reported: %+v", st)
	}
}

func TestPausedActionIgnoresTriggers(t *testing.T) {
	store := storage.NewMemory()
	e := newEnv(t, store, withTriggers)
	act, _, dir := e.newTriggeredAction(t, 1, onChange)
	waitFor(t, "first sync", func() bool { return e.state(t, act.ID).LastOutcome == model.OutcomeOK })
	if err := e.m.Pause(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := e.m.History(ctx, act.ID, 100)
	os.WriteFile(filepath.Join(dir, "s00.json"), []byte("while paused"), 0o644)
	time.Sleep(2500 * time.Millisecond)
	after, _ := e.m.History(ctx, act.ID, 100)
	if len(after) != len(before) {
		t.Fatal("a paused action synced on a trigger")
	}
	if err := e.m.Resume(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "sync after resume", func() bool {
		h, _ := e.m.History(ctx, act.ID, 100)
		return len(h) > len(after) && e.state(t, act.ID).State == api.StateIdle
	})
}

func TestRuleChangeRebuildsTriggers(t *testing.T) {
	e := newEnv(t, storage.NewMemory(), withTriggers)
	act, _, dir := e.newAction(t, 1) // no triggers yet
	time.Sleep(300 * time.Millisecond)
	if st := e.state(t, act.ID); !st.LastRun.IsZero() {
		t.Fatalf("an action without triggers synced by itself: %+v", st)
	}
	rules := act.Rules()
	onChange(&rules)
	if _, err := e.m.UpdateRules(ctx, act.ID, rules); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "s00.json"), []byte("now watched"), 0o644)
	waitFor(t, "sync after enabling onChange", func() bool { return e.state(t, act.ID).LastOutcome == model.OutcomeOK })
}

func TestStartupCatchUp(t *testing.T) {
	store := storage.NewMemory()
	e := newEnv(t, store)
	act, _, _ := e.newTriggeredAction(t, 1, onChange)
	e.m.Close()
	m2, err := New(Options{App: e.a, Triggers: true, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := m2.Start(); err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	waitFor(t, "catch-up sync at start", func() bool {
		st, _ := m2.Action(ctx, act.ID)
		return st.LastOutcome == model.OutcomeOK
	})
}

func TestNewSubdirectoryContentIsSynced(t *testing.T) {
	store := storage.NewMemory()
	a, b := newEnv(t, store, withTriggers), newEnv(t, store, withTriggers)
	act, it, dirA := a.newTriggeredAction(t, 1, onChange)
	waitFor(t, "A's first sync", func() bool { return a.state(t, act.ID).LastOutcome == model.OutcomeOK })
	dirB := b.join(t, act, it)
	waitFor(t, "B downloads", func() bool { _, err := os.Stat(filepath.Join(dirB, "s00.json")); return err == nil })

	// Content built elsewhere and moved in: only the directory's create event
	// is seen, so the directory path must match.
	staging := filepath.Join(t.TempDir(), "newdir")
	os.MkdirAll(staging, 0o755)
	os.WriteFile(filepath.Join(staging, "inner.json"), []byte("inner"), 0o644)
	if err := os.Rename(staging, filepath.Join(dirA, "newdir")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "moved-in directory to reach B", func() bool {
		data, err := os.ReadFile(filepath.Join(dirB, "newdir", "inner.json"))
		return err == nil && string(data) == "inner"
	})
}

func TestRemovedFileSyncs(t *testing.T) {
	store := storage.NewMemory()
	a, b := newEnv(t, store, withTriggers), newEnv(t, store, withTriggers)
	act, it, dirA := a.newTriggeredAction(t, 2, onChange)
	waitFor(t, "A's first sync", func() bool { return a.state(t, act.ID).LastOutcome == model.OutcomeOK })
	dirB := b.join(t, act, it)
	waitFor(t, "B downloads", func() bool { _, err := os.Stat(filepath.Join(dirB, "s01.json")); return err == nil })
	os.Remove(filepath.Join(dirA, "s01.json"))
	waitFor(t, "the removal to reach B", func() bool { _, err := os.Stat(filepath.Join(dirB, "s01.json")); return os.IsNotExist(err) })
}

func TestInvalidScheduleIsPermanentError(t *testing.T) {
	e := newEnv(t, storage.NewMemory(), withTriggers)
	act, _, _ := e.newAction(t, 1)
	// Bypass rule validation: store an unparseable schedule directly.
	a, err := e.a.Action(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Triggers.Schedule = model.Schedule{Kind: model.ScheduleCron, Cron: "not a cron"}
	if err := e.a.DB.PutAction(a); err != nil {
		t.Skipf("cannot store a raw action: %v", err)
	}
	e.m.Close()
	m2, err := New(Options{App: e.a, Triggers: true, PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := m2.Start(); err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	waitFor(t, "permanent error", func() bool {
		st, _ := m2.Action(ctx, act.ID)
		return st.State == api.StateError && strings.Contains(st.LastError, "invalid schedule")
	})
	time.Sleep(200 * time.Millisecond)
	if st, _ := m2.Action(ctx, act.ID); st.State != api.StateError || !st.NextRun.IsZero() {
		t.Fatalf("not permanent: %+v", st)
	}
}

func TestCloseWaitsForForgottenWorker(t *testing.T) {
	e := newEnv(t, storage.NewMemory(), withTriggers)
	act, _, _ := e.newTriggeredAction(t, 1, onChange)
	waitFor(t, "first sync", func() bool { return e.state(t, act.ID).LastOutcome == model.OutcomeOK })
	var finished atomic.Bool
	release := make(chan struct{})
	entered := make(chan struct{})
	hook := func() { close(entered); <-release; finished.Store(true) }
	stopTriggersHook.Store(&hook)
	t.Cleanup(func() { stopTriggersHook.Store(nil) })

	leaveDone := make(chan error, 1)
	go func() { leaveDone <- e.m.LeaveAction(ctx, act.ID, true) }()
	// The leave job forgot the worker; its loop is now stuck in stopTriggers.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("stopTriggers never started")
	}
	closed := make(chan struct{})
	go func() { e.m.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while a forgotten worker was still stopping")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	if !finished.Load() {
		t.Fatal("stopTriggers did not finish before Close returned")
	}
	if err := <-leaveDone; err != nil {
		t.Fatal(err)
	}
}

func TestRejoinNeverHasTwoTriggerSets(t *testing.T) {
	store := storage.NewMemory()
	a, b := newEnv(t, store, withTriggers), newEnv(t, store, withTriggers)
	act, _, _ := a.newTriggeredAction(t, 1, onChange)
	waitFor(t, "A's first sync", func() bool { return a.state(t, act.ID).LastOutcome == model.OutcomeOK })
	p, err := b.m.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	join := api.JoinActionRequest{ProfileID: p.ID, ActionID: act.ID}
	if _, err := b.m.JoinAction(ctx, join); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "snippets")
	if err := b.m.MapItem(ctx, act.ID, act.Items[0].ID, dir); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "B's trigger set", func() bool { return b.m.activeTriggerSets.Load() == 1 })
	var peak atomic.Int32
	hook := func() { time.Sleep(200 * time.Millisecond) }
	stopTriggersHook.Store(&hook)
	t.Cleanup(func() { stopTriggersHook.Store(nil) })
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if v := b.m.activeTriggerSets.Load(); v > peak.Load() {
				peak.Store(v)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	leaveDone := make(chan error, 1)
	go func() { leaveDone <- b.m.LeaveAction(ctx, act.ID, true) }()
	// Rejoin while the old worker is still stopping its triggers.
	waitFor(t, "the leave to cancel the worker", func() bool { _, err := b.m.worker(act.ID); return err != nil })
	if _, err := b.m.JoinAction(ctx, join); err != nil {
		t.Fatal(err)
	}
	if err := <-leaveDone; err != nil {
		t.Fatal(err)
	}
	close(stop)
	if peak.Load() > 1 {
		t.Fatalf("%d trigger sets were live at once", peak.Load())
	}
}

func TestStaleWorkerReportsNotFound(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	w, err := e.m.worker(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.LeaveAction(ctx, act.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err = w.do(ctx, func(context.Context) (any, error) { return nil, nil })
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("stale worker error = %v", err)
	}
	e.m.Close()
	_, err = w.do(ctx, func(context.Context) (any, error) { return nil, nil })
	if !errors.Is(err, api.ErrUnavailable) {
		t.Fatalf("after Close error = %v", err)
	}
}

// setBucketSchedule rewrites the bucket copy of an action's schedule behind
// the agent's back (as another machine without local validation could).
func (e *testEnv) setBucketSchedule(t *testing.T, store storage.Storage, id string, s model.Schedule) {
	t.Helper()
	a, err := e.a.Action(id)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.a.DB.GetProfile(a.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	key := engine.NewLayout(p.RootPrefix, id).ActionJSON()
	data, _, err := store.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var raw model.Action
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw.Triggers.Schedule = s
	raw.UpdatedAt = raw.UpdatedAt.Add(time.Second)
	data, _ = json.Marshal(raw)
	if _, err := store.Put(ctx, key, data, nil); err != nil {
		t.Fatal(err)
	}
}

func validSchedule() model.Schedule {
	return model.Schedule{Kind: model.ScheduleCron, Cron: "0 3 * * *"}
}

func TestBucketScheduleTooFastOrNeverIsPermanentError(t *testing.T) {
	for _, cron := range []string{"@every 1s", "0 0 30 2 *"} {
		t.Run(cron, func(t *testing.T) {
			store := storage.NewMemory()
			e := newEnv(t, store, withTriggers)
			act, _, _ := e.newAction(t, 1)
			e.setBucketSchedule(t, store, act.ID, model.Schedule{Kind: model.ScheduleCron, Cron: cron})
			if _, err := e.m.Sync(ctx, act.ID); err != nil && !errors.Is(err, errInvalidSchedule) {
				t.Fatal(err)
			}
			waitFor(t, "permanent error", func() bool {
				st := e.state(t, act.ID)
				return st.State == api.StateError && strings.Contains(st.LastError, "invalid schedule")
			})
			before := historyLen(t, e, act.ID)
			time.Sleep(2 * time.Second)
			if after := historyLen(t, e, act.ID); after != before {
				t.Fatalf("history grew %d -> %d: syncs keep running", before, after)
			}
			if st := e.state(t, act.ID); st.State != api.StateError || !st.NextRun.IsZero() {
				t.Fatalf("not a permanent error: %+v", st)
			}
			// Fixing the schedule clears the error and syncing resumes.
			rules := act.Rules()
			rules.Triggers.Schedule = validSchedule()
			if _, err := e.m.UpdateRules(ctx, act.ID, rules); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "error cleared and sync", func() bool {
				return e.state(t, act.ID).State == api.StateIdle && historyLen(t, e, act.ID) > before
			})
		})
	}
}

func TestFixedScheduleDoesNotOverridePause(t *testing.T) {
	store := storage.NewMemory()
	e := newEnv(t, store, withTriggers)
	act, _, _ := e.newAction(t, 1)
	e.setBucketSchedule(t, store, act.ID, model.Schedule{Kind: model.ScheduleCron, Cron: "@every 1s"})
	e.m.Sync(ctx, act.ID)
	waitFor(t, "permanent error", func() bool { return e.state(t, act.ID).State == api.StateError })
	if err := e.m.Pause(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	before := historyLen(t, e, act.ID)
	rules := act.Rules()
	rules.Triggers.Schedule = validSchedule()
	if _, err := e.m.UpdateRules(ctx, act.ID, rules); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if st := e.state(t, act.ID); st.State != api.StatePausedUser {
		t.Fatalf("state = %s, want paused-user", st.State)
	}
	if after := historyLen(t, e, act.ID); after != before {
		t.Fatalf("a paused action synced: history %d -> %d", before, after)
	}
	if err := e.m.Resume(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "sync after resume", func() bool {
		return historyLen(t, e, act.ID) > before && e.state(t, act.ID).State == api.StateIdle
	})
}

func TestPanicWithTriggersKeepsTriggersAndWorker(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g, withTriggers, func(o *Options) { o.Backoff = []time.Duration{time.Hour} })
	act, _, dir := e.newTriggeredAction(t, 1, onChange)
	waitFor(t, "first sync", func() bool { return e.state(t, act.ID).LastOutcome == model.OutcomeOK })
	w, _ := e.m.worker(act.ID)
	var stops atomic.Int32
	hook := func() { stops.Add(1) }
	stopTriggersHook.Store(&hook)
	t.Cleanup(func() { stopTriggersHook.Store(nil) })
	os.WriteFile(filepath.Join(dir, "s00.json"), []byte("changed"), 0o644)
	g.set("panic")
	w.trigger("wake")
	waitFor(t, "panic recorded", func() bool { st := e.state(t, act.ID); return st.State == api.StateError && st.Failures >= 1 })
	if _, err := w.do(ctx, func(context.Context) (any, error) {
		if w.runner != nil {
			t.Error("runner kept after panic")
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := e.m.activeTriggerSets.Load(); n != 1 || stops.Load() != 0 {
		t.Fatalf("trigger sets = %d, stops = %d; triggers must survive the panic", n, stops.Load())
	}
	g.set("")
	if _, err := e.m.Sync(ctx, act.ID); err != nil {
		t.Fatalf("sync after panic: %v", err)
	}
	if st := e.state(t, act.ID); st.State != api.StateIdle {
		t.Fatalf("state = %+v", st)
	}
}

func TestPanicWithoutActionLookupKeepsTriggers(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g, withTriggers, func(o *Options) { o.Backoff = []time.Duration{time.Hour} })
	act, _, dir := e.newTriggeredAction(t, 1, onChange)
	waitFor(t, "first sync", func() bool { return e.state(t, act.ID).LastOutcome == model.OutcomeOK })
	w, _ := e.m.worker(act.ID)
	var stops atomic.Int32
	hook := func() { stops.Add(1) }
	stopTriggersHook.Store(&hook)
	t.Cleanup(func() { stopTriggersHook.Store(nil) })
	saved, err := e.a.Action(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The local action row disappears just before the panic: the lookup
	// after the panic fails, and must not tear the triggers down.
	g.onPanic = func() { e.a.DB.DeleteAction(act.ID) }
	os.WriteFile(filepath.Join(dir, "s00.json"), []byte("changed"), 0o644)
	g.set("panic")
	w.trigger("wake")
	waitFor(t, "panic recorded", func() bool { return w.snapshot().failures >= 1 })
	time.Sleep(200 * time.Millisecond)
	if n := e.m.activeTriggerSets.Load(); n != 1 || stops.Load() != 0 {
		t.Fatalf("trigger sets = %d, stops = %d; a failed lookup tore the triggers down", n, stops.Load())
	}
	g.set("")
	e.a.DB.PutAction(saved)
}
