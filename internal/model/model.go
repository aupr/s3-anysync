// Package model holds the types shared by the engine, the state store, the
// agent and the GUI.
package model

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Direction says which way an action syncs on one machine.
type Direction string

const (
	TwoWay       Direction = "two-way"
	LocalToCloud Direction = "local-to-cloud"
	CloudToLocal Direction = "cloud-to-local"
)

// Valid reports whether d is one of the known directions.
func (d Direction) Valid() bool {
	switch d {
	case TwoWay, LocalToCloud, CloudToLocal:
		return true
	}
	return false
}

// ItemType says whether an item is a single file or a directory tree.
type ItemType string

const (
	ItemFile ItemType = "file"
	ItemDir  ItemType = "dir"
)

// Schedule kinds.
const (
	ScheduleNone     = "none"
	ScheduleInterval = "interval"
	ScheduleDaily    = "daily"
	ScheduleWeekly   = "weekly"
	ScheduleCron     = "cron"
)

// Defaults from the design spec.
const (
	DefaultDebounceSeconds   = 3
	DefaultRemotePollMinutes = 5
	DefaultSnapshotRetention = 20
)

// Profile is an S3 connection. The secret key lives in the keychain.
type Profile struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Provider          string `json:"provider"`
	Endpoint          string `json:"endpoint"`
	Region            string `json:"region"`
	Bucket            string `json:"bucket"`
	RootPrefix        string `json:"rootPrefix"`
	AccessKeyID       string `json:"accessKeyId"`
	PathStyle         bool   `json:"pathStyle"`
	ConditionalWrites bool   `json:"conditionalWrites"`
}

// Schedule is when an action syncs on a timer. Only the fields for Kind are used.
type Schedule struct {
	Kind         string `json:"kind"`
	EveryMinutes int    `json:"everyMinutes,omitempty"`
	At           string `json:"at,omitempty"`
	Weekday      int    `json:"weekday,omitempty"`
	Cron         string `json:"cron,omitempty"`
}

// Triggers says what starts a sync.
type Triggers struct {
	OnChange          bool     `json:"onChange"`
	DebounceSeconds   int      `json:"debounceSeconds"`
	Schedule          Schedule `json:"schedule"`
	RemotePollMinutes int      `json:"remotePollMinutes"`
}

// Item is one file or directory in an action.
type Item struct {
	ID         string            `json:"id"`
	RemoteName string            `json:"remoteName"`
	Type       ItemType          `json:"type"`
	Excludes   []string          `json:"excludes"`
	LocalPaths map[string]string `json:"localPaths"`
}

// Action is a group of items that sync together. It is shared between
// machines through <root>/<id>/.meta/action.json; ProfileID is local only.
type Action struct {
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	ProfileID         string               `json:"-"`
	Direction         Direction            `json:"direction"`
	MachineDirections map[string]Direction `json:"machineDirections"`
	Triggers          Triggers             `json:"triggers"`
	PropagateDeletes  bool                 `json:"propagateDeletes"`
	Encrypt           bool                 `json:"encrypt"`
	Excludes          []string             `json:"excludes"`
	SnapshotRetention int                  `json:"snapshotRetention"`
	Items             []Item               `json:"items"`
	Machines          map[string]string    `json:"machines"`
	UpdatedAt         time.Time            `json:"updatedAt"`
}

// DirectionFor returns the machine's override, or the action default.
func (a Action) DirectionFor(machineID string) Direction {
	if d, ok := a.MachineDirections[machineID]; ok && d.Valid() {
		return d
	}
	return a.Direction
}

// ApplyDefaults fills zero values with the spec defaults and makes maps non-nil.
// PropagateDeletes is not touched: callers set it explicitly when creating an action.
func (a *Action) ApplyDefaults() {
	if a.Direction == "" {
		a.Direction = TwoWay
	}
	if a.Triggers.DebounceSeconds <= 0 {
		a.Triggers.DebounceSeconds = DefaultDebounceSeconds
	}
	if a.Triggers.RemotePollMinutes <= 0 {
		a.Triggers.RemotePollMinutes = DefaultRemotePollMinutes
	}
	if a.Triggers.Schedule.Kind == "" {
		a.Triggers.Schedule.Kind = ScheduleNone
	}
	if a.SnapshotRetention <= 0 {
		a.SnapshotRetention = DefaultSnapshotRetention
	}
	if a.Machines == nil {
		a.Machines = map[string]string{}
	}
	if a.MachineDirections == nil {
		a.MachineDirections = map[string]Direction{}
	}
	for i := range a.Items {
		if a.Items[i].LocalPaths == nil {
			a.Items[i].LocalPaths = map[string]string{}
		}
	}
}

