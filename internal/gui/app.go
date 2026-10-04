package gui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/schedule"
	"github.com/niceit/s3-anysync/internal/storage"
)

// Dialogs are the native pickers ("" when cancelled).
type Dialogs interface {
	PickFile(title string) (string, error)
	PickFolder(title string) (string, error)
}

// App is bound to the frontend. Its api.Service methods forward to the agent
// and return errors as JSON {"code","message"}.
type App struct {
	conn    *Conn
	dlg     Dialogs
	dataDir string
	version string
	open    func(path string) error
	now     func() time.Time
}

// NewApp returns the bound object.
func NewApp(conn *Conn, dlg Dialogs, dataDir, version string) *App {
	return &App{conn: conn, dlg: dlg, dataDir: dataDir, version: version, open: OpenPath, now: time.Now}
}

// Version is the GUI's build version.
func (a *App) Version() string { return a.version }

// do calls the agent. Calls are not cut short by the GUI: a sync may take long.
func do[T any](a *App, f func(ctx context.Context, s api.Service) (T, error)) (T, error) {
	var zero T
	s, gen, err := a.conn.service()
	if err != nil {
		return zero, encode(err)
	}
	v, err := f(context.Background(), s)
	if err != nil {
		if lostConnection(err) {
			a.conn.Lost(gen)
			return zero, encode(fmt.Errorf("%w: %v", ErrUnreachable, err))
		}
		return zero, encode(err)
	}
	return v, nil
}

func do0(a *App, f func(ctx context.Context, s api.Service) error) error {
	_, err := do(a, func(ctx context.Context, s api.Service) (struct{}, error) { return struct{}{}, f(ctx, s) })
	return err
}

func (a *App) Status() (api.Status, error) {
	return do(a, func(c context.Context, s api.Service) (api.Status, error) { return s.Status(c) })
}

func (a *App) Settings() (api.Settings, error) {
	return do(a, func(c context.Context, s api.Service) (api.Settings, error) { return s.Settings(c) })
}

func (a *App) UpdateSettings(in api.Settings) (api.Settings, error) {
	return do(a, func(c context.Context, s api.Service) (api.Settings, error) { return s.UpdateSettings(c, in) })
}

func (a *App) Profiles() ([]model.Profile, error) {
	return do(a, func(c context.Context, s api.Service) ([]model.Profile, error) { return s.Profiles(c) })
}

func (a *App) AddProfile(req api.AddProfileRequest) (model.Profile, error) {
	return do(a, func(c context.Context, s api.Service) (model.Profile, error) { return s.AddProfile(c, req) })
}

func (a *App) UpdateProfile(req api.UpdateProfileRequest) (model.Profile, error) {
	return do(a, func(c context.Context, s api.Service) (model.Profile, error) { return s.UpdateProfile(c, req) })
}

func (a *App) RemoveProfile(id string) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.RemoveProfile(c, id) })
}

func (a *App) TestProfile(id string) (storage.ProbeResult, error) {
	return do(a, func(c context.Context, s api.Service) (storage.ProbeResult, error) { return s.TestProfile(c, id) })
}

func (a *App) Discover(profileID string) ([]model.Action, error) {
	return do(a, func(c context.Context, s api.Service) ([]model.Action, error) { return s.Discover(c, profileID) })
}

func (a *App) Actions() ([]api.ActionStatus, error) {
	return do(a, func(c context.Context, s api.Service) ([]api.ActionStatus, error) { return s.Actions(c) })
}

func (a *App) Action(id string) (api.ActionStatus, error) {
	return do(a, func(c context.Context, s api.Service) (api.ActionStatus, error) { return s.Action(c, id) })
}

func (a *App) CreateAction(req api.CreateActionRequest) (model.Action, error) {
	return do(a, func(c context.Context, s api.Service) (model.Action, error) { return s.CreateAction(c, req) })
}

func (a *App) JoinAction(req api.JoinActionRequest) (model.Action, error) {
	return do(a, func(c context.Context, s api.Service) (model.Action, error) { return s.JoinAction(c, req) })
}

func (a *App) UpdateRules(actionID string, req api.UpdateRulesRequest) (model.Action, error) {
	return do(a, func(c context.Context, s api.Service) (model.Action, error) { return s.UpdateRules(c, actionID, req) })
}

func (a *App) SetMachineDirection(actionID string, d model.Direction) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.SetMachineDirection(c, actionID, d) })
}

func (a *App) LeaveAction(actionID string, force bool) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.LeaveAction(c, actionID, force) })
}

