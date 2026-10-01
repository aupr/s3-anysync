# s3-anysync Plan 1: Core Engine and Debug CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the sync engine for s3-anysync: data model, SQLite state, S3 storage with locking, encryption, the three-way planner, snapshots, conflict resolution and restore. A debug command line (`s3-anysync-agent`) drives it end to end.

**Architecture:** One Go module. Pure packages under `internal/` (model, paths, crypto, secrets, state, storage, engine) are wired together by `internal/app`, which the CLI in `cmd/agent` uses. Later plans reuse the same `app` package from the agent's IPC server (Plan 2) and the Wails GUI (Plan 3). The engine depends only on the `storage.Storage` port and an `engine.Repo` interface, so it is tested against an in-memory store and, when configured, a real S3-compatible server.

**Tech Stack:** Go 1.27, `aws-sdk-go-v2` (S3), `modernc.org/sqlite`, `zalando/go-keyring`, `bmatcuk/doublestar/v4`, `golang.org/x/crypto/scrypt`, `google/uuid`.

**Spec:** `docs/superpowers/specs/2026-10-01-s3-anysync-design.md`

**Later plans (not in this document):** Plan 2 covers the agent (triggers, state machine, IPC, tray, autostart). Plan 3 covers the Wails GUI. Plan 4 covers packaging.

## Global Constraints

