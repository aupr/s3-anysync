package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/aupr/s3-anysync/internal/api"
	"github.com/aupr/s3-anysync/internal/engine"
	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/paths"
	"github.com/aupr/s3-anysync/internal/schedule"
	"github.com/aupr/s3-anysync/internal/trigger"
)

// stopTriggersHook is a test hook run at the start of every stopTriggers.
// (Manager.activeTriggerSets, also for tests, counts trigger sets built and
// not yet stopped.)
var stopTriggersHook atomic.Pointer[func()]

// triggerSet is the running triggers of one action.
type triggerSet struct {
	key     string
	cancel  context.CancelFunc
	watcher *trigger.Watcher
	sched   cron.Schedule
	err     error // the triggers could not be built (invalid schedule)
}

// triggerKey fingerprints everything the triggers depend on, so they are
// rebuilt only when it changes.
func triggerKey(a model.Action, machineID string) string {
	type itemKey struct {
		Name, Path string
		Excludes   []string
	}
	var items []itemKey
	for _, it := range a.Items {
		items = append(items, itemKey{it.RemoteName, it.LocalPaths[machineID], it.Excludes})
	}
	b, _ := json.Marshal(struct {
		T        model.Triggers
		Excludes []string
		Items    []itemKey
	}{a.Triggers, a.Excludes, items})
	return string(b)
}

// startTriggers builds the triggers at worker start and asks for one
// catch-up sync when the action has any trigger.
func (w *worker) startTriggers() {
	if !w.m.opts.Triggers {
		return
	}
	a, err := w.m.app.Action(w.id)
	if err != nil {
		w.m.log.Warn("load action for triggers", "action", w.id, "err", err)
		return
	}
	if err := w.buildTriggers(a); err != nil {
		w.afterRun(model.SyncResult{}, err)
		return
	}
	if hasTriggers(a) {
		w.trigger("startup")
	}
}

func hasTriggers(a model.Action) bool {
	return a.Triggers.OnChange || (a.Triggers.Schedule.Kind != "" && a.Triggers.Schedule.Kind != model.ScheduleNone)
}

func (w *worker) stopTriggers() {
	if h := stopTriggersHook.Load(); h != nil {
		(*h)()
	}
	if w.trig == nil {
		return
	}
	w.trig.cancel()
	if w.trig.watcher != nil {
		w.trig.watcher.Close()
	}
	w.trig = nil
	w.m.activeTriggerSets.Add(-1)
}

// refreshTriggers rebuilds the triggers if what they depend on changed
// (rules, excludes or this machine's paths), then syncs once so newly
// watched or newly mapped content is picked up without waiting for an edit.
// It returns errInvalidSchedule (wrapped) while the action's schedule cannot
// be compiled; the caller records that as the action's permanent error.
func (w *worker) refreshTriggers(a model.Action) error {
	if !w.m.opts.Triggers {
		return nil
	}
	if w.trig != nil && w.trig.key == triggerKey(a, w.m.app.MachineID) {
		return w.trig.err
	}
	w.stopTriggers()
	if err := w.buildTriggers(a); err != nil {
		return err
	}
	if w.schedBroken {
		// The schedule was fixed: leave the error state it caused.
		w.schedBroken = false
		// Only an error state is left: a user pause (or conflict/deletion
		// pause) stays.
		w.setState(func(s *wstate) {
			if s.permanent {
				s.permanent, s.failures, s.lastError = false, 0, ""
				if s.state == api.StateError {
					s.state = api.StateIdle
				}
			}
		})
	}
	if paused, err := w.m.app.Paused(w.id); err == nil && paused {
		return nil // resume syncs
	}
	if hasTriggers(a) {
		w.trigger("rules changed")
	}
	return nil
}

// buildTriggers starts the triggers of a. The set is recorded even when the
// schedule is invalid (so the key is remembered and the error repeated).
func (w *worker) buildTriggers(a model.Action) error {
	ctx, cancel := context.WithCancel(w.ctx)
	ts := &triggerSet{key: triggerKey(a, w.m.app.MachineID), cancel: cancel}
	w.trig = ts
	w.m.activeTriggerSets.Add(1)
	// The bucket copy is authoritative and may never have been validated
	// locally: enforce the minimum interval here too.
	err := schedule.Validate(a.Triggers.Schedule)
	var sched cron.Schedule
	if err == nil {
		sched, err = schedule.Compile(a.Triggers.Schedule)
	}
	if err != nil {
		ts.err = fmt.Errorf("%w: %v", errInvalidSchedule, err)
		w.schedBroken = true
		return ts.err
	}
	ts.sched = sched
	if sched != nil {
		go trigger.RunSchedule(ctx, sched, nil, func() { w.trigger("schedule") })
	}
	if !a.Triggers.OnChange {
		return nil
	}
	ms, err := engine.BuildMappings(a, w.m.app.MachineID, paths.Expand)
	if err == nil {
		err = engine.CheckProtected(ms, []string{w.m.app.DataDir})
	}
	if err != nil {
		w.m.log.Warn("watch paths", "action", w.id, "err", err)
	} else {
		var roots []trigger.Root
		for _, mp := range ms {
			roots = append(roots, trigger.Root{Path: mp.LocalPath, Dir: mp.Type == model.ItemDir})
		}
		watcher, err := trigger.Watch(trigger.WatchOptions{
			Roots:    roots,
			Debounce: time.Duration(a.Triggers.DebounceSeconds) * time.Second,
			Match:    func(abs string) bool { _, ok := ms.RemotePathFor(abs); return ok },
			Fire:     func() { w.trigger("change") },
			Log:      w.m.log,
		})
		if err != nil {
			w.m.log.Warn("start file watcher", "action", w.id, "err", err)
		} else {
			ts.watcher = watcher
		}
	}
	if sched == nil {
		interval := time.Duration(a.Triggers.RemotePollMinutes) * time.Minute
		if w.m.opts.PollInterval > 0 {
			interval = w.m.opts.PollInterval
		}
		if interval <= 0 {
			return nil
		}
		// A separate runner: ManifestETag only reads the bucket, and the
		// worker's own runner belongs to the worker goroutine.
		poller, err := w.m.app.Runner(w.id)
		if err != nil {
			w.m.log.Warn("remote poller", "action", w.id, "err", err)
			return nil
		}
		go trigger.RunPoller(ctx, interval, trigger.ETagChecker(poller.ManifestETag), func() { w.trigger("remote") },
			func(err error) { w.m.log.Debug("remote poll", "action", w.id, "err", err) })
	}
	return nil
}

// nextScheduled is the next scheduled run, or zero.
func (w *worker) nextScheduled() time.Time {
	if w.trig == nil || w.trig.sched == nil {
		return time.Time{}
	}
	return w.trig.sched.Next(time.Now())
}