func (a *App) AddItem(actionID string, req api.AddItemRequest) (model.Item, error) {
	return do(a, func(c context.Context, s api.Service) (model.Item, error) { return s.AddItem(c, actionID, req) })
}

func (a *App) MapItem(actionID, itemID, localPath string) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.MapItem(c, actionID, itemID, localPath) })
}

func (a *App) UnmapItem(actionID, itemID string) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.UnmapItem(c, actionID, itemID) })
}

func (a *App) RemoveItem(actionID, itemID string) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.RemoveItem(c, actionID, itemID) })
}

func (a *App) Sync(actionID string) (model.SyncResult, error) {
	return do(a, func(c context.Context, s api.Service) (model.SyncResult, error) { return s.Sync(c, actionID) })
}

func (a *App) CancelSync(actionID string) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.CancelSync(c, actionID) })
}

func (a *App) Snapshot(actionID string) (model.SnapshotInfo, error) {
	return do(a, func(c context.Context, s api.Service) (model.SnapshotInfo, error) { return s.Snapshot(c, actionID) })
}

func (a *App) Pause(actionID string) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.Pause(c, actionID) })
}

func (a *App) Resume(actionID string) error {
	return do0(a, func(c context.Context, s api.Service) error { return s.Resume(c, actionID) })
}

func (a *App) PendingDeletes(actionID string) (*model.PendingDeletes, error) {
	return do(a, func(c context.Context, s api.Service) (*model.PendingDeletes, error) {
		return s.PendingDeletes(c, actionID)
	})
}

func (a *App) ApproveDeletes(actionID string, reviewed []string) (model.SyncResult, error) {
	return do(a, func(c context.Context, s api.Service) (model.SyncResult, error) {
		return s.ApproveDeletes(c, actionID, reviewed)
	})
}

func (a *App) Snapshots(actionID string) ([]model.SnapshotInfo, error) {
	return do(a, func(c context.Context, s api.Service) ([]model.SnapshotInfo, error) { return s.Snapshots(c, actionID) })
}

func (a *App) Restore(actionID string, req api.RestoreRequest) (engine.RestoreResult, error) {
	return do(a, func(c context.Context, s api.Service) (engine.RestoreResult, error) {
		return s.Restore(c, actionID, req)
	})
}

func (a *App) Conflicts(actionID string) ([]model.Conflict, error) {
	return do(a, func(c context.Context, s api.Service) ([]model.Conflict, error) { return s.Conflicts(c, actionID) })
}

func (a *App) ConflictDetail(actionID, path string) (engine.ConflictDetail, error) {
	return do(a, func(c context.Context, s api.Service) (engine.ConflictDetail, error) {
		return s.ConflictDetail(c, actionID, path)
	})
}

func (a *App) Resolve(actionID string, req api.ResolveRequest) (model.SyncResult, error) {
	return do(a, func(c context.Context, s api.Service) (model.SyncResult, error) {
		return s.Resolve(c, actionID, req)
	})
}

func (a *App) History(actionID string, limit int) ([]model.HistoryEntry, error) {
	return do(a, func(c context.Context, s api.Service) ([]model.HistoryEntry, error) {
		return s.History(c, actionID, limit)
	})
}

// PreviewSchedule returns the next 3 runs (RFC 3339, local time) of s, using
// the agent's own schedule rules.
func (a *App) PreviewSchedule(s model.Schedule) ([]string, error) {
	if err := schedule.Validate(s); err != nil {
		return nil, encode(fmt.Errorf("%w: %v", api.ErrInvalid, err))
	}
	runs, err := schedule.NextRuns(s, a.now(), 3)
	if err != nil {
		return nil, encode(fmt.Errorf("%w: %v", api.ErrInvalid, err))
	}
	out := make([]string, len(runs))
	for i, t := range runs {
		out[i] = t.Format(time.RFC3339)
	}
	return out, nil
}

func (a *App) PickFile(title string) (string, error) {
	p, err := a.dlg.PickFile(title)
	return p, encode(err)
}

func (a *App) PickFolder(title string) (string, error) {
	p, err := a.dlg.PickFolder(title)
	return p, encode(err)
}

func (a *App) OpenDataFolder() error { return encode(a.open(a.dataDir)) }

func (a *App) OpenLogFolder() error { return encode(a.open(filepath.Join(a.dataDir, "logs"))) }

func (a *App) ConnectionState() ConnState { return a.conn.State() }

func (a *App) RetryConnect() { a.conn.Retry() }

func (a *App) DataDir() string { return a.dataDir }
