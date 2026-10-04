package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/logging"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func (m *Manager) Status(ctx context.Context) (api.Status, error) {
	acts, err := m.Actions(ctx)
	return api.Status{MachineID: m.app.MachineID, MachineName: m.app.CurrentMachineName(), Version: m.opts.Version, Agent: m.opts.Triggers, Actions: acts}, err
}

func (m *Manager) Settings(ctx context.Context) (api.Settings, error) {
	lvl, err := m.app.LogLevel()
	if err != nil {
		return api.Settings{}, err
	}
	s := api.Settings{MachineID: m.app.MachineID, MachineName: m.app.CurrentMachineName(), LogLevel: lvl, AutostartSupported: m.opts.Autostart != nil}
	if m.opts.Autostart != nil {
		if s.Autostart, err = m.opts.Autostart.Enabled(); err != nil {
			return s, err
		}
	}
	return s, nil
}

// UpdateSettings applies the whole Settings value: read it first, change
// what you need, send it back. MachineID and AutostartSupported are ignored.
func (m *Manager) UpdateSettings(ctx context.Context, s api.Settings) (api.Settings, error) {
	if s.MachineName != "" && s.MachineName != m.app.CurrentMachineName() {
		if err := m.app.SetMachineName(s.MachineName); err != nil {
			return api.Settings{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
		}
	}
	if s.LogLevel != "" {
		lvl, err := logging.ParseLevel(s.LogLevel)
		if err != nil {
			return api.Settings{}, fmt.Errorf("%w: %v", api.ErrInvalid, err)
		}
		if err := m.app.SetLogLevel(s.LogLevel); err != nil {
			return api.Settings{}, err
		}
		if m.opts.LogLevel != nil {
			m.opts.LogLevel.Set(lvl)
		}
	}
	cur, err := m.Settings(ctx)
	if err != nil {
		return cur, err
	}
	if s.Autostart != cur.Autostart {
		if m.opts.Autostart == nil {
			return cur, fmt.Errorf("autostart: %w", api.ErrUnsupported)
		}
		if s.Autostart {
			err = m.opts.Autostart.Enable()
		} else {
			err = m.opts.Autostart.Disable()
		}
		if err != nil {
			return cur, err
		}
	}
	return m.Settings(ctx)
}

func (m *Manager) Profiles(ctx context.Context) ([]model.Profile, error) { return m.app.Profiles() }

func (m *Manager) AddProfile(ctx context.Context, req api.AddProfileRequest) (model.Profile, error) {
	return m.app.AddProfile(req.Profile, req.Secret)
}

func (m *Manager) TestProfile(ctx context.Context, id string) (storage.ProbeResult, error) {
	return m.app.TestProfile(ctx, id)
}

func (m *Manager) Discover(ctx context.Context, profileID string) ([]model.Action, error) {
	return m.app.Discover(ctx, profileID)
}

func (m *Manager) Actions(ctx context.Context) ([]api.ActionStatus, error) {
	acts, err := m.app.Actions()
	if err != nil {
		return nil, err
	}
	out := make([]api.ActionStatus, 0, len(acts))
	for _, a := range acts {
		if w, err := m.worker(a.ID); err == nil {
			out = append(out, w.status(a))
		} else {
			out = append(out, api.ActionStatus{Action: a, ProfileID: a.ProfileID, State: api.StateIdle})
		}
	}
	return out, nil
}

func (m *Manager) Action(ctx context.Context, id string) (api.ActionStatus, error) {
	w, err := m.worker(id)
	if err != nil {
		return api.ActionStatus{}, err
	}
	a, err := m.app.Action(id)
	if err != nil {
		return api.ActionStatus{}, err
	}
	return w.status(a), nil
}

func (m *Manager) CreateAction(ctx context.Context, req api.CreateActionRequest) (model.Action, error) {
	a, err := m.app.CreateAction(ctx, req.ProfileID, req.Action, req.Passphrase)
	if err != nil {
		return a, err
	}
	m.startWorker(a.ID)
	m.bus.publish(api.Event{Type: api.EventActions, ActionID: a.ID})
	return a, nil
}

func (m *Manager) JoinAction(ctx context.Context, req api.JoinActionRequest) (model.Action, error) {
	a, err := m.app.Join(ctx, req.ProfileID, req.ActionID, req.Passphrase)
	if err != nil {
		return a, err
	}
	m.startWorker(a.ID)
	m.bus.publish(api.Event{Type: api.EventActions, ActionID: a.ID})
	return a, nil
}

// mutate runs an action change on the worker, then rebuilds its runner and
// triggers and announces the change.
func (m *Manager) mutate(ctx context.Context, id string, fn func(ctx context.Context) (any, error)) (any, error) {
	w, err := m.worker(id)
	if err != nil {
		return nil, err
	}
	return w.do(ctx, func(c context.Context) (any, error) {
		v, err := fn(c)
		if err != nil {
			return v, err
		}
		w.runner = nil
		if a, err := m.app.Action(id); err == nil {
			w.refreshTriggers(a)
		}
		m.bus.publish(api.Event{Type: api.EventActions, ActionID: id})
		w.publishState()
		return v, nil
	})
}

func (m *Manager) UpdateRules(ctx context.Context, id string, rules model.Rules) (model.Action, error) {
	v, err := m.mutate(ctx, id, func(c context.Context) (any, error) { return m.app.UpdateRules(c, id, rules) })
	a, _ := v.(model.Action)
	return a, err
}

func (m *Manager) SetMachineDirection(ctx context.Context, id string, d model.Direction) error {
	_, err := m.mutate(ctx, id, func(c context.Context) (any, error) { return nil, m.app.SetMachineDirection(c, id, d) })
	return err
}

func (m *Manager) AddItem(ctx context.Context, id string, req api.AddItemRequest) (model.Item, error) {
	v, err := m.mutate(ctx, id, func(c context.Context) (any, error) { return m.app.AddItem(c, id, req.Item, req.LocalPath) })
	it, _ := v.(model.Item)
	return it, err
}

func (m *Manager) MapItem(ctx context.Context, id, itemID, localPath string) error {
	_, err := m.mutate(ctx, id, func(c context.Context) (any, error) { return nil, m.app.MapItem(c, id, itemID, localPath) })
	return err
}

func (m *Manager) UnmapItem(ctx context.Context, id, itemID string) error {
	_, err := m.mutate(ctx, id, func(c context.Context) (any, error) { return nil, m.app.UnmapItem(c, id, itemID) })
	return err
}

func (m *Manager) RemoveItem(ctx context.Context, id, itemID string) error {
	_, err := m.mutate(ctx, id, func(c context.Context) (any, error) { return nil, m.app.RemoveItem(c, id, itemID) })
	return err
}

func (m *Manager) LeaveAction(ctx context.Context, id string, force bool) error {
	w, err := m.worker(id)
	if err != nil {
		return err
	}
	if _, err := w.do(ctx, func(c context.Context) (any, error) { return nil, m.app.LeaveAction(c, id, force) }); err != nil {
		return err
	}
	m.removeWorker(id)
	m.bus.publish(api.Event{Type: api.EventActions, ActionID: id})
	return nil
}

// syncJob runs a sync-like call on the action's worker.
func (m *Manager) syncJob(ctx context.Context, id string, f func(context.Context, *engine.Runner) (model.SyncResult, error)) (model.SyncResult, error) {
	w, err := m.worker(id)
	if err != nil {
		return model.SyncResult{}, err
	}
	v, err := w.do(ctx, func(context.Context) (any, error) { return w.runSync(f) })
	res, _ := v.(model.SyncResult)
	return res, err
}

func (m *Manager) Sync(ctx context.Context, id string) (model.SyncResult, error) {
	return m.syncJob(ctx, id, func(c context.Context, r *engine.Runner) (model.SyncResult, error) { return r.Sync(c) })
}

func (m *Manager) Resolve(ctx context.Context, id string, choices map[string]model.Side) (model.SyncResult, error) {
	return m.syncJob(ctx, id, func(c context.Context, r *engine.Runner) (model.SyncResult, error) { return r.Resolve(c, choices) })
}

func (m *Manager) ApproveDeletes(ctx context.Context, id string, reviewed []string) (model.SyncResult, error) {
	return m.syncJob(ctx, id, func(c context.Context, r *engine.Runner) (model.SyncResult, error) {
		return r.ApproveDeletes(c, reviewed)
	})
}

// runnerJob runs a non-sync engine call on the action's worker.
func (m *Manager) runnerJob(ctx context.Context, id string, f func(context.Context, *engine.Runner) (any, error)) (any, error) {
	w, err := m.worker(id)
	if err != nil {
		return nil, err
	}
	return w.do(ctx, func(c context.Context) (any, error) {
		r, err := w.getRunner()
		if err != nil {
			return nil, err
		}
		w.setRunning(true)
		defer w.setRunning(false)
		return f(c, r)
	})
}

func (m *Manager) Snapshot(ctx context.Context, id string) (model.SnapshotInfo, error) {
	v, err := m.runnerJob(ctx, id, func(c context.Context, r *engine.Runner) (any, error) { return r.Snapshot(c) })
	if errors.Is(err, engine.ErrConflictPending) {
		if w, werr := m.worker(id); werr == nil {
			n := 0
			if cs, cerr := m.app.Conflicts(id); cerr == nil {
				n = len(cs)
			}
			w.setState(func(s *wstate) { s.state, s.conflicts = api.StatePausedConflict, n })
		}
	}
	info, _ := v.(model.SnapshotInfo)
	return info, err
}

func (m *Manager) Snapshots(ctx context.Context, id string) ([]model.SnapshotInfo, error) {
	v, err := m.runnerJob(ctx, id, func(c context.Context, r *engine.Runner) (any, error) { return r.ListSnapshots(c) })
	list, _ := v.([]model.SnapshotInfo)
	return list, err
}

func (m *Manager) Restore(ctx context.Context, id string, req api.RestoreRequest) (engine.RestoreResult, error) {
	v, err := m.runnerJob(ctx, id, func(c context.Context, r *engine.Runner) (any, error) { return r.Restore(c, req.TS, req.Paths, req.To) })
	res, _ := v.(engine.RestoreResult)
	return res, err
}

func (m *Manager) ConflictDetail(ctx context.Context, id, path string) (engine.ConflictDetail, error) {
	v, err := m.runnerJob(ctx, id, func(c context.Context, r *engine.Runner) (any, error) { return r.ConflictDetail(c, path) })
	d, _ := v.(engine.ConflictDetail)
	return d, err
}

func (m *Manager) Pause(ctx context.Context, id string) error {
	w, err := m.worker(id)
	if err != nil {
		return err
	}
	_, err = w.do(ctx, func(context.Context) (any, error) {
		if err := m.app.SetPaused(id, true); err != nil {
			return nil, err
		}
		w.setState(func(s *wstate) { s.state = api.StatePausedUser })
		return nil, nil
	})
	return err
}

func (m *Manager) Resume(ctx context.Context, id string) error {
	w, err := m.worker(id)
	if err != nil {
		return err
	}
	_, err = w.do(ctx, func(context.Context) (any, error) {
		if err := m.app.SetPaused(id, false); err != nil {
			return nil, err
		}
		fresh := w.initialState()
		w.setState(func(s *wstate) { s.state, s.conflicts, s.pending = fresh.state, fresh.conflicts, fresh.pending })
		w.trigger("resume")
		return nil, nil
	})
	return err
}

func (m *Manager) PendingDeletes(ctx context.Context, id string) (*model.PendingDeletes, error) {
	if _, err := m.worker(id); err != nil {
		return nil, err
	}
	return m.app.PendingDeletes(id)
}

func (m *Manager) Conflicts(ctx context.Context, id string) ([]model.Conflict, error) {
	if _, err := m.worker(id); err != nil {
		return nil, err
	}
	return m.app.Conflicts(id)
}

func (m *Manager) History(ctx context.Context, id string, limit int) ([]model.HistoryEntry, error) {
	if _, err := m.worker(id); err != nil {
		return nil, err
	}
	return m.app.History(id, limit)
}

func (m *Manager) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	return m.bus.subscribe(ctx, m.ctx.Done()), nil
}