// Validate checks the action and its items.
func (a Action) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("action id is empty")
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("action name is empty")
	}
	if !a.Direction.Valid() {
		return fmt.Errorf("unknown direction %q", a.Direction)
	}
	for id, d := range a.MachineDirections {
		if !d.Valid() {
			return fmt.Errorf("machine %s: unknown direction %q", id, d)
		}
	}
	switch a.Triggers.Schedule.Kind {
	case ScheduleNone, ScheduleInterval, ScheduleDaily, ScheduleWeekly, ScheduleCron:
	default:
		return fmt.Errorf("unknown schedule kind %q", a.Triggers.Schedule.Kind)
	}
	if a.SnapshotRetention < 1 {
		return errors.New("snapshot retention must be at least 1")
	}
	ids := map[string]bool{}
	var names []string
	for _, it := range a.Items {
		if it.ID == "" {
			return errors.New("item id is empty")
		}
		if ids[it.ID] {
			return fmt.Errorf("duplicate item id %s", it.ID)
		}
		ids[it.ID] = true
		if it.Type != ItemFile && it.Type != ItemDir {
			return fmt.Errorf("item %s: unknown type %q", it.ID, it.Type)
		}
		n, err := NormalizeRemoteName(it.RemoteName)
		if err != nil {
			return fmt.Errorf("item %s: %w", it.ID, err)
		}
		if n != it.RemoteName {
			return fmt.Errorf("item %s: remote name %q is not normalized (use %q)", it.ID, it.RemoteName, n)
		}
		for _, o := range names {
			if n == o || strings.HasPrefix(n, o+"/") || strings.HasPrefix(o, n+"/") {
				return fmt.Errorf("items %q and %q overlap", o, n)
			}
		}
		names = append(names, n)
	}
	return nil
}

// NormalizeRemoteName cleans a user-typed remote name: backslashes become
// slashes, surrounding slashes are trimmed and "." and ".." are resolved.
func NormalizeRemoteName(s string) (string, error) {
	s = strings.Trim(strings.ReplaceAll(strings.TrimSpace(s), `\`, "/"), "/")
	if s == "" {
		return "", errors.New("remote name is empty")
	}
	c := path.Clean(s)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("remote name %q escapes the action folder", s)
	}
	return c, nil
}

// FileEntry describes one file by its path under current/ (slash separated).
// Hash is the hex SHA-256 of the plaintext. Size is the plaintext size.
// MTime is Unix nanoseconds.
type FileEntry struct {
	Path   string `json:"path"`
	Hash   string `json:"hash"`
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"`
	ETag   string `json:"etag,omitempty"`
	Writer string `json:"writer,omitempty"`
}

// Manifest is the cloud state of an action, stored in .meta/manifest.json.
type Manifest struct {
	Files     map[string]FileEntry `json:"files"`
	UpdatedAt time.Time            `json:"updatedAt"`
	UpdatedBy string               `json:"updatedBy"`
}

// Side picks the winner when resolving a conflict.
type Side string

const (
	SideLocal Side = "local"
	SideCloud Side = "cloud"
)

// Conflict is a path both sides changed. A nil side means it was deleted there.
type Conflict struct {
	Path       string     `json:"path"`
	Local      *FileEntry `json:"local"`
	Remote     *FileEntry `json:"remote"`
	DetectedAt time.Time  `json:"detectedAt"`
}

// SnapshotInfo is the index stored at snapshots/<ts>.json.
type SnapshotInfo struct {
	TS        string      `json:"ts"`
	Reason    string      `json:"reason"`
	MachineID string      `json:"machineId"`
	CreatedAt time.Time   `json:"createdAt"`
	Files     []FileEntry `json:"files"`
}

// Run outcomes.
const (
	OutcomeOK       = "ok"
	OutcomeConflict = "conflict"
	OutcomeError    = "error"
)

// History kinds.
const (
	HistorySync     = "sync"
	HistoryResolve  = "resolve"
	HistorySnapshot = "snapshot"
	HistoryRestore  = "restore"
)

// SyncResult reports what one sync run did.
type SyncResult struct {
	Outcome       string     `json:"outcome"`
	Uploaded      int        `json:"uploaded"`
	Downloaded    int        `json:"downloaded"`
	DeletedRemote int        `json:"deletedRemote"`
	DeletedLocal  int        `json:"deletedLocal"`
	Adopted       int        `json:"adopted"`
	Conflicts     []Conflict `json:"conflicts"`
}

// Summary is a one-line human description of the counts.
func (r SyncResult) Summary() string {
	return fmt.Sprintf("uploaded %d, downloaded %d, deleted in cloud %d, deleted locally %d, adopted %d, conflicts %d",
		r.Uploaded, r.Downloaded, r.DeletedRemote, r.DeletedLocal, r.Adopted, len(r.Conflicts))
}

// HistoryEntry is one row of an action's run history.
type HistoryEntry struct {
	ID         int64     `json:"id"`
	ActionID   string    `json:"actionId"`
	Kind       string    `json:"kind"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Outcome    string    `json:"outcome"`
	Message    string    `json:"message"`
}
