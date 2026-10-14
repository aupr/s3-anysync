package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/aupr/s3-anysync/internal/api"
	"github.com/aupr/s3-anysync/internal/engine"
	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
)

// errInvalidSchedule marks an action whose schedule cannot be compiled.
var errInvalidSchedule = errors.New("invalid schedule")

// permanentErrs are never retried automatically: they need the user.
var permanentErrs = []error{
	engine.ErrDecrypt, engine.ErrPassphraseMissing, engine.ErrKeyCheckMissing, engine.ErrRootMissing, engine.ErrProtectedPath,
	storage.ErrAccessDenied, storage.ErrNoSuchBucket, errInvalidSchedule,
}

func isPermanent(err error) bool {
	for _, e := range permanentErrs {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

type job struct {
	ctx  context.Context // the caller's: a job whose caller gave up is not run
	fn   func(ctx context.Context) (any, error)
	done chan jobResult
}

type jobResult struct {
	v   any
	err error
}

// wstate is a worker's live state, guarded by worker.mu.
type wstate struct {
	state       string
	running     bool
	lastRun     time.Time
	lastOutcome string
	lastSummary string
	lastError   string
	failures    int
	permanent   bool
	nextRun     time.Time
	conflicts   int
	pending     int
}

// worker serialises everything that happens to one action.
type worker struct {
	m      *Manager
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	jobs   chan *job
	wake   chan string // trigger reasons, coalesced
	done   chan struct{}

	// Owned by the loop goroutine.
	runner *engine.Runner
	gen    uint64 // Manager.nameGen when runner was built
	retry  *time.Timer
	trig   *triggerSet
	// schedBroken: the current error state comes from an invalid schedule.
	schedBroken bool

	mu sync.Mutex
	st wstate
	// jobCancel cancels the running job's context; guarded by mu. The job
	// context derives from w.ctx only (never from a caller's), so a job context
	// that ends while w.ctx lives was cancelled by CancelSync.
	jobCancel context.CancelFunc
}

func newWorker(m *Manager, id string) *worker {
	ctx, cancel := context.WithCancel(m.ctx)
	w := &worker{m: m, id: id, ctx: ctx, cancel: cancel, jobs: make(chan *job, 64), wake: make(chan string, 1), done: make(chan struct{})}
	w.st = w.initialState()
	return w
}

// initialState derives the state from what is stored: a user pause, saved
// conflicts or unapproved pending deletions.
func (w *worker) initialState() wstate {
	st := wstate{state: api.StateIdle}
	if cs, err := w.m.app.Conflicts(w.id); err == nil && len(cs) > 0 {
		st.state, st.conflicts = api.StatePausedConflict, len(cs)
	}
	if p, err := w.m.app.PendingDeletes(w.id); err == nil && p != nil && !p.Approved && st.state == api.StateIdle {
		st.state, st.pending = api.StatePausedDeletes, len(p.Paths)
	}
	if paused, err := w.m.app.Paused(w.id); err == nil && paused {
		st.state = api.StatePausedUser
	}
	return st
}

func (w *worker) loop() {
	defer close(w.done)
	defer w.stopTriggers()
	w.startTriggers()
	for {
		var retryC <-chan time.Time
		if w.retry != nil {
			retryC = w.retry.C
		}
		select {
		case <-w.ctx.Done():
			return
		case j := <-w.jobs:
			w.runJob(j)
		case reason := <-w.wake:
			if w.ctx.Err() != nil {
				return
			}
			w.guard(func() { w.cancellable(func(c context.Context) { w.autoSync(c, reason) }) })
		case <-retryC:
			w.retry = nil
			if w.ctx.Err() != nil {
				return
			}
			w.guard(func() { w.cancellable(func(c context.Context) { w.autoSync(c, "retry") }) })
		}
	}
}

// runJob executes a queued job unless the worker is stopping or the caller
// has already given up.
func (w *worker) runJob(j *job) {
	if err := w.ctx.Err(); err != nil {
		j.done <- jobResult{nil, w.stopErr()}
		return
	}
	if err := j.ctx.Err(); err != nil {
		j.done <- jobResult{nil, err}
		return
	}
	var v any
	var err error
	w.guard(func() { w.cancellable(func(c context.Context) { v, err = j.fn(c) }) }, &err)
	j.done <- jobResult{v, err}
}

// guard runs f and turns a panic into an error (stored in errp if given) so a
// bug in one run cannot kill the worker or the agent.
func (w *worker) guard(f func(), errp ...*error) {
	defer func() {
		if r := recover(); r != nil {
			w.m.log.Error("panic in worker", "action", w.id, "panic", r, "stack", string(debug.Stack()))
			w.runner = nil // may be in a half-updated state
			w.setRunning(false)
			if len(errp) > 0 {
				*errp[0] = fmt.Errorf("agent: internal error: %v", r)
			}
		}
	}()
	f()
}

// trigger asks for a sync; repeated requests while one is queued coalesce.
func (w *worker) trigger(reason string) {
	select {
	case w.wake <- reason:
	default:
	}
}

// stopErr is what callers of a stopped worker get: the agent shutting down is
// unavailable; a worker cancelled because its action was left is not found.
func (w *worker) stopErr() error {
	if w.m.ctx.Err() != nil {
		return errShuttingDown
	}
	return fmt.Errorf("action %s: %w", w.id, api.ErrNotFound)
}

// do runs fn on the worker goroutine and waits for its result.
func (w *worker) do(ctx context.Context, fn func(ctx context.Context) (any, error)) (any, error) {
	j := &job{ctx: ctx, fn: fn, done: make(chan jobResult, 1)}
	select {
	case w.jobs <- j:
	case <-w.ctx.Done():
		return nil, w.stopErr()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-j.done:
		return r.v, r.err
	case <-w.ctx.Done():
		// The job may be the one that stopped the worker (leave): its result
		// is delivered just before the loop exits.
		select {
		case r := <-j.done:
			return r.v, r.err
		case <-w.done:
			select {
			case r := <-j.done:
				return r.v, r.err
			default:
				return nil, w.stopErr()
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// autoSync runs a sync for a trigger unless the state forbids it.
func (w *worker) autoSync(ctx context.Context, reason string) {
	if w.ctx.Err() != nil {
		return // cancelled or left: nothing may run, record or publish
	}
	st := w.snapshot()
	switch st.state {
	case api.StatePausedUser, api.StatePausedConflict, api.StatePausedDeletes:
		return
	case api.StateError:
		if st.permanent || reason != "retry" {
			return
		}
	}
	w.m.log.Info("automatic sync", "action", w.id, "reason", reason)
	w.runSync(ctx, func(ctx context.Context, r *engine.Runner) (model.SyncResult, error) { return r.Sync(ctx) })
}

func (w *worker) getRunner() (*engine.Runner, error) {
	if gen := w.m.nameGen.Load(); w.runner == nil || w.gen != gen {
		r, err := w.m.app.Runner(w.id)
		if err != nil {
			return nil, err
		}
		w.runner, w.gen = r, gen
	}
	return w.runner, nil
}

// runSync runs a sync-like engine call on the worker goroutine and updates
// the state machine from its outcome.
func (w *worker) runSync(ctx context.Context, f func(context.Context, *engine.Runner) (model.SyncResult, error)) (model.SyncResult, error) {
	r, err := w.getRunner()
	if err != nil {
		w.afterRun(model.SyncResult{}, err)
		return model.SyncResult{}, err
	}
	w.setRunning(true)
	res, err := w.callEngine(ctx, f, r)
	w.setRunning(false)
	if errors.Is(err, engine.ErrInvalidInput) || errors.Is(err, engine.ErrConflictChanged) {
		// The caller's request was wrong; the action did not fail.
		return res, err
	}
	if w.ctx.Err() != nil {
		return res, err // shutting down: don't record an interruption as a failure
	}
	if err != nil && ctx.Err() != nil {
		w.afterCancel() // the user cancelled this run
		return res, api.ErrCancelled
	}
	a, aerr := r.Action(), error(nil)
	if w.runner != r {
		a, aerr = w.m.app.Action(w.id) // the runner was discarded after a panic
	}
	if aerr != nil {
		w.m.log.Warn("load action after panic", "action", w.id, "err", aerr)
	} else if terr := w.refreshTriggers(a); terr != nil && err == nil {
		err = terr // an invalid schedule is a permanent error of the action
	}
	w.afterRun(res, err)
	return res, err
}

// callEngine runs an engine call; a panic in it becomes an ordinary error
// (so it is recorded as a failure and retried) and discards the runner.
func (w *worker) callEngine(ctx context.Context, f func(context.Context, *engine.Runner) (model.SyncResult, error), r *engine.Runner) (res model.SyncResult, err error) {
	defer func() {
		if p := recover(); p != nil {
			w.m.log.Error("panic in sync", "action", w.id, "panic", p, "stack", string(debug.Stack()))
			w.runner = nil
			res, err = model.SyncResult{}, fmt.Errorf("agent: internal error: %v", p)
		}
	}()
	return f(ctx, r)
}

// cancellable runs f with a context that CancelSync can cancel.
func (w *worker) cancellable(f func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(w.ctx)
	w.mu.Lock()
	w.jobCancel = cancel
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.jobCancel = nil
		w.mu.Unlock()
		cancel()
	}()
	f(ctx)
}

// cancelRunning drops a pending automatic trigger and cancels the running
// engine job, if any; it reports whether a job was cancelled.
func (w *worker) cancelRunning() bool {
	select {
	case <-w.wake:
	default:
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.jobCancel == nil || !w.st.running {
		return false
	}
	w.jobCancel()
	return true
}

// afterCancel records a run the user cancelled. It is not a failure: the
// state, failure count and error stay as they were before the run.
func (w *worker) afterCancel() {
	retryIn := time.Duration(0)
	func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		st := &w.st
		st.lastRun = time.Now().UTC()
		st.lastOutcome, st.lastSummary = model.OutcomeCancelled, ""
		if st.state == api.StateError && !st.permanent && w.retry == nil {
			retryIn = w.m.backoff(st.failures) // a cancelled retry is re-armed
			st.nextRun = st.lastRun.Add(retryIn)
		}
	}()
	if retryIn > 0 {
		w.retry = time.NewTimer(retryIn)
	}
	w.m.bus.publish(api.Event{Type: api.EventResult, ActionID: w.id, Result: &model.SyncResult{Outcome: model.OutcomeCancelled}})
	w.publishState()
}

// snapshot copies the state; the lock never outlives the call.
func (w *worker) snapshot() wstate {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.st
}

func (w *worker) setRunning(on bool) {
	func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.st.running = on
	}()
	w.publishState()
}

// afterRun applies a run's outcome to the state machine.
func (w *worker) afterRun(res model.SyncResult, err error) {
	name := w.actionName()
	now := time.Now().UTC()
	var message string
	retryIn := time.Duration(0)
	paused, perr := w.m.app.Paused(w.id)
	paused = perr == nil && paused
	func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		st := &w.st
		st.lastRun = now
		switch {
		case err != nil:
			st.failures++
			st.lastOutcome, st.lastError, st.lastSummary = model.OutcomeError, err.Error(), ""
			st.state = api.StateError
			st.permanent = isPermanent(err)
			if st.permanent {
				st.nextRun = time.Time{}
				message = fmt.Sprintf("%s needs attention: %v", name, err)
			} else {
				if !paused {
					retryIn = w.m.backoff(st.failures)
					st.nextRun = now.Add(retryIn)
				}
				if st.failures == NotifyAfterFailures {
					message = fmt.Sprintf("%s failed %d times: %v", name, st.failures, err)
				}
			}
		case res.Outcome == model.OutcomeConflict:
			if st.state != api.StatePausedConflict {
				message = fmt.Sprintf("%s: %d conflict(s) need your choice", name, len(res.Conflicts))
			}
			st.state, st.conflicts = api.StatePausedConflict, len(res.Conflicts)
			st.failures, st.permanent, st.lastError, st.nextRun = 0, false, "", time.Time{}
			st.lastOutcome, st.lastSummary = res.Outcome, res.Summary()
		case res.Outcome == model.OutcomeDeletesPending:
			if st.state != api.StatePausedDeletes {
				message = fmt.Sprintf("%s: %d deletion(s) are waiting for your approval", name, len(res.PendingDeletes))
			}
			st.state, st.pending = api.StatePausedDeletes, len(res.PendingDeletes)
			st.failures, st.permanent, st.lastError, st.nextRun = 0, false, "", time.Time{}
			st.lastOutcome, st.lastSummary = res.Outcome, res.Summary()
		default:
			st.failures, st.permanent, st.lastError = 0, false, ""
			st.conflicts, st.pending = len(res.Conflicts), 0
			st.lastOutcome, st.lastSummary = res.Outcome, res.Summary()
			st.state = api.StateIdle
			st.nextRun = w.nextScheduled()
		}
		if paused {
			st.state = api.StatePausedUser
			st.nextRun = time.Time{}
		}
	}()

	if w.retry != nil {
		w.retry.Stop()
		w.retry = nil
	}
	if retryIn > 0 {
		w.retry = time.NewTimer(retryIn)
	}
	if message != "" {
		w.m.notify(message)
	}
	ev := api.Event{Type: api.EventResult, ActionID: w.id}
	if err != nil {
		ev.Error = err.Error()
	} else {
		r := res
		ev.Result = &r
	}
	w.m.bus.publish(ev)
	w.publishState()
}

func (w *worker) actionName() string {
	if a, err := w.m.app.Action(w.id); err == nil {
		return a.Name
	}
	return w.id
}

// setState changes the state outside a run (pause, resume, snapshot conflicts).
func (w *worker) setState(f func(*wstate)) {
	func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		f(&w.st)
	}()
	w.publishState()
}

func (w *worker) status(a model.Action) api.ActionStatus {
	st := w.snapshot()
	return api.ActionStatus{
		Action: a, ProfileID: a.ProfileID, State: st.state, Running: st.running,
		LastRun: st.lastRun, LastOutcome: st.lastOutcome, LastSummary: st.lastSummary, LastError: st.lastError,
		Failures: st.failures, NextRun: st.nextRun, Conflicts: st.conflicts, PendingDeletes: st.pending,
	}
}

func (w *worker) publishState() {
	a, err := w.m.app.Action(w.id)
	if err != nil {
		return
	}
	st := w.status(a)
	w.m.bus.publish(api.Event{Type: api.EventState, ActionID: w.id, Status: &st})
}