- Go 1.27 (Homebrew `go` 1.27.1). Module path: `github.com/niceit/s3-anysync`.
- Pure Go only: everything must build with `CGO_ENABLED=0`, for both `GOOS=darwin` and `GOOS=windows`.
- Local paths use `path/filepath`. Remote paths and keys always use `/` (package `path`).
- Remote layout (exact): `<root>/<actionId>/.meta/action.json`, `.meta/manifest.json`, `.meta/lock`, `.meta/keycheck`, `current/<path>`, `snapshots/<ts>/<path>`, `snapshots/<ts>.json`. `<ts>` is UTC `20060102T150405Z`.
- Object metadata keys (lowercase): `sha256`, `size`, `mtime`, `machine`.
- Encryption: AES-256-GCM, scrypt N=16384 r=8 p=1, 16-byte salt, 12-byte IV, JSON envelope `format: "s3-anysync"`, `version: 2`.
- Defaults: debounce 3 s, remote poll 5 min, snapshot retention 20, lock TTL 2 min, local backups kept per action 20.
- Secrets (S3 secret keys, passphrases) never go into SQLite, logs or the bucket. They live in the OS keychain (`secrets.Store`).
- App data dir: `os.UserConfigDir()/s3-anysync`, overridable with env `S3_ANYSYNC_DATA_DIR` (used to simulate two machines on one computer).
- One-way modes never delete a file on the destination that was never synced (not in the baseline).
- Run `gofmt -w` on every file you touch. Every commit message ends with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` (pass it as a second `-m`).

## File Structure

| File | Responsibility |
|---|---|
| `go.mod`, `.gitignore`, `README.md` | Module, ignores, developer docs |
| `internal/model/model.go` | Shared types: Profile, Action, Item, FileEntry, Manifest, Conflict, SnapshotInfo, SyncResult, HistoryEntry; validation and defaults |
| `internal/paths/expand.go` | Expand `~`, `$HOME`, `%APPDATA%`, `%USERPROFILE%`, `%LOCALAPPDATA%` |
| `internal/paths/exclude.go` | Glob exclude matcher |
| `internal/crypto/envelope.go` | Encrypt/decrypt the JSON envelope |
| `internal/secrets/secrets.go` | Keychain-backed and in-memory secret stores |
| `internal/state/*.go` | SQLite: schema, settings and machine identity, profiles, actions, baselines, conflicts, history |
| `internal/storage/storage.go` | Storage port, errors, `JoinKey` |
| `internal/storage/memory.go` | In-memory Storage for tests |
| `internal/storage/lock.go` | Remote lock (conditional or best effort) |
| `internal/storage/s3.go` | S3 adapter |
| `internal/storage/probe.go` | Connection test |
| `internal/storage/s3test/s3test.go` | Test helper that connects to an S3 server from env vars |
| `internal/engine/deps.go` | Engine errors, constants, `Deps`, `Repo` |
| `internal/engine/layout.go` | Remote key layout |
| `internal/engine/remote.go` | Load, rebuild and save the manifest |
| `internal/engine/shared.go` | Create, fetch, update, discover `action.json`; key check |
| `internal/engine/mapping.go` | Item → local path mapping, ownership, excludes |
| `internal/engine/scan.go` | Local scan with hash cache |
| `internal/engine/plan.go` | Pure three-way planner |
| `internal/engine/localio.go` | Atomic writes, local backups |
| `internal/engine/runner.go` | `Runner` type, action refresh, history |
| `internal/engine/snapshot.go` | Snapshot write/list/retention, Snapshot now |
| `internal/engine/sync.go` | Sync and Resolve pipeline |
| `internal/engine/restore.go` | Restore from a snapshot |
| `internal/app/app.go` | Wires state, secrets, storage and engine for the CLI, agent and GUI |
| `cmd/agent/main.go`, `cmd/agent/cli.go` | Debug CLI |

---

### Task 1: Toolchain, module scaffold and data model

**Files:**
- Create: `go.mod`, `.gitignore`
- Create: `internal/model/model.go`
- Test: `internal/model/model_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: package `model`:
  - Types `Direction` (`TwoWay`, `LocalToCloud`, `CloudToLocal`, `Valid()`) and `ItemType` (`ItemFile`, `ItemDir`)
  - Schedule kind constants `ScheduleNone|Interval|Daily|Weekly|Cron`, and `DefaultDebounceSeconds=3`, `DefaultRemotePollMinutes=5`, `DefaultSnapshotRetention=20`
  - Structs `Profile`, `Schedule`, `Triggers`, `Item`
  - `Action`, with `DirectionFor(machineID) Direction`, `(*Action).ApplyDefaults()` and `Validate() error`
  - `NormalizeRemoteName(string) (string, error)`
  - `FileEntry`, `Manifest`, `Side` (`SideLocal`, `SideCloud`), `Conflict`, `SnapshotInfo`
  - Outcome constants `OutcomeOK|Conflict|Error` and history kinds `HistorySync|Resolve|Snapshot|Restore`
  - `SyncResult` with `Summary() string`, and `HistoryEntry`

- [ ] **Step 1: Install Go and create the module**

```bash
brew install go
go version
```
Expected: `go version go1.27.1 darwin/arm64` (or `darwin/amd64`).

```bash
cd /Users/aman/oData/local-devs/s3-anysync
go mod init github.com/niceit/s3-anysync
```
Expected: `go: creating new go.mod: module github.com/niceit/s3-anysync`

Create `.gitignore`:
```gitignore
/bin/
*.db
*.db-wal
*.db-shm
/frontend/node_modules/
/frontend/dist/
/build/bin/
```

- [ ] **Step 2: Write the failing tests**

Create `internal/model/model_test.go`:
```go
package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func validAction() Action {
	a := Action{ID: "a1", Name: "VS Code", Items: []Item{
		{ID: "i1", RemoteName: "settings.json", Type: ItemFile},
		{ID: "i2", RemoteName: "snippets", Type: ItemDir},
	}}
	a.ApplyDefaults()
	return a
}

func TestApplyDefaults(t *testing.T) {
	var a Action
	a.ApplyDefaults()
	if a.Direction != TwoWay || a.Triggers.DebounceSeconds != 3 || a.Triggers.RemotePollMinutes != 5 ||
		a.SnapshotRetention != 20 || a.Triggers.Schedule.Kind != ScheduleNone ||
		a.Machines == nil || a.MachineDirections == nil {
		t.Fatalf("defaults not applied: %+v", a)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Action)
		wantErr string
	}{
		{"valid", func(*Action) {}, ""},
		{"empty name", func(a *Action) { a.Name = " " }, "name is empty"},
		{"bad direction", func(a *Action) { a.Direction = "sideways" }, "unknown direction"},
		{"bad machine direction", func(a *Action) { a.MachineDirections["m1"] = "up" }, "machine m1"},
		{"bad schedule", func(a *Action) { a.Triggers.Schedule.Kind = "hourly" }, "unknown schedule kind"},
		{"retention zero", func(a *Action) { a.SnapshotRetention = 0 }, "retention"},
		{"empty item id", func(a *Action) { a.Items[0].ID = "" }, "item id is empty"},
		{"bad item type", func(a *Action) { a.Items[0].Type = "link" }, "unknown type"},
		{"duplicate item id", func(a *Action) { a.Items[1].ID = "i1" }, "duplicate item id"},
		{"unnormalized name", func(a *Action) { a.Items[0].RemoteName = "/settings.json" }, "not normalized"},
		{"nested overlap", func(a *Action) { a.Items[0].RemoteName = "snippets/a.json" }, "overlap"},
		{"same name", func(a *Action) { a.Items[0].RemoteName = "snippets" }, "overlap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validAction()
			tt.mutate(&a)
			err := a.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestNormalizeRemoteName(t *testing.T) {
	ok := map[string]string{
		" /a/b/ ": "a/b",
		`a\b`:     "a/b",
		"a/./b":   "a/b",
		"a/../b":  "b",
		"x.json":  "x.json",
	}
	for in, want := range ok {
		got, err := NormalizeRemoteName(in)
		if err != nil || got != want {
			t.Errorf("NormalizeRemoteName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "/", ".", "..", "../x", "a/../../x"} {
		if got, err := NormalizeRemoteName(in); err == nil {
			t.Errorf("NormalizeRemoteName(%q) = %q, want error", in, got)
		}
	}
}

func TestDirectionFor(t *testing.T) {
	a := validAction()
	a.MachineDirections["mac"] = LocalToCloud
	if got := a.DirectionFor("mac"); got != LocalToCloud {
		t.Fatalf("override: got %s", got)
	}
	if got := a.DirectionFor("pc"); got != TwoWay {
		t.Fatalf("default: got %s", got)
	}
}

func TestActionJSONOmitsProfileID(t *testing.T) {
	a := validAction()
	a.ProfileID = "local-profile"
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "local-profile") {
		t.Fatalf("profile id leaked into shared JSON: %s", data)
	}
}

func TestSummary(t *testing.T) {
	r := SyncResult{Uploaded: 2, Conflicts: make([]Conflict, 1)}
	want := "uploaded 2, downloaded 0, deleted in cloud 0, deleted locally 0, adopted 0, conflicts 1"
	if got := r.Summary(); got != want {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/model/...`
Expected: FAIL with `undefined: Action` (and similar).

- [ ] **Step 4: Write the implementation**

Create `internal/model/model.go`:
```go
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
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	RootPrefix  string `json:"rootPrefix"`
	AccessKeyID string `json:"accessKeyId"`
	PathStyle   bool   `json:"pathStyle"`
	// ConditionalWrites is set by the connection test: true when the
	// provider rejects a second If-None-Match:* write to the same key.
	ConditionalWrites bool `json:"conditionalWrites"`
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
	ID         string   `json:"id"`
	RemoteName string   `json:"remoteName"`
	Type       ItemType `json:"type"`
	Excludes   []string `json:"excludes"`
	// LocalPaths maps machineId to that machine's local path for the item.
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
	// Machines maps machineId to the machine's friendly name.
	Machines  map[string]string `json:"machines"`
	UpdatedAt time.Time         `json:"updatedAt"`
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/model/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/model`

- [ ] **Step 6: Commit**

```bash
gofmt -l . && git add go.mod .gitignore internal/model
git commit -m "feat(model): module scaffold and shared data model" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Path expansion and exclude matching

**Files:**
- Create: `internal/paths/expand.go`, `internal/paths/exclude.go`
- Test: `internal/paths/expand_test.go`, `internal/paths/exclude_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: package `paths`:
  - `Expand(p string) (string, error)` and `ExpandWith(p, home string, getenv func(string) string) (string, error)`
  - `const TempFilePrefix = ".s3-anysync-tmp-"`
  - `NewMatcher(lists ...[]string) (*Matcher, error)`
  - `(*Matcher).Match(rel string) bool`, which tests one path, and `(*Matcher).MatchPath(rel string) bool`, which also tests every parent directory

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/bmatcuk/doublestar/v4@latest
```

- [ ] **Step 2: Write the failing tests**

Create `internal/paths/expand_test.go`:
```go
package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandWith(t *testing.T) {
	home := t.TempDir()
	appdata := filepath.Join(home, "AppData", "Roaming")
	env := map[string]string{"APPDATA": appdata}
	getenv := func(k string) string { return env[k] }

	ok := map[string]string{
		"~":                     home,
		"~/x/y.json":            filepath.Join(home, "x", "y.json"),
		"$HOME/.config/a":       filepath.Join(home, ".config", "a"),
		"%APPDATA%/Code/User":   filepath.Join(appdata, "Code", "User"),
		"%appdata%/Code":        filepath.Join(appdata, "Code"),
		home + "/a/../b":        filepath.Join(home, "b"),
		"  ~/trimmed  ":         filepath.Join(home, "trimmed"),
	}
	for in, want := range ok {
		got, err := ExpandWith(in, home, getenv)
		if err != nil || got != want {
			t.Errorf("ExpandWith(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	bad := map[string]string{
		"":                  "empty",
		"relative/x":        "not absolute",
		"%LOCALAPPDATA%/x":  "LOCALAPPDATA",
		"%PROGRAMFILES%/x":  "unsupported variable",
	}
	for in, want := range bad {
		_, err := ExpandWith(in, home, getenv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ExpandWith(%q): want error containing %q, got %v", in, want, err)
		}
	}
}
```

Create `internal/paths/exclude_test.go`:
```go
package paths

import "testing"

func TestMatcher(t *testing.T) {
	m, err := NewMatcher([]string{"*.log", "cache", "/root.txt", "build/"}, []string{"sub/*.tmp", "**/gen/**"})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"a.log":                  true,
		"deep/x/b.log":           true,
		"a.txt":                  false,
		"cache":                  true,
		"x/cache":                true,
		"root.txt":               true,
		"build":                  true,
		"sub/x.tmp":              true,
		"other/sub/x.tmp":        false,
		"a/gen/b":                true,
		".s3-anysync-tmp-123":    true,
		"dir/.s3-anysync-tmp-9":  true,
	}
	for rel, want := range cases {
		if got := m.Match(rel); got != want {
			t.Errorf("Match(%q) = %v, want %v", rel, got, want)
		}
	}
	if !m.MatchPath("cache/x/y.json") {
		t.Error("MatchPath should match a file under an excluded directory")
	}
	if m.MatchPath("keep/x.json") {
		t.Error("MatchPath matched a kept file")
	}
}

func TestMatcherInvalidPattern(t *testing.T) {
	if _, err := NewMatcher([]string{"["}); err == nil {
		t.Fatal("want error for invalid pattern")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/paths/...`
Expected: FAIL with `undefined: ExpandWith`.

- [ ] **Step 4: Write the implementation**

Create `internal/paths/expand.go`:
```go
// Package paths expands user-typed local paths and matches exclude patterns.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var winVar = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)

// windowsVars are the only %VAR% names accepted in item paths.
var windowsVars = map[string]bool{"APPDATA": true, "USERPROFILE": true, "LOCALAPPDATA": true}

// Expand resolves ~, $HOME and the supported %VAR% names against this
// machine and returns a clean absolute path.
func Expand(p string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return ExpandWith(p, home, os.Getenv)
}

// ExpandWith is Expand with the home directory and environment injected.
func ExpandWith(p, home string, getenv func(string) string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("path is empty")
	}
	p = replaceHome(p, "~", home)
	p = replaceHome(p, "$HOME", home)
	var expandErr error
	p = winVar.ReplaceAllStringFunc(p, func(m string) string {
		name := strings.ToUpper(m[1 : len(m)-1])
		if !windowsVars[name] {
			if expandErr == nil {
				expandErr = fmt.Errorf("unsupported variable %s (use ~, $HOME, %%APPDATA%%, %%USERPROFILE%% or %%LOCALAPPDATA%%)", m)
			}
			return m
		}
		v := getenv(name)
		if v == "" && expandErr == nil {
			expandErr = fmt.Errorf("environment variable %%%s%% is not set on this machine", name)
		}
		return v
	})
	if expandErr != nil {
		return "", expandErr
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("path %q is not absolute", p)
	}
	return p, nil
}

func replaceHome(p, token, home string) string {
	if p == token {
		return home
	}
	if strings.HasPrefix(p, token+"/") || strings.HasPrefix(p, token+`\`) {
		return home + p[len(token):]
	}
	return p
}
```

Create `internal/paths/exclude.go`:
```go
package paths

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// TempFilePrefix names the temporary files written during atomic downloads.
// They are always excluded.
const TempFilePrefix = ".s3-anysync-tmp-"

// Matcher matches slash-separated relative paths against exclude globs.
// A pattern without "/" matches the last path element at any depth; a
// pattern with "/" matches the whole relative path. "**" is supported.
// Backslashes in patterns are treated as separators.
type Matcher struct {
	patterns []string
}

// NewMatcher combines several pattern lists (for example the action's and an item's).
func NewMatcher(lists ...[]string) (*Matcher, error) {
	m := &Matcher{patterns: []string{TempFilePrefix + "*"}}
	for _, list := range lists {
		for _, raw := range list {
			p := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/"), "/")
			if p == "" {
				continue
			}
			if !doublestar.ValidatePattern(p) {
				return nil, fmt.Errorf("invalid exclude pattern %q", raw)
			}
			m.patterns = append(m.patterns, p)
		}
	}
	return m, nil
}

// Match reports whether rel itself is excluded.
func (m *Matcher) Match(rel string) bool {
	rel = strings.ReplaceAll(rel, `\`, "/")
	base := path.Base(rel)
	for _, p := range m.patterns {
		target := base
		if strings.Contains(p, "/") {
			target = rel
		}
		if ok, _ := doublestar.Match(p, target); ok {
			return true
		}
	}
	return false
}

// MatchPath reports whether rel or any of its parent directories is excluded.
func (m *Matcher) MatchPath(rel string) bool {
	parts := strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/")
	for i := 1; i <= len(parts); i++ {
		if m.Match(strings.Join(parts[:i], "/")) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/paths/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/paths`

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/paths && go mod tidy
git add go.mod go.sum internal/paths
git commit -m "feat(paths): path variable expansion and exclude matcher" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Encryption envelope

**Files:**
- Create: `internal/crypto/envelope.go`
- Test: `internal/crypto/envelope_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: package `crypto`:
  - `Encrypt(plaintext []byte, passphrase string) ([]byte, error)` and `Decrypt(payload []byte, passphrase string) ([]byte, error)`
  - `ErrAuth`, returned for a wrong passphrase or tampered data, and `ErrFormat`, returned when the payload is not an envelope
  - `const Format = "s3-anysync"`

- [ ] **Step 1: Add the dependency**

```bash
go get golang.org/x/crypto@latest
```

- [ ] **Step 2: Write the failing tests**

Create `internal/crypto/envelope_test.go`:
```go
package crypto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, pt := range [][]byte{[]byte(`{"theme":"dark"}`), {}} {
		enc, err := Encrypt(pt, "pass")
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(enc, []byte("dark")) {
			t.Fatal("ciphertext contains plaintext")
		}
		got, err := Decrypt(enc, "pass")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("got %q want %q", got, pt)
		}
	}
}

func TestWrongPassphrase(t *testing.T) {
	enc, _ := Encrypt([]byte("x"), "right")
	if _, err := Decrypt(enc, "wrong"); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestTamper(t *testing.T) {
	enc, _ := Encrypt([]byte("hello world"), "p")
	var env map[string]any
	if err := json.Unmarshal(enc, &env); err != nil {
		t.Fatal(err)
	}
	ct, _ := base64.StdEncoding.DecodeString(env["ciphertext"].(string))
	ct[0] ^= 0xff
	env["ciphertext"] = base64.StdEncoding.EncodeToString(ct)
	tampered, _ := json.Marshal(env)
	if _, err := Decrypt(tampered, "p"); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestFormatErrors(t *testing.T) {
	for _, payload := range []string{"plain text", `{"format":"other","version":2}`, `{"a":1}`} {
		if _, err := Decrypt([]byte(payload), "p"); !errors.Is(err, ErrFormat) {
			t.Errorf("Decrypt(%q): want ErrFormat, got %v", payload, err)
		}
	}
}

func TestSaltIsRandom(t *testing.T) {
	a, _ := Encrypt([]byte("x"), "p")
	b, _ := Encrypt([]byte("x"), "p")
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions produced identical output")
	}
}

func TestEmptyPassphrase(t *testing.T) {
	if _, err := Encrypt([]byte("x"), ""); err == nil {
		t.Fatal("want error for empty passphrase")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/crypto/...`
Expected: FAIL with `undefined: Encrypt`.

- [ ] **Step 4: Write the implementation**

Create `internal/crypto/envelope.go`:
```go
// Package crypto encrypts file contents into a self-describing JSON envelope:
// AES-256-GCM with a scrypt-derived key (ported from the Tabby plugin's v2 format).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

// Format identifies s3-anysync envelopes.
const Format = "s3-anysync"

const (
	version    = 2
	algorithm  = "aes-256-gcm"
	kdf        = "scrypt"
	keyLength  = 32
	saltLength = 16
	ivLength   = 12
	tagLength  = 16
	scryptN    = 16384
	scryptR    = 8
	scryptP    = 1
)

var (
	// ErrAuth means the passphrase is wrong or the data was modified.
	ErrAuth = errors.New("decryption failed: wrong passphrase or tampered data")
	// ErrFormat means the payload is not an s3-anysync envelope.
	ErrFormat = errors.New("not an s3-anysync encrypted payload")
)

type envelope struct {
	Format          string `json:"format"`
	Version         int    `json:"version"`
	Algorithm       string `json:"algorithm"`
	KDF             string `json:"kdf"`
	Salt            string `json:"salt"`
	IV              string `json:"iv"`
	AuthTag         string `json:"authTag"`
	Ciphertext      string `json:"ciphertext"`
	KeyLength       int    `json:"keyLength"`
	Cost            int    `json:"cost"`
	BlockSize       int    `json:"blockSize"`
	Parallelization int    `json:"parallelization"`
}

func newGCM(passphrase string, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, keyLength)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt seals plaintext (which may be empty) with the passphrase.
func Encrypt(plaintext []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("passphrase is empty")
	}
	salt := make([]byte, saltLength)
	iv := make([]byte, ivLength)
	rand.Read(salt)
	rand.Read(iv)
	gcm, err := newGCM(passphrase, salt)
	if err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, iv, plaintext, nil)
	ct, tag := sealed[:len(sealed)-tagLength], sealed[len(sealed)-tagLength:]
	b64 := base64.StdEncoding.EncodeToString
	return json.Marshal(envelope{
		Format: Format, Version: version, Algorithm: algorithm, KDF: kdf,
		Salt: b64(salt), IV: b64(iv), AuthTag: b64(tag), Ciphertext: b64(ct),
		KeyLength: keyLength, Cost: scryptN, BlockSize: scryptR, Parallelization: scryptP,
	})
}

// Decrypt opens an envelope produced by Encrypt.
func Decrypt(payload []byte, passphrase string) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, ErrFormat
	}
	if env.Format != Format || env.Version != version || env.Algorithm != algorithm || env.KDF != kdf {
		return nil, ErrFormat
	}
	if env.KeyLength != keyLength || env.Cost != scryptN || env.BlockSize != scryptR || env.Parallelization != scryptP {
		return nil, fmt.Errorf("%w: unsupported parameters", ErrFormat)
	}
	dec := base64.StdEncoding.DecodeString
	salt, err1 := dec(env.Salt)
	iv, err2 := dec(env.IV)
	tag, err3 := dec(env.AuthTag)
	ct, err4 := dec(env.Ciphertext)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if len(salt) != saltLength || len(iv) != ivLength || len(tag) != tagLength {
		return nil, fmt.Errorf("%w: bad field lengths", ErrFormat)
	}
	gcm, err := newGCM(passphrase, salt)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, iv, append(ct, tag...), nil)
	if err != nil {
		return nil, ErrAuth
	}
	if pt == nil {
		pt = []byte{}
	}
	return pt, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/crypto/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/crypto`

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/crypto && go mod tidy
git add go.mod go.sum internal/crypto
git commit -m "feat(crypto): AES-256-GCM envelope with scrypt key derivation" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Secret store

**Files:**
- Create: `internal/secrets/secrets.go`
- Test: `internal/secrets/secrets_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: package `secrets`:
  - `Store` interface: `Get(key) (string, error)`, `Set(key, value) error`, `Delete(key) error`
  - `ErrNotFound` and `const Service = "s3-anysync"`
  - `NewKeyring() Keyring`, backed by the OS keychain, and `NewMemory() *Memory`, for tests
  - `ProfileSecretKey(profileID) string` and `ActionPassphraseKey(actionID) string`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/zalando/go-keyring@latest
```

- [ ] **Step 2: Write the failing tests**

Create `internal/secrets/secrets_test.go`:
```go
package secrets

import (
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

func exercise(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: want ErrNotFound, got %v", err)
	}
	if err := s.Set("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get("k"); err != nil || v != "v1" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("second Delete should be a no-op, got %v", err)
	}
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestMemory(t *testing.T) { exercise(t, NewMemory()) }

func TestKeyring(t *testing.T) {
	keyring.MockInit()
	exercise(t, NewKeyring())
}

func TestKeyNames(t *testing.T) {
	if ProfileSecretKey("p1") != "profile:p1" || ActionPassphraseKey("a1") != "action:a1:passphrase" {
		t.Fatal("unexpected key names")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/secrets/...`
Expected: FAIL with `undefined: NewMemory`.

- [ ] **Step 4: Write the implementation**

Create `internal/secrets/secrets.go`:
```go
// Package secrets keeps S3 secret keys and action passphrases in the OS
// keychain (macOS Keychain, Windows Credential Manager).
package secrets

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name for every entry.
const Service = "s3-anysync"

// ErrNotFound is returned by Get when no secret is stored under the key.
var ErrNotFound = errors.New("secret not found")

// Store reads and writes secrets.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// ProfileSecretKey is the key for a profile's S3 secret access key.
func ProfileSecretKey(profileID string) string { return "profile:" + profileID }

// ActionPassphraseKey is the key for an encrypted action's passphrase.
func ActionPassphraseKey(actionID string) string { return "action:" + actionID + ":passphrase" }

// Keyring is a Store backed by the OS keychain.
type Keyring struct{}

// NewKeyring returns the OS keychain store.
func NewKeyring() Keyring { return Keyring{} }

func (Keyring) Get(key string) (string, error) {
	v, err := keyring.Get(Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (Keyring) Set(key, value string) error { return keyring.Set(Service, key, value) }

func (Keyring) Delete(key string) error {
	err := keyring.Delete(Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is an in-process Store for tests.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (s *Memory) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *Memory) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *Memory) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/secrets/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/secrets`

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/secrets && go mod tidy
git add go.mod go.sum internal/secrets
git commit -m "feat(secrets): keychain and in-memory secret stores" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: SQLite state store

**Files:**
- Create: `internal/state/db.go`, `internal/state/settings.go`, `internal/state/profiles.go`, `internal/state/actions.go`, `internal/state/baselines.go`, `internal/state/conflicts.go`, `internal/state/history.go`
- Test: `internal/state/state_test.go`

**Interfaces:**
- Consumes: `model.Profile`, `model.Action`, `model.FileEntry`, `model.Conflict`, `model.HistoryEntry`.
- Produces: package `state`:
  - `Open(path string) (*DB, error)`, `(*DB).Close() error` and `ErrNotFound`
  - Settings and machine: `GetSetting(key) (string, bool, error)`, `SetSetting(key, value) error`, `MachineIdentity() (id, name string, err error)`, `SetMachineName(name) error`
  - Profiles: `PutProfile(model.Profile) error`, `GetProfile(id) (model.Profile, error)`, `ListProfiles() ([]model.Profile, error)`, `DeleteProfile(id) error`
  - Actions: `PutAction(model.Action) error`, `GetAction(id) (model.Action, error)`, `ListActions() ([]model.Action, error)`, `DeleteAction(id) error`
  - Baselines: `LoadBaseline(actionID) (map[string]model.FileEntry, error)`, `SaveBaseline(actionID, map[string]model.FileEntry) error`
  - Conflicts: `SaveConflicts(actionID, []model.Conflict) error` (nil clears them) and `LoadConflicts(actionID) ([]model.Conflict, error)`
  - History: `AddHistory(model.HistoryEntry) error` and `ListHistory(actionID, limit) ([]model.HistoryEntry, error)`

- [ ] **Step 1: Add the dependencies**

```bash
go get modernc.org/sqlite@latest github.com/google/uuid@latest
```

- [ ] **Step 2: Write the failing tests**

Create `internal/state/state_test.go`:
```go
package state

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	// A space in the path mirrors "~/Library/Application Support".
	db, err := Open(filepath.Join(t.TempDir(), "with space", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesParentAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "with space", "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("k", "v"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v, ok, err := db.GetSetting("k"); err != nil || !ok || v != "v" {
		t.Fatalf("after reopen: %q %v %v", v, ok, err)
	}
}

func TestMachineIdentityIsStable(t *testing.T) {
	db := openTest(t)
	id1, name1, err := db.MachineIdentity()
	if err != nil || id1 == "" || name1 == "" {
		t.Fatalf("identity: %q %q %v", id1, name1, err)
	}
	id2, _, _ := db.MachineIdentity()
	if id1 != id2 {
		t.Fatal("machine id changed between calls")
	}
	if err := db.SetMachineName("Work Mac"); err != nil {
		t.Fatal(err)
	}
	if _, name, _ := db.MachineIdentity(); name != "Work Mac" {
		t.Fatalf("name = %q", name)
	}
	if err := db.SetMachineName("  "); err == nil {
		t.Fatal("empty name accepted")
	}
}

func TestProfilesAndActions(t *testing.T) {
	db := openTest(t)
	p := model.Profile{ID: "p1", Name: "Wasabi", Bucket: "b"}
	if err := db.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if got, err := db.GetProfile("p1"); err != nil || got.Name != "Wasabi" {
		t.Fatalf("GetProfile = %+v, %v", got, err)
	}
	if _, err := db.GetProfile("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing profile: %v", err)
	}

	a := model.Action{ID: "a1", Name: "Editor", ProfileID: "p1"}
	a.ApplyDefaults()
	if err := db.PutAction(a); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAction("a1")
	if err != nil || got.ProfileID != "p1" || got.Name != "Editor" {
		t.Fatalf("GetAction = %+v, %v", got, err)
	}
	if list, _ := db.ListActions(); len(list) != 1 {
		t.Fatalf("ListActions len %d", len(list))
	}
	if err := db.DeleteProfile("p1"); err == nil {
		t.Fatal("deleted a profile that is in use")
	}
	if err := db.PutAction(model.Action{ID: "a2", Name: "x"}); err == nil {
		t.Fatal("action without profile accepted")
	}
	if err := db.DeleteAction("a1"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProfile("p1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.ListProfiles(); len(list) != 0 {
		t.Fatalf("ListProfiles len %d", len(list))
	}
}

func TestBaselines(t *testing.T) {
	db := openTest(t)
	b := map[string]model.FileEntry{
		"a.json": {Path: "a.json", Hash: "h1", Size: 3, MTime: 10},
		"d/b":    {Path: "d/b", Hash: "h2", Size: 4, MTime: 20},
	}
	if err := db.SaveBaseline("a1", b); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadBaseline("a1")
	if err != nil || len(got) != 2 || got["d/b"].Hash != "h2" || got["a.json"].MTime != 10 {
		t.Fatalf("LoadBaseline = %+v, %v", got, err)
	}
	delete(b, "d/b")
	db.SaveBaseline("a1", b)
	if got, _ := db.LoadBaseline("a1"); len(got) != 1 {
		t.Fatalf("baseline not replaced: %+v", got)
	}
	if got, _ := db.LoadBaseline("other"); got == nil || len(got) != 0 {
		t.Fatal("missing baseline should be an empty, non-nil map")
	}
}

func TestConflictsAndHistory(t *testing.T) {
	db := openTest(t)
	cs := []model.Conflict{{Path: "z"}, {Path: "a", Local: &model.FileEntry{Hash: "l"}}}
	if err := db.SaveConflicts("a1", cs); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadConflicts("a1")
	if err != nil || len(got) != 2 || got[0].Path != "a" || got[0].Local.Hash != "l" {
		t.Fatalf("LoadConflicts = %+v, %v", got, err)
	}
	db.SaveConflicts("a1", nil)
	if got, _ := db.LoadConflicts("a1"); len(got) != 0 {
		t.Fatal("conflicts not cleared")
	}

	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, outcome := range []string{"ok", "error"} {
		db.AddHistory(model.HistoryEntry{ActionID: "a1", Kind: "sync", StartedAt: start, FinishedAt: start.Add(time.Duration(i) * time.Second), Outcome: outcome, Message: "m"})
	}
	h, err := db.ListHistory("a1", 10)
	if err != nil || len(h) != 2 || h[0].Outcome != "error" || !h[0].StartedAt.Equal(start) {
		t.Fatalf("ListHistory = %+v, %v", h, err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/state/...`
Expected: FAIL with `undefined: Open`.

- [ ] **Step 4: Write the implementation**

Create `internal/state/db.go`:
```go
// Package state is the agent's local SQLite store: profiles, actions,
// per-action baselines, conflicts, run history and settings.
package state

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("state: not found")

// DB is the local state store.
type DB struct {
	sql *sql.DB
}

var migrations = [][]string{
	{
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE profiles (id TEXT PRIMARY KEY, data TEXT NOT NULL)`,
		`CREATE TABLE actions (id TEXT PRIMARY KEY, profile_id TEXT NOT NULL REFERENCES profiles(id), data TEXT NOT NULL)`,
		`CREATE TABLE baselines (action_id TEXT NOT NULL, path TEXT NOT NULL, hash TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL, PRIMARY KEY (action_id, path))`,
		`CREATE TABLE conflicts (action_id TEXT NOT NULL, path TEXT NOT NULL, data TEXT NOT NULL, PRIMARY KEY (action_id, path))`,
		`CREATE TABLE history (id INTEGER PRIMARY KEY AUTOINCREMENT, action_id TEXT NOT NULL, kind TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT NOT NULL, outcome TEXT NOT NULL, message TEXT NOT NULL)`,
		`CREATE INDEX history_action ON history(action_id, id)`,
	},
}

// Open opens (creating if needed) the database at path and migrates it.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection keeps the pragmas below in effect and serialises writes.
	db.SetMaxOpenConns(1)
	for _, p := range []string{"PRAGMA busy_timeout = 5000", "PRAGMA journal_mode = WAL", "PRAGMA foreign_keys = ON"} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	d := &DB{sql: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) migrate() error {
	var version int
	if err := d.sql.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := d.sql.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range migrations[i] {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
```

Create `internal/state/settings.go`:
```go
package state

import (
	"database/sql"
	"errors"
	"os"
	"strings"

	"github.com/google/uuid"
)

const (
	settingMachineID   = "machine_id"
	settingMachineName = "machine_name"
)

// GetSetting returns the value and whether it exists.
func (d *DB) GetSetting(key string) (string, bool, error) {
	var v string
	err := d.sql.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting inserts or replaces a setting.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.sql.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// MachineIdentity returns this machine's id and friendly name, creating
// them on first use (a new UUID and the hostname).
func (d *DB) MachineIdentity() (string, string, error) {
	id, ok, err := d.GetSetting(settingMachineID)
	if err != nil {
		return "", "", err
	}
	if !ok {
		id = uuid.NewString()
		if err := d.SetSetting(settingMachineID, id); err != nil {
			return "", "", err
		}
	}
	name, ok, err := d.GetSetting(settingMachineName)
	if err != nil {
		return "", "", err
	}
	if !ok {
		name, _ = os.Hostname()
		if name == "" {
			name = "machine"
		}
		if err := d.SetSetting(settingMachineName, name); err != nil {
			return "", "", err
		}
	}
	return id, name, nil
}

// SetMachineName changes the friendly name shown to other machines.
func (d *DB) SetMachineName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("machine name is empty")
	}
	return d.SetSetting(settingMachineName, name)
}
```

Create `internal/state/profiles.go`:
```go
package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/niceit/s3-anysync/internal/model"
)

// PutProfile inserts or replaces a profile.
func (d *DB) PutProfile(p model.Profile) error {
	if p.ID == "" {
		return errors.New("profile id is empty")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`INSERT INTO profiles(id, data) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, p.ID, string(data))
	return err
}

// GetProfile returns a profile or ErrNotFound.
func (d *DB) GetProfile(id string) (model.Profile, error) {
	var data string
	err := d.sql.QueryRow(`SELECT data FROM profiles WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Profile{}, fmt.Errorf("profile %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.Profile{}, err
	}
	var p model.Profile
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return model.Profile{}, err
	}
	return p, nil
}

// ListProfiles returns all profiles sorted by name.
func (d *DB) ListProfiles() ([]model.Profile, error) {
	rows, err := d.sql.Query(`SELECT data FROM profiles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Profile
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p model.Profile
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, rows.Err()
}

// DeleteProfile removes a profile that no action uses.
func (d *DB) DeleteProfile(id string) error {
	var n int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM actions WHERE profile_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("profile is used by %d action(s)", n)
	}
	_, err := d.sql.Exec(`DELETE FROM profiles WHERE id = ?`, id)
	return err
}
```

Create `internal/state/actions.go`:
```go
package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/niceit/s3-anysync/internal/model"
)

// PutAction inserts or replaces an action; ProfileID is stored in its own column.
func (d *DB) PutAction(a model.Action) error {
	if a.ID == "" || a.ProfileID == "" {
		return errors.New("action id and profile id are required")
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`INSERT INTO actions(id, profile_id, data) VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET profile_id = excluded.profile_id, data = excluded.data`, a.ID, a.ProfileID, string(data))
	return err
}

func scanAction(profileID, data string) (model.Action, error) {
	var a model.Action
	if err := json.Unmarshal([]byte(data), &a); err != nil {
		return model.Action{}, err
	}
	a.ProfileID = profileID
	a.ApplyDefaults()
	return a, nil
}

// GetAction returns an action or ErrNotFound.
func (d *DB) GetAction(id string) (model.Action, error) {
	var pid, data string
	err := d.sql.QueryRow(`SELECT profile_id, data FROM actions WHERE id = ?`, id).Scan(&pid, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Action{}, fmt.Errorf("action %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.Action{}, err
	}
	return scanAction(pid, data)
}

// ListActions returns all actions sorted by name.
func (d *DB) ListActions() ([]model.Action, error) {
	rows, err := d.sql.Query(`SELECT profile_id, data FROM actions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Action
	for rows.Next() {
		var pid, data string
		if err := rows.Scan(&pid, &data); err != nil {
			return nil, err
		}
		a, err := scanAction(pid, data)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, rows.Err()
}

// DeleteAction removes an action with its baseline, conflicts and history.
func (d *DB) DeleteAction(id string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM baselines WHERE action_id = ?`,
		`DELETE FROM conflicts WHERE action_id = ?`,
		`DELETE FROM history WHERE action_id = ?`,
		`DELETE FROM actions WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
```

Create `internal/state/baselines.go`:
```go
package state

import "github.com/niceit/s3-anysync/internal/model"

// LoadBaseline returns the last committed state of an action on this
// machine, keyed by remote path. A missing baseline is an empty map.
func (d *DB) LoadBaseline(actionID string) (map[string]model.FileEntry, error) {
	rows, err := d.sql.Query(`SELECT path, hash, size, mtime FROM baselines WHERE action_id = ?`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.FileEntry{}
	for rows.Next() {
		var e model.FileEntry
		if err := rows.Scan(&e.Path, &e.Hash, &e.Size, &e.MTime); err != nil {
			return nil, err
		}
		out[e.Path] = e
	}
	return out, rows.Err()
}

// SaveBaseline replaces the action's baseline.
func (d *DB) SaveBaseline(actionID string, baseline map[string]model.FileEntry) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM baselines WHERE action_id = ?`, actionID); err != nil {
		tx.Rollback()
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO baselines(action_id, path, hash, size, mtime) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for p, e := range baseline {
		if _, err := stmt.Exec(actionID, p, e.Hash, e.Size, e.MTime); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
```

Create `internal/state/conflicts.go`:
```go
package state

import (
	"encoding/json"

	"github.com/niceit/s3-anysync/internal/model"
)

// SaveConflicts replaces the action's open conflicts; nil clears them.
func (d *DB) SaveConflicts(actionID string, conflicts []model.Conflict) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM conflicts WHERE action_id = ?`, actionID); err != nil {
		tx.Rollback()
		return err
	}
	for _, c := range conflicts {
		data, err := json.Marshal(c)
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`INSERT INTO conflicts(action_id, path, data) VALUES(?, ?, ?)`, actionID, c.Path, string(data)); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// LoadConflicts returns the action's open conflicts sorted by path.
func (d *DB) LoadConflicts(actionID string) ([]model.Conflict, error) {
	rows, err := d.sql.Query(`SELECT data FROM conflicts WHERE action_id = ? ORDER BY path`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Conflict
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var c model.Conflict
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

Create `internal/state/history.go`:
```go
package state

import (
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

// AddHistory appends a run to the action's history.
func (d *DB) AddHistory(h model.HistoryEntry) error {
	_, err := d.sql.Exec(`INSERT INTO history(action_id, kind, started_at, finished_at, outcome, message) VALUES(?, ?, ?, ?, ?, ?)`,
		h.ActionID, h.Kind, h.StartedAt.UTC().Format(time.RFC3339Nano), h.FinishedAt.UTC().Format(time.RFC3339Nano), h.Outcome, h.Message)
	return err
}

// ListHistory returns the newest entries first.
func (d *DB) ListHistory(actionID string, limit int) ([]model.HistoryEntry, error) {
	rows, err := d.sql.Query(`SELECT id, action_id, kind, started_at, finished_at, outcome, message FROM history WHERE action_id = ? ORDER BY id DESC LIMIT ?`, actionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.HistoryEntry
	for rows.Next() {
		var h model.HistoryEntry
		var started, finished string
		if err := rows.Scan(&h.ID, &h.ActionID, &h.Kind, &started, &finished, &h.Outcome, &h.Message); err != nil {
			return nil, err
		}
		if h.StartedAt, err = time.Parse(time.RFC3339Nano, started); err != nil {
			return nil, err
		}
		if h.FinishedAt, err = time.Parse(time.RFC3339Nano, finished); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/state/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/state`

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/state && go mod tidy
git add go.mod go.sum internal/state
git commit -m "feat(state): SQLite store for profiles, actions, baselines, conflicts, history" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Storage port and in-memory store

**Files:**
- Create: `internal/storage/storage.go`, `internal/storage/memory.go`
- Test: `internal/storage/contract_test.go` (external package `storage_test`), `internal/storage/memory_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: package `storage`:
  - `Object{Key, Size, ETag, LastModified, Metadata}`
  - Errors `ErrNotFound`, `ErrPreconditionFailed`, `ErrAccessDenied`, `ErrConditionalUnsupported`
  - `Storage` interface:
    - `Put(ctx, key, body, meta) (Object, error)`
    - `PutIfAbsent(ctx, key, body) (Object, error)`
    - `PutIfMatch(ctx, key, body, etag) (Object, error)`
    - `Get(ctx, key) ([]byte, Object, error)`
    - `Head(ctx, key) (Object, error)`
    - `List(ctx, prefix) ([]Object, error)`: recursive, sorted by key, no metadata
    - `ListPrefixes(ctx, prefix) ([]string, error)`: immediate child "directories", each ending in `/`
    - `Copy(ctx, srcKey, dstKey) (Object, error)`: keeps metadata
    - `Delete(ctx, key) error`: deleting a missing key is not an error
  - `JoinKey(parts ...string) string`
  - `NewMemory() *Memory`, with field `NoConditional bool` and method `Keys() []string`
  - The test-only helper `runContract(t, s, prefix)` in `contract_test.go` (package `storage_test`), which Task 8 reuses for S3

- [ ] **Step 1: Write the failing tests**

Create `internal/storage/contract_test.go`:
```go
package storage_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/niceit/s3-anysync/internal/storage"
)

// runContract checks behaviour every Storage implementation must share.
func runContract(t *testing.T, s storage.Storage, prefix string) {
	ctx := context.Background()
	key := func(name string) string { return storage.JoinKey(prefix, name) }

	t.Run("put get head", func(t *testing.T) {
		put, err := s.Put(ctx, key("a/file.txt"), []byte("hello"), map[string]string{"sha256": "abc"})
		if err != nil {
			t.Fatal(err)
		}
		if put.ETag == "" {
			t.Fatal("put returned no etag")
		}
		body, obj, err := s.Get(ctx, key("a/file.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "hello" || obj.Metadata["sha256"] != "abc" {
			t.Fatalf("get = %q %v", body, obj.Metadata)
		}
		head, err := s.Head(ctx, key("a/file.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if head.Size != 5 || head.Metadata["sha256"] != "abc" {
			t.Fatalf("head = %+v", head)
		}
	})

	t.Run("missing", func(t *testing.T) {
		if _, _, err := s.Get(ctx, key("missing")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("get missing: %v", err)
		}
		if _, err := s.Head(ctx, key("missing")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("head missing: %v", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		for _, n := range []string{"l/1", "l/2/x", "l/3"} {
			if _, err := s.Put(ctx, key(n), []byte(n), nil); err != nil {
				t.Fatal(err)
			}
		}
		objs, err := s.List(ctx, key("l")+"/")
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, o := range objs {
			keys = append(keys, o.Key)
		}
		if want := []string{key("l/1"), key("l/2/x"), key("l/3")}; !reflect.DeepEqual(keys, want) {
			t.Fatalf("list = %v, want %v", keys, want)
		}
		prefixes, err := s.ListPrefixes(ctx, key("l")+"/")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{key("l/2") + "/"}; !reflect.DeepEqual(prefixes, want) {
			t.Fatalf("prefixes = %v, want %v", prefixes, want)
		}
	})

	t.Run("copy keeps metadata", func(t *testing.T) {
		if _, err := s.Put(ctx, key("c/src"), []byte("data"), map[string]string{"sha256": "h"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Copy(ctx, key("c/src"), key("c/dst copy+1")); err != nil {
			t.Fatal(err)
		}
		body, obj, err := s.Get(ctx, key("c/dst copy+1"))
		if err != nil || string(body) != "data" || obj.Metadata["sha256"] != "h" {
			t.Fatalf("copied = %q %v %v", body, obj.Metadata, err)
		}
		if _, err := s.Copy(ctx, key("c/none"), key("c/x")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("copy missing: %v", err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		s.Put(ctx, key("d/x"), []byte("x"), nil)
		if err := s.Delete(ctx, key("d/x")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Get(ctx, key("d/x")); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("after delete: %v", err)
		}
		if err := s.Delete(ctx, key("d/x")); err != nil {
			t.Fatalf("delete missing: %v", err)
		}
	})

	t.Run("conditional writes", func(t *testing.T) {
		k := key("cond")
		first, err := s.PutIfAbsent(ctx, k, []byte("1"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutIfAbsent(ctx, k, []byte("2")); !errors.Is(err, storage.ErrPreconditionFailed) {
			t.Fatalf("second PutIfAbsent: %v", err)
		}
		if _, err := s.PutIfMatch(ctx, k, []byte("3"), first.ETag); err != nil {
			t.Fatalf("PutIfMatch with current etag: %v", err)
		}
		if _, err := s.PutIfMatch(ctx, k, []byte("4"), first.ETag); !errors.Is(err, storage.ErrPreconditionFailed) {
			t.Fatalf("PutIfMatch with stale etag: %v", err)
		}
		if body, _, _ := s.Get(ctx, k); string(body) != "3" {
			t.Fatalf("body = %q", body)
		}
	})
}

func TestMemoryContract(t *testing.T) {
	runContract(t, storage.NewMemory(), "contract")
}
```

Create `internal/storage/memory_test.go`:
```go
package storage

import (
	"context"
	"testing"
)

func TestMemoryNoConditionalIgnoresPreconditions(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	m.NoConditional = true
	if _, err := m.PutIfAbsent(ctx, "k", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutIfAbsent(ctx, "k", []byte("2")); err != nil {
		t.Fatalf("NoConditional should overwrite silently, got %v", err)
	}
	if _, err := m.PutIfMatch(ctx, "k", []byte("3"), "wrong"); err != nil {
		t.Fatalf("NoConditional should ignore If-Match, got %v", err)
	}
}

func TestJoinKey(t *testing.T) {
	cases := map[string][]string{
		"a/b/c": {"a", "/b/", "c"},
		"b":     {"", "b"},
		"":      {"", "/"},
	}
	for want, parts := range cases {
		if got := JoinKey(parts...); got != want {
			t.Errorf("JoinKey(%q) = %q, want %q", parts, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/storage/...`
Expected: FAIL with `undefined: storage.NewMemory` / `undefined: JoinKey`.

- [ ] **Step 3: Write the implementation**

Create `internal/storage/storage.go`:
```go
// Package storage is the port the engine uses to reach object storage,
// with an S3 adapter, an in-memory implementation and a remote lock.
package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound               = errors.New("storage: object not found")
	ErrPreconditionFailed     = errors.New("storage: precondition failed")
	ErrAccessDenied           = errors.New("storage: access denied")
	ErrConditionalUnsupported = errors.New("storage: conditional writes not supported")
)

// Object describes a stored object. Metadata keys are lowercase.
type Object struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	Metadata     map[string]string
}

// Storage is a flat key/value object store. Keys are full object keys.
type Storage interface {
	Put(ctx context.Context, key string, body []byte, meta map[string]string) (Object, error)
	// PutIfAbsent fails with ErrPreconditionFailed if the key exists.
	PutIfAbsent(ctx context.Context, key string, body []byte) (Object, error)
	// PutIfMatch fails with ErrPreconditionFailed if the key's ETag differs.
	PutIfMatch(ctx context.Context, key string, body []byte, etag string) (Object, error)
	Get(ctx context.Context, key string) ([]byte, Object, error)
	Head(ctx context.Context, key string) (Object, error)
	// List returns every object under prefix, sorted by key, without metadata.
	List(ctx context.Context, prefix string) ([]Object, error)
	// ListPrefixes returns the immediate sub-"directories" of prefix, each ending in "/".
	ListPrefixes(ctx context.Context, prefix string) ([]string, error)
	// Copy copies an object server-side, keeping its metadata.
	Copy(ctx context.Context, srcKey, dstKey string) (Object, error)
	// Delete removes a key; a missing key is not an error.
	Delete(ctx context.Context, key string) error
}

// JoinKey joins non-empty parts with "/", trimming slashes around each part.
func JoinKey(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.Trim(p, "/"); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}
```

Create `internal/storage/memory.go`:
```go
package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-memory Storage for tests. Set NoConditional to emulate a
// provider that silently ignores If-None-Match and If-Match.
type Memory struct {
	mu            sync.Mutex
	objects       map[string]memObject
	seq           int
	NoConditional bool
}

var _ Storage = (*Memory)(nil)

type memObject struct {
	body     []byte
	meta     map[string]string
	etag     string
	modified time.Time
}

// NewMemory returns an empty store.
func NewMemory() *Memory { return &Memory{objects: map[string]memObject{}} }

func copyMeta(meta map[string]string) map[string]string {
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		out[strings.ToLower(k)] = v
	}
	return out
}

func (o memObject) info(key string) Object {
	return Object{Key: key, Size: int64(len(o.body)), ETag: o.etag, LastModified: o.modified, Metadata: copyMeta(o.meta)}
}

// store must be called with mu held.
func (m *Memory) store(key string, body []byte, meta map[string]string) Object {
	m.seq++
	o := memObject{body: append([]byte(nil), body...), meta: copyMeta(meta), etag: fmt.Sprintf("\"%d\"", m.seq), modified: time.Now().UTC()}
	m.objects[key] = o
	return o.info(key)
}

func (m *Memory) Put(_ context.Context, key string, body []byte, meta map[string]string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store(key, body, meta), nil
}

func (m *Memory) PutIfAbsent(_ context.Context, key string, body []byte) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[key]; ok && !m.NoConditional {
		return Object{}, fmt.Errorf("%w: %s exists", ErrPreconditionFailed, key)
	}
	return m.store(key, body, nil), nil
}

func (m *Memory) PutIfMatch(_ context.Context, key string, body []byte, etag string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.NoConditional {
		if o, ok := m.objects[key]; !ok || o.etag != etag {
			return Object{}, fmt.Errorf("%w: %s changed", ErrPreconditionFailed, key)
		}
	}
	return m.store(key, body, nil), nil
}

func (m *Memory) Get(_ context.Context, key string) ([]byte, Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return nil, Object{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return append([]byte(nil), o.body...), o.info(key), nil
}

func (m *Memory) Head(_ context.Context, key string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[key]
	if !ok {
		return Object{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return o.info(key), nil
}

func (m *Memory) List(_ context.Context, prefix string) ([]Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Object
	for k, o := range m.objects {
		if strings.HasPrefix(k, prefix) {
			info := o.info(k)
			info.Metadata = nil
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *Memory) ListPrefixes(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := map[string]bool{}
	for k := range m.objects {
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			if i := strings.Index(rest, "/"); i >= 0 {
				set[prefix+rest[:i+1]] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func (m *Memory) Copy(_ context.Context, srcKey, dstKey string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[srcKey]
	if !ok {
		return Object{}, fmt.Errorf("%w: %s", ErrNotFound, srcKey)
	}
	return m.store(dstKey, o.body, o.meta), nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// Keys returns every stored key, sorted. For tests.
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.objects))
	for k := range m.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/storage/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/storage`

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/storage
git add internal/storage
git commit -m "feat(storage): storage port, in-memory store and contract tests" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Remote lock

**Files:**
- Create: `internal/storage/lock.go`
- Test: `internal/storage/lock_test.go`

**Interfaces:**
- Consumes: `Storage`, `ErrPreconditionFailed`, `ErrNotFound` (Task 6).
- Produces:
  - `LockMode` (`LockConditional`, `LockBestEffort`) and `ErrLocked`
  - `LockInfo{MachineID, ExpiresAt}` (JSON fields `machineId`, `expiresAt`)
  - `Locker{Store, Key, MachineID, TTL, Mode, Settle, Now}` with `Acquire(ctx) error` and `Release(ctx) error`. Release only deletes the lock if this machine owns it.

- [ ] **Step 1: Write the failing tests**

Create `internal/storage/lock_test.go`:
```go
package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func lockPair(mem *Memory, mode LockMode, clock func() time.Time) (*Locker, *Locker) {
	mk := func(id string) *Locker {
		return &Locker{Store: mem, Key: "x/.meta/lock", MachineID: id, TTL: time.Minute, Mode: mode, Now: clock}
	}
	return mk("A"), mk("B")
}

func testLockLifecycle(t *testing.T, mem *Memory, mode LockMode) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	a, b := lockPair(mem, mode, func() time.Time { return now })

	if err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquire(ctx); !errors.Is(err, ErrLocked) {
		t.Fatalf("B acquired a held lock: %v", err)
	}
	if err := a.Acquire(ctx); err != nil {
		t.Fatalf("A could not re-acquire its own lock: %v", err)
	}
	if err := b.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Head(ctx, "x/.meta/lock"); err != nil {
		t.Fatal("B's release removed A's lock")
	}
	if err := a.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquire(ctx); err != nil {
		t.Fatalf("B after A released: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := a.Acquire(ctx); err != nil {
		t.Fatalf("A could not take over an expired lock: %v", err)
	}
}

func TestLockConditional(t *testing.T) {
	testLockLifecycle(t, NewMemory(), LockConditional)
}

func TestLockBestEffort(t *testing.T) {
	mem := NewMemory()
	mem.NoConditional = true
	testLockLifecycle(t, mem, LockBestEffort)
}

func TestLockCorruptBodyIsTreatedAsExpired(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	mem.Put(ctx, "x/.meta/lock", []byte("garbage"), nil)
	a, _ := lockPair(mem, LockConditional, time.Now)
	if err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/storage/ -run Lock`
Expected: FAIL with `undefined: Locker`.

- [ ] **Step 3: Write the implementation**

Create `internal/storage/lock.go`:
```go
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// LockMode selects how the lock is taken.
type LockMode int

const (
	// LockConditional uses If-None-Match / If-Match writes (atomic).
	LockConditional LockMode = iota
	// LockBestEffort writes, waits Settle, then reads back the owner. Used
	// for providers that ignore conditional writes.
	LockBestEffort
)

// ErrLocked means another machine holds an unexpired lock.
var ErrLocked = errors.New("storage: locked by another machine")

// LockInfo is the lock object's JSON body.
type LockInfo struct {
	MachineID string    `json:"machineId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Locker takes and releases the per-action lock object.
type Locker struct {
	Store     Storage
	Key       string
	MachineID string
	TTL       time.Duration
	Mode      LockMode
	Settle    time.Duration
	Now       func() time.Time
}

func (l *Locker) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Locker) body() []byte {
	b, _ := json.Marshal(LockInfo{MachineID: l.MachineID, ExpiresAt: l.now().Add(l.TTL).UTC()})
	return b
}

// readLock parses a lock body. ok is false for garbage, which is treated as expired.
func readLock(data []byte) (LockInfo, bool) {
	var info LockInfo
	if json.Unmarshal(data, &info) != nil || info.MachineID == "" {
		return info, false
	}
	return info, true
}

func (l *Locker) heldByOther(info LockInfo, ok bool) bool {
	return ok && info.MachineID != l.MachineID && l.now().Before(info.ExpiresAt)
}

func lockedErr(info LockInfo) error {
	return fmt.Errorf("%w (%s until %s)", ErrLocked, info.MachineID, info.ExpiresAt.Format(time.RFC3339))
}

// Acquire takes the lock, replacing an expired or own lock.
func (l *Locker) Acquire(ctx context.Context) error {
	if l.Mode == LockBestEffort {
		return l.acquireBestEffort(ctx)
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, err := l.Store.PutIfAbsent(ctx, l.Key, l.body())
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrPreconditionFailed) {
			return err
		}
		data, obj, err := l.Store.Get(ctx, l.Key)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if info, ok := readLock(data); l.heldByOther(info, ok) {
			return lockedErr(info)
		}
		_, err = l.Store.PutIfMatch(ctx, l.Key, l.body(), obj.ETag)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrPreconditionFailed) && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return ErrLocked
}

func (l *Locker) acquireBestEffort(ctx context.Context) error {
	data, _, err := l.Store.Get(ctx, l.Key)
	switch {
	case err == nil:
		if info, ok := readLock(data); l.heldByOther(info, ok) {
			return lockedErr(info)
		}
	case !errors.Is(err, ErrNotFound):
		return err
	}
	if _, err := l.Store.Put(ctx, l.Key, l.body(), nil); err != nil {
		return err
	}
	if l.Settle > 0 {
		t := time.NewTimer(l.Settle)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	data, _, err = l.Store.Get(ctx, l.Key)
	if err != nil {
		return err
	}
	if info, ok := readLock(data); !ok || info.MachineID != l.MachineID {
		return lockedErr(info)
	}
	return nil
}

// Release deletes the lock if this machine owns it.
func (l *Locker) Release(ctx context.Context) error {
	data, _, err := l.Store.Get(ctx, l.Key)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if info, ok := readLock(data); ok && info.MachineID != l.MachineID {
		return nil
	}
	return l.Store.Delete(ctx, l.Key)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/storage/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/storage`

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/storage
git add internal/storage
git commit -m "feat(storage): per-action remote lock with conditional and best-effort modes" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: S3 adapter, connection probe and integration tests

**Files:**
- Create: `internal/storage/s3.go`, `internal/storage/probe.go`, `internal/storage/s3test/s3test.go`
- Test: `internal/storage/s3_test.go`, `internal/storage/probe_test.go`, `internal/storage/s3_integration_test.go` (package `storage_test`)

**Interfaces:**
- Consumes: `Storage`, the error values and `JoinKey` (Task 6).
- Produces:
  - `S3Config{Endpoint, Region, Bucket, AccessKeyID, SecretAccessKey string; PathStyle bool}`
  - `NewS3(S3Config) (*S3, error)`; `*S3` implements `Storage` and has `EnsureBucket(ctx) error`
  - `ProbeResult{List, Put, Get, Delete, ConditionalWrites bool}` and `Probe(ctx, s Storage, prefix string) (ProbeResult, error)`
  - Package `s3test` with `New(t testing.TB) (*storage.S3, string)`. It returns a store and a unique key prefix, skips the test when `S3_TEST_ENDPOINT` is unset, and deletes the prefix on cleanup.

- [ ] **Step 1: Add the dependencies**

```bash
go get github.com/aws/aws-sdk-go-v2/aws@latest github.com/aws/aws-sdk-go-v2/credentials@latest github.com/aws/aws-sdk-go-v2/service/s3@latest github.com/aws/smithy-go@latest
```

- [ ] **Step 2: Write the failing tests**

Create `internal/storage/s3_test.go`:
```go
package storage

import (
	"errors"
	"testing"

	"github.com/aws/smithy-go"
)

func TestMapErr(t *testing.T) {
	cases := map[string]error{
		"NoSuchKey":                  ErrNotFound,
		"NotFound":                   ErrNotFound,
		"PreconditionFailed":         ErrPreconditionFailed,
		"ConditionalRequestConflict": ErrPreconditionFailed,
		"AccessDenied":               ErrAccessDenied,
		"InvalidAccessKeyId":         ErrAccessDenied,
		"SignatureDoesNotMatch":      ErrAccessDenied,
		"NotImplemented":             ErrConditionalUnsupported,
	}
	for code, want := range cases {
		err := mapErr(&smithy.GenericAPIError{Code: code, Message: "x"})
		if !errors.Is(err, want) {
			t.Errorf("mapErr(%s) = %v, want %v", code, err, want)
		}
	}
	other := errors.New("boom")
	if mapErr(other) != other {
		t.Error("unknown errors must pass through unchanged")
	}
	if mapErr(nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestEscapeKey(t *testing.T) {
	if got := escapeKey("dir a/c+d/é.json"); got != "dir%20a/c%2Bd/%C3%A9.json" {
		t.Fatalf("escapeKey = %q", got)
	}
}

func TestNewS3Validates(t *testing.T) {
	if _, err := NewS3(S3Config{AccessKeyID: "k", SecretAccessKey: "s"}); err == nil {
		t.Fatal("missing bucket accepted")
	}
	if _, err := NewS3(S3Config{Bucket: "b"}); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
```

Create `internal/storage/probe_test.go`:
```go
package storage

import (
	"context"
	"testing"
)

func TestProbe(t *testing.T) {
	mem := NewMemory()
	res, err := Probe(context.Background(), mem, "root")
	if err != nil {
		t.Fatal(err)
	}
	if !res.List || !res.Put || !res.Get || !res.Delete || !res.ConditionalWrites {
		t.Fatalf("res = %+v", res)
	}
	if keys := mem.Keys(); len(keys) != 0 {
		t.Fatalf("probe left objects behind: %v", keys)
	}
}

func TestProbeWithoutConditionalWrites(t *testing.T) {
	mem := NewMemory()
	mem.NoConditional = true
	res, err := Probe(context.Background(), mem, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.ConditionalWrites || !res.Delete {
		t.Fatalf("res = %+v", res)
	}
}
```

Create `internal/storage/s3_integration_test.go`:
```go
package storage_test

import (
	"context"
	"testing"

	"github.com/niceit/s3-anysync/internal/storage"
	"github.com/niceit/s3-anysync/internal/storage/s3test"
)

func TestS3Contract(t *testing.T) {
	s, prefix := s3test.New(t)
	runContract(t, s, prefix)
}

func TestS3Probe(t *testing.T) {
	s, prefix := s3test.New(t)
	res, err := storage.Probe(context.Background(), s, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if !res.List || !res.Put || !res.Get || !res.Delete {
		t.Fatalf("res = %+v", res)
	}
	t.Logf("conditional writes supported: %v", res.ConditionalWrites)
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/storage/...`
Expected: FAIL with `undefined: mapErr`, `undefined: Probe` and a missing `s3test` package.

- [ ] **Step 4: Write the implementation**

Create `internal/storage/s3.go`:
```go
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// S3Config is what NewS3 needs. Region defaults to us-east-1.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	PathStyle       bool
}

// S3 is a Storage on any S3-compatible service.
type S3 struct {
	client *s3.Client
	bucket string
}

var _ Storage = (*S3)(nil)

// NewS3 builds a client. It does not contact the server.
func NewS3(cfg S3Config) (*S3, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("bucket is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("access key and secret key are required")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	awsCfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle
		// Several S3-compatible providers reject the newer default checksums.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &S3{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3) put(ctx context.Context, in *s3.PutObjectInput, body []byte) (Object, error) {
	in.Bucket = aws.String(s.bucket)
	in.Body = bytes.NewReader(body)
	in.ContentLength = aws.Int64(int64(len(body)))
	out, err := s.client.PutObject(ctx, in)
	if err != nil {
		return Object{}, mapErr(err)
	}
	return Object{Key: aws.ToString(in.Key), Size: int64(len(body)), ETag: aws.ToString(out.ETag), Metadata: lowerKeys(in.Metadata)}, nil
}

func (s *S3) Put(ctx context.Context, key string, body []byte, meta map[string]string) (Object, error) {
	return s.put(ctx, &s3.PutObjectInput{Key: aws.String(key), Metadata: meta}, body)
}

func (s *S3) PutIfAbsent(ctx context.Context, key string, body []byte) (Object, error) {
	return s.put(ctx, &s3.PutObjectInput{Key: aws.String(key), IfNoneMatch: aws.String("*")}, body)
}

func (s *S3) PutIfMatch(ctx context.Context, key string, body []byte, etag string) (Object, error) {
	return s.put(ctx, &s3.PutObjectInput{Key: aws.String(key), IfMatch: aws.String(etag)}, body)
}

func (s *S3) Get(ctx context.Context, key string) ([]byte, Object, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, Object{}, mapErr(err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, Object{}, err
	}
	return data, Object{
		Key: key, Size: int64(len(data)), ETag: aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified), Metadata: lowerKeys(out.Metadata),
	}, nil
}

func (s *S3) Head(ctx context.Context, key string) (Object, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return Object{}, mapErr(err)
	}
	return Object{
		Key: key, Size: aws.ToInt64(out.ContentLength), ETag: aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified), Metadata: lowerKeys(out.Metadata),
	}, nil
}

func (s *S3) List(ctx context.Context, prefix string) ([]Object, error) {
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)})
	var out []Object
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, mapErr(err)
		}
		for _, o := range page.Contents {
			out = append(out, Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), ETag: aws.ToString(o.ETag), LastModified: aws.ToTime(o.LastModified)})
		}
	}
	return out, nil
}

func (s *S3) ListPrefixes(ctx context.Context, prefix string) ([]string, error) {
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix), Delimiter: aws.String("/")})
	var out []string
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, mapErr(err)
		}
		for _, cp := range page.CommonPrefixes {
			out = append(out, aws.ToString(cp.Prefix))
		}
	}
	return out, nil
}

func (s *S3) Copy(ctx context.Context, srcKey, dstKey string) (Object, error) {
	out, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(s.bucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(s.bucket + "/" + escapeKey(srcKey)),
	})
	if err != nil {
		return Object{}, mapErr(err)
	}
	obj := Object{Key: dstKey}
	if out.CopyObjectResult != nil {
		obj.ETag = aws.ToString(out.CopyObjectResult.ETag)
	}
	return obj, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return mapErr(err)
}

// EnsureBucket creates the bucket if it does not exist. Used by tests.
func (s *S3) EnsureBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err == nil {
		return nil
	}
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "BucketAlreadyOwnedByYou" || apiErr.ErrorCode() == "BucketAlreadyExists") {
		return nil
	}
	return mapErr(err)
}

// escapeKey URL-encodes each path segment for the CopySource header.
func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(url.QueryEscape(p), "+", "%20")
	}
	return strings.Join(parts, "/")
}

func lowerKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}

// mapErr turns SDK errors into the package's sentinel errors.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return fmt.Errorf("%w: %v", ErrNotFound, err)
		case "PreconditionFailed", "ConditionalRequestConflict":
			return fmt.Errorf("%w: %v", ErrPreconditionFailed, err)
		case "AccessDenied", "Forbidden", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return fmt.Errorf("%w: %v", ErrAccessDenied, err)
		case "NotImplemented":
			return fmt.Errorf("%w: %v", ErrConditionalUnsupported, err)
		}
	}
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.HTTPStatusCode() {
		case 404:
			return fmt.Errorf("%w: %v", ErrNotFound, err)
		case 412:
			return fmt.Errorf("%w: %v", ErrPreconditionFailed, err)
		case 403:
			return fmt.Errorf("%w: %v", ErrAccessDenied, err)
		case 501:
			return fmt.Errorf("%w: %v", ErrConditionalUnsupported, err)
		}
	}
	return err
}
```

Create `internal/storage/probe.go`:
```go
package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// ProbeResult reports which operations the credentials and provider support.
type ProbeResult struct {
	List              bool `json:"list"`
	Put               bool `json:"put"`
	Get               bool `json:"get"`
	Delete            bool `json:"delete"`
	ConditionalWrites bool `json:"conditionalWrites"`
}

// Probe exercises list, put, get, delete and conditional writes under
// prefix, and removes everything it wrote.
func Probe(ctx context.Context, s Storage, prefix string) (ProbeResult, error) {
	var res ProbeResult
	token := make([]byte, 8)
	rand.Read(token)
	base := JoinKey(prefix, ".s3-anysync-probe-"+hex.EncodeToString(token))
	dataKey, condKey := base+"-data", base+"-cond"

	listPrefix := JoinKey(prefix)
	if listPrefix != "" {
		listPrefix += "/"
	}
	if _, err := s.ListPrefixes(ctx, listPrefix); err != nil {
		return res, fmt.Errorf("list: %w", err)
	}
	res.List = true

	payload := []byte("s3-anysync probe")
	if _, err := s.Put(ctx, dataKey, payload, nil); err != nil {
		return res, fmt.Errorf("put: %w", err)
	}
	res.Put = true

	got, _, err := s.Get(ctx, dataKey)
	if err != nil {
		return res, fmt.Errorf("get: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return res, errors.New("get: content differs from what was written")
	}
	res.Get = true

	res.ConditionalWrites = probeConditional(ctx, s, condKey)

	for _, k := range []string{dataKey, condKey} {
		if err := s.Delete(ctx, k); err != nil {
			return res, fmt.Errorf("delete: %w", err)
		}
	}
	res.Delete = true
	return res, nil
}

// probeConditional is true only when a second If-None-Match:* write is rejected.
func probeConditional(ctx context.Context, s Storage, key string) bool {
	if _, err := s.PutIfAbsent(ctx, key, []byte("1")); err != nil {
		return false
	}
	_, err := s.PutIfAbsent(ctx, key, []byte("2"))
	return errors.Is(err, ErrPreconditionFailed)
}
```

Create `internal/storage/s3test/s3test.go`:
```go
// Package s3test connects tests to a real S3-compatible server configured by
// S3_TEST_ENDPOINT, S3_TEST_KEY, S3_TEST_SECRET, S3_TEST_BUCKET and
// optionally S3_TEST_REGION.
package s3test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/niceit/s3-anysync/internal/storage"
)

// New returns a store and a unique key prefix, or skips the test when no
// server is configured. Everything under the prefix is deleted on cleanup.
func New(t testing.TB) (*storage.S3, string) {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set S3_TEST_ENDPOINT, S3_TEST_KEY, S3_TEST_SECRET and S3_TEST_BUCKET to run S3 integration tests")
	}
	region := os.Getenv("S3_TEST_REGION")
	if region == "" {
		region = "us-east-1"
	}
	s, err := storage.NewS3(storage.S3Config{
		Endpoint: endpoint, Region: region, Bucket: os.Getenv("S3_TEST_BUCKET"),
		AccessKeyID: os.Getenv("S3_TEST_KEY"), SecretAccessKey: os.Getenv("S3_TEST_SECRET"), PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	rand.Read(b)
	prefix := "s3-anysync-it-" + hex.EncodeToString(b)
	t.Cleanup(func() {
		ctx := context.Background()
		objs, _ := s.List(ctx, prefix+"/")
		for _, o := range objs {
			_ = s.Delete(ctx, o.Key)
		}
	})
	return s, prefix
}
```

- [ ] **Step 5: Run the unit tests to verify they pass**

Run: `go test ./internal/storage/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/storage`. The S3 integration tests report SKIP when `-v` is used.

- [ ] **Step 6: Run the integration tests against MinIO**

Start Docker Desktop, then:
```bash
docker run -d --rm --name s3-anysync-minio -p 9000:9000 -e MINIO_ROOT_USER=minio -e MINIO_ROOT_PASSWORD=minio12345 quay.io/minio/minio server /data
```
```bash
S3_TEST_ENDPOINT=http://localhost:9000 S3_TEST_KEY=minio S3_TEST_SECRET=minio12345 S3_TEST_BUCKET=s3-anysync-test go test ./internal/storage/... -run S3 -v
```
Expected: `TestS3Contract` and `TestS3Probe` PASS, and the log shows `conditional writes supported: true`.
If the image cannot be pulled, point the `S3_TEST_*` variables at any S3-compatible bucket you control and record which server you used in the commit message.

- [ ] **Step 7: Commit**

```bash
gofmt -w internal/storage && go mod tidy
git add go.mod go.sum internal/storage
git commit -m "feat(storage): S3 adapter, connection probe and integration tests" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Engine dependencies, remote layout, manifest and shared action definitions

**Files:**
- Create: `internal/engine/deps.go`, `internal/engine/layout.go`, `internal/engine/remote.go`, `internal/engine/shared.go`
- Test: `internal/engine/helpers_test.go`, `internal/engine/remote_test.go`, `internal/engine/shared_test.go`

**Interfaces:**
- Consumes:
  - `storage.Storage`, `storage.Locker`, `storage.LockMode`, `storage.ErrNotFound`, `storage.JoinKey`
  - `crypto.Encrypt` and `crypto.Decrypt`
  - `paths.Expand`
  - `model.*`
- Produces: package `engine`:
  - Errors `ErrPassphraseMissing`, `ErrDecrypt`, `ErrConflictPending`
  - Constants `LockTTL = 2 * time.Minute` and `LocalBackupKeep = 20`
  - `Repo` interface:
    - `PutAction(model.Action) error`
    - `LoadBaseline(string) (map[string]model.FileEntry, error)` and `SaveBaseline(string, map[string]model.FileEntry) error`
    - `SaveConflicts(string, []model.Conflict) error` and `LoadConflicts(string) ([]model.Conflict, error)`
    - `AddHistory(model.HistoryEntry) error`
  - `Deps{Storage, Repo, MachineID, MachineName, Passphrase, BackupRoot, LockMode, LockSettle, Expand, Now, Log}`
  - `Layout`, created by `NewLayout(root, actionID)`, with methods `ActionJSON()`, `Manifest()`, `Lock()`, `KeyCheck()`, `CurrentPrefix()`, `Current(p)`, `SnapshotsPrefix()`, `SnapshotFile(ts, p)`, `SnapshotIndex(ts)`
  - Unexported helpers `loadManifest(ctx, s, l) (model.Manifest, string, error)`, `manifestETag`, `saveManifest` and `sortedEntries`
  - `FetchAction(ctx, s, root, id) (model.Action, error)`
  - `DiscoverActions(ctx, s, root) ([]model.Action, error)`
  - `CreateSharedAction(ctx, d Deps, root string, a model.Action) error`
  - `UpdateSharedAction(ctx, d Deps, root, id string, mutate func(*model.Action) error) (model.Action, error)`
  - `VerifyPassphrase(ctx, d Deps, root string, a model.Action) error`

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/helpers_test.go`:
```go
package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

var ctx = context.Background()

func newDB(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func basicAction(id string) model.Action {
	a := model.Action{ID: id, Name: id, PropagateDeletes: true, Items: []model.Item{
		{ID: "settings", RemoteName: "settings.json", Type: model.ItemFile},
	}}
	a.ApplyDefaults()
	return a
}

func memDeps(mem *storage.Memory, machineID string) Deps {
	return Deps{Storage: mem, MachineID: machineID, MachineName: "machine " + machineID, LockMode: storage.LockConditional}
}
```

Create `internal/engine/remote_test.go`:
```go
package engine

import (
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func TestLayout(t *testing.T) {
	l := NewLayout("/cfg/", "a1")
	cases := map[string]string{
		l.ActionJSON():                  "cfg/a1/.meta/action.json",
		l.Manifest():                    "cfg/a1/.meta/manifest.json",
		l.Lock():                        "cfg/a1/.meta/lock",
		l.KeyCheck():                    "cfg/a1/.meta/keycheck",
		l.Current("d/x.json"):           "cfg/a1/current/d/x.json",
		l.SnapshotFile("TS", "d/x"):     "cfg/a1/snapshots/TS/d/x",
		l.SnapshotIndex("TS"):           "cfg/a1/snapshots/TS.json",
		NewLayout("", "a1").Manifest():  "a1/.meta/manifest.json",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestManifestRoundTripAndRebuild(t *testing.T) {
	mem := storage.NewMemory()
	l := NewLayout("cfg", "a1")

	m, etag, err := loadManifest(ctx, mem, l)
	if err != nil || etag != "" || m.Files == nil || len(m.Files) != 0 {
		t.Fatalf("empty: %+v %q %v", m, etag, err)
	}

	// Objects written by s3-anysync carry metadata; foreign ones are skipped.
	mem.Put(ctx, l.Current("a.json"), []byte("x"), map[string]string{metaSHA256: "h1", metaSize: "1", metaMTime: "42", metaMachine: "A"})
	mem.Put(ctx, l.Current("foreign.txt"), []byte("y"), nil)
	m, _, err = loadManifest(ctx, mem, l)
	if err != nil || len(m.Files) != 1 || m.Files["a.json"].Hash != "h1" || m.Files["a.json"].MTime != 42 || m.Files["a.json"].Writer != "A" {
		t.Fatalf("rebuilt: %+v %v", m, err)
	}

	m.Files["b.json"] = model.FileEntry{Path: "b.json", Hash: "h2"}
	if err := saveManifest(ctx, mem, l, m); err != nil {
		t.Fatal(err)
	}
	got, etag, err := loadManifest(ctx, mem, l)
	if err != nil || etag == "" || len(got.Files) != 2 {
		t.Fatalf("saved: %+v %q %v", got, etag, err)
	}
	if cur, _ := manifestETag(ctx, mem, l); cur != etag {
		t.Fatalf("manifestETag = %q want %q", cur, etag)
	}

	mem.Put(ctx, l.Manifest(), []byte("not json"), nil)
	got, etag, err = loadManifest(ctx, mem, l)
	if err != nil || etag == "" || len(got.Files) != 1 {
		t.Fatalf("corrupt manifest should be rebuilt from objects: %+v %q %v", got, etag, err)
	}
}
```

Create `internal/engine/shared_test.go`:
```go
package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func TestCreateFetchDiscover(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	for _, id := range []string{"b-action", "a-action"} {
		a := basicAction(id)
		if err := CreateSharedAction(ctx, d, "cfg", a); err != nil {
			t.Fatal(err)
		}
	}
	mem.Put(ctx, "cfg/stray/readme.txt", []byte("x"), nil)

	got, err := FetchAction(ctx, mem, "cfg", "a-action")
	if err != nil || got.Name != "a-action" || got.Machines == nil {
		t.Fatalf("FetchAction = %+v, %v", got, err)
	}
	if _, err := FetchAction(ctx, mem, "cfg", "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing action: %v", err)
	}
	list, err := DiscoverActions(ctx, mem, "cfg")
	if err != nil || len(list) != 2 || list[0].ID != "a-action" {
		t.Fatalf("DiscoverActions = %+v, %v", list, err)
	}
}

func TestUpdateSharedAction(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Now = fixedClock()
	a := basicAction("x")
	CreateSharedAction(ctx, d, "", a)

	got, err := UpdateSharedAction(ctx, d, "", "x", func(a *model.Action) error {
		a.Items[0].LocalPaths["A"] = "~/s.json"
		return nil
	})
	if err != nil || got.Items[0].LocalPaths["A"] != "~/s.json" || !got.UpdatedAt.Equal(d.Now()) {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if _, err := mem.Head(ctx, NewLayout("", "x").Lock()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("lock not released after update")
	}

	// Validation errors are not published.
	_, err = UpdateSharedAction(ctx, d, "", "x", func(a *model.Action) error { a.Name = ""; return nil })
	if err == nil {
		t.Fatal("invalid update accepted")
	}

	// A lock held by another machine blocks updates.
	mem.Put(ctx, NewLayout("", "x").Lock(), []byte(`{"machineId":"B","expiresAt":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}`), nil)
	if _, err := UpdateSharedAction(ctx, d, "", "x", func(*model.Action) error { return nil }); !errors.Is(err, storage.ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
}

func TestVerifyPassphrase(t *testing.T) {
	mem := storage.NewMemory()
	a := basicAction("enc")
	a.Encrypt = true
	d := memDeps(mem, "A")
	if err := CreateSharedAction(ctx, d, "", a); !errors.Is(err, ErrPassphraseMissing) {
		t.Fatalf("create without passphrase: %v", err)
	}
	d.Passphrase = "right"
	if err := CreateSharedAction(ctx, d, "", a); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassphrase(ctx, d, "", a); err != nil {
		t.Fatal(err)
	}
	d.Passphrase = "wrong"
	if err := VerifyPassphrase(ctx, d, "", a); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	d.Passphrase = ""
	if err := VerifyPassphrase(ctx, d, "", a); !errors.Is(err, ErrPassphraseMissing) {
		t.Fatalf("no passphrase: %v", err)
	}
	if err := VerifyPassphrase(ctx, d, "", basicAction("plain")); err != nil {
		t.Fatalf("unencrypted action: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/engine/...`
Expected: FAIL with `undefined: NewLayout` and similar.

- [ ] **Step 3: Write the implementation**

Create `internal/engine/deps.go`:
```go
// Package engine runs one action's sync: scan, plan, execute and commit,
// plus snapshots, conflict resolution and restore.
package engine

import (
	"errors"
	"log/slog"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
	"github.com/niceit/s3-anysync/internal/storage"
)

var (
	// ErrPassphraseMissing: the action is encrypted and no passphrase is set here.
	ErrPassphraseMissing = errors.New("this action is encrypted and this machine has no passphrase for it")
	// ErrDecrypt: wrong passphrase or damaged data. Never retried automatically.
	ErrDecrypt = errors.New("cannot decrypt: wrong passphrase or damaged data")
	// ErrConflictPending: the action is paused until its conflicts are resolved.
	ErrConflictPending = errors.New("the action has unresolved conflicts")
)

const (
	// LockTTL is how long a remote lock is valid without renewal.
	LockTTL = 2 * time.Minute
	// LocalBackupKeep is how many local backup folders are kept per action.
	LocalBackupKeep = 20
	maxSyncAttempts = 3
)

// Repo is the local state the engine needs. *state.DB implements it.
type Repo interface {
	PutAction(a model.Action) error
	LoadBaseline(actionID string) (map[string]model.FileEntry, error)
	SaveBaseline(actionID string, baseline map[string]model.FileEntry) error
	SaveConflicts(actionID string, conflicts []model.Conflict) error
	LoadConflicts(actionID string) ([]model.Conflict, error)
	AddHistory(h model.HistoryEntry) error
}

// Deps is everything a Runner needs for one action on this machine.
type Deps struct {
	Storage     storage.Storage
	Repo        Repo
	MachineID   string
	MachineName string
	// Passphrase is empty for unencrypted actions.
	Passphrase string
	// BackupRoot is where local files are copied before being overwritten or deleted.
	BackupRoot string
	LockMode   storage.LockMode
	LockSettle time.Duration
	// Expand resolves item paths; nil means paths.Expand.
	Expand func(string) (string, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Log is optional.
	Log *slog.Logger
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (d Deps) expand(p string) (string, error) {
	if d.Expand != nil {
		return d.Expand(p)
	}
	return paths.Expand(p)
}

func (d Deps) locker(l Layout) *storage.Locker {
	return &storage.Locker{
		Store: d.Storage, Key: l.Lock(), MachineID: d.MachineID, TTL: LockTTL,
		Mode: d.LockMode, Settle: d.LockSettle, Now: d.Now,
	}
}
```

Create `internal/engine/layout.go`:
```go
package engine

import "github.com/niceit/s3-anysync/internal/storage"

// Layout builds the object keys for one action.
type Layout struct {
	base string
}

// NewLayout returns the layout for <root>/<actionID>.
func NewLayout(root, actionID string) Layout {
	return Layout{base: storage.JoinKey(root, actionID)}
}

func (l Layout) ActionJSON() string      { return l.base + "/.meta/action.json" }
func (l Layout) Manifest() string        { return l.base + "/.meta/manifest.json" }
func (l Layout) Lock() string            { return l.base + "/.meta/lock" }
func (l Layout) KeyCheck() string        { return l.base + "/.meta/keycheck" }
func (l Layout) CurrentPrefix() string   { return l.base + "/current/" }
func (l Layout) Current(p string) string { return l.CurrentPrefix() + p }
func (l Layout) SnapshotsPrefix() string { return l.base + "/snapshots/" }

func (l Layout) SnapshotFile(ts, p string) string { return l.SnapshotsPrefix() + ts + "/" + p }
func (l Layout) SnapshotIndex(ts string) string   { return l.SnapshotsPrefix() + ts + ".json" }
```

Create `internal/engine/remote.go`:
```go
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// Object metadata written with every uploaded file.
const (
	metaSHA256  = "sha256"
	metaSize    = "size"
	metaMTime   = "mtime"
	metaMachine = "machine"
)

// loadManifest returns the cloud state and the manifest object's ETag ("" if
// it does not exist). A missing or unreadable manifest is rebuilt from the
// objects under current/.
func loadManifest(ctx context.Context, s storage.Storage, l Layout) (model.Manifest, string, error) {
	data, obj, err := s.Get(ctx, l.Manifest())
	if errors.Is(err, storage.ErrNotFound) {
		m, err := rebuildManifest(ctx, s, l)
		return m, "", err
	}
	if err != nil {
		return model.Manifest{}, "", fmt.Errorf("read manifest: %w", err)
	}
	var m model.Manifest
	if err := json.Unmarshal(data, &m); err == nil && m.Files != nil {
		return m, obj.ETag, nil
	}
	m, err = rebuildManifest(ctx, s, l)
	return m, obj.ETag, err
}

// rebuildManifest lists current/ and reads each object's metadata. Objects
// without our sha256 metadata were not written by s3-anysync and are skipped.
func rebuildManifest(ctx context.Context, s storage.Storage, l Layout) (model.Manifest, error) {
	m := model.Manifest{Files: map[string]model.FileEntry{}}
	prefix := l.CurrentPrefix()
	objs, err := s.List(ctx, prefix)
	if err != nil {
		return m, fmt.Errorf("list cloud files: %w", err)
	}
	for _, o := range objs {
		head, err := s.Head(ctx, o.Key)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return m, err
		}
		hash := head.Metadata[metaSHA256]
		if hash == "" {
			continue
		}
		size, _ := strconv.ParseInt(head.Metadata[metaSize], 10, 64)
		mtime, _ := strconv.ParseInt(head.Metadata[metaMTime], 10, 64)
		p := strings.TrimPrefix(o.Key, prefix)
		m.Files[p] = model.FileEntry{Path: p, Hash: hash, Size: size, MTime: mtime, ETag: head.ETag, Writer: head.Metadata[metaMachine]}
	}
	return m, nil
}

// manifestETag returns the manifest's current ETag, or "" if it does not exist.
func manifestETag(ctx context.Context, s storage.Storage, l Layout) (string, error) {
	obj, err := s.Head(ctx, l.Manifest())
	if errors.Is(err, storage.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return obj.ETag, nil
}

func saveManifest(ctx context.Context, s storage.Storage, l Layout, m model.Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.Put(ctx, l.Manifest(), data, nil)
	return err
}

func sortedEntries(files map[string]model.FileEntry) []model.FileEntry {
	out := make([]model.FileEntry, 0, len(files))
	for _, e := range files {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
```

Create `internal/engine/shared.go`:
```go
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/niceit/s3-anysync/internal/crypto"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// keyCheckPlaintext is encrypted into .meta/keycheck so a machine can verify
// its passphrase before uploading anything.
const keyCheckPlaintext = "s3-anysync-keycheck-v1"

// FetchAction reads an action definition from the bucket. ProfileID is empty.
func FetchAction(ctx context.Context, s storage.Storage, root, id string) (model.Action, error) {
	data, _, err := s.Get(ctx, NewLayout(root, id).ActionJSON())
	if err != nil {
		return model.Action{}, err
	}
	var a model.Action
	if err := json.Unmarshal(data, &a); err != nil {
		return model.Action{}, fmt.Errorf("parse action %s: %w", id, err)
	}
	a.ApplyDefaults()
	return a, nil
}

func publishAction(ctx context.Context, s storage.Storage, l Layout, a model.Action) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.Put(ctx, l.ActionJSON(), data, nil)
	return err
}

// DiscoverActions lists every action under root, sorted by name.
func DiscoverActions(ctx context.Context, s storage.Storage, root string) ([]model.Action, error) {
	prefix := storage.JoinKey(root)
	if prefix != "" {
		prefix += "/"
	}
	dirs, err := s.ListPrefixes(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []model.Action
	for _, dir := range dirs {
		id := strings.TrimSuffix(strings.TrimPrefix(dir, prefix), "/")
		a, err := FetchAction(ctx, s, root, id)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if a.ID == id {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CreateSharedAction publishes a new action, writing the key check first
// for encrypted actions.
func CreateSharedAction(ctx context.Context, d Deps, root string, a model.Action) error {
	if err := a.Validate(); err != nil {
		return err
	}
	l := NewLayout(root, a.ID)
	if a.Encrypt {
		if err := writeKeyCheck(ctx, d, l); err != nil {
			return err
		}
	}
	return publishAction(ctx, d.Storage, l, a)
}

func writeKeyCheck(ctx context.Context, d Deps, l Layout) error {
	if d.Passphrase == "" {
		return ErrPassphraseMissing
	}
	body, err := crypto.Encrypt([]byte(keyCheckPlaintext), d.Passphrase)
	if err != nil {
		return err
	}
	_, err = d.Storage.Put(ctx, l.KeyCheck(), body, nil)
	return err
}

// VerifyPassphrase checks d.Passphrase against the action's key check. For an
// encrypted action without a key check, it writes one.
func VerifyPassphrase(ctx context.Context, d Deps, root string, a model.Action) error {
	if !a.Encrypt {
		return nil
	}
	if d.Passphrase == "" {
		return ErrPassphraseMissing
	}
	l := NewLayout(root, a.ID)
	data, _, err := d.Storage.Get(ctx, l.KeyCheck())
	if errors.Is(err, storage.ErrNotFound) {
		return writeKeyCheck(ctx, d, l)
	}
	if err != nil {
		return err
	}
	pt, err := crypto.Decrypt(data, d.Passphrase)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	if string(pt) != keyCheckPlaintext {
		return ErrDecrypt
	}
	return nil
}

// UpdateSharedAction applies mutate to the bucket's copy of the action under
// the action lock, stamps UpdatedAt and republishes it. ProfileID is empty
// in the result.
func UpdateSharedAction(ctx context.Context, d Deps, root, id string, mutate func(*model.Action) error) (model.Action, error) {
	l := NewLayout(root, id)
	lk := d.locker(l)
	if err := lk.Acquire(ctx); err != nil {
		return model.Action{}, err
	}
	defer func() {
		if err := lk.Release(context.WithoutCancel(ctx)); err != nil {
			d.log().Warn("release lock", "action", id, "err", err)
		}
	}()
	a, err := FetchAction(ctx, d.Storage, root, id)
	if err != nil {
		return model.Action{}, err
	}
	if err := mutate(&a); err != nil {
		return model.Action{}, err
	}
	a.UpdatedAt = d.now().UTC()
	if err := a.Validate(); err != nil {
		return model.Action{}, err
	}
	if err := publishAction(ctx, d.Storage, l, a); err != nil {
		return model.Action{}, err
	}
	return a, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/engine/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/engine`

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine
git add internal/engine
git commit -m "feat(engine): remote layout, manifest, shared action definitions and key check" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Item mappings

**Files:**
- Create: `internal/engine/mapping.go`
- Test: `internal/engine/mapping_test.go`

**Interfaces:**
- Consumes: `model.Action`, `model.Item` and `paths.NewMatcher`.
- Produces:
  - `Mapping{ItemID, RemoteName string; Type model.ItemType; LocalPath string}`, plus an unexported matcher
  - `Mappings []Mapping`
  - `BuildMappings(a model.Action, machineID string, expand func(string) (string, error)) (Mappings, error)`
  - `(Mappings).LocalPathFor(remotePath) (string, bool)` and `(Mappings).Owns(remotePath) bool`
  - The unexported `filterOwned(files map[string]model.FileEntry, ms Mappings) map[string]model.FileEntry`

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/mapping_test.go`:
```go
package engine

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func identity(p string) (string, error) { return filepath.FromSlash(p), nil }

func mappingAction() model.Action {
	a := model.Action{ID: "a", Name: "a", Excludes: []string{"*.log"}, Items: []model.Item{
		{ID: "f", RemoteName: "settings.json", Type: model.ItemFile, LocalPaths: map[string]string{"M": "/cfg/settings.json"}},
		{ID: "d", RemoteName: "snippets", Type: model.ItemDir, Excludes: []string{"cache"}, LocalPaths: map[string]string{"M": "/cfg/snippets"}},
		{ID: "u", RemoteName: "other", Type: model.ItemDir, LocalPaths: map[string]string{"X": "/x"}},
	}}
	a.ApplyDefaults()
	return a
}

func TestBuildMappings(t *testing.T) {
	ms, err := BuildMappings(mappingAction(), "M", identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("want 2 mapped items, got %d", len(ms))
	}
	if p, ok := ms.LocalPathFor("settings.json"); !ok || p != filepath.FromSlash("/cfg/settings.json") {
		t.Fatalf("file item: %q %v", p, ok)
	}
	if p, ok := ms.LocalPathFor("snippets/a/b.json"); !ok || p != filepath.Join(filepath.FromSlash("/cfg/snippets"), "a", "b.json") {
		t.Fatalf("dir item: %q %v", p, ok)
	}
	if _, ok := ms.LocalPathFor("other/x"); ok {
		t.Fatal("unmapped item resolved")
	}
	owns := map[string]bool{
		"settings.json":           true,
		"snippets/ok.json":        true,
		"snippets/x.log":          false,
		"snippets/cache/x.json":   false,
		"snippetsX/a":             false,
		"snippets/../etc/passwd":  false,
		"snippets/a//b":           false,
		`snippets/a\..\b`:         false,
		"other/x":                 false,
	}
	for p, want := range owns {
		if got := ms.Owns(p); got != want {
			t.Errorf("Owns(%q) = %v, want %v", p, got, want)
		}
	}
	files := map[string]model.FileEntry{"settings.json": {}, "other/x": {}, "snippets/x.log": {}}
	if got := filterOwned(files, ms); len(got) != 1 {
		t.Fatalf("filterOwned = %v", got)
	}
}

func TestBuildMappingsExpandError(t *testing.T) {
	_, err := BuildMappings(mappingAction(), "M", func(string) (string, error) { return "", errors.New("no APPDATA") })
	if err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("want error naming the item, got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/engine/ -run Mapping`
Expected: FAIL with `undefined: BuildMappings`.

- [ ] **Step 3: Write the implementation**

Create `internal/engine/mapping.go`:
```go
package engine

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
)

// Mapping ties one item to its local path on this machine.
type Mapping struct {
	ItemID     string
	RemoteName string
	Type       model.ItemType
	LocalPath  string
	matcher    *paths.Matcher
}

// Mappings are the items mapped on this machine.
type Mappings []Mapping

// BuildMappings returns the action's items that have a local path for
// machineID, with paths expanded. Unmapped items are left out.
func BuildMappings(a model.Action, machineID string, expand func(string) (string, error)) (Mappings, error) {
	var ms Mappings
	for _, it := range a.Items {
		raw := strings.TrimSpace(it.LocalPaths[machineID])
		if raw == "" {
			continue
		}
		abs, err := expand(raw)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", it.RemoteName, err)
		}
		m, err := paths.NewMatcher(a.Excludes, it.Excludes)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", it.RemoteName, err)
		}
		ms = append(ms, Mapping{ItemID: it.ID, RemoteName: it.RemoteName, Type: it.Type, LocalPath: abs, matcher: m})
	}
	return ms, nil
}

// safeRel rejects relative paths that could escape the item's directory.
func safeRel(rel string) bool {
	if rel == "" || strings.Contains(rel, `\`) {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func (ms Mappings) resolve(remotePath string) (Mapping, string, bool) {
	for _, m := range ms {
		switch m.Type {
		case model.ItemFile:
			if remotePath == m.RemoteName {
				return m, path.Base(m.RemoteName), true
			}
		case model.ItemDir:
			if rel, ok := strings.CutPrefix(remotePath, m.RemoteName+"/"); ok && safeRel(rel) {
				return m, rel, true
			}
		}
	}
	return Mapping{}, "", false
}

// LocalPathFor returns where a remote path lives on this machine.
func (ms Mappings) LocalPathFor(remotePath string) (string, bool) {
	m, rel, ok := ms.resolve(remotePath)
	if !ok {
		return "", false
	}
	if m.Type == model.ItemFile {
		return m.LocalPath, true
	}
	return filepath.Join(m.LocalPath, filepath.FromSlash(rel)), true
}

// Owns reports whether a remote path belongs to a mapped item and is not excluded.
func (ms Mappings) Owns(remotePath string) bool {
	m, rel, ok := ms.resolve(remotePath)
	return ok && !m.matcher.MatchPath(rel)
}

// filterOwned keeps only entries this machine owns.
func filterOwned(files map[string]model.FileEntry, ms Mappings) map[string]model.FileEntry {
	out := make(map[string]model.FileEntry, len(files))
	for p, e := range files {
		if ms.Owns(p) {
			out[p] = e
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/engine/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/engine`

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine
git add internal/engine
git commit -m "feat(engine): item to local path mappings with excludes" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Local scan with hash cache

**Files:**
- Create: `internal/engine/scan.go`
- Test: `internal/engine/scan_test.go`

**Interfaces:**
- Consumes: `Mappings` (Task 10).
- Produces:
  - The unexported `scanLocal(ms Mappings, baseline map[string]model.FileEntry) (map[string]model.FileEntry, error)`
  - The unexported helpers `hashFile(path) (string, error)` and `hashBytes([]byte) string`, which return a hex SHA-256

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/scan_test.go`:
```go
package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanFixture(t *testing.T) (string, Mappings) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "settings.json"), "{}")
	writeFile(t, filepath.Join(dir, "snippets", "a.json"), "a")
	writeFile(t, filepath.Join(dir, "snippets", "sub", "b.json"), "bb")
	writeFile(t, filepath.Join(dir, "snippets", "cache", "c.json"), "c")
	writeFile(t, filepath.Join(dir, "snippets", "x.log"), "log")
	a := model.Action{ID: "a", Name: "a", Excludes: []string{"*.log"}, Items: []model.Item{
		{ID: "f", RemoteName: "settings.json", Type: model.ItemFile, LocalPaths: map[string]string{"M": filepath.Join(dir, "settings.json")}},
		{ID: "d", RemoteName: "snippets", Type: model.ItemDir, Excludes: []string{"cache"}, LocalPaths: map[string]string{"M": filepath.Join(dir, "snippets")}},
	}}
	a.ApplyDefaults()
	ms, err := BuildMappings(a, "M", func(p string) (string, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	return dir, ms
}

func keys(m map[string]model.FileEntry) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestScanLocal(t *testing.T) {
	_, ms := scanFixture(t)
	got, err := scanLocal(ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"settings.json", "snippets/a.json", "snippets/sub/b.json"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("keys = %v, want %v", keys(got), want)
	}
	if e := got["snippets/a.json"]; e.Hash != hashBytes([]byte("a")) || e.Size != 1 || e.MTime == 0 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestScanReusesBaselineHash(t *testing.T) {
	_, ms := scanFixture(t)
	first, _ := scanLocal(ms, nil)
	cached := first["settings.json"]
	cached.Hash = "cached"
	got, _ := scanLocal(ms, map[string]model.FileEntry{"settings.json": cached})
	if got["settings.json"].Hash != "cached" {
		t.Fatal("hash not reused when size and mtime match")
	}
	cached.MTime++
	got, _ = scanLocal(ms, map[string]model.FileEntry{"settings.json": cached})
	if got["settings.json"].Hash == "cached" {
		t.Fatal("hash reused although mtime differs")
	}
}

func TestScanMissingAndMismatched(t *testing.T) {
	dir, ms := scanFixture(t)
	os.RemoveAll(filepath.Join(dir, "snippets"))
	os.Remove(filepath.Join(dir, "settings.json"))
	got, err := scanLocal(ms, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing paths: %v %v", got, err)
	}
	os.MkdirAll(filepath.Join(dir, "settings.json"), 0o755)
	if _, err := scanLocal(ms, nil); err == nil || !strings.Contains(err.Error(), "expects a file") {
		t.Fatalf("want type mismatch error, got %v", err)
	}
}

func TestScanFollowsFileSymlinks(t *testing.T) {
	dir, ms := scanFixture(t)
	target := filepath.Join(t.TempDir(), "real.json")
	writeFile(t, target, "real")
	if err := os.Symlink(target, filepath.Join(dir, "snippets", "link.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "snippets", "linkdir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := scanLocal(ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["snippets/link.json"].Hash != hashBytes([]byte("real")) {
		t.Fatal("file symlink not followed")
	}
	for k := range got {
		if strings.HasPrefix(k, "snippets/linkdir") {
			t.Fatal("descended into a symlinked directory")
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/engine/ -run Scan`
Expected: FAIL with `undefined: scanLocal`.

- [ ] **Step 3: Write the implementation**

Create `internal/engine/scan.go`:
```go
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/niceit/s3-anysync/internal/model"
)

// scanLocal lists every mapped, non-excluded local file keyed by remote path.
// A file whose size and mtime match its baseline entry reuses the baseline
// hash. Symlinks to files are followed; symlinked directories and special
// files are skipped.
func scanLocal(ms Mappings, baseline map[string]model.FileEntry) (map[string]model.FileEntry, error) {
	out := map[string]model.FileEntry{}
	for _, m := range ms {
		var err error
		if m.Type == model.ItemFile {
			err = scanFile(m, baseline, out)
		} else {
			err = scanDir(m, baseline, out)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanFile(m Mapping, baseline, out map[string]model.FileEntry) error {
	info, err := os.Stat(m.LocalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("item %s expects a file but %s is a directory", m.RemoteName, m.LocalPath)
	}
	if !info.Mode().IsRegular() || m.matcher.Match(path.Base(m.RemoteName)) {
		return nil
	}
	e, err := entryFor(m.RemoteName, m.LocalPath, info, baseline)
	if err != nil {
		return err
	}
	out[m.RemoteName] = e
	return nil
}

func scanDir(m Mapping, baseline, out map[string]model.FileEntry) error {
	info, err := os.Stat(m.LocalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("item %s expects a directory but %s is a file", m.RemoteName, m.LocalPath)
	}
	return filepath.WalkDir(m.LocalPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == m.LocalPath {
			return nil
		}
		rel, err := filepath.Rel(m.LocalPath, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if m.matcher.Match(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if m.matcher.Match(rel) {
			return nil
		}
		info, err := os.Stat(p) // follows symlinks
		if errors.Is(err, fs.ErrNotExist) {
			return nil // dangling symlink
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		key := m.RemoteName + "/" + rel
		e, err := entryFor(key, p, info, baseline)
		if err != nil {
			return err
		}
		out[key] = e
		return nil
	})
}

func entryFor(key, abs string, info fs.FileInfo, baseline map[string]model.FileEntry) (model.FileEntry, error) {
	e := model.FileEntry{Path: key, Size: info.Size(), MTime: info.ModTime().UnixNano()}
	if b, ok := baseline[key]; ok && b.Hash != "" && b.Size == e.Size && b.MTime == e.MTime {
		e.Hash = b.Hash
		return e, nil
	}
	h, err := hashFile(abs)
	if err != nil {
		return e, err
	}
	e.Hash = h
	return e, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/engine/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/engine`

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine
git add internal/engine
git commit -m "feat(engine): local scan with baseline hash cache" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Three-way planner

**Files:**
- Create: `internal/engine/plan.go`
- Test: `internal/engine/plan_test.go`

**Interfaces:**
- Consumes: `model.Direction` and `model.FileEntry`.
- Produces:
  - `OpKind` constants `OpUpload`, `OpDownload`, `OpDeleteRemote`, `OpDeleteLocal`, `OpAdopt`, `OpDrop`, `OpKeep`, `OpConflict`
  - `Op{Path string; Kind OpKind; Local, Remote *model.FileEntry}`
  - `PlanInput{Direction model.Direction; PropagateDeletes bool; Local, Remote, Baseline map[string]model.FileEntry}`
  - `Plan(PlanInput) []Op`: ops are sorted by path, and paths that should be left alone get no op

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/plan_test.go`:
```go
package engine

import (
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func side(hash string) map[string]model.FileEntry {
	if hash == "" {
		return map[string]model.FileEntry{}
	}
	return map[string]model.FileEntry{"f": {Path: "f", Hash: hash}}
}

func TestPlanTable(t *testing.T) {
	const (
		tw  = model.TwoWay
		l2c = model.LocalToCloud
		c2l = model.CloudToLocal
	)
	// l, r, b are hashes on the local side, the cloud side and the baseline; "" means absent.
	// want "" means no op at all (the path is left alone).
	tests := []struct {
		name    string
		dir     model.Direction
		prop    bool
		l, r, b string
		want    OpKind
	}{
		{"unchanged", tw, true, "a", "a", "a", OpKeep},
		{"local edit", tw, true, "b", "a", "a", OpUpload},
		{"cloud edit", tw, true, "a", "b", "a", OpDownload},
		{"both edited differently", tw, true, "b", "c", "a", OpConflict},
		{"both edited identically", tw, true, "b", "b", "a", OpAdopt},
		{"local delete propagates", tw, true, "", "a", "a", OpDeleteRemote},
		{"local delete not propagated", tw, false, "", "a", "a", OpDownload},
		{"cloud delete propagates", tw, true, "a", "", "a", OpDeleteLocal},
		{"cloud delete not propagated", tw, false, "a", "", "a", OpUpload},
		{"deleted on both sides", tw, true, "", "", "a", OpDrop},
		{"new local file", tw, true, "a", "", "", OpUpload},
		{"new cloud file", tw, true, "", "a", "", OpDownload},
		{"new on both, different", tw, true, "a", "b", "", OpConflict},
		{"new on both, same", tw, true, "a", "a", "", OpAdopt},
		{"local edit vs cloud delete", tw, true, "b", "", "a", OpConflict},

		{"l2c local edit", l2c, true, "b", "a", "a", OpUpload},
		{"l2c cloud edit overwritten", l2c, true, "a", "b", "a", OpUpload},
		{"l2c both edited", l2c, true, "b", "c", "a", OpUpload},
		{"l2c cloud delete restored", l2c, true, "a", "", "a", OpUpload},
		{"l2c local delete propagates", l2c, true, "", "a", "a", OpDeleteRemote},
		{"l2c local delete not propagated", l2c, false, "", "a", "a", OpDownload},
		{"l2c local delete vs cloud edit", l2c, true, "", "b", "a", OpDeleteRemote},
		{"l2c untracked cloud file left alone", l2c, true, "", "a", "", ""},
		{"l2c new on both", l2c, true, "a", "b", "", OpUpload},

		{"c2l local edit replaced", c2l, true, "b", "a", "a", OpDownload},
		{"c2l cloud edit", c2l, true, "a", "b", "a", OpDownload},
		{"c2l local delete restored", c2l, true, "", "a", "a", OpDownload},
		{"c2l cloud delete propagates", c2l, true, "a", "", "a", OpDeleteLocal},
		{"c2l cloud delete not propagated", c2l, false, "a", "", "a", OpUpload},
		{"c2l untracked local file left alone", c2l, true, "a", "", "", ""},
		{"c2l both edited", c2l, true, "b", "c", "a", OpDownload},
		{"c2l cloud delete vs local edit", c2l, true, "b", "", "a", OpDeleteLocal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := Plan(PlanInput{Direction: tt.dir, PropagateDeletes: tt.prop, Local: side(tt.l), Remote: side(tt.r), Baseline: side(tt.b)})
			if tt.want == "" {
				if len(ops) != 0 {
					t.Fatalf("want no op, got %+v", ops)
				}
				return
			}
			if len(ops) != 1 || ops[0].Kind != tt.want {
				t.Fatalf("want %s, got %+v", tt.want, ops)
			}
			op := ops[0]
			if (tt.l != "") != (op.Local != nil) || (tt.r != "") != (op.Remote != nil) {
				t.Fatalf("op sides do not match inputs: %+v", op)
			}
		})
	}
}

func TestPlanSortedByPath(t *testing.T) {
	local := map[string]model.FileEntry{"b": {Path: "b", Hash: "1"}, "a": {Path: "a", Hash: "1"}, "c/d": {Path: "c/d", Hash: "1"}}
	ops := Plan(PlanInput{Direction: model.TwoWay, Local: local, Remote: map[string]model.FileEntry{}, Baseline: map[string]model.FileEntry{}})
	if len(ops) != 3 || ops[0].Path != "a" || ops[1].Path != "b" || ops[2].Path != "c/d" {
		t.Fatalf("ops = %+v", ops)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/engine/ -run Plan`
Expected: FAIL with `undefined: Plan`.

- [ ] **Step 3: Write the implementation**

Create `internal/engine/plan.go`:
```go
package engine

import (
	"sort"

	"github.com/niceit/s3-anysync/internal/model"
)

// OpKind is what the executor does with one path.
type OpKind string

const (
	OpUpload       OpKind = "upload"
	OpDownload     OpKind = "download"
	OpDeleteRemote OpKind = "delete-remote"
	OpDeleteLocal  OpKind = "delete-local"
	OpAdopt        OpKind = "adopt"    // both sides already equal; record as baseline
	OpDrop         OpKind = "drop"     // gone on both sides; forget it
	OpKeep         OpKind = "keep"     // unchanged; refresh baseline from the scan
	OpConflict     OpKind = "conflict" // both sides changed differently (two-way only)
)

// Op is the planned action for one remote path. Local and Remote are nil
// when the file does not exist on that side.
type Op struct {
	Path   string
	Kind   OpKind
	Local  *model.FileEntry
	Remote *model.FileEntry
}

// PlanInput is the three-way comparison input. All maps are keyed by remote path.
type PlanInput struct {
	Direction        model.Direction
	PropagateDeletes bool
	Local            map[string]model.FileEntry
	Remote           map[string]model.FileEntry
	Baseline         map[string]model.FileEntry
}

// Plan compares local, remote and baseline and returns one op per path that
// needs handling, sorted by path. It is a pure function.
func Plan(in PlanInput) []Op {
	seen := map[string]bool{}
	for _, m := range []map[string]model.FileEntry{in.Local, in.Remote, in.Baseline} {
		for p := range m {
			seen[p] = true
		}
	}
	all := make([]string, 0, len(seen))
	for p := range seen {
		all = append(all, p)
	}
	sort.Strings(all)

	var ops []Op
	for _, p := range all {
		l, lok := in.Local[p]
		r, rok := in.Remote[p]
		b, bok := in.Baseline[p]
		lc := changed(lok, l, bok, b)
		rc := changed(rok, r, bok, b)
		kind, ok := decide(in.Direction, in.PropagateDeletes, lok, rok, lc, rc, lok && rok && l.Hash == r.Hash)
		if !ok {
			continue
		}
		op := Op{Path: p, Kind: kind}
		if lok {
			op.Local = &l
		}
		if rok {
			op.Remote = &r
		}
		ops = append(ops, op)
	}
	return ops
}

func changed(ok bool, e model.FileEntry, bok bool, b model.FileEntry) bool {
	if ok != bok {
		return true
	}
	return ok && e.Hash != b.Hash
}

// decide maps the change matrix to an op. ok=false means leave the path alone.
func decide(dir model.Direction, propagate, lok, rok, lc, rc, same bool) (OpKind, bool) {
	switch {
	case !lc && !rc:
		return OpKeep, true
	case lc && rc:
		if !lok && !rok {
			return OpDrop, true
		}
		if same {
			return OpAdopt, true
		}
		switch dir {
		case model.LocalToCloud:
			if lok {
				return OpUpload, true
			}
			if propagate {
				return OpDeleteRemote, true
			}
			return OpDownload, true
		case model.CloudToLocal:
			if rok {
				return OpDownload, true
			}
			if propagate {
				return OpDeleteLocal, true
			}
			return OpUpload, true
		default:
			return OpConflict, true
		}
	case lc: // only the local side changed
		if dir == model.CloudToLocal {
			if rok {
				return OpDownload, true
			}
			return "", false // untracked local-only file
		}
		if lok {
			return OpUpload, true
		}
		if propagate {
			return OpDeleteRemote, true
		}
		return OpDownload, true
	default: // only the cloud side changed
		if dir == model.LocalToCloud {
			if lok {
				return OpUpload, true
			}
			return "", false // untracked cloud-only file
		}
		if rok {
			return OpDownload, true
		}
		if propagate {
			return OpDeleteLocal, true
		}
		return OpUpload, true
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/engine/ -run Plan -v`
Expected: every `TestPlanTable/...` subtest PASSes.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine
git add internal/engine
git commit -m "feat(engine): pure three-way sync planner" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Atomic local writes and local backups

**Files:**
- Create: `internal/engine/localio.go`
- Test: `internal/engine/localio_test.go`

**Interfaces:**
- Consumes: `paths.TempFilePrefix` and `LocalBackupKeep` (Task 9).
- Produces, all unexported:
  - `writeFileAtomic(dst string, data []byte, mtime time.Time) (fs.FileInfo, error)`
  - `backupLocal(root, actionID, ts string, files map[string]string) error`, where `files` maps remote path to absolute local path. It prunes old backups to `LocalBackupKeep`.
  - `pruneBackups(dir string, keep int) error`
  - `removeLocal(abs string) error`

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/localio_test.go`:
```go
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "a", "b", "file.json")
	mtime := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	info, err := writeFileAtomic(dst, []byte("one"), mtime)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(mtime) {
		t.Fatalf("mtime = %v", info.ModTime())
	}
	if runtime.GOOS != "windows" {
		os.Chmod(dst, 0o600)
	}
	if _, err := writeFileAtomic(dst, []byte("two"), mtime); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "two" {
		t.Fatalf("content = %q", data)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(dst); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode not preserved: %v", st.Mode())
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".s3-anysync-tmp-") {
			t.Fatal("temp file left behind")
		}
	}
	if _, err := writeFileAtomic(dir, []byte("x"), mtime); err == nil {
		t.Fatal("overwrote a directory")
	}
}

func TestBackupLocalAndPrune(t *testing.T) {
	src := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(src, "s.json"), "keep me")
	files := map[string]string{"snippets/s.json": filepath.Join(src, "s.json"), "gone.json": filepath.Join(src, "missing")}
	if err := backupLocal(root, "act", "20261001T120000Z", files); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "act", "20261001T120000Z", "snippets", "s.json"))
	if err != nil || string(data) != "keep me" {
		t.Fatalf("backup = %q, %v", data, err)
	}

	for i := 0; i < LocalBackupKeep+2; i++ {
		os.MkdirAll(filepath.Join(root, "act", fmt.Sprintf("20270101T0000%02dZ", i)), 0o755)
	}
	if err := pruneBackups(filepath.Join(root, "act"), LocalBackupKeep); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "act"))
	if len(entries) != LocalBackupKeep {
		t.Fatalf("kept %d backups", len(entries))
	}
	if entries[0].Name() == "20261001T120000Z" {
		t.Fatal("oldest backup was not pruned")
	}
}

func TestRemoveLocal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	writeFile(t, p, "x")
	if err := removeLocal(p); err != nil {
		t.Fatal(err)
	}
	if err := removeLocal(p); err != nil {
		t.Fatalf("removing a missing file: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/engine/ -run 'Atomic|Backup|RemoveLocal'`
Expected: FAIL with `undefined: writeFileAtomic`.

- [ ] **Step 3: Write the implementation**

Create `internal/engine/localio.go`:
```go
package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/niceit/s3-anysync/internal/paths"
)

// writeFileAtomic writes data to a temp file next to dst, then renames it
// into place, keeping dst's permissions if it existed and setting mtime.
func writeFileAtomic(dst string, data []byte, mtime time.Time) (fs.FileInfo, error) {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(dst); err == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory", dst)
		}
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, paths.TempFilePrefix+"*")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	fail := func(err error) (fs.FileInfo, error) {
		tmp.Close()
		os.Remove(name)
		return nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return fail(err)
	}
	if err := os.Chtimes(name, mtime, mtime); err != nil {
		return fail(err)
	}
	if err := os.Rename(name, dst); err != nil {
		return fail(err)
	}
	return os.Stat(dst)
}

// backupLocal copies files (remote path → absolute local path) into
// root/actionID/ts/<remote path>, skipping files that no longer exist, then
// prunes old backups.
func backupLocal(root, actionID, ts string, files map[string]string) error {
	if root == "" {
		return errors.New("local backup folder is not configured")
	}
	for remotePath, abs := range files {
		data, err := os.ReadFile(abs)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		dst := filepath.Join(root, actionID, ts, filepath.FromSlash(remotePath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
	}
	return pruneBackups(filepath.Join(root, actionID), LocalBackupKeep)
}

// pruneBackups keeps the newest `keep` timestamped folders in dir.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.RemoveAll(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// removeLocal deletes a file; a missing file is not an error.
func removeLocal(abs string) error {
	err := os.Remove(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/engine/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/engine`

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine
git add internal/engine
git commit -m "feat(engine): atomic local writes and local backups" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Runner and snapshot primitives

**Files:**
- Create: `internal/engine/runner.go`, `internal/engine/snapshot.go`
- Test: `internal/engine/snapshot_test.go`

**Interfaces:**
- Consumes: `Deps`, `Layout`, `FetchAction`, `VerifyPassphrase`, `loadManifest` and `sortedEntries` (Task 9).
- Produces:
  - `Runner` with `NewRunner(d Deps, root string, a model.Action) *Runner` and `(*Runner).Action() model.Action`
  - Unexported methods `refreshAction(ctx) error` and `addHistory(kind string, started time.Time, outcome, message string)`
  - Snapshot reasons `ReasonManual = "manual"`, `ReasonAutoPreSync = "auto-pre-sync"`, `ReasonPreResolve = "pre-resolve"`, `ReasonPreRestore = "pre-restore"`
  - `(*Runner).ListSnapshots(ctx) ([]model.SnapshotInfo, error)`, newest first
  - Unexported `writeSnapshot(ctx, reason string, files []model.FileEntry) (model.SnapshotInfo, error)`; the caller must hold the lock
  - Unexported `snapshotInfo(ctx, ts) (model.SnapshotInfo, error)`, `applyRetention(ctx) error` and `const snapshotTSLayout = "20060102T150405Z"`

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/snapshot_test.go`:
```go
package engine

import (
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func snapshotRunner(t *testing.T, retention int) (*Runner, *storage.Memory, Layout) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Now = fixedClock()
	a := basicAction("snap")
	a.SnapshotRetention = retention
	l := NewLayout("root", a.ID)
	for _, p := range []string{"settings.json", "d/x.json"} {
		mem.Put(ctx, l.Current(p), []byte(p), map[string]string{metaSHA256: "h-" + p})
	}
	return NewRunner(d, "root", a), mem, l
}

func TestWriteSnapshot(t *testing.T) {
	r, mem, l := snapshotRunner(t, 20)
	files := []model.FileEntry{{Path: "settings.json", Hash: "h-settings.json"}, {Path: "d/x.json", Hash: "h-d/x.json"}}
	info, err := r.writeSnapshot(ctx, ReasonManual, files)
	if err != nil {
		t.Fatal(err)
	}
	if info.TS != "20261001T120000Z" || info.Reason != ReasonManual || info.MachineID != "A" || len(info.Files) != 2 || info.Files[0].Path != "d/x.json" {
		t.Fatalf("info = %+v", info)
	}
	body, obj, err := mem.Get(ctx, l.SnapshotFile(info.TS, "d/x.json"))
	if err != nil || string(body) != "d/x.json" || obj.Metadata[metaSHA256] != "h-d/x.json" {
		t.Fatalf("snapshot copy = %q %v %v", body, obj.Metadata, err)
	}

	// The same clock second is taken, so the next snapshot moves one second on.
	second, err := r.writeSnapshot(ctx, ReasonAutoPreSync, files[:1])
	if err != nil || second.TS != "20261001T120001Z" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	list, err := r.ListSnapshots(ctx)
	if err != nil || len(list) != 2 || list[0].TS != second.TS {
		t.Fatalf("ListSnapshots = %+v, %v", list, err)
	}
	got, err := r.snapshotInfo(ctx, info.TS)
	if err != nil || len(got.Files) != 2 {
		t.Fatalf("snapshotInfo = %+v, %v", got, err)
	}
	if _, err := r.snapshotInfo(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing snapshot: %v", err)
	}
}

func TestSnapshotRetention(t *testing.T) {
	r, mem, l := snapshotRunner(t, 2)
	files := []model.FileEntry{{Path: "settings.json"}}
	var all []string
	for i := 0; i < 3; i++ {
		info, err := r.writeSnapshot(ctx, ReasonManual, files)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, info.TS)
	}
	list, _ := r.ListSnapshots(ctx)
	if len(list) != 2 || list[0].TS != all[2] || list[1].TS != all[1] {
		t.Fatalf("kept = %+v", list)
	}
	for _, k := range mem.Keys() {
		if strings.HasPrefix(k, l.SnapshotsPrefix()+all[0]) {
			t.Fatalf("object of pruned snapshot left behind: %s", k)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/engine/ -run Snapshot`
Expected: FAIL with `undefined: NewRunner`.

- [ ] **Step 3: Write the implementation**

Create `internal/engine/runner.go`:
```go
package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

// Runner performs syncs, snapshots, resolution and restores for one action
// on this machine. Use one Runner per action; calls must not overlap.
type Runner struct {
	d      Deps
	root   string
	action model.Action
	layout Layout
}

// NewRunner returns a runner for action a stored under root.
func NewRunner(d Deps, root string, a model.Action) *Runner {
	return &Runner{d: d, root: root, action: a, layout: NewLayout(root, a.ID)}
}

// Action returns the runner's current copy of the action definition.
func (r *Runner) Action() model.Action { return r.action }

// refreshAction picks up rule changes made on other machines and checks the
// passphrase of encrypted actions.
func (r *Runner) refreshAction(ctx context.Context) error {
	remote, err := FetchAction(ctx, r.d.Storage, r.root, r.action.ID)
	if err != nil {
		return fmt.Errorf("read action definition: %w", err)
	}
	if remote.UpdatedAt.After(r.action.UpdatedAt) {
		remote.ProfileID = r.action.ProfileID
		if err := r.d.Repo.PutAction(remote); err != nil {
			return err
		}
		r.action = remote
	}
	return VerifyPassphrase(ctx, r.d, r.root, r.action)
}

func (r *Runner) addHistory(kind string, started time.Time, outcome, message string) {
	if r.d.Repo == nil {
		return
	}
	h := model.HistoryEntry{ActionID: r.action.ID, Kind: kind, StartedAt: started, FinishedAt: r.d.now(), Outcome: outcome, Message: message}
	if err := r.d.Repo.AddHistory(h); err != nil {
		r.d.log().Warn("record history", "action", r.action.ID, "err", err)
	}
}
```

Create `internal/engine/snapshot.go`:
```go
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

const snapshotTSLayout = "20060102T150405Z"

// Snapshot reasons recorded in the index.
const (
	ReasonManual      = "manual"
	ReasonAutoPreSync = "auto-pre-sync"
	ReasonPreResolve  = "pre-resolve"
	ReasonPreRestore  = "pre-restore"
)

// newSnapshotTS returns the current UTC second, moved forward one second at a
// time while a snapshot with that name already exists.
func (r *Runner) newSnapshotTS(ctx context.Context) (string, error) {
	t := r.d.now().UTC()
	for i := 0; i < 120; i++ {
		ts := t.Add(time.Duration(i) * time.Second).Format(snapshotTSLayout)
		_, err := r.d.Storage.Head(ctx, r.layout.SnapshotIndex(ts))
		if errors.Is(err, storage.ErrNotFound) {
			return ts, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not pick a free snapshot timestamp")
}

// writeSnapshot copies the given current/ files server-side into a new
// snapshot and writes its index. The caller must hold the action lock.
func (r *Runner) writeSnapshot(ctx context.Context, reason string, files []model.FileEntry) (model.SnapshotInfo, error) {
	ts, err := r.newSnapshotTS(ctx)
	if err != nil {
		return model.SnapshotInfo{}, err
	}
	files = append([]model.FileEntry(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		if _, err := r.d.Storage.Copy(ctx, r.layout.Current(f.Path), r.layout.SnapshotFile(ts, f.Path)); err != nil {
			return model.SnapshotInfo{}, fmt.Errorf("snapshot %s: %w", f.Path, err)
		}
	}
	info := model.SnapshotInfo{TS: ts, Reason: reason, MachineID: r.d.MachineID, CreatedAt: r.d.now().UTC(), Files: files}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return model.SnapshotInfo{}, err
	}
	if _, err := r.d.Storage.Put(ctx, r.layout.SnapshotIndex(ts), data, nil); err != nil {
		return model.SnapshotInfo{}, err
	}
	if err := r.applyRetention(ctx); err != nil {
		r.d.log().Warn("snapshot retention", "action", r.action.ID, "err", err)
	}
	return info, nil
}

// ListSnapshots returns the action's snapshots, newest first.
func (r *Runner) ListSnapshots(ctx context.Context) ([]model.SnapshotInfo, error) {
	prefix := r.layout.SnapshotsPrefix()
	objs, err := r.d.Storage.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []model.SnapshotInfo
	for _, o := range objs {
		rest := strings.TrimPrefix(o.Key, prefix)
		if strings.Contains(rest, "/") || !strings.HasSuffix(rest, ".json") {
			continue
		}
		info, err := r.snapshotInfo(ctx, strings.TrimSuffix(rest, ".json"))
		if err != nil {
			r.d.log().Warn("skip unreadable snapshot index", "key", o.Key, "err", err)
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out, nil
}

func (r *Runner) snapshotInfo(ctx context.Context, ts string) (model.SnapshotInfo, error) {
	data, _, err := r.d.Storage.Get(ctx, r.layout.SnapshotIndex(ts))
	if errors.Is(err, storage.ErrNotFound) {
		return model.SnapshotInfo{}, fmt.Errorf("snapshot %s not found", ts)
	}
	if err != nil {
		return model.SnapshotInfo{}, err
	}
	var info model.SnapshotInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return model.SnapshotInfo{}, fmt.Errorf("snapshot %s: %w", ts, err)
	}
	return info, nil
}

// applyRetention deletes the oldest snapshots beyond the action's retention.
func (r *Runner) applyRetention(ctx context.Context) error {
	snaps, err := r.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	keep := r.action.SnapshotRetention
	if keep < 1 || len(snaps) <= keep {
		return nil
	}
	for _, s := range snaps[keep:] {
		if err := r.deleteSnapshot(ctx, s.TS); err != nil {
			return err
		}
	}
	return nil
}

// deleteSnapshot removes the index first, so a half-deleted snapshot is never listed.
func (r *Runner) deleteSnapshot(ctx context.Context, ts string) error {
	if err := r.d.Storage.Delete(ctx, r.layout.SnapshotIndex(ts)); err != nil {
		return err
	}
	objs, err := r.d.Storage.List(ctx, r.layout.SnapshotsPrefix()+ts+"/")
	if err != nil {
		return err
	}
	for _, o := range objs {
		if err := r.d.Storage.Delete(ctx, o.Key); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/engine/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/engine`

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/engine
git add internal/engine
git commit -m "feat(engine): runner and snapshot write, list and retention" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Sync pipeline and multi-machine scenarios

**Files:**
- Create: `internal/engine/sync.go`
- Test: `internal/engine/harness_test.go`, `internal/engine/sync_scenarios_test.go`

**Interfaces:**
- Consumes everything from Tasks 9–14.
- Produces:
  - `(*Runner).Sync(ctx) (model.SyncResult, error)`
  - `(*Runner).Resolve(ctx, choices map[string]model.Side) (model.SyncResult, error)`
  - `(*Runner).Conflicts() ([]model.Conflict, error)`
  - Unexported `decode(path string, body []byte, wantHash string) ([]byte, error)`, which Task 16 reuses
  - The test harness: `storeFactory`, `scenarios`, `runScenarios`, `newHarness`, `(*harness).machine`, `create` and `join`, `syncedPair`, `testAction`, `defaultPaths`, `expectCounts`, and the machine helpers `write`, `read`, `exists`, `remove`, `sync`

- [ ] **Step 1: Write the test harness**

Create `internal/engine/harness_test.go`:
```go
package engine

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

// storeFactory returns a shared bucket and a root prefix for one scenario.
type storeFactory func(t *testing.T) (storage.Storage, string)

// scenarios run against every storage implementation (memory always, S3 when configured).
var scenarios = map[string]func(t *testing.T, f storeFactory){}

func memoryStore(*testing.T) (storage.Storage, string) { return storage.NewMemory(), "cfg" }

func TestScenariosMemory(t *testing.T) { runScenarios(t, memoryStore) }

func runScenarios(t *testing.T, f storeFactory) {
	names := make([]string, 0, len(scenarios))
	for n := range scenarios {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fn := scenarios[n]
		t.Run(n, func(t *testing.T) { fn(t, f) })
	}
}

type harness struct {
	t     *testing.T
	store storage.Storage
	root  string
}

func newHarness(t *testing.T, f storeFactory) *harness {
	s, root := f(t)
	return &harness{t: t, store: s, root: root}
}

// machine simulates one computer: its own home folder, state DB and backups.
type machine struct {
	t      *testing.T
	h      *harness
	id     string
	home   string
	db     *state.DB
	deps   Deps
	runner *Runner
}

func (h *harness) machine(id, passphrase string) *machine {
	t := h.t
	db := newDB(t)
	if err := db.PutProfile(model.Profile{ID: "p", Name: "test", Bucket: "b"}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	m := &machine{t: t, h: h, id: id, home: home, db: db}
	m.deps = Deps{
		Storage: h.store, Repo: db, MachineID: id, MachineName: "machine " + id, Passphrase: passphrase,
		BackupRoot: filepath.Join(home, "backups"), LockMode: storage.LockConditional,
		Expand: func(p string) (string, error) { return paths.ExpandWith(p, home, func(string) string { return "" }) },
	}
	return m
}

const (
	itemSettings = "settings"
	itemSnippets = "snippets"
)

var defaultPaths = map[string]string{itemSettings: "~/app/settings.json", itemSnippets: "~/app/snippets"}

func testAction(name string) model.Action {
	a := model.Action{ID: "act-" + name, Name: name, PropagateDeletes: true, Items: []model.Item{
		{ID: itemSettings, RemoteName: "settings.json", Type: model.ItemFile},
		{ID: itemSnippets, RemoteName: "snippets", Type: model.ItemDir},
	}}
	a.ApplyDefaults()
	return a
}

// create publishes a with every item mapped on m.
func (h *harness) create(m *machine, a model.Action) model.Action {
	h.t.Helper()
	for i := range a.Items {
		a.Items[i].LocalPaths[m.id] = defaultPaths[a.Items[i].ID]
	}
	a.Machines[m.id] = m.deps.MachineName
	a.UpdatedAt = time.Now().UTC()
	if err := CreateSharedAction(ctx, m.deps, h.root, a); err != nil {
		h.t.Fatal(err)
	}
	m.attach(a)
	return a
}

// join maps the given items (item id → local path) on m.
func (h *harness) join(m *machine, actionID string, items map[string]string) model.Action {
	h.t.Helper()
	a, err := UpdateSharedAction(ctx, m.deps, h.root, actionID, func(a *model.Action) error {
		a.Machines[m.id] = m.deps.MachineName
		for i := range a.Items {
			if p, ok := items[a.Items[i].ID]; ok {
				a.Items[i].LocalPaths[m.id] = p
			}
		}
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
	m.attach(a)
	return a
}

func (m *machine) attach(a model.Action) {
	a.ProfileID = "p"
	if err := m.db.PutAction(a); err != nil {
		m.t.Fatal(err)
	}
	m.runner = NewRunner(m.deps, m.h.root, a)
}

func (m *machine) path(rel string) string { return filepath.Join(m.home, filepath.FromSlash(rel)) }

func (m *machine) write(rel, content string) {
	m.t.Helper()
	writeFile(m.t, m.path(rel), content)
}

func (m *machine) read(rel string) string {
	m.t.Helper()
	data, err := os.ReadFile(m.path(rel))
	if err != nil {
		m.t.Fatal(err)
	}
	return string(data)
}

func (m *machine) exists(rel string) bool {
	_, err := os.Stat(m.path(rel))
	return err == nil
}

func (m *machine) remove(rel string) {
	m.t.Helper()
	if err := os.Remove(m.path(rel)); err != nil {
		m.t.Fatal(err)
	}
}

func (m *machine) sync() model.SyncResult {
	m.t.Helper()
	res, err := m.runner.Sync(ctx)
	if err != nil {
		m.t.Fatalf("sync on %s: %v", m.id, err)
	}
	return res
}

func expectCounts(t *testing.T, res model.SyncResult, up, down, delRemote, delLocal int) {
	t.Helper()
	if res.Uploaded != up || res.Downloaded != down || res.DeletedRemote != delRemote || res.DeletedLocal != delLocal {
		t.Fatalf("got %s; want uploaded %d, downloaded %d, deleted in cloud %d, deleted locally %d",
			res.Summary(), up, down, delRemote, delLocal)
	}
}

// syncedPair returns machines A and B that both hold settings.json ({"v":1})
// and snippets/go.json ("go") in sync.
func syncedPair(t *testing.T, f storeFactory, a model.Action) (*harness, *machine, *machine, model.Action) {
	t.Helper()
	h := newHarness(t, f)
	ma, mb := h.machine("A", ""), h.machine("B", "")
	ma.write("app/settings.json", `{"v":1}`)
	ma.write("app/snippets/go.json", "go")
	act := h.create(ma, a)
	expectCounts(t, ma.sync(), 2, 0, 0, 0)
	h.join(mb, act.ID, defaultPaths)
	expectCounts(t, mb.sync(), 0, 2, 0, 0)
	return h, ma, mb, act
}
```

- [ ] **Step 2: Write the failing scenarios**

Create `internal/engine/sync_scenarios_test.go`:
```go
package engine

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func init() {
	scenarios["two-way propagation"] = scenarioTwoWay
	scenarios["conflict pauses the whole action"] = scenarioConflict
	scenarios["delete propagates after a snapshot"] = scenarioDelete
	scenarios["delete not propagated when disabled"] = scenarioDeleteOff
	scenarios["first run adopts equal files and flags different ones"] = scenarioFirstRun
	scenarios["unmapped items are left alone"] = scenarioUnmapped
	scenarios["per-machine one-way directions"] = scenarioOneWay
	scenarios["encrypted action"] = scenarioEncrypted
	scenarios["lock held by another machine"] = scenarioLockHeld
}

func scenarioTwoWay(t *testing.T, f storeFactory) {
	_, a, b, _ := syncedPair(t, f, testAction("twoway"))
	if got := b.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("B settings = %q", got)
	}
	b.write("app/settings.json", `{"v":22}`)
	expectCounts(t, b.sync(), 1, 0, 0, 0)
	expectCounts(t, a.sync(), 0, 1, 0, 0)
	if got := a.read("app/settings.json"); got != `{"v":22}` {
		t.Fatalf("A settings = %q", got)
	}
	expectCounts(t, a.sync(), 0, 0, 0, 0)
	expectCounts(t, b.sync(), 0, 0, 0, 0)
}

func scenarioConflict(t *testing.T, f storeFactory) {
	h, a, b, act := syncedPair(t, f, testAction("conflict"))
	a.write("app/settings.json", `{"v":2}`)
	b.write("app/settings.json", `{"v":333}`)
	b.write("app/snippets/new.json", "new")
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	res := b.sync()
	if res.Outcome != model.OutcomeConflict || len(res.Conflicts) != 1 || res.Conflicts[0].Path != "settings.json" {
		t.Fatalf("want one conflict on settings.json, got %+v", res)
	}
	if _, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current("snippets/new.json")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("new snippet uploaded while the action is paused (err=%v)", err)
	}
	if saved, err := b.runner.Conflicts(); err != nil || len(saved) != 1 {
		t.Fatalf("saved conflicts = %+v, %v", saved, err)
	}
	if got := b.read("app/settings.json"); got != `{"v":333}` {
		t.Fatalf("local file changed during a conflict: %q", got)
	}
}

func scenarioDelete(t *testing.T, f storeFactory) {
	_, a, b, act := syncedPair(t, f, testAction("delete"))
	a.remove("app/snippets/go.json")
	expectCounts(t, a.sync(), 0, 0, 1, 0)

	snaps, err := a.runner.ListSnapshots(ctx)
	if err != nil || len(snaps) != 1 || snaps[0].Reason != ReasonAutoPreSync || len(snaps[0].Files) != 1 || snaps[0].Files[0].Path != "snippets/go.json" {
		t.Fatalf("safety snapshot = %+v, %v", snaps, err)
	}

	expectCounts(t, b.sync(), 0, 0, 0, 1)
	if b.exists("app/snippets/go.json") {
		t.Fatal("B still has the deleted file")
	}
	backups, _ := filepath.Glob(filepath.Join(b.deps.BackupRoot, act.ID, "*", "snippets", "go.json"))
	if len(backups) != 1 {
		t.Fatalf("local backup missing: %v", backups)
	}
}

func scenarioDeleteOff(t *testing.T, f storeFactory) {
	act := testAction("nodelete")
	act.PropagateDeletes = false
	_, a, _, _ := syncedPair(t, f, act)
	a.remove("app/snippets/go.json")
	expectCounts(t, a.sync(), 0, 1, 0, 0)
	if got := a.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("file not restored: %q", got)
	}
}

func scenarioFirstRun(t *testing.T, f storeFactory) {
	h := newHarness(t, f)
	a, b, c := h.machine("A", ""), h.machine("B", ""), h.machine("C", "")
	a.write("app/settings.json", `{"v":1}`)
	act := h.create(a, testAction("firstrun"))
	a.sync()

	b.write("app/settings.json", `{"v":1}`)
	h.join(b, act.ID, defaultPaths)
	res := b.sync()
	expectCounts(t, res, 0, 0, 0, 0)
	if res.Adopted != 1 {
		t.Fatalf("want 1 adopted, got %s", res.Summary())
	}

	c.write("app/settings.json", `{"v":"other"}`)
	h.join(c, act.ID, defaultPaths)
	if res := c.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("want conflict on first run with different content, got %+v", res)
	}
}

func scenarioUnmapped(t *testing.T, f storeFactory) {
	h := newHarness(t, f)
	a, b := h.machine("A", ""), h.machine("B", "")
	a.write("app/settings.json", `{"v":1}`)
	a.write("app/snippets/go.json", "go")
	act := h.create(a, testAction("unmapped"))
	a.sync()

	h.join(b, act.ID, map[string]string{itemSettings: defaultPaths[itemSettings]})
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if b.exists("app/snippets/go.json") {
		t.Fatal("unmapped item was downloaded")
	}
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	expectCounts(t, a.sync(), 0, 0, 0, 0)
	if _, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current("snippets/go.json")); err != nil {
		t.Fatalf("cloud copy of the unmapped item disappeared: %v", err)
	}
}

func scenarioOneWay(t *testing.T, f storeFactory) {
	h := newHarness(t, f)
	a, b := h.machine("A", ""), h.machine("B", "")
	act := testAction("oneway")
	act.MachineDirections = map[string]model.Direction{"A": model.LocalToCloud, "B": model.CloudToLocal}
	a.write("app/settings.json", `{"v":1}`)
	act = h.create(a, act)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	h.join(b, act.ID, defaultPaths)
	expectCounts(t, b.sync(), 0, 1, 0, 0)

	// The receiver's local edit is replaced by the cloud copy.
	b.write("app/settings.json", `{"v":"local edit"}`)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("receiver kept its edit: %q", got)
	}

	// Files the receiver never synced are neither uploaded nor deleted.
	b.write("app/snippets/mine.json", "mine")
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	if !b.exists("app/snippets/mine.json") {
		t.Fatal("receiver's untracked file was deleted")
	}

	// The source keeps uploading and the receiver follows.
	a.write("app/settings.json", `{"v":2}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":2}` {
		t.Fatalf("receiver = %q", got)
	}
}

func scenarioEncrypted(t *testing.T, f storeFactory) {
	h := newHarness(t, f)
	a := h.machine("A", "correct horse")
	act := testAction("encrypted")
	act.Encrypt = true
	a.write("app/settings.json", `{"secret":"value"}`)
	act = h.create(a, act)
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	body, _, err := h.store.Get(ctx, NewLayout(h.root, act.ID).Current("settings.json"))
	if err != nil || bytes.Contains(body, []byte("value")) {
		t.Fatalf("cloud object is not encrypted (err=%v)", err)
	}

	wrong := h.machine("W", "wrong")
	h.join(wrong, act.ID, defaultPaths)
	if _, err := wrong.runner.Sync(ctx); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("want ErrDecrypt, got %v", err)
	}
	if wrong.exists("app/settings.json") {
		t.Fatal("file written despite a wrong passphrase")
	}

	b := h.machine("B", "correct horse")
	h.join(b, act.ID, defaultPaths)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"secret":"value"}` {
		t.Fatalf("decrypted = %q", got)
	}
}

func scenarioLockHeld(t *testing.T, f storeFactory) {
	h, a, _, act := syncedPair(t, f, testAction("locked"))
	lock := fmt.Sprintf(`{"machineId":"Z","expiresAt":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	if _, err := h.store.Put(ctx, NewLayout(h.root, act.ID).Lock(), []byte(lock), nil); err != nil {
		t.Fatal(err)
	}
	a.write("app/settings.json", `{"v":"changed"}`)
	if _, err := a.runner.Sync(ctx); !errors.Is(err, storage.ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
}
```

- [ ] **Step 3: Run the scenarios to verify they fail**

Run: `go test ./internal/engine/ -run Scenarios`
Expected: FAIL to compile with `m.runner.Sync undefined`.

- [ ] **Step 4: Write the implementation**

Create `internal/engine/sync.go`:
```go
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/niceit/s3-anysync/internal/crypto"
	"github.com/niceit/s3-anysync/internal/model"
)

// Sync runs one sync of the action. A conflict is not an error: the result's
// Outcome is OutcomeConflict, nothing was changed, and the conflicts are saved.
func (r *Runner) Sync(ctx context.Context) (model.SyncResult, error) {
	return r.run(ctx, model.HistorySync, nil)
}

// Resolve settles conflicts (path → winning side) and runs the rest of the
// sync. The losing side is snapshotted or backed up first.
func (r *Runner) Resolve(ctx context.Context, choices map[string]model.Side) (model.SyncResult, error) {
	if len(choices) == 0 {
		return model.SyncResult{}, errors.New("no conflict choices given")
	}
	for p, side := range choices {
		if side != model.SideLocal && side != model.SideCloud {
			return model.SyncResult{}, fmt.Errorf("%s: unknown side %q", p, side)
		}
	}
	return r.run(ctx, model.HistoryResolve, choices)
}

// Conflicts returns the conflicts saved by the last run.
func (r *Runner) Conflicts() ([]model.Conflict, error) {
	return r.d.Repo.LoadConflicts(r.action.ID)
}

func (r *Runner) run(ctx context.Context, kind string, overrides map[string]model.Side) (res model.SyncResult, err error) {
	started := r.d.now()
	defer func() {
		outcome, msg := res.Outcome, res.Summary()
		if err != nil {
			outcome, msg = model.OutcomeError, err.Error()
		}
		r.addHistory(kind, started, outcome, msg)
	}()
	if err = r.refreshAction(ctx); err != nil {
		return res, err
	}
	ms, err := BuildMappings(r.action, r.d.MachineID, r.d.expand)
	if err != nil {
		return res, err
	}
	for attempt := 0; attempt < maxSyncAttempts; attempt++ {
		var retry bool
		res, retry, err = r.syncOnce(ctx, ms, overrides)
		if !retry {
			return res, err
		}
		r.d.log().Info("cloud changed during sync, planning again", "action", r.action.ID, "attempt", attempt+1)
	}
	return res, errors.New("the cloud copy kept changing during sync; try again later")
}

// syncOnce scans, plans and applies. retry=true means another machine
// changed the manifest between planning and locking.
func (r *Runner) syncOnce(ctx context.Context, ms Mappings, overrides map[string]model.Side) (model.SyncResult, bool, error) {
	id := r.action.ID
	baseline, err := r.d.Repo.LoadBaseline(id)
	if err != nil {
		return model.SyncResult{}, false, fmt.Errorf("load baseline: %w", err)
	}
	baseline = filterOwned(baseline, ms)
	local, err := scanLocal(ms, baseline)
	if err != nil {
		return model.SyncResult{}, false, fmt.Errorf("scan local files: %w", err)
	}
	local = filterOwned(local, ms)
	manifest, etag, err := loadManifest(ctx, r.d.Storage, r.layout)
	if err != nil {
		return model.SyncResult{}, false, err
	}
	remote := filterOwned(manifest.Files, ms)

	ops := Plan(PlanInput{
		Direction: r.action.DirectionFor(r.d.MachineID), PropagateDeletes: r.action.PropagateDeletes,
		Local: local, Remote: remote, Baseline: baseline,
	})
	ops, conflicts := applyOverrides(ops, overrides, r.d.now().UTC())
	if len(conflicts) > 0 {
		if err := r.d.Repo.SaveConflicts(id, conflicts); err != nil {
			return model.SyncResult{}, false, fmt.Errorf("save conflicts: %w", err)
		}
		return model.SyncResult{Outcome: model.OutcomeConflict, Conflicts: conflicts}, false, nil
	}

	if hasWrites(ops) {
		lk := r.d.locker(r.layout)
		if err := lk.Acquire(ctx); err != nil {
			return model.SyncResult{}, false, err
		}
		defer func() {
			if err := lk.Release(context.WithoutCancel(ctx)); err != nil {
				r.d.log().Warn("release lock", "action", id, "err", err)
			}
		}()
		current, err := manifestETag(ctx, r.d.Storage, r.layout)
		if err != nil {
			return model.SyncResult{}, false, err
		}
		if current != etag {
			return model.SyncResult{}, true, nil
		}
		reason := ReasonAutoPreSync
		if overrides != nil {
			reason = ReasonPreResolve
		}
		if err := r.guard(ctx, ms, ops, reason); err != nil {
			return model.SyncResult{}, false, err
		}
	}

	res, err := r.apply(ctx, ms, ops, baseline, &manifest)
	if err != nil {
		return res, false, err
	}
	if err := r.d.Repo.SaveConflicts(id, nil); err != nil {
		return res, false, fmt.Errorf("clear conflicts: %w", err)
	}
	res.Outcome = model.OutcomeOK
	return res, false, nil
}

// applyOverrides turns chosen conflicts into uploads/downloads/deletes and
// returns the conflicts that remain.
func applyOverrides(ops []Op, overrides map[string]model.Side, now time.Time) ([]Op, []model.Conflict) {
	var conflicts []model.Conflict
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		if op.Kind == OpConflict {
			switch overrides[op.Path] {
			case model.SideLocal:
				op.Kind = OpUpload
				if op.Local == nil {
					op.Kind = OpDeleteRemote
				}
			case model.SideCloud:
				op.Kind = OpDownload
				if op.Remote == nil {
					op.Kind = OpDeleteLocal
				}
			default:
				conflicts = append(conflicts, model.Conflict{Path: op.Path, Local: op.Local, Remote: op.Remote, DetectedAt: now})
				continue
			}
		}
		out = append(out, op)
	}
	return out, conflicts
}

func hasWrites(ops []Op) bool {
	for _, op := range ops {
		switch op.Kind {
		case OpUpload, OpDownload, OpDeleteRemote, OpDeleteLocal:
			return true
		}
	}
	return false
}

// guard snapshots cloud files about to be overwritten or deleted, and backs
// up local files about to be overwritten or deleted.
func (r *Runner) guard(ctx context.Context, ms Mappings, ops []Op, reason string) error {
	var remoteVictims []model.FileEntry
	localVictims := map[string]string{}
	for _, op := range ops {
		switch op.Kind {
		case OpUpload, OpDeleteRemote:
			if op.Remote != nil {
				remoteVictims = append(remoteVictims, *op.Remote)
			}
		case OpDownload, OpDeleteLocal:
			if op.Local != nil {
				if abs, ok := ms.LocalPathFor(op.Path); ok {
					localVictims[op.Path] = abs
				}
			}
		}
	}
	if len(remoteVictims) > 0 {
		if _, err := r.writeSnapshot(ctx, reason, remoteVictims); err != nil {
			return fmt.Errorf("safety snapshot: %w", err)
		}
	}
	if len(localVictims) > 0 {
		ts := r.d.now().UTC().Format(snapshotTSLayout)
		if err := backupLocal(r.d.BackupRoot, r.action.ID, ts, localVictims); err != nil {
			return fmt.Errorf("local backup: %w", err)
		}
	}
	return nil
}

// apply executes ops in order, then writes the manifest (if the cloud
// changed) and the new baseline. On a failed op it stops, but still records
// what was done.
func (r *Runner) apply(ctx context.Context, ms Mappings, ops []Op, baseline map[string]model.FileEntry, manifest *model.Manifest) (model.SyncResult, error) {
	var res model.SyncResult
	next := make(map[string]model.FileEntry, len(baseline))
	for p, e := range baseline {
		next[p] = e
	}
	remoteDirty := false
	var opErr error
loop:
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			opErr = err
			break
		}
		switch op.Kind {
		case OpKeep:
			next[op.Path] = *op.Local
		case OpAdopt:
			next[op.Path] = *op.Local
			res.Adopted++
		case OpDrop:
			delete(next, op.Path)
		case OpUpload:
			e, err := r.upload(ctx, ms, *op.Local)
			if err != nil {
				opErr = err
				break loop
			}
			manifest.Files[op.Path] = e
			next[op.Path] = e
			remoteDirty = true
			res.Uploaded++
		case OpDownload:
			e, err := r.download(ctx, ms, *op.Remote)
			if err != nil {
				opErr = err
				break loop
			}
			next[op.Path] = e
			res.Downloaded++
		case OpDeleteRemote:
			if err := r.d.Storage.Delete(ctx, r.layout.Current(op.Path)); err != nil {
				opErr = fmt.Errorf("delete %s in the cloud: %w", op.Path, err)
				break loop
			}
			delete(manifest.Files, op.Path)
			delete(next, op.Path)
			remoteDirty = true
			res.DeletedRemote++
		case OpDeleteLocal:
			if abs, ok := ms.LocalPathFor(op.Path); ok {
				if err := removeLocal(abs); err != nil {
					opErr = fmt.Errorf("delete %s: %w", abs, err)
					break loop
				}
			}
			delete(next, op.Path)
			res.DeletedLocal++
		}
	}
	if remoteDirty {
		manifest.UpdatedAt = r.d.now().UTC()
		manifest.UpdatedBy = r.d.MachineID
		if err := saveManifest(context.WithoutCancel(ctx), r.d.Storage, r.layout, *manifest); err != nil {
			// Without the new manifest the new baseline would misread the
			// cloud next time, so keep the old baseline.
			return res, errors.Join(opErr, fmt.Errorf("write manifest: %w", err))
		}
	}
	if err := r.d.Repo.SaveBaseline(r.action.ID, next); err != nil {
		return res, errors.Join(opErr, fmt.Errorf("save baseline: %w", err))
	}
	return res, opErr
}

func (r *Runner) upload(ctx context.Context, ms Mappings, e model.FileEntry) (model.FileEntry, error) {
	abs, ok := ms.LocalPathFor(e.Path)
	if !ok {
		return e, fmt.Errorf("upload %s: no local path on this machine", e.Path)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return e, fmt.Errorf("upload %s: %w", e.Path, err)
	}
	// The file may have changed since the scan; upload what is on disk now.
	e.Hash, e.Size = hashBytes(data), int64(len(data))
	body := data
	if r.action.Encrypt {
		if body, err = crypto.Encrypt(data, r.d.Passphrase); err != nil {
			return e, fmt.Errorf("encrypt %s: %w", e.Path, err)
		}
	}
	meta := map[string]string{
		metaSHA256:  e.Hash,
		metaSize:    strconv.FormatInt(e.Size, 10),
		metaMTime:   strconv.FormatInt(e.MTime, 10),
		metaMachine: r.d.MachineID,
	}
	obj, err := r.d.Storage.Put(ctx, r.layout.Current(e.Path), body, meta)
	if err != nil {
		return e, fmt.Errorf("upload %s: %w", e.Path, err)
	}
	e.ETag, e.Writer = obj.ETag, r.d.MachineID
	return e, nil
}

func (r *Runner) download(ctx context.Context, ms Mappings, e model.FileEntry) (model.FileEntry, error) {
	abs, ok := ms.LocalPathFor(e.Path)
	if !ok {
		return e, fmt.Errorf("download %s: no local path on this machine", e.Path)
	}
	body, _, err := r.d.Storage.Get(ctx, r.layout.Current(e.Path))
	if err != nil {
		return e, fmt.Errorf("download %s: %w", e.Path, err)
	}
	data, err := r.decode(e.Path, body, e.Hash)
	if err != nil {
		return e, err
	}
	info, err := writeFileAtomic(abs, data, time.Unix(0, e.MTime))
	if err != nil {
		return e, fmt.Errorf("write %s: %w", abs, err)
	}
	return model.FileEntry{Path: e.Path, Hash: e.Hash, Size: info.Size(), MTime: info.ModTime().UnixNano()}, nil
}

// decode decrypts (for encrypted actions) and verifies the plaintext hash.
func (r *Runner) decode(p string, body []byte, wantHash string) ([]byte, error) {
	data := body
	if r.action.Encrypt {
		var err error
		if data, err = crypto.Decrypt(body, r.d.Passphrase); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrDecrypt, p, err)
		}
	}
	if got := hashBytes(data); got != wantHash {
		return nil, fmt.Errorf("%s: cloud content does not match its recorded hash", p)
	}
	return data, nil
}
```

- [ ] **Step 5: Run the scenarios to verify they pass**

Run: `go test ./internal/engine/ -run Scenarios -v`
Expected: all nine `TestScenariosMemory/...` subtests PASS.

Run: `go test ./internal/engine/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/engine`

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/engine
git add internal/engine
git commit -m "feat(engine): sync pipeline with safety snapshots and multi-machine scenarios" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: Snapshot now, conflict resolution and restore

**Files:**
- Modify: `internal/engine/snapshot.go` (add the `Snapshot` method)
- Create: `internal/engine/restore.go`
- Test: `internal/engine/resolve_restore_scenarios_test.go`

**Interfaces:**
- Consumes:
  - `Runner.Sync`, `Runner.Resolve`, `decode` and `guard` behaviour (Task 15)
  - `writeSnapshot` and `snapshotInfo` (Task 14)
  - `backupLocal` and `writeFileAtomic` (Task 13)
- Produces:
  - `(*Runner).Snapshot(ctx) (model.SnapshotInfo, error)`
  - `RestoreTarget` (`RestoreLocal`, `RestoreCloud`, `RestoreBoth`) and `RestoreResult{Restored, SkippedUnmapped []string}`
  - `(*Runner).Restore(ctx, ts string, only []string, target RestoreTarget) (RestoreResult, error)`

- [ ] **Step 1: Write the failing scenarios**

Create `internal/engine/resolve_restore_scenarios_test.go`:
```go
package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func init() {
	scenarios["resolve keeping the local copy"] = scenarioResolveLocal
	scenarios["resolve keeping the cloud copy"] = scenarioResolveCloud
	scenarios["snapshot now and restore"] = scenarioSnapshotRestore
	scenarios["snapshot retention"] = scenarioRetention
}

// conflicted returns A and B where both edited settings.json, A synced
// first and B is paused on the conflict.
func conflicted(t *testing.T, f storeFactory, name string) (*machine, *machine, model.Action) {
	t.Helper()
	_, a, b, act := syncedPair(t, f, testAction(name))
	a.write("app/settings.json", `{"v":"from A"}`)
	b.write("app/settings.json", `{"v":"from B!"}`)
	a.sync()
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("setup: want conflict, got %+v", res)
	}
	return a, b, act
}

func scenarioResolveLocal(t *testing.T, f storeFactory) {
	a, b, _ := conflicted(t, f, "resolvelocal")
	res, err := b.runner.Resolve(ctx, map[string]model.Side{"settings.json": model.SideLocal})
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 1, 0, 0, 0)
	if cs, _ := b.runner.Conflicts(); len(cs) != 0 {
		t.Fatalf("conflicts not cleared: %+v", cs)
	}
	snaps, _ := b.runner.ListSnapshots(ctx)
	if len(snaps) == 0 || snaps[0].Reason != ReasonPreResolve {
		t.Fatalf("cloud copy not snapshotted before being overwritten: %+v", snaps)
	}
	expectCounts(t, a.sync(), 0, 1, 0, 0)
	if got := a.read("app/settings.json"); got != `{"v":"from B!"}` {
		t.Fatalf("A = %q", got)
	}
}

func scenarioResolveCloud(t *testing.T, f storeFactory) {
	_, b, act := conflicted(t, f, "resolvecloud")
	res, err := b.runner.Resolve(ctx, map[string]model.Side{"settings.json": model.SideCloud})
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":"from A"}` {
		t.Fatalf("B = %q", got)
	}
	backups, _ := filepath.Glob(filepath.Join(b.deps.BackupRoot, act.ID, "*", "settings.json"))
	if len(backups) != 1 {
		t.Fatalf("local copy not backed up: %v", backups)
	}
	if data, _ := os.ReadFile(backups[0]); string(data) != `{"v":"from B!"}` {
		t.Fatalf("backup = %q", data)
	}
	if _, err := b.runner.Resolve(ctx, nil); err == nil {
		t.Fatal("Resolve with no choices accepted")
	}
}

func scenarioSnapshotRestore(t *testing.T, f storeFactory) {
	_, a, b, _ := syncedPair(t, f, testAction("restore"))
	info, err := a.runner.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Reason != ReasonManual || len(info.Files) != 2 {
		t.Fatalf("snapshot = %+v", info)
	}
	a.write("app/settings.json", `{"v":2222}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	// Restoring only the local copy: the next two-way sync uploads it again.
	rr, err := a.runner.Restore(ctx, info.TS, []string{"settings.json"}, RestoreLocal)
	if err != nil || len(rr.Restored) != 1 {
		t.Fatalf("restore local = %+v, %v", rr, err)
	}
	if got := a.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("A after restore = %q", got)
	}
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	// Restoring the cloud copy of everything: B downloads what changed.
	a.write("app/snippets/go.json", "go v2")
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if _, err := a.runner.Restore(ctx, info.TS, nil, RestoreCloud); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("B snippet = %q", got)
	}

	if _, err := a.runner.Restore(ctx, info.TS, []string{"nope.json"}, RestoreLocal); err == nil {
		t.Fatal("restore of a file not in the snapshot accepted")
	}
	if _, err := a.runner.Restore(ctx, info.TS, nil, "sideways"); err == nil {
		t.Fatal("unknown restore target accepted")
	}
}

func scenarioRetention(t *testing.T, f storeFactory) {
	act := testAction("retention")
	act.SnapshotRetention = 2
	_, a, _, _ := syncedPair(t, f, act)
	var last model.SnapshotInfo
	for i := 0; i < 3; i++ {
		info, err := a.runner.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		last = info
	}
	snaps, err := a.runner.ListSnapshots(ctx)
	if err != nil || len(snaps) != 2 || snaps[0].TS != last.TS {
		t.Fatalf("snapshots = %+v, %v", snaps, err)
	}
}
```

- [ ] **Step 2: Run the scenarios to verify they fail**

Run: `go test ./internal/engine/ -run Scenarios`
Expected: FAIL to compile with `a.runner.Snapshot undefined` and `undefined: RestoreLocal`.

- [ ] **Step 3: Add `Snapshot` to `internal/engine/snapshot.go`**

Append to `internal/engine/snapshot.go`:
```go
// Snapshot takes a manual snapshot of everything in current/. Unless this
// machine is cloud-to-local, it syncs first so local changes are included.
func (r *Runner) Snapshot(ctx context.Context) (info model.SnapshotInfo, err error) {
	started := r.d.now()
	defer func() {
		outcome, msg := model.OutcomeOK, fmt.Sprintf("snapshot %s with %d file(s)", info.TS, len(info.Files))
		if err != nil {
			outcome, msg = model.OutcomeError, err.Error()
		}
		r.addHistory(model.HistorySnapshot, started, outcome, msg)
	}()
	if r.action.DirectionFor(r.d.MachineID) != model.CloudToLocal {
		res, err := r.Sync(ctx)
		if err != nil {
			return info, err
		}
		if res.Outcome == model.OutcomeConflict {
			return info, ErrConflictPending
		}
	} else if err := r.refreshAction(ctx); err != nil {
		return info, err
	}
	lk := r.d.locker(r.layout)
	if err := lk.Acquire(ctx); err != nil {
		return info, err
	}
	defer func() {
		if err := lk.Release(context.WithoutCancel(ctx)); err != nil {
			r.d.log().Warn("release lock", "action", r.action.ID, "err", err)
		}
	}()
	m, _, err := loadManifest(ctx, r.d.Storage, r.layout)
	if err != nil {
		return info, err
	}
	return r.writeSnapshot(ctx, ReasonManual, sortedEntries(m.Files))
}
```

- [ ] **Step 4: Write `internal/engine/restore.go`**

```go
package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

// RestoreTarget says which side a restore writes to.
type RestoreTarget string

const (
	RestoreLocal RestoreTarget = "local"
	RestoreCloud RestoreTarget = "cloud"
	RestoreBoth  RestoreTarget = "both"
)

// RestoreResult lists restored paths and paths skipped because they have no
// local path on this machine.
type RestoreResult struct {
	Restored        []string `json:"restored"`
	SkippedUnmapped []string `json:"skippedUnmapped"`
}

// Restore copies files from snapshot ts back to the cloud, the local machine
// or both. only limits the files (nil means all). The current versions are
// snapshotted (cloud) or backed up (local) first. Restore never deletes files.
func (r *Runner) Restore(ctx context.Context, ts string, only []string, target RestoreTarget) (res RestoreResult, err error) {
	started := r.d.now()
	defer func() {
		outcome, msg := model.OutcomeOK, fmt.Sprintf("restored %d file(s) from %s to %s", len(res.Restored), ts, target)
		if err != nil {
			outcome, msg = model.OutcomeError, err.Error()
		}
		r.addHistory(model.HistoryRestore, started, outcome, msg)
	}()
	if target != RestoreLocal && target != RestoreCloud && target != RestoreBoth {
		return res, fmt.Errorf("unknown restore target %q", target)
	}
	if err = r.refreshAction(ctx); err != nil {
		return res, err
	}
	info, err := r.snapshotInfo(ctx, ts)
	if err != nil {
		return res, err
	}
	files, err := selectFiles(info.Files, only)
	if err != nil {
		return res, err
	}
	ms, err := BuildMappings(r.action, r.d.MachineID, r.d.expand)
	if err != nil {
		return res, err
	}

	lk := r.d.locker(r.layout)
	if err = lk.Acquire(ctx); err != nil {
		return res, err
	}
	defer func() {
		if err := lk.Release(context.WithoutCancel(ctx)); err != nil {
			r.d.log().Warn("release lock", "action", r.action.ID, "err", err)
		}
	}()

	restored := map[string]bool{}
	if target != RestoreLocal {
		if err = r.restoreCloud(ctx, ts, files); err != nil {
			return res, err
		}
		for _, f := range files {
			restored[f.Path] = true
		}
	}
	if target != RestoreCloud {
		done, skipped, err := r.restoreLocal(ctx, ts, files, ms)
		res.SkippedUnmapped = skipped
		if err != nil {
			return res, err
		}
		for _, p := range done {
			restored[p] = true
		}
	}
	for _, f := range files {
		if restored[f.Path] {
			res.Restored = append(res.Restored, f.Path)
		}
	}
	return res, nil
}

func selectFiles(all []model.FileEntry, only []string) ([]model.FileEntry, error) {
	if len(only) == 0 {
		return all, nil
	}
	byPath := map[string]model.FileEntry{}
	for _, f := range all {
		byPath[f.Path] = f
	}
	var out []model.FileEntry
	for _, p := range only {
		f, ok := byPath[strings.TrimSpace(p)]
		if !ok {
			return nil, fmt.Errorf("the snapshot has no file %q", p)
		}
		out = append(out, f)
	}
	return out, nil
}

func (r *Runner) restoreCloud(ctx context.Context, ts string, files []model.FileEntry) error {
	m, _, err := loadManifest(ctx, r.d.Storage, r.layout)
	if err != nil {
		return err
	}
	var victims []model.FileEntry
	for _, f := range files {
		if cur, ok := m.Files[f.Path]; ok {
			victims = append(victims, cur)
		}
	}
	if len(victims) > 0 {
		if _, err := r.writeSnapshot(ctx, ReasonPreRestore, victims); err != nil {
			return fmt.Errorf("safety snapshot: %w", err)
		}
	}
	for _, f := range files {
		obj, err := r.d.Storage.Copy(ctx, r.layout.SnapshotFile(ts, f.Path), r.layout.Current(f.Path))
		if err != nil {
			return fmt.Errorf("restore %s to the cloud: %w", f.Path, err)
		}
		e := f
		e.ETag, e.Writer = obj.ETag, r.d.MachineID
		m.Files[f.Path] = e
	}
	m.UpdatedAt, m.UpdatedBy = r.d.now().UTC(), r.d.MachineID
	return saveManifest(context.WithoutCancel(ctx), r.d.Storage, r.layout, m)
}

func (r *Runner) restoreLocal(ctx context.Context, ts string, files []model.FileEntry, ms Mappings) (done, skipped []string, err error) {
	targets := map[string]string{}
	victims := map[string]string{}
	for _, f := range files {
		abs, ok := ms.LocalPathFor(f.Path)
		if !ok || !ms.Owns(f.Path) {
			skipped = append(skipped, f.Path)
			continue
		}
		targets[f.Path] = abs
		if _, err := os.Stat(abs); err == nil {
			victims[f.Path] = abs
		}
	}
	if len(victims) > 0 {
		if err := backupLocal(r.d.BackupRoot, r.action.ID, r.d.now().UTC().Format(snapshotTSLayout), victims); err != nil {
			return nil, skipped, fmt.Errorf("local backup: %w", err)
		}
	}
	for _, f := range files {
		abs, ok := targets[f.Path]
		if !ok {
			continue
		}
		body, _, err := r.d.Storage.Get(ctx, r.layout.SnapshotFile(ts, f.Path))
		if err != nil {
			return done, skipped, fmt.Errorf("read snapshot copy of %s: %w", f.Path, err)
		}
		data, err := r.decode(f.Path, body, f.Hash)
		if err != nil {
			return done, skipped, err
		}
		if _, err := writeFileAtomic(abs, data, time.Unix(0, f.MTime)); err != nil {
			return done, skipped, fmt.Errorf("write %s: %w", abs, err)
		}
		done = append(done, f.Path)
	}
	return done, skipped, nil
}
```

- [ ] **Step 5: Run the scenarios to verify they pass**

Run: `go test ./internal/engine/ -run Scenarios -v`
Expected: all thirteen `TestScenariosMemory/...` subtests PASS.

Run: `go test ./...`
Expected: every package `ok`.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/engine
git add internal/engine
git commit -m "feat(engine): snapshot now, conflict resolution and restore" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: App wiring

**Files:**
- Create: `internal/app/app.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: `state.DB`, `secrets.Store`, `storage.NewS3`, `storage.Probe`, and from `engine`: `CreateSharedAction`, `UpdateSharedAction`, `FetchAction`, `DiscoverActions`, `VerifyPassphrase`, `NewRunner`.
- Produces: package `app`:
  - `App{DB, Secrets, DataDir, MachineID, MachineName, NewStorage, LockSettle, Log}`
  - `DefaultDataDir() (string, error)` and `Open(dataDir string, sec secrets.Store, log *slog.Logger) (*App, error)`
  - `NewS3Storage(p model.Profile, secret string) (storage.Storage, error)`
  - Methods on `*App`:
    - `Close() error` and `SetMachineName(name) error`
    - `AddProfile(p, secret) (model.Profile, error)` and `TestProfile(ctx, id) (storage.ProbeResult, error)`
    - `CreateAction(ctx, profileID, a, passphrase) (model.Action, error)`
    - `AddItem(ctx, actionID, it, localPath) (model.Item, error)` and `MapItem(ctx, actionID, itemID, localPath) error`
    - `SetMachineDirection(ctx, actionID, d) error`, where `""` clears the override
    - `Discover(ctx, profileID) ([]model.Action, error)` and `Join(ctx, profileID, actionID, passphrase) (model.Action, error)`
    - `Runner(actionID) (*engine.Runner, error)`
    - `Profiles() ([]model.Profile, error)` and `Actions() ([]model.Action, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/app/app_test.go`:
```go
package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

var ctx = context.Background()

func newTestApp(t *testing.T, store *storage.Memory) *App {
	t.Helper()
	a, err := Open(t.TempDir(), secrets.NewMemory(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
	a.LockSettle = 0
	t.Cleanup(func() { a.Close() })
	return a
}

func TestTwoMachinesThroughApp(t *testing.T) {
	store := storage.NewMemory()
	a, b := newTestApp(t, store), newTestApp(t, store)
	if a.MachineID == b.MachineID {
		t.Fatal("two data dirs share a machine id")
	}

	p, err := a.AddProfile(model.Profile{Name: "test", Bucket: "bkt", RootPrefix: `\cfg\`, AccessKeyID: "k"}, "secret")
	if err != nil || p.RootPrefix != "cfg" {
		t.Fatalf("AddProfile = %+v, %v", p, err)
	}
	if _, err := a.AddProfile(model.Profile{Name: "x", Bucket: "b"}, ""); err == nil {
		t.Fatal("profile without secret accepted")
	}
	res, err := a.TestProfile(ctx, p.ID)
	if err != nil || !res.ConditionalWrites {
		t.Fatalf("TestProfile = %+v, %v", res, err)
	}
	if saved, _ := a.DB.GetProfile(p.ID); !saved.ConditionalWrites {
		t.Fatal("conditional-write support not stored on the profile")
	}

	if _, err := a.CreateAction(ctx, p.ID, model.Action{Name: "Editor", Encrypt: true}, ""); !errors.Is(err, engine.ErrPassphraseMissing) {
		t.Fatalf("encrypted action without passphrase: %v", err)
	}
	act, err := a.CreateAction(ctx, p.ID, model.Action{Name: "Editor", Encrypt: true, PropagateDeletes: true}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	dirA := t.TempDir()
	os.WriteFile(filepath.Join(dirA, "settings.json"), []byte(`{"a":1}`), 0o644)
	it, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "/settings.json", Type: model.ItemFile}, filepath.Join(dirA, "settings.json"))
	if err != nil || it.RemoteName != "settings.json" {
		t.Fatalf("AddItem = %+v, %v", it, err)
	}
	ra, err := a.Runner(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := ra.Sync(ctx); err != nil || res.Uploaded != 1 {
		t.Fatalf("A sync = %+v, %v", res, err)
	}

	pb, err := b.AddProfile(model.Profile{Name: "test", Bucket: "bkt", RootPrefix: "cfg", AccessKeyID: "k"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	found, err := b.Discover(ctx, pb.ID)
	if err != nil || len(found) != 1 || found[0].ID != act.ID {
		t.Fatalf("Discover = %+v, %v", found, err)
	}
	if _, err := b.Join(ctx, pb.ID, act.ID, "wrong"); !errors.Is(err, engine.ErrDecrypt) {
		t.Fatalf("join with wrong passphrase: %v", err)
	}
	if _, err := b.Join(ctx, pb.ID, act.ID, "pw"); err != nil {
		t.Fatal(err)
	}
	dirB := t.TempDir()
	if err := b.MapItem(ctx, act.ID, it.ID, filepath.Join(dirB, "settings.json")); err != nil {
		t.Fatal(err)
	}
	rb, _ := b.Runner(act.ID)
	if res, err := rb.Sync(ctx); err != nil || res.Downloaded != 1 {
		t.Fatalf("B sync = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dirB, "settings.json")); string(data) != `{"a":1}` {
		t.Fatalf("B file = %q", data)
	}

	if err := b.SetMachineDirection(ctx, act.ID, model.CloudToLocal); err != nil {
		t.Fatal(err)
	}
	got, _ := b.DB.GetAction(act.ID)
	if got.DirectionFor(b.MachineID) != model.CloudToLocal || got.Machines[b.MachineID] != b.MachineName {
		t.Fatalf("direction override not stored: %+v", got)
	}
	if err := b.SetMachineDirection(ctx, act.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.DB.GetAction(act.ID); got.DirectionFor(b.MachineID) != model.TwoWay {
		t.Fatal("direction override not cleared")
	}
	if err := b.SetMachineDirection(ctx, act.ID, "up"); err == nil {
		t.Fatal("invalid direction accepted")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/...`
Expected: FAIL with `undefined: Open`.

- [ ] **Step 3: Write the implementation**

Create `internal/app/app.go`:
```go
// Package app wires the state store, secrets, storage and engine together.
// The debug CLI uses it now; the agent's IPC server and the GUI use it later.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

// App is one machine's view of s3-anysync.
type App struct {
	DB          *state.DB
	Secrets     secrets.Store
	DataDir     string
	MachineID   string
	MachineName string
	// NewStorage builds the storage for a profile; tests replace it.
	NewStorage func(p model.Profile, secret string) (storage.Storage, error)
	// LockSettle is the best-effort lock read-back delay.
	LockSettle time.Duration
	Log        *slog.Logger
}

// DefaultDataDir is S3_ANYSYNC_DATA_DIR if set, otherwise
// <user config dir>/s3-anysync (~/Library/Application Support on macOS,
// %APPDATA% on Windows).
func DefaultDataDir() (string, error) {
	if d := os.Getenv("S3_ANYSYNC_DATA_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "s3-anysync"), nil
}

// Open opens the state store in dataDir and loads the machine identity.
func Open(dataDir string, sec secrets.Store, log *slog.Logger) (*App, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := state.Open(filepath.Join(dataDir, "state.db"))
	if err != nil {
		return nil, err
	}
	id, name, err := db.MachineIdentity()
	if err != nil {
		db.Close()
		return nil, err
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &App{
		DB: db, Secrets: sec, DataDir: dataDir, MachineID: id, MachineName: name,
		NewStorage: NewS3Storage, LockSettle: time.Second, Log: log,
	}, nil
}

// NewS3Storage is the production storage factory.
func NewS3Storage(p model.Profile, secret string) (storage.Storage, error) {
	return storage.NewS3(storage.S3Config{
		Endpoint: p.Endpoint, Region: p.Region, Bucket: p.Bucket,
		AccessKeyID: p.AccessKeyID, SecretAccessKey: secret, PathStyle: p.PathStyle,
	})
}

// Close closes the state store.
func (a *App) Close() error { return a.DB.Close() }

// SetMachineName changes this machine's friendly name.
func (a *App) SetMachineName(name string) error {
	if err := a.DB.SetMachineName(name); err != nil {
		return err
	}
	a.MachineName = strings.TrimSpace(name)
	return nil
}

func (a *App) storageFor(p model.Profile) (storage.Storage, error) {
	secret, err := a.Secrets.Get(secrets.ProfileSecretKey(p.ID))
	if err != nil {
		return nil, fmt.Errorf("secret key for profile %s: %w", p.Name, err)
	}
	return a.NewStorage(p, secret)
}

func lockMode(p model.Profile) storage.LockMode {
	if p.ConditionalWrites {
		return storage.LockConditional
	}
	return storage.LockBestEffort
}

// deps builds engine dependencies. The passphrase is loaded for encrypted actions.
func (a *App) deps(p model.Profile, actionID string, encrypted bool) (engine.Deps, error) {
	st, err := a.storageFor(p)
	if err != nil {
		return engine.Deps{}, err
	}
	d := engine.Deps{
		Storage: st, Repo: a.DB, MachineID: a.MachineID, MachineName: a.MachineName,
		BackupRoot: filepath.Join(a.DataDir, "local-backups"),
		LockMode:   lockMode(p), LockSettle: a.LockSettle, Log: a.Log,
	}
	if encrypted {
		pass, err := a.Secrets.Get(secrets.ActionPassphraseKey(actionID))
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return engine.Deps{}, err
		}
		d.Passphrase = pass
	}
	return d, nil
}

// AddProfile stores a profile and its secret key.
func (a *App) AddProfile(p model.Profile, secret string) (model.Profile, error) {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Bucket) == "" {
		return p, errors.New("profile name and bucket are required")
	}
	if secret == "" {
		return p, errors.New("secret access key is required")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	p.RootPrefix = strings.Trim(strings.ReplaceAll(strings.TrimSpace(p.RootPrefix), `\`, "/"), "/")
	if err := a.Secrets.Set(secrets.ProfileSecretKey(p.ID), secret); err != nil {
		return p, fmt.Errorf("store secret key: %w", err)
	}
	return p, a.DB.PutProfile(p)
}

// TestProfile probes the bucket and records whether conditional writes work.
func (a *App) TestProfile(ctx context.Context, id string) (storage.ProbeResult, error) {
	p, err := a.DB.GetProfile(id)
	if err != nil {
		return storage.ProbeResult{}, err
	}
	st, err := a.storageFor(p)
	if err != nil {
		return storage.ProbeResult{}, err
	}
	res, err := storage.Probe(ctx, st, p.RootPrefix)
	if err != nil {
		return res, err
	}
	p.ConditionalWrites = res.ConditionalWrites
	return res, a.DB.PutProfile(p)
}

// CreateAction publishes a new action from this machine.
func (a *App) CreateAction(ctx context.Context, profileID string, act model.Action, passphrase string) (model.Action, error) {
	p, err := a.DB.GetProfile(profileID)
	if err != nil {
		return act, err
	}
	if act.Encrypt && passphrase == "" {
		return act, engine.ErrPassphraseMissing
	}
	act.ID = uuid.NewString()
	act.ProfileID = profileID
	act.ApplyDefaults()
	act.Machines[a.MachineID] = a.MachineName
	act.UpdatedAt = time.Now().UTC()
	d, err := a.deps(p, act.ID, false)
	if err != nil {
		return act, err
	}
	d.Passphrase = passphrase
	if err := engine.CreateSharedAction(ctx, d, p.RootPrefix, act); err != nil {
		return act, err
	}
	if act.Encrypt {
		if err := a.Secrets.Set(secrets.ActionPassphraseKey(act.ID), passphrase); err != nil {
			return act, fmt.Errorf("store passphrase: %w", err)
		}
	}
	return act, a.DB.PutAction(act)
}

// updateShared changes the bucket's copy of an action and stores the result locally.
func (a *App) updateShared(ctx context.Context, actionID string, mutate func(*model.Action) error) (model.Action, error) {
	act, err := a.DB.GetAction(actionID)
	if err != nil {
		return act, err
	}
	p, err := a.DB.GetProfile(act.ProfileID)
	if err != nil {
		return act, err
	}
	d, err := a.deps(p, act.ID, act.Encrypt)
	if err != nil {
		return act, err
	}
	updated, err := engine.UpdateSharedAction(ctx, d, p.RootPrefix, actionID, func(x *model.Action) error {
		x.Machines[a.MachineID] = a.MachineName
		return mutate(x)
	})
	if err != nil {
		return act, err
	}
	updated.ProfileID = act.ProfileID
	return updated, a.DB.PutAction(updated)
}

// AddItem adds a file or directory, mapped to localPath on this machine.
func (a *App) AddItem(ctx context.Context, actionID string, it model.Item, localPath string) (model.Item, error) {
	name, err := model.NormalizeRemoteName(it.RemoteName)
	if err != nil {
		return it, err
	}
	if _, err := paths.Expand(localPath); err != nil {
		return it, err
	}
	it.ID = uuid.NewString()
	it.RemoteName = name
	it.LocalPaths = map[string]string{a.MachineID: localPath}
	_, err = a.updateShared(ctx, actionID, func(act *model.Action) error {
		act.Items = append(act.Items, it)
		return nil
	})
	return it, err
}

// MapItem sets this machine's local path for an existing item.
func (a *App) MapItem(ctx context.Context, actionID, itemID, localPath string) error {
	if _, err := paths.Expand(localPath); err != nil {
		return err
	}
	_, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		for i := range act.Items {
			if act.Items[i].ID == itemID {
				act.Items[i].LocalPaths[a.MachineID] = localPath
				return nil
			}
		}
		return fmt.Errorf("action has no item %s", itemID)
	})
	return err
}

// SetMachineDirection overrides the action's direction on this machine; "" removes the override.
func (a *App) SetMachineDirection(ctx context.Context, actionID string, d model.Direction) error {
	if d != "" && !d.Valid() {
		return fmt.Errorf("unknown direction %q", d)
	}
	_, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		if d == "" {
			delete(act.MachineDirections, a.MachineID)
		} else {
			act.MachineDirections[a.MachineID] = d
		}
		return nil
	})
	return err
}

// Discover lists the actions stored under a profile's root prefix.
func (a *App) Discover(ctx context.Context, profileID string) ([]model.Action, error) {
	p, err := a.DB.GetProfile(profileID)
	if err != nil {
		return nil, err
	}
	st, err := a.storageFor(p)
	if err != nil {
		return nil, err
	}
	return engine.DiscoverActions(ctx, st, p.RootPrefix)
}

// Join registers this machine on an existing action. Items still need MapItem.
func (a *App) Join(ctx context.Context, profileID, actionID, passphrase string) (model.Action, error) {
	p, err := a.DB.GetProfile(profileID)
	if err != nil {
		return model.Action{}, err
	}
	d, err := a.deps(p, actionID, false)
	if err != nil {
		return model.Action{}, err
	}
	d.Passphrase = passphrase
	act, err := engine.FetchAction(ctx, d.Storage, p.RootPrefix, actionID)
	if err != nil {
		return model.Action{}, err
	}
	if err := engine.VerifyPassphrase(ctx, d, p.RootPrefix, act); err != nil {
		return model.Action{}, err
	}
	if act.Encrypt {
		if err := a.Secrets.Set(secrets.ActionPassphraseKey(actionID), passphrase); err != nil {
			return model.Action{}, fmt.Errorf("store passphrase: %w", err)
		}
	}
	updated, err := engine.UpdateSharedAction(ctx, d, p.RootPrefix, actionID, func(x *model.Action) error {
		x.Machines[a.MachineID] = a.MachineName
		return nil
	})
	if err != nil {
		return model.Action{}, err
	}
	updated.ProfileID = profileID
	return updated, a.DB.PutAction(updated)
}

// Runner returns an engine runner for a local action.
func (a *App) Runner(actionID string) (*engine.Runner, error) {
	act, err := a.DB.GetAction(actionID)
	if err != nil {
		return nil, err
	}
	p, err := a.DB.GetProfile(act.ProfileID)
	if err != nil {
		return nil, err
	}
	d, err := a.deps(p, act.ID, act.Encrypt)
	if err != nil {
		return nil, err
	}
	return engine.NewRunner(d, p.RootPrefix, act), nil
}

// Profiles lists local profiles.
func (a *App) Profiles() ([]model.Profile, error) { return a.DB.ListProfiles() }

// Actions lists local actions.
func (a *App) Actions() ([]model.Action, error) { return a.DB.ListActions() }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app/...`
Expected: `ok  	github.com/niceit/s3-anysync/internal/app`

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/app && go mod tidy
git add go.mod go.sum internal/app
git commit -m "feat(app): wire state, secrets, storage and engine" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 18: Debug CLI

**Files:**
- Create: `cmd/agent/main.go`, `cmd/agent/cli.go`
- Test: `cmd/agent/cli_test.go`

**Interfaces:**
- Consumes: everything in `app.App`, plus `engine.Runner` (`Sync`, `Snapshot`, `ListSnapshots`, `Restore`, `Conflicts`, `Resolve`) and `engine.RestoreTarget`.
- Produces: the `s3-anysync-agent` binary with the subcommands in `usage` below, and the testable `run(args []string, stdout, stderr io.Writer, open func() (*app.App, error)) int`. Exit codes are 0 for ok, 1 for an error and 2 for bad usage. Plan 2 adds a `run` mode (the background agent) to this same binary.

- [ ] **Step 1: Write the failing tests**

Create `cmd/agent/cli_test.go`:
```go
package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

// opener simulates one machine: its own data dir and secrets, a shared bucket.
func opener(t *testing.T, store *storage.Memory) func() (*app.App, error) {
	dir, sec := t.TempDir(), secrets.NewMemory()
	return func() (*app.App, error) {
		a, err := app.Open(dir, sec, slog.New(slog.DiscardHandler))
		if err != nil {
			return nil, err
		}
		a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
		a.LockSettle = 0
		return a, nil
	}
}

func runOK(t *testing.T, open func() (*app.App, error), args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb, open); code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, errb.String())
	}
	return out.String()
}

func valueAfter(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, label+": "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no %q line in:\n%s", label, out)
	return ""
}

func TestCLIEndToEnd(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	store := storage.NewMemory()
	a, b := opener(t, store), opener(t, store)

	out := runOK(t, a, "profile", "add", "--name", "test", "--bucket", "bkt", "--access-key", "k", "--prefix", "cfg")
	pa := valueAfter(t, out, "profile created")
	if !strings.Contains(out, "conditional writes") {
		t.Fatalf("probe result not printed:\n%s", out)
	}
	act := valueAfter(t, runOK(t, a, "action", "create", "--profile", pa, "--name", "Editor"), "action created")

	dirA := t.TempDir()
	os.WriteFile(filepath.Join(dirA, "settings.json"), []byte(`{"a":1}`), 0o644)
	item := valueAfter(t, runOK(t, a, "item", "add", "--remote-name", "settings.json", "--type", "file", "--path", filepath.Join(dirA, "settings.json"), act), "item added")
	if out := runOK(t, a, "sync", act); !strings.Contains(out, "uploaded 1,") {
		t.Fatalf("sync A:\n%s", out)
	}
	if out := runOK(t, a, "snapshot", act); !strings.Contains(out, "snapshot taken:") {
		t.Fatalf("snapshot:\n%s", out)
	}
	if out := runOK(t, a, "snapshots", act); !strings.Contains(out, "manual") {
		t.Fatalf("snapshots:\n%s", out)
	}

	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "test", "--bucket", "bkt", "--access-key", "k", "--prefix", "cfg"), "profile created")
	if out := runOK(t, b, "action", "discover", "--profile", pb); !strings.Contains(out, act) {
		t.Fatalf("discover:\n%s", out)
	}
	if out := runOK(t, b, "action", "join", "--profile", pb, act); !strings.Contains(out, "not mapped") {
		t.Fatalf("join should list unmapped items:\n%s", out)
	}
	dirB := t.TempDir()
	runOK(t, b, "item", "map", "--path", filepath.Join(dirB, "settings.json"), act, item)
	if out := runOK(t, b, "sync", act); !strings.Contains(out, "downloaded 1,") {
		t.Fatalf("sync B:\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(dirB, "settings.json")); string(data) != `{"a":1}` {
		t.Fatalf("B file = %q", data)
	}

	// Conflict, list it, resolve it.
	os.WriteFile(filepath.Join(dirA, "settings.json"), []byte(`{"a":"A"}`), 0o644)
	os.WriteFile(filepath.Join(dirB, "settings.json"), []byte(`{"a":"BB"}`), 0o644)
	runOK(t, a, "sync", act)
	if out := runOK(t, b, "sync", act); !strings.Contains(out, "conflict: settings.json") {
		t.Fatalf("conflict not reported:\n%s", out)
	}
	if out := runOK(t, b, "conflicts", act); !strings.Contains(out, "settings.json") {
		t.Fatalf("conflicts:\n%s", out)
	}
	runOK(t, b, "resolve", "--all", "cloud", act)
	if data, _ := os.ReadFile(filepath.Join(dirB, "settings.json")); string(data) != `{"a":"A"}` {
		t.Fatalf("after resolve B = %q", data)
	}
	runOK(t, b, "action", "direction", act, "cloud-to-local")
	if out := runOK(t, b, "action", "list"); !strings.Contains(out, "cloud-to-local") {
		t.Fatalf("action list:\n%s", out)
	}
}

func TestCLIUsageAndErrors(t *testing.T) {
	open := opener(t, storage.NewMemory())
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb, open); code != 2 {
		t.Fatalf("no args: exit %d", code)
	}
	if code := run([]string{"bogus"}, &out, &errb, open); code != 2 {
		t.Fatalf("unknown command: exit %d", code)
	}
	errb.Reset()
	if code := run([]string{"sync"}, &out, &errb, open); code != 1 || !strings.Contains(errb.String(), "ACTION_ID") {
		t.Fatalf("sync without id: exit %d, %s", code, errb.String())
	}
	errb.Reset()
	t.Setenv("S3_ANYSYNC_SECRET", "")
	if code := run([]string{"profile", "add", "--name", "x", "--bucket", "b"}, &out, &errb, open); code != 1 || !strings.Contains(errb.String(), "S3_ANYSYNC_SECRET") {
		t.Fatalf("missing secret: exit %d, %s", code, errb.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agent/...`
Expected: FAIL with `undefined: run`.

- [ ] **Step 3: Write the implementation**

Create `cmd/agent/main.go`:
```go
// Command s3-anysync-agent is the s3-anysync background agent. In this plan
// it only has the debug subcommands; Plan 2 adds the agent run mode.
package main

import (
	"log/slog"
	"os"

	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/secrets"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, openDefault))
}

func openDefault() (*app.App, error) {
	dir, err := app.DefaultDataDir()
	if err != nil {
		return nil, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return app.Open(dir, secrets.NewKeyring(), log)
}
```

Create `cmd/agent/cli.go`:
```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
)

const usage = `usage: s3-anysync-agent <command> [flags] [arguments]

Flags always come before arguments.

  machine [--name NAME]
  profile add --name N --bucket B --access-key K [--provider P] [--endpoint URL] [--region R] [--prefix P] [--path-style]
              (secret key from S3_ANYSYNC_SECRET)
  profile list
  profile test PROFILE_ID
  action create --profile ID --name N [--direction D] [--no-propagate-deletes] [--encrypt] [--exclude GLOB]... [--retention N]
              (passphrase from S3_ANYSYNC_PASSPHRASE when --encrypt)
  action list
  action discover --profile ID
  action join --profile ID ACTION_ID      (passphrase from S3_ANYSYNC_PASSPHRASE for encrypted actions)
  action direction ACTION_ID two-way|local-to-cloud|cloud-to-local|default
  item add --remote-name NAME --type file|dir --path LOCAL_PATH [--exclude GLOB]... ACTION_ID
  item map --path LOCAL_PATH ACTION_ID ITEM_ID
  sync ACTION_ID
  snapshot ACTION_ID
  snapshots ACTION_ID
  restore --to local|cloud|both [--path P]... ACTION_ID TS
  conflicts ACTION_ID
  resolve (--all local|cloud | --path P=local|cloud ...) ACTION_ID

Environment: S3_ANYSYNC_DATA_DIR overrides the data folder.
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type cli struct {
	app *app.App
	out io.Writer
}

type command func(*cli, context.Context, []string) error

func run(args []string, stdout, stderr io.Writer, open func() (*app.App, error)) int {
	commands := map[string]command{
		"machine": (*cli).machine, "profile": (*cli).profile, "action": (*cli).action, "item": (*cli).item,
		"sync": (*cli).sync, "snapshot": (*cli).snapshot, "snapshots": (*cli).snapshots,
		"restore": (*cli).restore, "conflicts": (*cli).conflicts, "resolve": (*cli).resolve,
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	a, err := open()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	defer a.Close()
	if err := cmd(&cli{app: a, out: stdout}, context.Background(), args[1:]); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

// parseArgs parses flags and checks the positional arguments by name.
func parseArgs(fs *flag.FlagSet, args []string, positional ...string) ([]string, error) {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("%s: %w", fs.Name(), err)
	}
	if fs.NArg() != len(positional) {
		if len(positional) == 0 {
			return nil, fmt.Errorf("%s takes no arguments", fs.Name())
		}
		return nil, fmt.Errorf("%s: expected %s", fs.Name(), strings.Join(positional, " "))
	}
	return fs.Args(), nil
}

func (c *cli) table() *tabwriter.Writer { return tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0) }

func (c *cli) machine(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("machine", flag.ContinueOnError)
	name := fs.String("name", "", "new friendly name")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *name != "" {
		if err := c.app.SetMachineName(*name); err != nil {
			return err
		}
	}
	fmt.Fprintf(c.out, "machine id: %s\nmachine name: %s\n", c.app.MachineID, c.app.MachineName)
	return nil
}

func (c *cli) profile(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("profile: expected add, list or test")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("profile add", flag.ContinueOnError)
		var p model.Profile
		fs.StringVar(&p.Name, "name", "", "profile name")
		fs.StringVar(&p.Provider, "provider", "custom", "provider label")
		fs.StringVar(&p.Endpoint, "endpoint", "", "endpoint URL (empty for AWS)")
		fs.StringVar(&p.Region, "region", "", "region")
		fs.StringVar(&p.Bucket, "bucket", "", "bucket")
		fs.StringVar(&p.RootPrefix, "prefix", "", "folder inside the bucket")
		fs.StringVar(&p.AccessKeyID, "access-key", "", "access key id")
		fs.BoolVar(&p.PathStyle, "path-style", false, "path-style addressing")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		secret := os.Getenv("S3_ANYSYNC_SECRET")
		if secret == "" {
			return errors.New("set S3_ANYSYNC_SECRET to the secret access key")
		}
		p, err := c.app.AddProfile(p, secret)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "profile created: %s\n", p.ID)
		return c.printProbe(ctx, p.ID)
	case "list":
		if _, err := parseArgs(flag.NewFlagSet("profile list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		profiles, err := c.app.Profiles()
		if err != nil {
			return err
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tBUCKET\tPREFIX\tENDPOINT\tLOCK")
		for _, p := range profiles {
			lock := "best effort"
			if p.ConditionalWrites {
				lock = "conditional"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Bucket, p.RootPrefix, p.Endpoint, lock)
		}
		return w.Flush()
	case "test":
		pos, err := parseArgs(flag.NewFlagSet("profile test", flag.ContinueOnError), args[1:], "PROFILE_ID")
		if err != nil {
			return err
		}
		return c.printProbe(ctx, pos[0])
	}
	return fmt.Errorf("profile: unknown subcommand %q", args[0])
}

func (c *cli) printProbe(ctx context.Context, id string) error {
	res, err := c.app.TestProfile(ctx, id)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	if res.ConditionalWrites {
		fmt.Fprintln(c.out, "connection ok (lock mode: conditional writes)")
	} else {
		fmt.Fprintln(c.out, "connection ok (lock mode: best effort; this provider ignores conditional writes)")
	}
	return nil
}

func (c *cli) action(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("action: expected create, list, discover, join or direction")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("action create", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		var a model.Action
		fs.StringVar(&a.Name, "name", "", "action name")
		dir := fs.String("direction", string(model.TwoWay), "default direction")
		noDel := fs.Bool("no-propagate-deletes", false, "never delete on the other side")
		fs.BoolVar(&a.Encrypt, "encrypt", false, "encrypt files")
		var excludes multiFlag
		fs.Var(&excludes, "exclude", "exclude glob (repeatable)")
		fs.IntVar(&a.SnapshotRetention, "retention", model.DefaultSnapshotRetention, "snapshots to keep")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		a.Direction = model.Direction(*dir)
		a.PropagateDeletes = !*noDel
		a.Excludes = excludes
		created, err := c.app.CreateAction(ctx, *profile, a, os.Getenv("S3_ANYSYNC_PASSPHRASE"))
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "action created: %s\n", created.ID)
		return nil
	case "list":
		if _, err := parseArgs(flag.NewFlagSet("action list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		actions, err := c.app.Actions()
		if err != nil {
			return err
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tDIRECTION (THIS MACHINE)\tITEMS\tENCRYPTED")
		for _, a := range actions {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%v\n", a.ID, a.Name, a.DirectionFor(c.app.MachineID), len(a.Items), a.Encrypt)
		}
		return w.Flush()
	case "discover":
		fs := flag.NewFlagSet("action discover", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		found, err := c.app.Discover(ctx, *profile)
		if err != nil {
			return err
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tITEMS\tJOINED")
		for _, a := range found {
			_, err := c.app.DB.GetAction(a.ID)
			fmt.Fprintf(w, "%s\t%s\t%d\t%v\n", a.ID, a.Name, len(a.Items), err == nil)
		}
		return w.Flush()
	case "join":
		fs := flag.NewFlagSet("action join", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID")
		if err != nil {
			return err
		}
		a, err := c.app.Join(ctx, *profile, pos[0], os.Getenv("S3_ANYSYNC_PASSPHRASE"))
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "joined action: %s\n", a.ID)
		w := c.table()
		fmt.Fprintln(w, "ITEM_ID\tREMOTE NAME\tTYPE\tLOCAL PATH")
		for _, it := range a.Items {
			local := it.LocalPaths[c.app.MachineID]
			if local == "" {
				local = "not mapped on this machine (use: item map)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", it.ID, it.RemoteName, it.Type, local)
		}
		return w.Flush()
	case "direction":
		pos, err := parseArgs(flag.NewFlagSet("action direction", flag.ContinueOnError), args[1:], "ACTION_ID", "DIRECTION")
		if err != nil {
			return err
		}
		d := model.Direction(pos[1])
		if pos[1] == "default" {
			d = ""
		}
		if err := c.app.SetMachineDirection(ctx, pos[0], d); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "direction updated")
		return nil
	}
	return fmt.Errorf("action: unknown subcommand %q", args[0])
}

func (c *cli) item(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("item: expected add or map")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("item add", flag.ContinueOnError)
		var it model.Item
		fs.StringVar(&it.RemoteName, "remote-name", "", "name under current/")
		typ := fs.String("type", "", "file or dir")
		local := fs.String("path", "", "local path on this machine")
		var excludes multiFlag
		fs.Var(&excludes, "exclude", "exclude glob (repeatable)")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID")
		if err != nil {
			return err
		}
		it.Type = model.ItemType(*typ)
		it.Excludes = excludes
		added, err := c.app.AddItem(ctx, pos[0], it, *local)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "item added: %s\n", added.ID)
		return nil
	case "map":
		fs := flag.NewFlagSet("item map", flag.ContinueOnError)
		local := fs.String("path", "", "local path on this machine")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID", "ITEM_ID")
		if err != nil {
			return err
		}
		if err := c.app.MapItem(ctx, pos[0], pos[1], *local); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "item mapped")
		return nil
	}
	return fmt.Errorf("item: unknown subcommand %q", args[0])
}

// runner parses a command that takes exactly ACTION_ID (plus flags in fs).
func (c *cli) runner(fs *flag.FlagSet, args []string, extra ...string) (*engine.Runner, []string, error) {
	pos, err := parseArgs(fs, args, append([]string{"ACTION_ID"}, extra...)...)
	if err != nil {
		return nil, nil, err
	}
	r, err := c.app.Runner(pos[0])
	return r, pos, err
}

func (c *cli) printResult(res model.SyncResult) {
	fmt.Fprintf(c.out, "%s: %s\n", res.Outcome, res.Summary())
	for _, cf := range res.Conflicts {
		fmt.Fprintf(c.out, "  conflict: %s\n", cf.Path)
	}
}

func (c *cli) sync(ctx context.Context, args []string) error {
	r, _, err := c.runner(flag.NewFlagSet("sync", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	res, err := r.Sync(ctx)
	if err != nil {
		return err
	}
	c.printResult(res)
	return nil
}

func (c *cli) snapshot(ctx context.Context, args []string) error {
	r, _, err := c.runner(flag.NewFlagSet("snapshot", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	info, err := r.Snapshot(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "snapshot taken: %s (%d files)\n", info.TS, len(info.Files))
	return nil
}

func (c *cli) snapshots(ctx context.Context, args []string) error {
	r, _, err := c.runner(flag.NewFlagSet("snapshots", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	list, err := r.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	machines := r.Action().Machines
	w := c.table()
	fmt.Fprintln(w, "TS\tREASON\tMACHINE\tFILES")
	for _, s := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", s.TS, s.Reason, nameOr(machines, s.MachineID), len(s.Files))
	}
	return w.Flush()
}

func (c *cli) restore(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	to := fs.String("to", "", "local, cloud or both")
	var only multiFlag
	fs.Var(&only, "path", "restore only this remote path (repeatable)")
	r, pos, err := c.runner(fs, args, "TS")
	if err != nil {
		return err
	}
	res, err := r.Restore(ctx, pos[1], only, engine.RestoreTarget(*to))
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "restored %d file(s)\n", len(res.Restored))
	for _, p := range res.SkippedUnmapped {
		fmt.Fprintf(c.out, "  skipped (not mapped on this machine): %s\n", p)
	}
	return nil
}

func (c *cli) conflicts(_ context.Context, args []string) error {
	r, _, err := c.runner(flag.NewFlagSet("conflicts", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	list, err := r.Conflicts()
	if err != nil {
		return err
	}
	machines := r.Action().Machines
	w := c.table()
	fmt.Fprintln(w, "PATH\tLOCAL\tCLOUD\tCLOUD WRITTEN BY")
	for _, cf := range list {
		writer := ""
		if cf.Remote != nil {
			writer = nameOr(machines, cf.Remote.Writer)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cf.Path, describe(cf.Local), describe(cf.Remote), writer)
	}
	return w.Flush()
}

func (c *cli) resolve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	all := fs.String("all", "", "resolve every conflict: local or cloud")
	var each multiFlag
	fs.Var(&each, "path", "PATH=local|cloud (repeatable)")
	r, _, err := c.runner(fs, args)
	if err != nil {
		return err
	}
	choices := map[string]model.Side{}
	if *all != "" {
		list, err := r.Conflicts()
		if err != nil {
			return err
		}
		for _, cf := range list {
			choices[cf.Path] = model.Side(*all)
		}
	}
	for _, kv := range each {
		p, side, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--path %q: expected PATH=local or PATH=cloud", kv)
		}
		choices[p] = model.Side(side)
	}
	if len(choices) == 0 {
		return errors.New("nothing to resolve: pass --all or --path, or there are no conflicts")
	}
	res, err := r.Resolve(ctx, choices)
	if err != nil {
		return err
	}
	c.printResult(res)
	return nil
}

func describe(e *model.FileEntry) string {
	if e == nil {
		return "deleted"
	}
	return fmt.Sprintf("%d bytes, %s", e.Size, time.Unix(0, e.MTime).Format("2006-01-02 15:04:05"))
}

func nameOr(names map[string]string, id string) string {
	if n := names[id]; n != "" {
		return n
	}
	return id
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/agent/...`
Expected: `ok  	github.com/niceit/s3-anysync/cmd/agent`

- [ ] **Step 5: Build the binary**

Run: `CGO_ENABLED=0 go build -o bin/s3-anysync-agent ./cmd/agent && ./bin/s3-anysync-agent`
Expected: the usage text is printed, with exit code 2.

- [ ] **Step 6: Commit**

```bash
gofmt -w cmd
git add cmd
git commit -m "feat(cli): debug commands for profiles, actions, items, sync, snapshots, restore and conflicts" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 19: S3 end-to-end scenarios, README and final verification

**Files:**
- Create: `internal/engine/s3_scenarios_test.go`, `README.md`

**Interfaces:**
- Consumes: `runScenarios` (Task 15) and `s3test.New` (Task 8).
- Produces: every engine scenario running against a real S3-compatible server, plus developer documentation.

- [ ] **Step 1: Add the S3 scenario runner**

Create `internal/engine/s3_scenarios_test.go`:
```go
package engine

import (
	"os"
	"testing"

	"github.com/niceit/s3-anysync/internal/storage"
	"github.com/niceit/s3-anysync/internal/storage/s3test"
)

func TestScenariosS3(t *testing.T) {
	if os.Getenv("S3_TEST_ENDPOINT") == "" {
		t.Skip("S3_TEST_ENDPOINT not set")
	}
	runScenarios(t, func(t *testing.T) (storage.Storage, string) {
		s, prefix := s3test.New(t)
		return s, prefix
	})
}
```

- [ ] **Step 2: Run the scenarios against MinIO**

With the MinIO container from Task 8 running:
```bash
S3_TEST_ENDPOINT=http://localhost:9000 S3_TEST_KEY=minio S3_TEST_SECRET=minio12345 S3_TEST_BUCKET=s3-anysync-test go test ./internal/engine/ -run ScenariosS3 -v
```
Expected: all thirteen `TestScenariosS3/...` subtests PASS.

- [ ] **Step 3: Write `README.md`**

````markdown
# s3-anysync

Keep application settings files and config folders in sync across macOS and Windows machines through your own S3-compatible bucket (AWS S3, Wasabi, Backblaze B2, Cloudflare R2, MinIO, …).

Design: [docs/superpowers/specs/2026-10-01-s3-anysync-design.md](docs/superpowers/specs/2026-10-01-s3-anysync-design.md)

## Status

Plan 1 is done: the sync engine and a debug command line. The background agent (tray, schedules, file watching), the GUI and the installers come in later plans.

## Build

Requires Go 1.27.

```bash
CGO_ENABLED=0 go build -o bin/s3-anysync-agent ./cmd/agent
```

## Try it with two "machines" on one computer

Each data folder acts as a separate machine (its own id, state and baseline). Secrets go to the OS keychain.

```bash
export S3_ANYSYNC_SECRET=minio12345
export S3_ANYSYNC_DATA_DIR=/tmp/anysync-a
./bin/s3-anysync-agent profile add --name local --endpoint http://localhost:9000 --path-style --bucket s3-anysync-test --access-key minio --prefix demo
./bin/s3-anysync-agent action create --profile <PROFILE_ID> --name "VS Code"
./bin/s3-anysync-agent item add --remote-name settings.json --type file --path "~/Library/Application Support/Code/User/settings.json" <ACTION_ID>
./bin/s3-anysync-agent sync <ACTION_ID>
./bin/s3-anysync-agent snapshot <ACTION_ID>
```

Second machine:

```bash
export S3_ANYSYNC_DATA_DIR=/tmp/anysync-b
./bin/s3-anysync-agent profile add --name local --endpoint http://localhost:9000 --path-style --bucket s3-anysync-test --access-key minio --prefix demo
./bin/s3-anysync-agent action discover --profile <PROFILE_ID>
./bin/s3-anysync-agent action join --profile <PROFILE_ID> <ACTION_ID>
./bin/s3-anysync-agent item map --path /tmp/anysync-b-files/settings.json <ACTION_ID> <ITEM_ID>
./bin/s3-anysync-agent sync <ACTION_ID>
```

Run `./bin/s3-anysync-agent` with no arguments for every command.

## Tests

```bash
go test ./...
```

The S3 integration tests run when an S3-compatible server is configured:

```bash
docker run -d --rm --name s3-anysync-minio -p 9000:9000 -e MINIO_ROOT_USER=minio -e MINIO_ROOT_PASSWORD=minio12345 quay.io/minio/minio server /data
```

```bash
S3_TEST_ENDPOINT=http://localhost:9000 S3_TEST_KEY=minio S3_TEST_SECRET=minio12345 S3_TEST_BUCKET=s3-anysync-test go test ./...
```

## Bucket permissions

The access key needs `s3:ListBucket` on the bucket and `s3:GetObject`, `s3:PutObject` and `s3:DeleteObject` on its objects.
````

- [ ] **Step 4: Run the full verification**

```bash
gofmt -l .
```
Expected: no output.

```bash
go vet ./...
```
Expected: no output.

```bash
go test ./...
```
Expected: every package `ok`.

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
```
Expected: no output (the Windows build compiles).

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./... && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./...
```
Expected: no output.

- [ ] **Step 5: Manual check with the real keychain and MinIO**

Follow the README's "two machines" walkthrough, using a scratch file instead of real VS Code settings. Confirm that:
1. `sync` on machine B downloads the file.
2. Editing the file on both machines, then syncing A and then B, reports `conflict: settings.json`.
3. `resolve --all local <ACTION_ID>` on B uploads B's copy, and `snapshots <ACTION_ID>` lists a `pre-resolve` snapshot.
4. macOS Keychain Access shows `s3-anysync` entries (`profile:<id>`).

- [ ] **Step 6: Commit**

```bash
git add README.md internal/engine/s3_scenarios_test.go
git commit -m "test: run engine scenarios against S3; add README" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
