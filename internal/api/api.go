// Package api is the contract between s3-anysync's front ends (CLI, tray,
// GUI) and its agent. agent.Manager implements Service in-process and
// ipc.Client implements it over the local IPC connection, so callers work
// the same way whichever one they hold.
package api

import (
	"context"
	"time"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// Service is everything a front end can ask of s3-anysync.
type Service interface {
	Status(ctx context.Context) (Status, error)
	Settings(ctx context.Context) (Settings, error)
	UpdateSettings(ctx context.Context, s Settings) (Settings, error)

	Profiles(ctx context.Context) ([]model.Profile, error)
	AddProfile(ctx context.Context, req AddProfileRequest) (model.Profile, error)
	UpdateProfile(ctx context.Context, req UpdateProfileRequest) (model.Profile, error)
	// RemoveProfile deletes a profile and its secret; code in-use while actions use it.
	RemoveProfile(ctx context.Context, id string) error
	TestProfile(ctx context.Context, profileID string) (storage.ProbeResult, error)
	Discover(ctx context.Context, profileID string) ([]model.Action, error)

	Actions(ctx context.Context) ([]ActionStatus, error)
	Action(ctx context.Context, actionID string) (ActionStatus, error)
	CreateAction(ctx context.Context, req CreateActionRequest) (model.Action, error)
	JoinAction(ctx context.Context, req JoinActionRequest) (model.Action, error)
	// UpdateRules fails with code conflict-edit when the action changed since req.ExpectedUpdatedAt.
	UpdateRules(ctx context.Context, actionID string, req UpdateRulesRequest) (model.Action, error)
	SetMachineDirection(ctx context.Context, actionID string, d model.Direction) error
	LeaveAction(ctx context.Context, actionID string, force bool) error

	AddItem(ctx context.Context, actionID string, req AddItemRequest) (model.Item, error)
	MapItem(ctx context.Context, actionID, itemID, localPath string) error
	UnmapItem(ctx context.Context, actionID, itemID string) error
	RemoveItem(ctx context.Context, actionID, itemID string) error

	Sync(ctx context.Context, actionID string) (model.SyncResult, error)
	Snapshot(ctx context.Context, actionID string) (model.SnapshotInfo, error)
	Pause(ctx context.Context, actionID string) error
	Resume(ctx context.Context, actionID string) error
	// CancelSync cancels the action's running job, if any; the cancelled
	// caller gets code cancelled. A pending automatic trigger is dropped.
	CancelSync(ctx context.Context, actionID string) error
	PendingDeletes(ctx context.Context, actionID string) (*model.PendingDeletes, error)
	// ApproveDeletes approves the pending deletions, but only if the saved list
	// still equals reviewed (the paths the user was shown); otherwise nothing
	// is approved and the result is deletes-pending with the fresh list.
	ApproveDeletes(ctx context.Context, actionID string, reviewed []string) (model.SyncResult, error)

	Snapshots(ctx context.Context, actionID string) ([]model.SnapshotInfo, error)
	Restore(ctx context.Context, actionID string, req RestoreRequest) (engine.RestoreResult, error)
	Conflicts(ctx context.Context, actionID string) ([]model.Conflict, error)
	ConflictDetail(ctx context.Context, actionID, path string) (engine.ConflictDetail, error)
	// Resolve settles the saved conflicts; it fails with conflict-changed if they
	// no longer hold the versions in req.Reviewed.
	Resolve(ctx context.Context, actionID string, req ResolveRequest) (model.SyncResult, error)
	History(ctx context.Context, actionID string, limit int) ([]model.HistoryEntry, error)

	// Subscribe streams events until ctx ends; the channel is then closed.
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// ConflictVersion identifies the two sides of a conflict as the user saw them:
// the content hashes, "" for a side that was deleted.
type ConflictVersion = engine.ConflictVersion

// ResolveRequest resolves the saved conflicts. Choices has a side for every
// conflict; Reviewed has, for the same paths, the versions the choices were made on.
type ResolveRequest struct {
	Choices  map[string]model.Side      `json:"choices"`
	Reviewed map[string]ConflictVersion `json:"reviewed"`
}

// Action states.
const (
	StateIdle           = "idle"
	StateRunning        = "running"
	StatePausedConflict = "paused-conflict"
	StatePausedDeletes  = "paused-deletes"
	StatePausedUser     = "paused"
	StateError          = "error"
)

// Status is the agent's overview.
type Status struct {
	MachineID   string `json:"machineId"`
	MachineName string `json:"machineName"`
	Version     string `json:"version"`
	// Agent is true when the background agent serves this Status.
	Agent   bool           `json:"agent"`
	Actions []ActionStatus `json:"actions"`
}

// ActionStatus is one action with its live state.
type ActionStatus struct {
	Action    model.Action `json:"action"`
	ProfileID string       `json:"profileId"`
	State     string       `json:"state"`
	// Running is true while a job (sync, snapshot, restore...) runs.
	Running        bool      `json:"running"`
	LastRun        time.Time `json:"lastRun,omitzero"`
	LastOutcome    string    `json:"lastOutcome,omitempty"`
	LastSummary    string    `json:"lastSummary,omitempty"`
	LastError      string    `json:"lastError,omitempty"`
	Failures       int       `json:"failures"`
	NextRun        time.Time `json:"nextRun,omitzero"` // next scheduled run or retry
	Conflicts      int       `json:"conflicts"`
	PendingDeletes int       `json:"pendingDeletes"`
}

// Settings are the agent's settings.
type Settings struct {
	MachineID          string `json:"machineId"` // read-only
	MachineName        string `json:"machineName"`
	Autostart          bool   `json:"autostart"`
	AutostartSupported bool   `json:"autostartSupported"` // read-only
	LogLevel           string `json:"logLevel"`
}

// UpdateProfileRequest replaces a profile's fields; an empty Secret keeps the stored one.
type UpdateProfileRequest struct {
	Profile model.Profile `json:"profile"`
	Secret  string        `json:"secret,omitempty"`
}

// AddProfileRequest adds a profile; Secret goes to the keychain.
type AddProfileRequest struct {
	Profile model.Profile `json:"profile"`
	Secret  string        `json:"secret"`
}

// CreateActionRequest creates an action from this machine.
type CreateActionRequest struct {
	ProfileID  string       `json:"profileId"`
	Action     model.Action `json:"action"`
	Passphrase string       `json:"passphrase,omitempty"`
}

// JoinActionRequest registers this machine on an existing action.
type JoinActionRequest struct {
	ProfileID  string `json:"profileId"`
	ActionID   string `json:"actionId"`
	Passphrase string `json:"passphrase,omitempty"`
}

// UpdateRulesRequest edits an action's rules; ExpectedUpdatedAt is the
// UpdatedAt of the action as the editor loaded it.
type UpdateRulesRequest struct {
	Rules             model.Rules `json:"rules"`
	ExpectedUpdatedAt time.Time   `json:"expectedUpdatedAt"`
}

// AddItemRequest adds an item mapped to LocalPath on this machine.
type AddItemRequest struct {
	Item      model.Item `json:"item"`
	LocalPath string     `json:"localPath"`
}

// RestoreRequest restores files from a snapshot.
type RestoreRequest struct {
	TS    string               `json:"ts"`
	Paths []string             `json:"paths,omitempty"`
	To    engine.RestoreTarget `json:"to"`
}

// Event types.
const (
	EventState   = "action.state"    // an action's status changed
	EventResult  = "action.result"   // a job finished
	EventActions = "actions.changed" // actions were added, removed or edited
	EventLog     = "log"             // an activity line
)

// Event is pushed to subscribers.
type Event struct {
	Type     string            `json:"type"`
	ActionID string            `json:"actionId,omitempty"`
	Status   *ActionStatus     `json:"status,omitempty"`
	Result   *model.SyncResult `json:"result,omitempty"`
	Error    string            `json:"error,omitempty"`
	Message  string            `json:"message,omitempty"`
	Time     time.Time         `json:"time"`
}
