# s3-anysync Plan 2: Background Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the Plan 1 engine into a background agent. The agent runs at login, keeps one worker and state machine per action, syncs on file changes, schedules and remote polls, pauses on conflicts and mass deletions, notifies the user, shows a tray icon, and serves a local IPC API. The CLI (and later the GUI) uses that API.

**Architecture:**
- A new `internal/agent.Manager` owns one worker goroutine per action. The worker serialises every run and every change to that action, and drives the state machine.
- `Manager` implements `internal/api.Service`. The same interface is served over HTTP on a Unix socket or Windows named pipe by `internal/ipc` and consumed by `ipc.Client`. The CLI therefore talks to `api.Service`: in-process when it owns the data folder, over IPC when the agent does.
- Triggers live in `internal/trigger` (an fsnotify watcher with debounce, a cron schedule, and a remote manifest poller). Platform pieces are small packages: `datalock`, `autostart`, `notify`, `logging`, `tray`.
- Engine additions: a mass-delete guard, split snapshot retention, reporting of names this OS can't hold, conflict diff data, and lock-release safety.

**Tech Stack:** Go 1.27.1, `fsnotify/fsnotify`, `robfig/cron/v3`, `Microsoft/go-winio`, `gofrs/flock`, `fyne.io/systray` (CGO on macOS), `gen2brain/beeep`, `golang.org/x/sys/windows`, plus Plan 1's dependencies.

**Spec:** `docs/superpowers/specs/2026-10-01-s3-anysync-design.md`, sections 6.8, 6.9, 7, 8 and **13 (Plan 2 decisions)**.

**Builds on:** Plan 1 (`docs/superpowers/plans/2026-10-01-s3-anysync-plan-1-core.md`), as amended during its execution. Read the current code; the Plan 1 text is older than the code.

## Global Constraints

- Module `github.com/niceit/s3-anysync`, Go 1.27.1. Run `gofmt -w` on every file you touch.
- Every `internal/` package except `internal/tray` builds and tests with `CGO_ENABLED=0` on darwin and windows. `cmd/agent` needs `CGO_ENABLED=1` on macOS for the real tray. With CGO off it builds with a headless no-op tray. Windows builds use `CGO_ENABLED=0`.
- Data folder layout (`app.DefaultDataDir()`, overridable with `S3_ANYSYNC_DATA_DIR`):
  - `state.db`
  - `agent.lock`: OS file lock, one owner process
  - `agent.sock`: Unix only, mode 0600
  - `ipc.token`: 32 random bytes, hex, mode 0600, rotated at every agent start
  - `logs/agent-YYYY-MM-DD.log`: kept 7 days
  - `local-backups/`
- Windows pipe: `\\.\pipe\s3-anysync-<first 16 hex chars of sha256(clean data dir path)>`, with DACL `D:P(A;;GA;;;<current user SID>)`.
- IPC: HTTP/1.1, JSON bodies, header `Authorization: Bearer <token>`, error body `{"code": "...", "message": "..."}`.
- Retry backoff: 30s, 2m, 10m, 30m, then 1h repeating. Notify after 3 consecutive failures.
- Errors that are never retried automatically: `engine.ErrDecrypt`, `engine.ErrPassphraseMissing`, `engine.ErrKeyCheckMissing`, `engine.ErrRootMissing`, `storage.ErrAccessDenied`, `storage.ErrNoSuchBucket`, and an invalid schedule.
- Delete guard defaults: `deleteGuardMinFiles` 5, `deleteGuardPercent` 50.
- Snapshot retention:
  - manual: newest `snapshotRetention` (default 20);
  - automatic: newest 50 OR younger than 7 days;
  - automatic snapshots that record deletions: at least 30 days.
- Autostart: macOS `~/Library/LaunchAgents/com.niceit.s3-anysync.agent.plist` (label `com.niceit.s3-anysync.agent`); Windows HKCU `Software\Microsoft\Windows\CurrentVersion\Run` value `s3-anysync`. Both run `<agent executable> run`.
- Secrets never go to SQLite, logs, events, IPC responses or the bucket. The S3 secret and passphrases travel only in request bodies to the agent, never in responses.
- Every commit message ends with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`, passed as a second `-m`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/model/model.go` (modify) | Delete-guard fields, `Rules`, `PendingDeletes`, new outcomes and result fields, `SnapshotInfo.Deletes` |
| `internal/state/actionstate.go` (new), `actions.go` (modify) | Pending deletions and the pause flag per action |
| `internal/engine/deleteguard.go` (new), `sync.go`, `deps.go` (modify) | Mass-delete guard and `ApproveDeletes` |
| `internal/engine/snapshot.go`, `runner.go` (modify) | Split retention and deletions recorded in safety snapshots |
| `internal/engine/mapping.go` (modify), `conflictdetail.go` (new) | OS name rules, `Unsupported`, `RemotePathFor`, `ConflictDetail`, `ManifestETag` |
| `internal/storage/lock.go` (modify) | Do not release a lock that was lost |
| `internal/schedule/schedule.go` | Compile and validate action schedules |
| `internal/app/manage.go` (new) | `UpdateRules`, `UnmapItem`, `RemoveItem`, `LeaveAction`, history, pause flag, log level |
| `internal/datalock/datalock.go` | One owner process per data folder |
| `internal/api/api.go`, `errors.go` | `Service` interface, DTOs, error codes |
| `internal/notify/notify.go`, `internal/logging/logging.go` | Desktop notifications; daily rotating JSON logs |
| `internal/agent/*.go` | `Manager`, workers, state machine, event bus, triggers wiring, `api.Service` implementation |
| `internal/trigger/watch.go`, `schedule.go` | fsnotify watcher with debounce; schedule and poll loops |
| `internal/ipc/*.go` | Transport (Unix socket / named pipe), token, HTTP server with SSE, client |
| `internal/autostart/*.go` | LaunchAgent / HKCU Run |
| `internal/tray/*.go` | Tray menu (real with CGO or on Windows; no-op stub otherwise) |
| `cmd/agent/*.go` | CLI on `api.Service` (direct or remote), `run` mode, new commands |

---

### Task 1: Model and state additions

**Files:**
- Modify: `internal/model/model.go`, `internal/model/model_test.go`
- Create: `internal/state/actionstate.go`
- Modify: `internal/state/actions.go` (`DeleteAction`), `internal/state/state_test.go`
- Modify: `internal/engine/deps.go` (`Repo` interface)

**Interfaces:**
- Consumes: the existing `model.Action`, `ApplyDefaults` and `Validate`, and `state.DB` settings helpers.
- Produces:
  - Model constants: `DefaultDeleteGuardMinFiles=5`, `DefaultDeleteGuardPercent=50`, `OutcomeDeletesPending="deletes-pending"`, `HistoryApproveDeletes="approve-deletes"`.
  - `model.Action` fields `DeleteGuardMinFiles`, `DeleteGuardPercent`.
  - `model.SyncResult` fields `PendingDeletes []string`, `Unsupported []string`.
  - `model.SnapshotInfo.Deletes []string`.
  - `model.Rules`, with `(Action).Rules()` and `(*Action).ApplyRules(Rules)`.
  - `model.PendingDeletes{Paths, Approved, DetectedAt}`.
  - State methods: `SavePendingDeletes(id, *model.PendingDeletes) error` (nil clears), `LoadPendingDeletes(id) (*model.PendingDeletes, error)`, `SetPaused(id, bool) error`, `Paused(id) (bool, error)`, `DeleteSetting(key) error`.
  - `engine.Repo` gains `SavePendingDeletes` and `LoadPendingDeletes`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/model/model_test.go`:
```go
func TestDeleteGuardDefaultsAndValidation(t *testing.T) {
	var a Action
	a.ApplyDefaults()
	if a.DeleteGuardMinFiles != 5 || a.DeleteGuardPercent != 50 {
		t.Fatalf("delete guard defaults = %d/%d", a.DeleteGuardMinFiles, a.DeleteGuardPercent)
	}
	for _, tc := range []struct{ min, pct int }{{5, 0}, {5, 101}, {0, 50}} {
		v := validAction()
		v.DeleteGuardMinFiles, v.DeleteGuardPercent = tc.min, tc.pct
		if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "delete guard") {
			t.Errorf("min=%d pct=%d: want delete guard error, got %v", tc.min, tc.pct, err)
		}
	}
}

func TestSummaryExtras(t *testing.T) {
	r := SyncResult{PendingDeletes: []string{"a"}, Unsupported: []string{"b", "c"}}
	want := "uploaded 0, downloaded 0, deleted in cloud 0, deleted locally 0, adopted 0, conflicts 0, pending deletions 1, unsupported 2"
	if got := r.Summary(); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRulesRoundTrip(t *testing.T) {
	a := validAction()
	a.Encrypt = true
	r := a.Rules()
	r.Name = "Renamed"
	r.Excludes = []string{"*.log"}
	r.Triggers.OnChange = true
	r.Triggers.DebounceSeconds = 0 // defaulted again by ApplyRules
	r.DeleteGuardPercent = 80
	a.ApplyRules(r)
	if a.Name != "Renamed" || a.Excludes[0] != "*.log" || !a.Triggers.OnChange || a.Triggers.DebounceSeconds != DefaultDebounceSeconds || a.DeleteGuardPercent != 80 {
		t.Fatalf("rules not applied: %+v", a)
	}
	if a.ID != "a1" || len(a.Items) != 2 || !a.Encrypt {
		t.Fatal("ApplyRules changed identity, items or encryption")
	}
}
```

Append to `internal/state/state_test.go`:
```go
func TestPendingDeletesAndPause(t *testing.T) {
	db := openTest(t)
	if p, err := db.LoadPendingDeletes("a1"); err != nil || p != nil {
		t.Fatalf("empty: %+v %v", p, err)
	}
	want := &model.PendingDeletes{Paths: []string{"x", "y"}, Approved: true, DetectedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	if err := db.SavePendingDeletes("a1", want); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadPendingDeletes("a1")
	if err != nil || got == nil || len(got.Paths) != 2 || !got.Approved || !got.DetectedAt.Equal(want.DetectedAt) {
		t.Fatalf("loaded %+v %v", got, err)
	}
	if err := db.SavePendingDeletes("a1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.LoadPendingDeletes("a1"); got != nil {
		t.Fatal("nil did not clear pending deletes")
	}
	if p, _ := db.Paused("a1"); p {
		t.Fatal("paused by default")
	}
	db.SetPaused("a1", true)
	if p, _ := db.Paused("a1"); !p {
		t.Fatal("pause not stored")
	}
	db.SetPaused("a1", false)
	if p, _ := db.Paused("a1"); p {
		t.Fatal("pause not cleared")
	}
}

func TestDeleteActionClearsActionState(t *testing.T) {
	db := openTest(t)
	db.PutProfile(model.Profile{ID: "p1", Name: "p", Bucket: "b"})
	a := model.Action{ID: "a1", Name: "x", ProfileID: "p1"}
	a.ApplyDefaults()
	if err := db.PutAction(a); err != nil {
		t.Fatal(err)
	}
	db.SetPaused("a1", true)
	db.SavePendingDeletes("a1", &model.PendingDeletes{Paths: []string{"x"}})
	if err := db.DeleteAction("a1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := db.Paused("a1"); p {
		t.Fatal("pause flag survived DeleteAction")
	}
	if got, _ := db.LoadPendingDeletes("a1"); got != nil {
		t.Fatal("pending deletes survived DeleteAction")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/model/ ./internal/state/`
Expected: FAIL with `a.DeleteGuardMinFiles undefined`, `undefined: PendingDeletes`, and similar.

- [ ] **Step 3: Implement the model changes**

In `internal/model/model.go`:

1. Add to the "Defaults from the design spec" const block:
```go
	DefaultDeleteGuardMinFiles = 5
	DefaultDeleteGuardPercent  = 50
```
2. Add these fields to `Action`, after `SnapshotRetention`:
```go
	// DeleteGuardMinFiles and DeleteGuardPercent: a run that would delete at
	// least DeleteGuardMinFiles files AND at least DeleteGuardPercent percent
	// of the files synced so far pauses until the deletions are approved.
	DeleteGuardMinFiles int `json:"deleteGuardMinFiles"`
	DeleteGuardPercent  int `json:"deleteGuardPercent"`
```
3. In `ApplyDefaults`, add:
```go
	if a.DeleteGuardMinFiles <= 0 {
		a.DeleteGuardMinFiles = DefaultDeleteGuardMinFiles
	}
	if a.DeleteGuardPercent <= 0 {
		a.DeleteGuardPercent = DefaultDeleteGuardPercent
	}
```
4. In `Validate`, after the retention check, add:
```go
	if a.DeleteGuardMinFiles < 1 || a.DeleteGuardPercent < 1 || a.DeleteGuardPercent > 100 {
		return errors.New("delete guard needs at least 1 file and a percent between 1 and 100")
	}
```
5. Add `OutcomeDeletesPending = "deletes-pending"` to the outcomes block and `HistoryApproveDeletes = "approve-deletes"` to the history kinds.
6. Add these fields to `SyncResult`, after `Deferred`:
```go
	// PendingDeletes lists the deletions held back by the delete guard
	// (Outcome is OutcomeDeletesPending; nothing was applied).
	PendingDeletes []string `json:"pendingDeletes"`
	// Unsupported lists cloud files whose names cannot exist on this OS;
	// they are not synced to this machine.
	Unsupported []string `json:"unsupported"`
```
   and extend `Summary` (after the deferred clause):
```go
	if len(r.PendingDeletes) > 0 {
		s += fmt.Sprintf(", pending deletions %d", len(r.PendingDeletes))
	}
	if len(r.Unsupported) > 0 {
		s += fmt.Sprintf(", unsupported %d", len(r.Unsupported))
	}
```
7. Add to `SnapshotInfo`:
```go
	// Deletes lists the files of this snapshot that were about to be deleted.
	Deletes []string `json:"deletes,omitempty"`
```
8. Add at the end of the file:
```go
// Rules are the settings of an action a user can edit after creating it:
// everything except its identity, items, per-machine paths and directions,
// and encryption (fixed at creation).
type Rules struct {
	Name                  string    `json:"name"`
	Direction             Direction `json:"direction"`
	Triggers              Triggers  `json:"triggers"`
	PropagateDeletes      bool      `json:"propagateDeletes"`
	ReceiverRestoresCloud bool      `json:"receiverRestoresCloud"`
	Excludes              []string  `json:"excludes"`
	SnapshotRetention     int       `json:"snapshotRetention"`
	DeleteGuardMinFiles   int       `json:"deleteGuardMinFiles"`
	DeleteGuardPercent    int       `json:"deleteGuardPercent"`
}

// Rules returns the action's editable settings.
func (a Action) Rules() Rules {
	return Rules{
		Name: a.Name, Direction: a.Direction, Triggers: a.Triggers,
		PropagateDeletes: a.PropagateDeletes, ReceiverRestoresCloud: a.ReceiverRestoresCloud,
		Excludes: append([]string(nil), a.Excludes...), SnapshotRetention: a.SnapshotRetention,
		DeleteGuardMinFiles: a.DeleteGuardMinFiles, DeleteGuardPercent: a.DeleteGuardPercent,
	}
}

// ApplyRules overwrites the editable settings and re-applies defaults.
func (a *Action) ApplyRules(r Rules) {
	a.Name = r.Name
	a.Direction = r.Direction
	a.Triggers = r.Triggers
	a.PropagateDeletes = r.PropagateDeletes
	a.ReceiverRestoresCloud = r.ReceiverRestoresCloud
	a.Excludes = append([]string(nil), r.Excludes...)
	a.SnapshotRetention = r.SnapshotRetention
	a.DeleteGuardMinFiles = r.DeleteGuardMinFiles
	a.DeleteGuardPercent = r.DeleteGuardPercent
	a.ApplyDefaults()
}

// PendingDeletes are deletions held back by the delete guard of one action.
type PendingDeletes struct {
	Paths      []string  `json:"paths"`
	Approved   bool      `json:"approved"`
	DetectedAt time.Time `json:"detectedAt"`
}
```

- [ ] **Step 4: Implement the state changes**

Create `internal/state/actionstate.go`:
```go
package state

import (
	"encoding/json"
	"fmt"

	"github.com/niceit/s3-anysync/internal/model"
)

const (
	settingPendingDeletesPrefix = "pending_deletes:"
	settingPausedPrefix         = "paused:"
)

// DeleteSetting removes a setting; a missing key is not an error.
func (d *DB) DeleteSetting(key string) error {
	_, err := d.sql.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// SavePendingDeletes stores the deletions held back for an action; nil or an
// empty path list clears them.
func (d *DB) SavePendingDeletes(actionID string, p *model.PendingDeletes) error {
	key := settingPendingDeletesPrefix + actionID
	if p == nil || len(p.Paths) == 0 {
		return d.DeleteSetting(key)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return d.SetSetting(key, string(data))
}

// LoadPendingDeletes returns the held-back deletions, or nil if there are none.
func (d *DB) LoadPendingDeletes(actionID string) (*model.PendingDeletes, error) {
	v, ok, err := d.GetSetting(settingPendingDeletesPrefix + actionID)
	if err != nil || !ok {
		return nil, err
	}
	var p model.PendingDeletes
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return nil, fmt.Errorf("pending deletes of %s: %w", actionID, err)
	}
	return &p, nil
}

// SetPaused stores whether the user paused an action.
func (d *DB) SetPaused(actionID string, paused bool) error {
	if !paused {
		return d.DeleteSetting(settingPausedPrefix + actionID)
	}
	return d.SetSetting(settingPausedPrefix+actionID, "1")
}

// Paused reports whether the user paused an action.
func (d *DB) Paused(actionID string) (bool, error) {
	_, ok, err := d.GetSetting(settingPausedPrefix + actionID)
	return ok, err
}
```

In `internal/state/actions.go` `DeleteAction`, inside the transaction, after the existing loop of `DELETE` statements and before `tx.Commit()`, add:
```go
	for _, key := range []string{settingPendingDeletesPrefix + id, settingPausedPrefix + id} {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, key); err != nil {
			tx.Rollback()
			return err
		}
	}
```

In `internal/engine/deps.go`, add to the `Repo` interface:
```go
	SavePendingDeletes(actionID string, p *model.PendingDeletes) error
	LoadPendingDeletes(actionID string) (*model.PendingDeletes, error)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -race ./...`
Expected: every package `ok`. If some test fake implements `engine.Repo`, add the two methods to it and say so in the report.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && git add internal
git commit -m "feat(model,state): delete guard settings, rules, pending deletions and pause flag" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Mass-delete guard and ApproveDeletes

**Files:**
- Create: `internal/engine/deleteguard.go`
- Modify: `internal/engine/sync.go`
- Test: `internal/engine/deleteguard_scenarios_test.go`, `internal/engine/deleteguard_test.go`

**Interfaces:**
- Consumes: Task 1 (`PendingDeletes`, `Repo.Save/LoadPendingDeletes`, `OutcomeDeletesPending`, `HistoryApproveDeletes`), and the existing `run`, `syncOnce` and `applyOverrides` in sync.go.
- Produces:
  - `(*Runner).ApproveDeletes(ctx) (model.SyncResult, error)`. It returns an error when nothing is pending.
  - `Sync` and `Resolve` may now return `Outcome == OutcomeDeletesPending` with `PendingDeletes` set, having applied nothing.
  - The unexported `massDeletes(ops []Op, resolved map[string]bool, tracked int, a model.Action) []string`.
- Semantics (spec §13):
  - A run trips the guard when it would delete at least `DeleteGuardMinFiles` paths AND at least `DeleteGuardPercent`% of `len(baseline)`. Deletes on both sides count, and deletions chosen in a conflict resolution are excluded.
  - On a trip it saves `PendingDeletes{Paths, Approved:false}` and returns deletes-pending.
  - `ApproveDeletes` marks the saved set approved and runs a sync.
  - A run applies the deletions only if every path it would delete is in an approved saved set.
  - Pending deletions are cleared when a run plans no tripping deletions, and after a successful apply.
  - Conflicts are checked first.

- [ ] **Step 1: Write the failing unit test**

Create `internal/engine/deleteguard_test.go`:
```go
package engine

import (
	"reflect"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func TestMassDeletes(t *testing.T) {
	a := model.Action{}
	a.ApplyDefaults() // 5 files and 50%
	ops := func(n int, kind OpKind) []Op {
		var out []Op
		for i := 0; i < n; i++ {
			out = append(out, Op{Path: string(rune('a' + i)), Kind: kind})
		}
		return out
	}
	cases := []struct {
		name    string
		ops     []Op
		tracked int
		want    int
	}{
		{"below min files", ops(4, OpDeleteRemote), 4, 0},
		{"min files and half", ops(5, OpDeleteRemote), 10, 5},
		{"min files below half", ops(5, OpDeleteLocal), 11, 0},
		{"local deletes count", ops(6, OpDeleteLocal), 8, 6},
		{"uploads do not count", ops(9, OpUpload), 9, 0},
	}
	for _, tc := range cases {
		got := massDeletes(tc.ops, nil, tc.tracked, a)
		if len(got) != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	resolved := map[string]bool{"a": true, "b": true}
	if got := massDeletes(ops(6, OpDeleteRemote), resolved, 6, a); got != nil {
		t.Errorf("resolved conflict deletions counted: %v", got)
	}
	got := massDeletes([]Op{{Path: "z", Kind: OpDeleteRemote}, {Path: "a", Kind: OpDeleteRemote}, {Path: "m", Kind: OpDeleteLocal}, {Path: "b", Kind: OpDeleteLocal}, {Path: "c", Kind: OpDeleteLocal}}, nil, 5, a)
	if !reflect.DeepEqual(got, []string{"a", "b", "c", "m", "z"}) {
		t.Errorf("not sorted: %v", got)
	}
	a.DeleteGuardMinFiles = 10
	if got := massDeletes(ops(6, OpDeleteRemote), nil, 6, a); got != nil {
		t.Errorf("per-action min files ignored: %v", got)
	}
}
```

- [ ] **Step 2: Write the failing scenarios**

Create `internal/engine/deleteguard_scenarios_test.go`:
```go
package engine

import (
	"fmt"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func init() {
	scenarios["mass delete waits for approval on each machine"] = scenarioMassDelete
	scenarios["deletes below the guard go through"] = scenarioFewDeletes
	scenarios["new deletions after approval trip the guard again"] = scenarioGuardRetrips
	scenarios["recreated files clear pending deletions"] = scenarioPendingCleared
	scenarios["resolve and approve deletions in either order"] = scenarioResolveThenApprove
}

func snippetPath(i int) string { return fmt.Sprintf("app/snippets/s%02d.json", i) }

func writeSnippets(m *machine, n int) {
	for i := 0; i < n; i++ {
		m.write(snippetPath(i), fmt.Sprintf("snippet %d", i))
	}
}

func syncedSnippets(t *testing.T, f storeFactory, name string, n int) (*harness, *machine, *machine, model.Action) {
	t.Helper()
	h := newHarness(t, f)
	a, b := h.machine("A", ""), h.machine("B", "")
	writeSnippets(a, n)
	act := h.create(a, testAction(name))
	expectCounts(t, a.sync(), n, 0, 0, 0)
	h.join(b, act.ID, defaultPaths)
	expectCounts(t, b.sync(), 0, n, 0, 0)
	return h, a, b, act
}

func scenarioMassDelete(t *testing.T, f storeFactory) {
	h, a, b, act := syncedSnippets(t, f, "massdelete", 8)
	for i := 0; i < 5; i++ {
		a.remove(snippetPath(i))
	}
	res := a.sync()
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 5 {
		t.Fatalf("want 5 pending deletions, got %+v", res)
	}
	expectCounts(t, res, 0, 0, 0, 0)
	if _, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current("snippets/s00.json")); err != nil {
		t.Fatalf("cloud file deleted before approval: %v", err)
	}
	if res := a.sync(); res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("second unapproved sync applied deletions: %+v", res)
	}

	res, err := a.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 5, 0)
	if p, _ := a.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending deletions not cleared: %+v", p)
	}
	if _, err := a.runner.ApproveDeletes(ctx); err == nil {
		t.Fatal("approve with nothing pending accepted")
	}

	// B receives 5 cloud deletions out of 8 synced files: its own guard trips.
	res = b.sync()
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 5 {
		t.Fatalf("B: want 5 pending deletions, got %+v", res)
	}
	if !b.exists(snippetPath(0)) {
		t.Fatal("B deleted a local file before approval")
	}
	res, err = b.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 0, 5)
	if b.exists(snippetPath(0)) {
		t.Fatal("B kept a file after approving its deletion")
	}
}

func scenarioFewDeletes(t *testing.T, f storeFactory) {
	_, a, _, _ := syncedSnippets(t, f, "fewdeletes", 8)
	for i := 0; i < 4; i++ {
		a.remove(snippetPath(i))
	}
	expectCounts(t, a.sync(), 0, 0, 4, 0)

	_, c, _, _ := syncedSnippets(t, f, "quarterdeletes", 20)
	for i := 0; i < 5; i++ {
		c.remove(snippetPath(i))
	}
	expectCounts(t, c.sync(), 0, 0, 5, 0)
}

func scenarioGuardRetrips(t *testing.T, f storeFactory) {
	_, a, _, act := syncedSnippets(t, f, "retrip", 8)
	for i := 0; i < 5; i++ {
		a.remove(snippetPath(i))
	}
	if res := a.sync(); res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("setup: %+v", res)
	}
	a.remove(snippetPath(5))
	res, err := a.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 6 {
		t.Fatalf("want the guard to trip again with 6 paths, got %+v", res)
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p == nil || p.Approved || len(p.Paths) != 6 {
		t.Fatalf("pending after re-trip = %+v", p)
	}
	res, err = a.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 6, 0)
}

func scenarioPendingCleared(t *testing.T, f storeFactory) {
	_, a, _, act := syncedSnippets(t, f, "cleared", 8)
	for i := 0; i < 5; i++ {
		a.remove(snippetPath(i))
	}
	if res := a.sync(); res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("setup: %+v", res)
	}
	writeSnippets(a, 5) // same content as before
	res := a.sync()
	if res.Outcome != model.OutcomeOK {
		t.Fatalf("want ok after recreating the files, got %+v", res)
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending deletions not cleared: %+v", p)
	}
}

func scenarioResolveThenApprove(t *testing.T, f storeFactory) {
	_, a, b, act := syncedSnippets(t, f, "resolveapprove", 8)
	// A conflict on s07 plus five local deletions on B.
	a.write(snippetPath(7), "edit from A")
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	b.write(snippetPath(7), "edit from B!")
	for i := 0; i < 5; i++ {
		b.remove(snippetPath(i))
	}
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("conflicts are checked first, got %+v", res)
	}
	res, err := b.runner.Resolve(ctx, map[string]model.Side{"snippets/s07.json": model.SideLocal})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("resolve must still stop at the delete guard, got %+v", res)
	}
	res, err = b.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeConflict {
		t.Fatalf("approval alone cannot settle the conflict, got %+v", res)
	}
	res, err = b.runner.Resolve(ctx, map[string]model.Side{"snippets/s07.json": model.SideLocal})
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 1, 0, 5, 0)
	if cs, _ := b.runner.Conflicts(); len(cs) != 0 {
		t.Fatalf("conflicts left: %+v", cs)
	}
	if p, _ := b.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending deletions left: %+v", p)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/engine/ -run 'MassDeletes|Scenarios'`
Expected: FAIL to compile (`undefined: massDeletes`, `ApproveDeletes`).

- [ ] **Step 4: Implement**

Create `internal/engine/deleteguard.go`:
```go
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/niceit/s3-anysync/internal/model"
)

// massDeletes returns the paths this run would delete (on either side) when
// they trip the action's delete guard, or nil. Deletions chosen in a conflict
// resolution (resolved) don't count. tracked is the number of synced files
// (the baseline).
func massDeletes(ops []Op, resolved map[string]bool, tracked int, a model.Action) []string {
	var del []string
	for _, op := range ops {
		if (op.Kind == OpDeleteRemote || op.Kind == OpDeleteLocal) && !resolved[op.Path] {
			del = append(del, op.Path)
		}
	}
	if len(del) == 0 || len(del) < a.DeleteGuardMinFiles || len(del)*100 < a.DeleteGuardPercent*tracked {
		return nil
	}
	sort.Strings(del)
	return del
}

// covers reports whether every path in del is in the approved pending set.
func covers(p *model.PendingDeletes, del []string) bool {
	if p == nil || !p.Approved {
		return false
	}
	ok := make(map[string]bool, len(p.Paths))
	for _, x := range p.Paths {
		ok[x] = true
	}
	for _, x := range del {
		if !ok[x] {
			return false
		}
	}
	return true
}

// ApproveDeletes approves the deletions held back by the delete guard and
// runs a sync. The approval covers exactly the saved paths and lasts until a
// run applies them; a run that would delete more trips the guard again.
func (r *Runner) ApproveDeletes(ctx context.Context) (model.SyncResult, error) {
	p, err := r.d.Repo.LoadPendingDeletes(r.action.ID)
	if err != nil {
		return model.SyncResult{}, fmt.Errorf("load pending deletions: %w", err)
	}
	if p == nil {
		return model.SyncResult{}, errors.New("no deletions are waiting for approval")
	}
	p.Approved = true
	if err := r.d.Repo.SavePendingDeletes(r.action.ID, p); err != nil {
		return model.SyncResult{}, err
	}
	return r.run(ctx, model.HistoryApproveDeletes, nil)
}
```

In `internal/engine/sync.go` `syncOnce`, right after the block that returns `OutcomeConflict` when `len(conflicts) > 0`, insert:
```go
	resolved := map[string]bool{}
	if overrides != nil {
		for p := range conflictOps {
			resolved[p] = true
		}
	}
	pending, err := r.d.Repo.LoadPendingDeletes(id)
	if err != nil {
		return model.SyncResult{}, false, fmt.Errorf("load pending deletions: %w", err)
	}
	if del := massDeletes(ops, resolved, len(baseline), r.action); del == nil {
		if pending != nil {
			if err := r.d.Repo.SavePendingDeletes(id, nil); err != nil {
				return model.SyncResult{}, false, err
			}
			pending = nil
		}
	} else if !covers(pending, del) {
		if err := r.d.Repo.SavePendingDeletes(id, &model.PendingDeletes{Paths: del, DetectedAt: now}); err != nil {
			return model.SyncResult{}, false, fmt.Errorf("save pending deletions: %w", err)
		}
		return model.SyncResult{Outcome: model.OutcomeDeletesPending, PendingDeletes: del}, false, nil
	}
```
After `res, err := r.apply(...)` succeeds (`err == nil`), and before saving the kept conflicts, add:
```go
	if pending != nil {
		if err := r.d.Repo.SavePendingDeletes(id, nil); err != nil {
			return res, false, fmt.Errorf("clear pending deletions: %w", err)
		}
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/engine/`
Expected: `ok`. Every pre-existing scenario still passes, because they delete at most 1–2 files.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && git add internal
git commit -m "feat(engine): mass-delete guard with explicit approval" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Split snapshot retention and deletions recorded in safety snapshots

**Files:**
- Modify: `internal/engine/snapshot.go`, `internal/engine/sync.go` (`guard`), `internal/engine/runner.go` (cache field)
- Test: `internal/engine/retention_test.go` (new); adapt the existing tests in `internal/engine/snapshot_test.go`

**Interfaces:**
- Consumes: the existing `writeSnapshot`, `writeSnapshotProtecting`, `applyRetention`, `applyRetentionKeeping`, `deleteSnapshot`, `snapshotInfo`, and Task 1's `SnapshotInfo.Deletes`.
- Produces:
  - Constants `AutoSnapshotKeep = 50`, `AutoSnapshotMinAge = 7*24h`, `DeleteSnapshotMinAge = 30*24h`.
  - The unexported `snapshotSpec{reason, files, deletes, protect}` and `writeSnapshotSpec(ctx, snapshotSpec)`. `writeSnapshot` and `writeSnapshotProtecting` keep their signatures and wrap it.
  - A per-Runner cache of snapshot kinds.
  - `guard` records `OpDeleteRemote` paths in the snapshot's `Deletes`.
- Rules (spec §13):
  - Manual snapshots (reason `manual`): keep the newest `SnapshotRetention`, with protected ones counting toward it.
  - Automatic snapshots (any other reason, or an unreadable index): keep if among the newest `AutoSnapshotKeep` automatic ones, OR younger than `AutoSnapshotMinAge`, OR (it has deletions and is younger than `DeleteSnapshotMinAge`).
  - Age is measured from the timestamp in the name. Protected snapshots are never deleted. Orphans are swept as before.
  - `SnapshotRetention < 1` keeps everything, including automatic snapshots and orphans, as before.
  - A transient error while reading an index aborts the retention pass without deleting anything.

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/retention_test.go`:
```go
package engine

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func retentionRunner(t *testing.T, manualKeep int) (*Runner, *storage.Memory, *clock) {
	t.Helper()
	mem := storage.NewMemory()
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	d := memDeps(mem, "A")
	d.Now = c.now
	a := basicAction("ret")
	a.SnapshotRetention = manualKeep
	l := NewLayout("root", a.ID)
	mem.Put(ctx, l.Current("settings.json"), []byte("x"), map[string]string{metaSHA256: "h"})
	return NewRunner(d, "root", a), mem, c
}

func countSnapshots(t *testing.T, r *Runner) (manual, auto int) {
	t.Helper()
	list, err := r.ListSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Reason == ReasonManual {
			manual++
		} else {
			auto++
		}
	}
	return manual, auto
}

var oneFile = []model.FileEntry{{Path: "settings.json", Hash: "h"}}

func TestRetentionManualAndAutomaticAreSeparate(t *testing.T) {
	r, _, _ := retentionRunner(t, 2)
	for i := 0; i < 3; i++ {
		if _, err := r.writeSnapshot(ctx, ReasonManual, oneFile); err != nil {
			t.Fatal(err)
		}
		if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
			t.Fatal(err)
		}
	}
	if m, a := countSnapshots(t, r); m != 2 || a != 3 {
		t.Fatalf("manual=%d auto=%d, want 2 and 3", m, a)
	}
}

func TestRetentionAutomaticNewest50OrYoungerThan7Days(t *testing.T) {
	r, _, c := retentionRunner(t, 20)
	for i := 0; i < 55; i++ {
		if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
			t.Fatal(err)
		}
	}
	if _, a := countSnapshots(t, r); a != 55 {
		t.Fatalf("young automatic snapshots pruned: %d left", a)
	}
	c.set(c.now().Add(8 * 24 * time.Hour))
	if _, err := r.writeSnapshot(ctx, ReasonPreRestore, oneFile); err != nil {
		t.Fatal(err)
	}
	if _, a := countSnapshots(t, r); a != 50 {
		t.Fatalf("want the newest 50 automatic snapshots, got %d", a)
	}
}

func TestRetentionKeepsDeletionSnapshots30Days(t *testing.T) {
	r, _, c := retentionRunner(t, 20)
	for i := 0; i < 2; i++ {
		if _, err := r.writeSnapshotSpec(ctx, snapshotSpec{reason: ReasonAutoPreSync, files: oneFile, deletes: []string{"settings.json"}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 50; i++ {
		if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
			t.Fatal(err)
		}
	}
	c.set(c.now().Add(8 * 24 * time.Hour))
	if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListSnapshots(ctx)
	withDeletes := 0
	for _, s := range list {
		if len(s.Deletes) > 0 {
			withDeletes++
		}
	}
	if withDeletes != 2 || len(list) != 52 {
		t.Fatalf("after 8 days: %d snapshots, %d with deletions; want 52 and 2", len(list), withDeletes)
	}
	c.set(c.now().Add(23 * 24 * time.Hour)) // 31 days after the deletion snapshots
	if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
		t.Fatal(err)
	}
	list, _ = r.ListSnapshots(ctx)
	for _, s := range list {
		if len(s.Deletes) > 0 {
			t.Fatalf("deletion snapshot older than 30 days and beyond the newest 50 kept: %s", s.TS)
		}
	}
}

func TestGuardSnapshotRecordsDeletions(t *testing.T) {
	_, a, _, _ := syncedPair(t, memoryStore, testAction("recorddeletes"))
	a.remove("app/snippets/go.json")
	expectCounts(t, a.sync(), 0, 0, 1, 0)
	list, err := a.runner.ListSnapshots(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("snapshots = %+v, %v", list, err)
	}
	if len(list[0].Deletes) != 1 || list[0].Deletes[0] != "snippets/go.json" {
		t.Fatalf("deletes = %v", list[0].Deletes)
	}
	if !strings.HasPrefix(list[0].Reason, "auto") {
		t.Fatalf("reason = %s", list[0].Reason)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/engine/ -run 'Retention|GuardSnapshot'`
Expected: FAIL to compile (`undefined: snapshotSpec`, `writeSnapshotSpec`).

- [ ] **Step 3: Implement**

In `internal/engine/runner.go`, add a field to `Runner`:
```go
	// kinds caches what retention needs to know about each snapshot; snapshot
	// indexes never change, so entries stay valid until the snapshot is deleted.
	kinds map[string]snapKind
```

In `internal/engine/snapshot.go`:

1. Add, after the reason constants:
```go
// Retention of automatic snapshots (every reason except manual).
const (
	AutoSnapshotKeep     = 50
	AutoSnapshotMinAge   = 7 * 24 * time.Hour
	DeleteSnapshotMinAge = 30 * 24 * time.Hour
)

// snapshotSpec describes a snapshot to write.
type snapshotSpec struct {
	reason  string
	files   []model.FileEntry
	deletes []string // files about to be deleted (recorded in the index)
	protect []string // snapshots retention must not delete
}

// snapKind is what retention needs to know about one snapshot.
type snapKind struct {
	manual  bool
	deletes bool
}
```
2. Replace `writeSnapshot` and `writeSnapshotProtecting` with thin wrappers, and rename the body of the old `writeSnapshotProtecting` to `writeSnapshotSpec`:
```go
func (r *Runner) writeSnapshot(ctx context.Context, reason string, files []model.FileEntry) (model.SnapshotInfo, error) {
	return r.writeSnapshotSpec(ctx, snapshotSpec{reason: reason, files: files})
}

func (r *Runner) writeSnapshotProtecting(ctx context.Context, reason string, files []model.FileEntry, extraProtect ...string) (model.SnapshotInfo, error) {
	return r.writeSnapshotSpec(ctx, snapshotSpec{reason: reason, files: files, protect: extraProtect})
}
```
   In `writeSnapshotSpec`, use `spec.reason` and `spec.files`. Build the index with:
```go
	deletes := append([]string(nil), spec.deletes...)
	sort.Strings(deletes)
	info := model.SnapshotInfo{TS: ts, Reason: spec.reason, MachineID: r.d.MachineID, CreatedAt: r.d.now().UTC(), Files: files, Deletes: deletes}
```
   and call `r.applyRetentionKeeping(ctx, append([]string{ts}, spec.protect...)...)`. Keep the existing doc comment from `writeSnapshot` on `writeSnapshotSpec`.
3. Add the kind lookup:
```go
// snapshotKind returns whether a snapshot is manual and records deletions.
// An unreadable or vanished index counts as an automatic snapshot without
// deletions; any other error is returned.
func (r *Runner) snapshotKind(ctx context.Context, ts string) (snapKind, error) {
	if k, ok := r.kinds[ts]; ok {
		return k, nil
	}
	info, err := r.snapshotInfo(ctx, ts)
	if errors.Is(err, errCorruptIndex) || errors.Is(err, storage.ErrNotFound) {
		return snapKind{}, nil
	}
	if err != nil {
		return snapKind{}, err
	}
	k := snapKind{manual: info.Reason == ReasonManual, deletes: len(info.Deletes) > 0}
	if r.kinds == nil {
		r.kinds = map[string]snapKind{}
	}
	r.kinds[ts] = k
	return k, nil
}

func tsTime(ts string) time.Time {
	t, err := time.Parse(snapshotTSLayout, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}
```
4. Replace the body of `applyRetentionKeeping` after the `indexed`/`hasObjects`/`prot` collection with:
```go
	var manual, auto []string
	kinds := map[string]snapKind{}
	for ts := range indexed {
		k, err := r.snapshotKind(ctx, ts)
		if err != nil {
			return fmt.Errorf("read snapshot %s: %w", ts, err)
		}
		kinds[ts] = k
		if k.manual {
			manual = append(manual, ts)
		} else {
			auto = append(auto, ts)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(manual)))
	sort.Sort(sort.Reverse(sort.StringSlice(auto)))
	var doomed []string
	if len(manual) > keep {
		slots := keep
		for _, ts := range manual {
			if prot[ts] {
				slots--
			}
		}
		for _, ts := range manual {
			if prot[ts] {
				continue
			}
			if slots > 0 {
				slots--
				continue
			}
			doomed = append(doomed, ts)
		}
	}
	now := r.d.now().UTC()
	for i, ts := range auto {
		if prot[ts] || i < AutoSnapshotKeep {
			continue
		}
		age := now.Sub(tsTime(ts))
		if age < AutoSnapshotMinAge || (kinds[ts].deletes && age < DeleteSnapshotMinAge) {
			continue
		}
		doomed = append(doomed, ts)
	}
	for ts := range hasObjects {
		if !indexed[ts] && !prot[ts] {
			doomed = append(doomed, ts)
		}
	}
```
   Keep the existing early `if keep < 1 { return nil }`, the sorting of `doomed`, and the delete loop. Update the doc comment of `applyRetention` to describe the two retention rules.
5. In `deleteSnapshot`, after deleting the index, add `delete(r.kinds, ts)`.

In `internal/engine/sync.go` `guard`, collect the deletions and pass them in:
```go
	var deletes []string
	// inside the existing switch:
	case OpUpload, OpDeleteRemote:
		if op.Remote != nil {
			remoteVictims = append(remoteVictims, *op.Remote)
			if op.Kind == OpDeleteRemote {
				deletes = append(deletes, op.Path)
			}
		}
	// and replace the writeSnapshot call with:
	if _, err := r.writeSnapshotSpec(ctx, snapshotSpec{reason: reason, files: remoteVictims, deletes: deletes}); err != nil {
```

- [ ] **Step 4: Adapt the existing retention tests**

Existing tests in `snapshot_test.go`, `restore_test.go` and the scenarios that relied on automatic snapshots being pruned by `SnapshotRetention` must now use `ReasonManual` for the snapshots they expect to be pruned. Keep every assertion's intent, and list each changed test in your report. Tests about orphans, protection, corrupt indexes and failure cleanup keep their meaning. A corrupt index now counts as an automatic snapshot, so a "corrupt index is pruned" expectation holds only when it is older than 7 days and beyond the newest 50. Adjust such a test with a clock (`retentionRunner` pattern) rather than deleting it.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/engine/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && git add internal
git commit -m "feat(engine): separate retention for manual and automatic snapshots; record deletions" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: OS file-name rules, RemotePathFor, ManifestETag and ConflictDetail

**Files:**
- Modify: `internal/engine/mapping.go`, `internal/engine/sync.go`
- Create: `internal/engine/conflictdetail.go`
- Test: `internal/engine/osnames_test.go`, `internal/engine/conflictdetail_test.go`

**Interfaces:**
- Consumes: `Mappings`, `safeRel`, `resolve`, `LocalPathFor`, `Owns`, `decode`, `manifestETag` and `Repo.LoadConflicts`.
- Produces:
  - The package variable `targetOS` (a test seam, default `runtime.GOOS`).
  - `(Mappings).Unsupported(remotePath) bool` and `(Mappings).RemotePathFor(abs) (string, bool)`.
  - `(*Runner).ManifestETag(ctx) (string, error)`.
  - `MaxDiffBytes = 1 << 20` and the `ConflictDetail{Conflict, LocalText, RemoteText, Binary, TooLarge}` type with JSON tags.
  - `(*Runner).ConflictDetail(ctx, path) (ConflictDetail, error)`.
  - `SyncResult.Unsupported` is filled by every sync.

- [ ] **Step 1: Write the failing tests**

Create `internal/engine/osnames_test.go`:
```go
package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func withTargetOS(t *testing.T, goos string) {
	t.Helper()
	old := targetOS
	targetOS = goos
	t.Cleanup(func() { targetOS = old })
}

func TestWindowsNameRules(t *testing.T) {
	ms, err := BuildMappings(mappingAction(), "M", identity)
	if err != nil {
		t.Fatal(err)
	}
	withTargetOS(t, "windows")
	bad := []string{"snippets/what?.json", "snippets/a<b", "snippets/x|y", `snippets/q"q`, "snippets/star*", "snippets/CON", "snippets/nul.txt", "snippets/con.d/x", "snippets/Com1.json", "snippets/trail.", "snippets/space ", "snippets/LPT9/x.json"}
	for _, p := range bad {
		if _, ok := ms.LocalPathFor(p); ok {
			t.Errorf("windows accepted %q", p)
		}
		if !ms.Unsupported(p) {
			t.Errorf("Unsupported(%q) = false", p)
		}
	}
	for _, p := range []string{"snippets/ok.json", "snippets/console/x", "snippets/nul2.txt", "settings.json"} {
		if _, ok := ms.LocalPathFor(p); !ok {
			t.Errorf("windows rejected %q", p)
		}
		if ms.Unsupported(p) {
			t.Errorf("Unsupported(%q) = true", p)
		}
	}
	if ms.Unsupported("snippets/x.log") || ms.Unsupported("other/x") {
		t.Error("excluded or unmapped paths reported as unsupported")
	}
	withTargetOS(t, "darwin")
	if _, ok := ms.LocalPathFor("snippets/what?.json"); !ok {
		t.Error("darwin rejected a name that is valid there")
	}
}

func TestUnsupportedNamesAreReportedAndNeverDeleted(t *testing.T) {
	h := newHarness(t, memoryStore)
	a, b := h.machine("A", ""), h.machine("B", "")
	a.write("app/snippets/what?.json", "odd name")
	a.write("app/snippets/ok.json", "ok")
	act := h.create(a, testAction("osnames"))
	expectCounts(t, a.sync(), 2, 0, 0, 0)
	h.join(b, act.ID, defaultPaths)

	withTargetOS(t, "windows")
	res := b.sync()
	expectCounts(t, res, 0, 1, 0, 0)
	if len(res.Unsupported) != 1 || res.Unsupported[0] != "snippets/what?.json" {
		t.Fatalf("unsupported = %v", res.Unsupported)
	}
	if res := b.sync(); res.DeletedRemote != 0 || len(res.Unsupported) != 1 {
		t.Fatalf("second sync = %+v", res)
	}
	withTargetOS(t, "darwin")
	if _, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current("snippets/what?.json")); err != nil {
		t.Fatalf("cloud copy removed: %v", err)
	}
	expectCounts(t, a.sync(), 0, 0, 0, 0)
}

func TestRemotePathFor(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	a := model.Action{ID: "a", Name: "a", Excludes: []string{"*.log"}, Items: []model.Item{
		{ID: "f", RemoteName: "settings.json", Type: model.ItemFile, LocalPaths: map[string]string{"M": filepath.Join(base, "settings.json")}},
		{ID: "d", RemoteName: "snippets", Type: model.ItemDir, LocalPaths: map[string]string{"M": link}},
	}}
	a.ApplyDefaults()
	ms, err := BuildMappings(a, "M", func(p string) (string, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(base, "settings.json"): "settings.json",
		filepath.Join(link, "a", "b.json"):   "snippets/a/b.json",
		filepath.Join(real, "c.json"):        "snippets/c.json",
	}
	for abs, want := range cases {
		if got, ok := ms.RemotePathFor(abs); !ok || got != want {
			t.Errorf("RemotePathFor(%s) = %q, %v; want %q", abs, got, ok, want)
		}
	}
	for _, abs := range []string{filepath.Join(real, "x.log"), filepath.Join(base, "other.json"), real, link, base} {
		if got, ok := ms.RemotePathFor(abs); ok {
			t.Errorf("RemotePathFor(%s) = %q, want not owned", abs, got)
		}
	}
}
```

Create `internal/engine/conflictdetail_test.go`:
```go
package engine

import (
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func conflictedPair(t *testing.T, act model.Action, aPass, bPass, aContent, bContent string) (*harness, *machine, *machine) {
	t.Helper()
	h := newHarness(t, memoryStore)
	a, b := h.machine("A", aPass), h.machine("B", bPass)
	a.write("app/settings.json", `{"v":1}`)
	act = h.create(a, act)
	a.sync()
	h.join(b, act.ID, defaultPaths)
	b.sync()
	a.write("app/settings.json", aContent)
	a.sync()
	b.write("app/settings.json", bContent)
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("setup: %+v", res)
	}
	return h, a, b
}

func TestConflictDetailText(t *testing.T) {
	_, _, b := conflictedPair(t, testAction("detail"), "", "", `{"v":"A"}`, `{"v":"B!"}`)
	d, err := b.runner.ConflictDetail(ctx, "settings.json")
	if err != nil {
		t.Fatal(err)
	}
	if d.LocalText != `{"v":"B!"}` || d.RemoteText != `{"v":"A"}` || d.Binary || d.TooLarge || d.Conflict.Path != "settings.json" {
		t.Fatalf("detail = %+v", d)
	}
	if _, err := b.runner.ConflictDetail(ctx, "nope.json"); err == nil || !strings.Contains(err.Error(), "not a conflict") {
		t.Fatalf("unknown path: %v", err)
	}
	if etag, err := b.runner.ManifestETag(ctx); err != nil || etag == "" {
		t.Fatalf("ManifestETag = %q, %v", etag, err)
	}
}

func TestConflictDetailEncryptedAndBinary(t *testing.T) {
	act := testAction("detailenc")
	act.Encrypt = true
	_, _, b := conflictedPair(t, act, "pw", "pw", "plain A", "plain BB")
	d, err := b.runner.ConflictDetail(ctx, "settings.json")
	if err != nil || d.RemoteText != "plain A" || d.LocalText != "plain BB" {
		t.Fatalf("encrypted detail = %+v, %v", d, err)
	}
	_, _, c := conflictedPair(t, testAction("detailbin"), "", "", "\xff\xfe A", "\xff\xfe BB")
	d, err = c.runner.ConflictDetail(ctx, "settings.json")
	if err != nil || !d.Binary || d.LocalText != "" || d.RemoteText != "" {
		t.Fatalf("binary detail = %+v, %v", d, err)
	}
}

func TestConflictDetailTooLarge(t *testing.T) {
	big := strings.Repeat("x", MaxDiffBytes+1)
	_, _, b := conflictedPair(t, testAction("detailbig"), "", "", big, big+"y")
	d, err := b.runner.ConflictDetail(ctx, "settings.json")
	if err != nil || !d.TooLarge || d.LocalText != "" || d.RemoteText != "" {
		t.Fatalf("large detail: TooLarge=%v err=%v", d.TooLarge, err)
	}
}

func TestConflictDetailCloudChangedSince(t *testing.T) {
	_, a, b := conflictedPair(t, testAction("detailstale"), "", "", `{"v":"A"}`, `{"v":"B!"}`)
	a.write("app/settings.json", `{"v":"A again"}`)
	a.sync()
	if _, err := b.runner.ConflictDetail(ctx, "settings.json"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("want a 'changed since' error, got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/engine/ -run 'WindowsName|Unsupported|RemotePathFor|ConflictDetail'`
Expected: FAIL to compile (`undefined: targetOS`, and similar).

- [ ] **Step 3: Implement the mapping changes**

In `internal/engine/mapping.go` (add `"runtime"` to the imports):
```go
// targetOS is the OS whose file-name rules apply to local paths (a test seam).
var targetOS = runtime.GOOS

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// localNameOK reports whether every segment of rel can be a file name on targetOS.
func localNameOK(rel string) bool {
	if targetOS != "windows" {
		return true
	}
	for _, seg := range strings.Split(rel, "/") {
		if strings.ContainsAny(seg, `<>:"|?*`) || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return false
		}
		base := strings.ToUpper(seg)
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if windowsReserved[base] {
			return false
		}
	}
	return true
}
```
Refactor `LocalPathFor` into a status lookup that `Owns` and `Unsupported` share. Keep the existing safety checks exactly as they are:
```go
type lookupStatus int

const (
	notMapped lookupStatus = iota
	excluded
	unsupportedName
	owned
)

func (ms Mappings) lookup(remotePath string) (string, lookupStatus) {
	m, rel, ok := ms.resolve(remotePath)
	if !ok {
		return "", notMapped
	}
	if m.matcher.MatchPath(rel) {
		return "", excluded
	}
	if !localNameOK(rel) {
		return "", unsupportedName
	}
	if m.Type == model.ItemFile {
		return m.LocalPath, owned
	}
	return filepath.Join(m.LocalPath, filepath.FromSlash(rel)), owned
}

// LocalPathFor returns where a remote path lives on this machine. ok is true
// only for paths this machine owns: mapped, not excluded, and a valid name on
// this OS. It is safe to use as the only gate before reading or writing.
func (ms Mappings) LocalPathFor(remotePath string) (string, bool) {
	p, st := ms.lookup(remotePath)
	return p, st == owned
}

// Unsupported reports whether a remote path belongs to a mapped item and is
// not excluded, but cannot exist on this OS.
func (ms Mappings) Unsupported(remotePath string) bool {
	_, st := ms.lookup(remotePath)
	return st == unsupportedName
}
```
`Owns` stays `_, ok := ms.LocalPathFor(p); return ok`. `resolve` keeps its nil-matcher and RemoteName guards, so `lookup` never sees a nil matcher.

Add `RemotePathFor`:
```go
// RemotePathFor maps a local path back to the remote path it syncs to, when
// this machine owns it (used to filter file-watcher events). A symlinked
// root matches both as configured and as resolved. Item roots themselves
// map to nothing.
func (ms Mappings) RemotePathFor(abs string) (string, bool) {
	abs = filepath.Clean(abs)
	for _, m := range ms {
		roots := []string{m.LocalPath}
		if real, err := filepath.EvalSymlinks(m.LocalPath); err == nil && real != m.LocalPath {
			roots = append(roots, real)
		}
		for _, root := range roots {
			var key string
			switch m.Type {
			case model.ItemFile:
				if abs != root {
					continue
				}
				key = m.RemoteName
			case model.ItemDir:
				rel, err := filepath.Rel(root, abs)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue
				}
				key = m.RemoteName + "/" + filepath.ToSlash(rel)
			}
			if ms.Owns(key) {
				return key, true
			}
		}
	}
	return "", false
}
```

In `internal/engine/sync.go` `syncOnce`, right after `remote := filterOwned(manifest.Files, ms)`, add:
```go
	var unsupported []string
	for p := range manifest.Files {
		if ms.Unsupported(p) {
			unsupported = append(unsupported, p)
		}
	}
	sort.Strings(unsupported)
```
Set `Unsupported: unsupported` on every `model.SyncResult` that `syncOnce` returns (conflict, deletes-pending and ok results). For the ok path, set `res.Unsupported = unsupported` before returning.

- [ ] **Step 4: Implement ConflictDetail and ManifestETag**

Create `internal/engine/conflictdetail.go`:
```go
package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"unicode/utf8"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// MaxDiffBytes is the largest side ConflictDetail returns as text.
const MaxDiffBytes = 1 << 20

// ConflictDetail is a saved conflict with both sides' contents for a diff view.
type ConflictDetail struct {
	Conflict   model.Conflict `json:"conflict"`
	LocalText  string         `json:"localText"`
	RemoteText string         `json:"remoteText"`
	// Binary: a side is not valid UTF-8 (no text returned).
	Binary bool `json:"binary"`
	// TooLarge: a side is larger than MaxDiffBytes (no text returned).
	TooLarge bool `json:"tooLarge"`
}

// ManifestETag returns the ETag of the action's manifest ("" if none yet);
// a remote poller compares it to notice changes made by other machines.
func (r *Runner) ManifestETag(ctx context.Context) (string, error) {
	return manifestETag(ctx, r.d.Storage, r.layout)
}

// ConflictDetail returns a saved conflict with both sides' contents. A nil
// side (deleted) has empty text. It fails if the cloud copy changed since the
// conflict was detected.
func (r *Runner) ConflictDetail(ctx context.Context, p string) (ConflictDetail, error) {
	saved, err := r.d.Repo.LoadConflicts(r.action.ID)
	if err != nil {
		return ConflictDetail{}, err
	}
	var c *model.Conflict
	for i := range saved {
		if saved[i].Path == p {
			c = &saved[i]
		}
	}
	if c == nil {
		return ConflictDetail{}, fmt.Errorf("%s is not a conflict of this action", p)
	}
	d := ConflictDetail{Conflict: *c}
	if (c.Local != nil && c.Local.Size > MaxDiffBytes) || (c.Remote != nil && c.Remote.Size > MaxDiffBytes) {
		d.TooLarge = true
		return d, nil
	}
	var local, remote []byte
	if c.Local != nil {
		ms, err := BuildMappings(r.action, r.d.MachineID, r.d.expand)
		if err != nil {
			return d, err
		}
		if abs, ok := ms.LocalPathFor(p); ok {
			local, err = os.ReadFile(abs)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return d, err
			}
		}
	}
	if c.Remote != nil {
		body, _, err := r.d.Storage.Get(ctx, r.layout.Current(p))
		if errors.Is(err, storage.ErrNotFound) {
			return d, fmt.Errorf("%s: the cloud copy changed since the conflict was detected; sync again", p)
		}
		if err != nil {
			return d, err
		}
		if remote, err = r.decode(p, body, c.Remote.Hash); err != nil {
			if errors.Is(err, ErrDecrypt) {
				return d, err
			}
			return d, fmt.Errorf("%s: the cloud copy changed since the conflict was detected; sync again: %w", p, err)
		}
	}
	if len(local) > MaxDiffBytes || len(remote) > MaxDiffBytes {
		d.TooLarge = true
		return d, nil
	}
	if !utf8.Valid(local) || !utf8.Valid(remote) {
		d.Binary = true
		return d, nil
	}
	d.LocalText, d.RemoteText = string(local), string(remote)
	return d, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/engine/`, then `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/engine/`
Expected: `ok`, and the vet run is clean.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && git add internal
git commit -m "feat(engine): skip names this OS can't hold, map watcher paths, conflict detail" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Do not release a lock that was lost

**Files:**
- Modify: `internal/storage/lock.go` (`Hold`'s release func)
- Test: `internal/storage/lock_test.go`

**Interfaces:**
- Consumes: `Locker.Hold` and `ErrLockLost`.
- Produces: when the held context's cause is `ErrLockLost`, the release func skips the remote `Release`. The lock is no longer ours, and releasing could block on a hung store or race a new owner.

- [ ] **Step 1: Write the failing test**

Append to `internal/storage/lock_test.go`:
```go
// hangingGets blocks every Get until its context ends, once hang is set.
type hangingGets struct {
	*Memory
	mu   sync.Mutex
	hang bool
}

func (h *hangingGets) Get(ctx context.Context, key string) ([]byte, Object, error) {
	h.mu.Lock()
	hang := h.hang
	h.mu.Unlock()
	if hang {
		<-ctx.Done()
		return nil, Object{}, ctx.Err()
	}
	return h.Memory.Get(ctx, key)
}

func TestReleaseAfterLossDoesNotTouchTheStore(t *testing.T) {
	store := &hangingGets{Memory: NewMemory()}
	l := &Locker{Store: store, Key: "x/.meta/lock", MachineID: "A", TTL: 300 * time.Millisecond, RenewEvery: 20 * time.Millisecond, Mode: LockConditional}
	held, release, err := l.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.hang = true // renewals now hang until the watchdog gives up
	store.mu.Unlock()
	select {
	case <-held.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("lock loss not detected")
	}
	if !errors.Is(context.Cause(held), ErrLockLost) {
		t.Fatalf("cause = %v", context.Cause(held))
	}
	start := time.Now()
	release()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("release blocked %v on a lost lock", d)
	}
}
```
(Add `"sync"` to the test file's imports if it is not already there.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/storage/ -run ReleaseAfterLoss -v`
Expected: FAIL with "release blocked 15s on a lost lock", because the current release always calls `Release` with a 15s timeout.

- [ ] **Step 3: Implement**

In `Hold`'s release func, right after `wg.Wait()`:
```go
			if errors.Is(context.Cause(held), ErrLockLost) {
				// The lock is no longer ours: releasing could only block on a
				// broken store or race the machine that took it over.
				return
			}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/storage/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal && git add internal
git commit -m "fix(storage): never release a lock that was lost" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Schedule compilation

**Files:**
- Create: `internal/schedule/schedule.go`
- Test: `internal/schedule/schedule_test.go`

**Interfaces:**
- Consumes: `model.Schedule` and the `model.Schedule*` kind constants.
- Produces:
  - `schedule.Compile(model.Schedule) (cron.Schedule, error)`, which returns `nil, nil` for kind `none` or empty.
  - `schedule.Validate(model.Schedule) error`.
  - `schedule.NextRuns(s model.Schedule, from time.Time, n int) ([]time.Time, error)`, which the GUI uses to preview the next runs.
  - All times are in the `from` time's location (local time in production).

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/robfig/cron/v3@v3.0.1
```

- [ ] **Step 2: Write the failing tests**

Create `internal/schedule/schedule_test.go`:
```go
package schedule

import (
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

func TestCompile(t *testing.T) {
	from := time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local) // a Thursday
	cases := []struct {
		name string
		s    model.Schedule
		want time.Time
	}{
		{"interval", model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: 15}, from.Add(15 * time.Minute)},
		{"daily later today", model.Schedule{Kind: model.ScheduleDaily, At: "09:30"}, time.Date(2026, 10, 1, 9, 30, 0, 0, time.Local)},
		{"daily tomorrow", model.Schedule{Kind: model.ScheduleDaily, At: "07:00"}, time.Date(2026, 10, 2, 7, 0, 0, 0, time.Local)},
		{"weekly monday", model.Schedule{Kind: model.ScheduleWeekly, Weekday: 1, At: "10:00"}, time.Date(2026, 10, 5, 10, 0, 0, 0, time.Local)},
		{"cron", model.Schedule{Kind: model.ScheduleCron, Cron: "*/10 * * * *"}, from.Add(10 * time.Minute)},
		{"cron descriptor", model.Schedule{Kind: model.ScheduleCron, Cron: "@every 1s"}, from.Add(time.Second)},
	}
	for _, tc := range cases {
		s, err := Compile(tc.s)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := s.Next(from); !got.Equal(tc.want) {
			t.Errorf("%s: next = %v, want %v", tc.name, got, tc.want)
		}
	}
	for _, none := range []model.Schedule{{}, {Kind: model.ScheduleNone}} {
		if s, err := Compile(none); s != nil || err != nil {
			t.Errorf("none: %v %v", s, err)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	bad := []model.Schedule{
		{Kind: model.ScheduleInterval},
		{Kind: model.ScheduleDaily, At: "25:00"},
		{Kind: model.ScheduleDaily, At: "9:30"},
		{Kind: model.ScheduleDaily},
		{Kind: model.ScheduleWeekly, Weekday: 7, At: "10:00"},
		{Kind: model.ScheduleCron, Cron: "not a cron"},
		{Kind: "hourly"},
	}
	for _, s := range bad {
		if err := Validate(s); err == nil {
			t.Errorf("Validate(%+v) = nil", s)
		}
	}
}

func TestNextRuns(t *testing.T) {
	from := time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local)
	runs, err := NextRuns(model.Schedule{Kind: model.ScheduleDaily, At: "09:30"}, from, 3)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	for i, r := range runs {
		want := time.Date(2026, 10, 1+i, 9, 30, 0, 0, time.Local)
		if !r.Equal(want) {
			t.Errorf("run %d = %v, want %v", i, r, want)
		}
	}
	if runs, err := NextRuns(model.Schedule{Kind: model.ScheduleNone}, from, 3); err != nil || len(runs) != 0 {
		t.Fatalf("none: %v %v", runs, err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/schedule/`
Expected: FAIL with `undefined: Compile`.

- [ ] **Step 4: Implement**

Create `internal/schedule/schedule.go`:
```go
// Package schedule turns an action's schedule settings into cron schedules.
// Times are interpreted in the location of the time passed to Next (local
// time in the agent).
package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/niceit/s3-anysync/internal/model"
)

// Compile returns the schedule for s, or nil for no schedule.
func Compile(s model.Schedule) (cron.Schedule, error) {
	switch s.Kind {
	case "", model.ScheduleNone:
		return nil, nil
	case model.ScheduleInterval:
		if s.EveryMinutes < 1 {
			return nil, errors.New("schedule: every-minutes must be at least 1")
		}
		return cron.Every(time.Duration(s.EveryMinutes) * time.Minute), nil
	case model.ScheduleDaily:
		h, m, err := parseAt(s.At)
		if err != nil {
			return nil, err
		}
		return cron.ParseStandard(fmt.Sprintf("%d %d * * *", m, h))
	case model.ScheduleWeekly:
		if s.Weekday < 0 || s.Weekday > 6 {
			return nil, fmt.Errorf("schedule: weekday must be 0 (Sunday) to 6, got %d", s.Weekday)
		}
		h, m, err := parseAt(s.At)
		if err != nil {
			return nil, err
		}
		return cron.ParseStandard(fmt.Sprintf("%d %d * * %d", m, h, s.Weekday))
	case model.ScheduleCron:
		sched, err := cron.ParseStandard(strings.TrimSpace(s.Cron))
		if err != nil {
			return nil, fmt.Errorf("schedule: %w", err)
		}
		return sched, nil
	}
	return nil, fmt.Errorf("schedule: unknown kind %q", s.Kind)
}

func parseAt(at string) (int, int, error) {
	t, err := time.Parse("15:04", at)
	if err != nil {
		return 0, 0, fmt.Errorf("schedule: time must be HH:MM (24h), got %q", at)
	}
	return t.Hour(), t.Minute(), nil
}

// Validate reports whether s can be compiled.
func Validate(s model.Schedule) error {
	_, err := Compile(s)
	return err
}

// NextRuns returns the next n run times after from (none for no schedule).
func NextRuns(s model.Schedule, from time.Time, n int) ([]time.Time, error) {
	sched, err := Compile(s)
	if err != nil || sched == nil {
		return nil, err
	}
	out := make([]time.Time, 0, n)
	t := from
	for i := 0; i < n; i++ {
		t = sched.Next(t)
		out = append(out, t)
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/schedule/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && go mod tidy && git add go.mod go.sum internal/schedule
git commit -m "feat(schedule): compile and preview action schedules" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: App operations for rules, items, leaving and agent settings

**Files:**
- Create: `internal/app/manage.go`
- Modify: `internal/app/app.go` (`MapItem` uses the shared helper)
- Test: `internal/app/manage_test.go`

**Interfaces:**
- Consumes: `updateShared`, `dropItemBaseline`, `model.Rules`, `schedule.Validate`, and Task 1's state methods.
- Produces these `*App` methods:
  - `Action(id) (model.Action, error)` and `Profile(id) (model.Profile, error)`
  - `UpdateRules(ctx, id, model.Rules) (model.Action, error)`
  - `UnmapItem(ctx, id, itemID) error` and `RemoveItem(ctx, id, itemID) error`
  - `LeaveAction(ctx, id, force bool) error`
  - `History(id, limit) ([]model.HistoryEntry, error)`, where `limit <= 0` means 50
  - `Conflicts(id) ([]model.Conflict, error)` and `PendingDeletes(id) (*model.PendingDeletes, error)`
  - `SetPaused(id, bool) error` and `Paused(id) (bool, error)`
  - `LogLevel() (string, error)`, default `"info"`, and `SetLogLevel(string) error`, which accepts debug, info, warn and error
  - The unexported `dropBaselineOfItem(actionID, itemID) error`

- [ ] **Step 1: Write the failing tests**

Create `internal/app/manage_test.go`:
```go
package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

// manageFixture: machine A with a synced action holding one file item and one dir item.
func manageFixture(t *testing.T, encrypt bool) (*App, *storage.Memory, model.Action, model.Item, model.Item, string) {
	t.Helper()
	store := storage.NewMemory()
	a := newTestApp(t, store)
	p, err := a.AddProfile(model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, "s")
	if err != nil {
		t.Fatal(err)
	}
	pass := ""
	if encrypt {
		pass = "pw"
	}
	act, err := a.CreateAction(ctx, p.ID, model.Action{Name: "Editor", PropagateDeletes: true, Encrypt: encrypt}, pass)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}"), 0o644)
	os.MkdirAll(filepath.Join(dir, "snippets"), 0o755)
	os.WriteFile(filepath.Join(dir, "snippets", "a.json"), []byte("a"), 0o644)
	file, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "settings.json", Type: model.ItemFile}, filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "snippets", Type: model.ItemDir}, filepath.Join(dir, "snippets"))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Runner(act.ID)
	if res, err := r.Sync(ctx); err != nil || res.Uploaded != 2 {
		t.Fatalf("setup sync = %+v, %v", res, err)
	}
	return a, store, act, file, folder, dir
}

func TestUpdateRules(t *testing.T) {
	a, store, act, _, _, _ := manageFixture(t, false)
	rules := act.Rules()
	rules.Name = "Editor settings"
	rules.Triggers.OnChange = true
	rules.Triggers.Schedule = model.Schedule{Kind: model.ScheduleDaily, At: "09:30"}
	rules.Excludes = []string{"*.log"}
	updated, err := a.UpdateRules(ctx, act.ID, rules)
	if err != nil || updated.Name != "Editor settings" || !updated.Triggers.OnChange {
		t.Fatalf("UpdateRules = %+v, %v", updated, err)
	}
	shared, err := engine.FetchAction(ctx, store, "", act.ID)
	if err != nil || shared.Name != "Editor settings" || shared.Excludes[0] != "*.log" {
		t.Fatalf("bucket copy = %+v, %v", shared, err)
	}
	if local, _ := a.Action(act.ID); local.Triggers.Schedule.At != "09:30" {
		t.Fatalf("local copy = %+v", local.Triggers)
	}
	rules.Triggers.Schedule = model.Schedule{Kind: model.ScheduleDaily, At: "9:30"}
	rules.Name = "Bad"
	if _, err := a.UpdateRules(ctx, act.ID, rules); err == nil {
		t.Fatal("invalid schedule accepted")
	}
	if shared, _ := engine.FetchAction(ctx, store, "", act.ID); shared.Name != "Editor settings" {
		t.Fatal("an invalid update was published")
	}
}

func TestUnmapAndRemoveItem(t *testing.T) {
	a, store, act, file, folder, _ := manageFixture(t, false)
	if err := a.UnmapItem(ctx, act.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	base, err := a.DB.LoadBaseline(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := base["snippets/a.json"]; ok {
		t.Fatal("baseline of the unmapped item kept")
	}
	local, _ := a.Action(act.ID)
	for _, it := range local.Items {
		if it.ID == folder.ID && it.LocalPaths[a.MachineID] != "" {
			t.Fatal("path still mapped")
		}
	}
	r, _ := a.Runner(act.ID)
	if res, err := r.Sync(ctx); err != nil || res.DeletedRemote != 0 {
		t.Fatalf("sync after unmap = %+v, %v", res, err)
	}
	if _, err := store.Head(ctx, engine.NewLayout("", act.ID).Current("snippets/a.json")); err != nil {
		t.Fatal("cloud copy of the unmapped item removed")
	}

	if err := a.RemoveItem(ctx, act.ID, file.ID); err != nil {
		t.Fatal(err)
	}
	shared, _ := engine.FetchAction(ctx, store, "", act.ID)
	if len(shared.Items) != 1 || shared.Items[0].ID != folder.ID {
		t.Fatalf("items after remove = %+v", shared.Items)
	}
	if err := a.RemoveItem(ctx, act.ID, "nope"); err == nil {
		t.Fatal("removing an unknown item accepted")
	}
	if err := a.UnmapItem(ctx, act.ID, "nope"); err == nil {
		t.Fatal("unmapping an unknown item accepted")
	}
}

func TestLeaveAction(t *testing.T) {
	a, store, act, _, _, _ := manageFixture(t, true)
	if err := a.SetPaused(act.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := a.LeaveAction(ctx, act.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Action(act.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("local action kept: %v", err)
	}
	if _, err := a.Secrets.Get(secrets.ActionPassphraseKey(act.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("passphrase kept")
	}
	if p, _ := a.Paused(act.ID); p {
		t.Fatal("pause flag kept")
	}
	shared, err := engine.FetchAction(ctx, store, "", act.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := shared.Machines[a.MachineID]; ok {
		t.Fatal("machine still listed in the shared action")
	}
	for _, it := range shared.Items {
		if it.LocalPaths[a.MachineID] != "" {
			t.Fatal("machine path still in the shared action")
		}
	}
	if _, err := store.Head(ctx, engine.NewLayout("", act.ID).Current("settings.json")); err != nil {
		t.Fatal("cloud files removed by leaving")
	}
}

func TestLeaveActionOffline(t *testing.T) {
	a, _, act, _, _, _ := manageFixture(t, false)
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return nil, errors.New("offline") }
	if err := a.LeaveAction(ctx, act.ID, false); err == nil {
		t.Fatal("leave without force succeeded offline")
	}
	if _, err := a.Action(act.ID); err != nil {
		t.Fatal("failed leave removed the local action")
	}
	if err := a.LeaveAction(ctx, act.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Action(act.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("forced leave kept the local action")
	}
}

func TestHistoryLogLevelAndPause(t *testing.T) {
	a, _, act, _, _, _ := manageFixture(t, false)
	h, err := a.History(act.ID, 0)
	if err != nil || len(h) == 0 || h[0].Kind != model.HistorySync {
		t.Fatalf("history = %+v, %v", h, err)
	}
	if lvl, err := a.LogLevel(); err != nil || lvl != "info" {
		t.Fatalf("default level = %q, %v", lvl, err)
	}
	if err := a.SetLogLevel("debug"); err != nil {
		t.Fatal(err)
	}
	if lvl, _ := a.LogLevel(); lvl != "debug" {
		t.Fatalf("level = %q", lvl)
	}
	if err := a.SetLogLevel("loud"); err == nil {
		t.Fatal("unknown level accepted")
	}
	if c, err := a.Conflicts(act.ID); err != nil || len(c) != 0 {
		t.Fatalf("conflicts = %v, %v", c, err)
	}
	if p, err := a.PendingDeletes(act.ID); err != nil || p != nil {
		t.Fatalf("pending = %v, %v", p, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/`
Expected: FAIL to compile (`a.UpdateRules undefined`, and similar).

- [ ] **Step 3: Implement**

Create `internal/app/manage.go`:
```go
package app

import (
	"context"
	"fmt"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/schedule"
	"github.com/niceit/s3-anysync/internal/secrets"
)

const settingLogLevel = "log_level"

var logLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Action returns a local action.
func (a *App) Action(id string) (model.Action, error) { return a.DB.GetAction(id) }

// Profile returns a local profile.
func (a *App) Profile(id string) (model.Profile, error) { return a.DB.GetProfile(id) }

// UpdateRules changes an action's editable settings for every machine.
func (a *App) UpdateRules(ctx context.Context, actionID string, r model.Rules) (model.Action, error) {
	if err := schedule.Validate(r.Triggers.Schedule); err != nil {
		return model.Action{}, err
	}
	return a.updateShared(ctx, actionID, func(act *model.Action) error {
		act.ApplyRules(r)
		return nil
	})
}

// dropBaselineOfItem drops the baseline of an item known in the local copy
// of the action (an unknown item has no baseline to drop).
func (a *App) dropBaselineOfItem(actionID, itemID string) error {
	cur, err := a.DB.GetAction(actionID)
	if err != nil {
		return nil
	}
	for _, it := range cur.Items {
		if it.ID == itemID {
			return a.dropItemBaseline(actionID, it.RemoteName)
		}
	}
	return nil
}

// UnmapItem stops syncing an item on this machine. Local files and the cloud
// copy stay; the item's baseline is dropped first, so mapping it again later
// behaves like a first run.
func (a *App) UnmapItem(ctx context.Context, actionID, itemID string) error {
	if err := a.dropBaselineOfItem(actionID, itemID); err != nil {
		return err
	}
	_, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		for i := range act.Items {
			if act.Items[i].ID == itemID {
				delete(act.Items[i].LocalPaths, a.MachineID)
				return nil
			}
		}
		return fmt.Errorf("action has no item %s", itemID)
	})
	return err
}

// RemoveItem removes an item from the action on every machine. Local files
// and the cloud copies stay in place; the cloud copies are no longer synced.
func (a *App) RemoveItem(ctx context.Context, actionID, itemID string) error {
	if err := a.dropBaselineOfItem(actionID, itemID); err != nil {
		return err
	}
	_, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		for i := range act.Items {
			if act.Items[i].ID == itemID {
				act.Items = append(act.Items[:i], act.Items[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("action has no item %s", itemID)
	})
	return err
}

// LeaveAction stops syncing an action on this machine. This machine's paths,
// direction override and name are removed from the shared definition, then
// the local action, its baseline, history, conflicts, pending deletions,
// pause flag and passphrase are deleted. Local files and the cloud copy
// stay. With force, a failure to update the shared definition (for example
// when offline) is ignored.
func (a *App) LeaveAction(ctx context.Context, actionID string, force bool) error {
	act, err := a.DB.GetAction(actionID)
	if err != nil {
		return err
	}
	_, err = a.updateShared(ctx, actionID, func(x *model.Action) error {
		for i := range x.Items {
			delete(x.Items[i].LocalPaths, a.MachineID)
		}
		delete(x.MachineDirections, a.MachineID)
		delete(x.Machines, a.MachineID)
		return nil
	})
	if err != nil && !force {
		return err
	}
	if err := a.DB.DeleteAction(actionID); err != nil {
		return err
	}
	if act.Encrypt {
		if err := a.Secrets.Delete(secrets.ActionPassphraseKey(actionID)); err != nil {
			return fmt.Errorf("remove passphrase: %w", err)
		}
	}
	return nil
}

// History returns the newest runs of an action; limit <= 0 means 50.
func (a *App) History(actionID string, limit int) ([]model.HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	return a.DB.ListHistory(actionID, limit)
}

// Conflicts returns an action's saved conflicts.
func (a *App) Conflicts(actionID string) ([]model.Conflict, error) {
	return a.DB.LoadConflicts(actionID)
}

// PendingDeletes returns the deletions held back by the delete guard, or nil.
func (a *App) PendingDeletes(actionID string) (*model.PendingDeletes, error) {
	return a.DB.LoadPendingDeletes(actionID)
}

// SetPaused stores whether the user paused an action.
func (a *App) SetPaused(actionID string, paused bool) error { return a.DB.SetPaused(actionID, paused) }

// Paused reports whether the user paused an action.
func (a *App) Paused(actionID string) (bool, error) { return a.DB.Paused(actionID) }

// LogLevel returns the agent's log level (debug, info, warn or error).
func (a *App) LogLevel() (string, error) {
	v, ok, err := a.DB.GetSetting(settingLogLevel)
	if err != nil || !ok {
		return "info", err
	}
	return v, nil
}

// SetLogLevel stores the agent's log level.
func (a *App) SetLogLevel(level string) error {
	if !logLevels[level] {
		return fmt.Errorf("unknown log level %q (use debug, info, warn or error)", level)
	}
	return a.DB.SetSetting(settingLogLevel, level)
}
```

In `internal/app/app.go` `MapItem`, replace the inline "find the item in the local copy and drop its baseline" block with:
```go
	if err := a.dropBaselineOfItem(actionID, itemID); err != nil {
		return err
	}
```
Keep the belt-and-braces drop after the shared update.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/app/`
Expected: `ok`. The existing MapItem tests still pass.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal && git add internal/app
git commit -m "feat(app): rules editing, unmap/remove items, leave action, history, pause and log level" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: One owner per data folder

**Files:**
- Create: `internal/datalock/datalock.go`
- Test: `internal/datalock/datalock_test.go`

**Interfaces:**
- Produces:
  - `datalock.Acquire(dataDir string) (*Lock, error)`, which creates the folder (0700) and takes a non-blocking OS lock on `<dataDir>/agent.lock`.
  - `ErrBusy` when another process (or another open of the lock in this process) holds it.
  - `(*Lock).Release() error`.

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/gofrs/flock@v0.13.1
```

- [ ] **Step 2: Write the failing test**

Create `internal/datalock/datalock_test.go`:
```go
package datalock

import (
	"errors"
	"testing"
)

func TestAcquireIsExclusive(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Acquire: want ErrBusy, got %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	l2.Release()
}

func TestAcquireCreatesFolder(t *testing.T) {
	dir := t.TempDir() + "/nested/data"
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/datalock/`
Expected: FAIL with `undefined: Acquire`.

- [ ] **Step 4: Implement**

Create `internal/datalock/datalock.go`:
```go
// Package datalock makes one process the owner of an s3-anysync data folder.
// The agent holds the lock while it runs; the CLI takes it only when no agent
// runs, and otherwise talks to the agent over IPC.
package datalock

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// ErrBusy means another s3-anysync process owns the data folder.
var ErrBusy = errors.New("the data folder is in use by another s3-anysync process (is the agent running?)")

// Lock is a held data-folder lock.
type Lock struct {
	f *flock.Flock
}

// Acquire takes the lock on dataDir without waiting.
func Acquire(dataDir string) (*Lock, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	f := flock.New(filepath.Join(dataDir, "agent.lock"))
	ok, err := f.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrBusy
	}
	return &Lock{f: f}, nil
}

// Release gives the folder up.
func (l *Lock) Release() error { return l.f.Unlock() }
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/datalock/` and `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/datalock/`
Expected: `ok`, and the vet run is clean.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && go mod tidy && git add go.mod go.sum internal/datalock
git commit -m "feat(datalock): one owner process per data folder" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: The api contract

**Files:**
- Create: `internal/api/api.go`, `internal/api/errors.go`
- Test: `internal/api/errors_test.go`

**Interfaces:**
- Consumes: `model`, `engine.RestoreResult`, `engine.ConflictDetail`, `storage.ProbeResult`, and the sentinel errors of `engine`, `storage`, `state` and `datalock`.
- Produces:
  - The `api.Service` interface (below).
  - DTOs: `Status`, `ActionStatus`, `Settings`, `AddProfileRequest`, `CreateActionRequest`, `JoinActionRequest`, `AddItemRequest`, `RestoreRequest` and `Event`.
  - State constants `StateIdle`, `StateRunning`, `StatePausedConflict`, `StatePausedDeletes`, `StatePausedUser`, `StateError`, and event types `EventState`, `EventResult`, `EventActions`, `EventLog`.
  - `ErrNotFound`, `ErrInvalid`, `ErrUnsupported`, `Code(err) string`, `HTTPStatus(code) int`, and the `*RemoteError` type whose `Unwrap` returns the sentinels of its code.

- [ ] **Step 1: Write the failing test**

Create `internal/api/errors_test.go`:
```go
package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

func TestCodesRoundTrip(t *testing.T) {
	cases := map[error]string{
		engine.ErrConflictPending:   "conflict-pending",
		engine.ErrPassphraseMissing: "passphrase-missing",
		engine.ErrDecrypt:           "decrypt",
		engine.ErrKeyCheckMissing:   "keycheck-missing",
		engine.ErrRootMissing:       "root-missing",
		storage.ErrLocked:           "locked",
		storage.ErrLockLost:         "lock-lost",
		storage.ErrAccessDenied:     "access-denied",
		storage.ErrNoSuchBucket:     "no-such-bucket",
		datalock.ErrBusy:            "busy",
		state.ErrNotFound:           "not-found",
		storage.ErrNotFound:         "not-found",
		ErrNotFound:                 "not-found",
		ErrInvalid:                  "invalid",
		ErrUnsupported:              "unsupported",
	}
	for sentinel, code := range cases {
		wrapped := fmt.Errorf("context: %w", sentinel)
		if got := Code(wrapped); got != code {
			t.Errorf("Code(%v) = %q, want %q", sentinel, got, code)
		}
		remote := &RemoteError{Code: code, Message: wrapped.Error()}
		if !errors.Is(remote, sentinel) {
			t.Errorf("RemoteError{%s} does not match %v", code, sentinel)
		}
		if remote.Error() != wrapped.Error() {
			t.Errorf("message lost: %q", remote.Error())
		}
	}
	if Code(errors.New("boom")) != "internal" || Code(nil) != "" {
		t.Error("unexpected code for plain or nil error")
	}
	if Code(errors.Join(errors.New("x"), storage.ErrLockLost)) != "lock-lost" {
		t.Error("joined errors not matched")
	}
}

func TestHTTPStatus(t *testing.T) {
	cases := map[string]int{
		"not-found": http.StatusNotFound, "invalid": http.StatusBadRequest, "conflict-pending": http.StatusConflict,
		"locked": http.StatusConflict, "busy": http.StatusConflict, "access-denied": http.StatusForbidden,
		"unsupported": http.StatusNotImplemented, "internal": http.StatusInternalServerError, "decrypt": http.StatusUnprocessableEntity,
	}
	for code, want := range cases {
		if got := HTTPStatus(code); got != want {
			t.Errorf("HTTPStatus(%s) = %d, want %d", code, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/`
Expected: FAIL with `undefined: Code`.

- [ ] **Step 3: Implement**

Create `internal/api/api.go`:
```go
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
	TestProfile(ctx context.Context, profileID string) (storage.ProbeResult, error)
	Discover(ctx context.Context, profileID string) ([]model.Action, error)

	Actions(ctx context.Context) ([]ActionStatus, error)
	Action(ctx context.Context, actionID string) (ActionStatus, error)
	CreateAction(ctx context.Context, req CreateActionRequest) (model.Action, error)
	JoinAction(ctx context.Context, req JoinActionRequest) (model.Action, error)
	UpdateRules(ctx context.Context, actionID string, rules model.Rules) (model.Action, error)
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
	PendingDeletes(ctx context.Context, actionID string) (*model.PendingDeletes, error)
	ApproveDeletes(ctx context.Context, actionID string) (model.SyncResult, error)

	Snapshots(ctx context.Context, actionID string) ([]model.SnapshotInfo, error)
	Restore(ctx context.Context, actionID string, req RestoreRequest) (engine.RestoreResult, error)
	Conflicts(ctx context.Context, actionID string) ([]model.Conflict, error)
	ConflictDetail(ctx context.Context, actionID, path string) (engine.ConflictDetail, error)
	Resolve(ctx context.Context, actionID string, choices map[string]model.Side) (model.SyncResult, error)
	History(ctx context.Context, actionID string, limit int) ([]model.HistoryEntry, error)

	// Subscribe streams events until ctx ends; the channel is then closed.
	Subscribe(ctx context.Context) (<-chan Event, error)
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
	MachineID   string         `json:"machineId"`
	MachineName string         `json:"machineName"`
	Version     string         `json:"version"`
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
```

Create `internal/api/errors.go`:
```go
package api

import (
	"errors"
	"net/http"

	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

var (
	// ErrNotFound: the profile, action, item or snapshot does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: the request is malformed.
	ErrInvalid = errors.New("invalid request")
	// ErrUnsupported: the operation is not available on this system.
	ErrUnsupported = errors.New("not supported on this system")
)

// codes lists error codes with the sentinels they stand for, most specific first.
var codes = []struct {
	code   string
	status int
	errs   []error
}{
	{"conflict-pending", http.StatusConflict, []error{engine.ErrConflictPending}},
	{"passphrase-missing", http.StatusUnprocessableEntity, []error{engine.ErrPassphraseMissing}},
	{"decrypt", http.StatusUnprocessableEntity, []error{engine.ErrDecrypt}},
	{"keycheck-missing", http.StatusUnprocessableEntity, []error{engine.ErrKeyCheckMissing}},
	{"root-missing", http.StatusUnprocessableEntity, []error{engine.ErrRootMissing}},
	{"locked", http.StatusConflict, []error{storage.ErrLocked}},
	{"lock-lost", http.StatusConflict, []error{storage.ErrLockLost}},
	{"access-denied", http.StatusForbidden, []error{storage.ErrAccessDenied}},
	{"no-such-bucket", http.StatusUnprocessableEntity, []error{storage.ErrNoSuchBucket}},
	{"busy", http.StatusConflict, []error{datalock.ErrBusy}},
	{"not-found", http.StatusNotFound, []error{ErrNotFound, state.ErrNotFound, storage.ErrNotFound}},
	{"invalid", http.StatusBadRequest, []error{ErrInvalid}},
	{"unsupported", http.StatusNotImplemented, []error{ErrUnsupported}},
}

// Code returns the stable code of err ("internal" if none matches, "" for nil).
func Code(err error) string {
	if err == nil {
		return ""
	}
	for _, c := range codes {
		for _, e := range c.errs {
			if errors.Is(err, e) {
				return c.code
			}
		}
	}
	return "internal"
}

// HTTPStatus is the HTTP status the IPC server uses for a code.
func HTTPStatus(code string) int {
	for _, c := range codes {
		if c.code == code {
			return c.status
		}
	}
	return http.StatusInternalServerError
}

// RemoteError is an error reported by the agent over IPC. errors.Is matches
// the sentinel errors of its code, so callers handle it like a local error.
type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RemoteError) Error() string { return e.Message }

// Unwrap returns the sentinel errors the code stands for.
func (e *RemoteError) Unwrap() []error {
	for _, c := range codes {
		if c.code == e.Code {
			return c.errs
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/api/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal && git add internal/api
git commit -m "feat(api): service contract, DTOs and stable error codes" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Notifications and rotating logs

**Files:**
- Create: `internal/notify/notify.go`, `internal/logging/logging.go`
- Test: `internal/notify/notify_test.go`, `internal/logging/logging_test.go`

**Interfaces:**
- Produces:
  - `notify.Notifier` interface `{ Notify(title, message string) error }`.
  - Implementations: `notify.NewDesktop()` (beeep, app name "s3-anysync"), `notify.Nop{}`, and `*notify.Recorder` (for tests, with `Messages() []string`).
  - `logging.OpenDaily(dir string, keep int, now func() time.Time) (*DailyFile, error)`. It is an `io.WriteCloser` that writes `agent-YYYY-MM-DD.log` by local date and deletes files older than `keep` days whenever it switches files.
  - `logging.ParseLevel(string) (slog.Level, error)` and `logging.New(w io.Writer, level *slog.LevelVar) *slog.Logger` (JSON handler).

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/gen2brain/beeep@v0.11.2
```

- [ ] **Step 2: Write the failing tests**

Create `internal/notify/notify_test.go`:
```go
package notify

import "testing"

func TestRecorderAndNop(t *testing.T) {
	var n Notifier = &Recorder{}
	n.Notify("s3-anysync", "conflict in Editor")
	n.Notify("s3-anysync", "Editor failed 3 times")
	if got := n.(*Recorder).Messages(); len(got) != 2 || got[0] != "s3-anysync: conflict in Editor" {
		t.Fatalf("messages = %v", got)
	}
	if err := (Nop{}).Notify("a", "b"); err != nil {
		t.Fatal(err)
	}
	var _ Notifier = NewDesktop()
}
```

Create `internal/logging/logging_test.go`:
```go
package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyFileRotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 1, 23, 59, 0, 0, time.Local)
	now := func() time.Time { return day }
	old := filepath.Join(dir, "agent-2026-09-20.log")
	keepMe := filepath.Join(dir, "agent-2026-09-28.log")
	other := filepath.Join(dir, "notes.txt")
	for _, p := range []string{old, keepMe, other} {
		os.WriteFile(p, []byte("x"), 0o600)
	}
	f, err := OpenDaily(dir, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write([]byte("first\n"))
	day = day.Add(2 * time.Minute) // past midnight
	f.Write([]byte("second\n"))

	b1, _ := os.ReadFile(filepath.Join(dir, "agent-2026-10-01.log"))
	b2, _ := os.ReadFile(filepath.Join(dir, "agent-2026-10-02.log"))
	if string(b1) != "first\n" || string(b2) != "second\n" {
		t.Fatalf("day files = %q / %q", b1, b2)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("log older than 7 days not deleted")
	}
	for _, p := range []string{keepMe, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s deleted", p)
		}
	}
}

func TestLevels(t *testing.T) {
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("unknown level accepted")
	}
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelWarn)
	log := New(&buf, lv)
	log.Info("hidden")
	log.Warn("shown", "k", "v")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), `"msg":"shown"`) {
		t.Fatalf("log = %s", buf.String())
	}
	lv.Set(slog.LevelInfo)
	log.Info("now visible")
	if !strings.Contains(buf.String(), "now visible") {
		t.Fatal("level change not applied")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/notify/ ./internal/logging/`
Expected: FAIL with `undefined: Recorder` and `undefined: OpenDaily`.

- [ ] **Step 4: Implement**

Create `internal/notify/notify.go`:
```go
// Package notify shows desktop notifications.
package notify

import (
	"sync"

	"github.com/gen2brain/beeep"
)

// Notifier shows a notification to the user.
type Notifier interface {
	Notify(title, message string) error
}

var nameOnce sync.Once

// Desktop uses the OS notification center.
type Desktop struct{}

// NewDesktop returns the OS notifier.
func NewDesktop() Desktop {
	nameOnce.Do(func() { beeep.AppName = "s3-anysync" })
	return Desktop{}
}

func (Desktop) Notify(title, message string) error { return beeep.Notify(title, message, "") }

// Nop discards notifications.
type Nop struct{}

func (Nop) Notify(string, string) error { return nil }

// Recorder keeps notifications in memory (for tests).
type Recorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *Recorder) Notify(title, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, title+": "+message)
	return nil
}

// Messages returns the notifications so far as "title: message".
func (r *Recorder) Messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.msgs...)
}
```

Create `internal/logging/logging.go`:
```go
// Package logging writes the agent's JSON logs to daily files.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const prefix, suffix = "agent-", ".log"

// DailyFile writes to <dir>/agent-YYYY-MM-DD.log (local date), switching files
// when the date changes and deleting logs older than keep days.
type DailyFile struct {
	dir  string
	keep int
	now  func() time.Time
	mu   sync.Mutex
	day  string
	f    *os.File
}

// OpenDaily opens today's log file in dir (created with mode 0700).
func OpenDaily(dir string, keep int, now func() time.Time) (*DailyFile, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d := &DailyFile{dir: dir, keep: keep, now: now}
	if err := d.rotate(now().Format("2006-01-02")); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DailyFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if day := d.now().Format("2006-01-02"); day != d.day {
		if err := d.rotate(day); err != nil {
			return 0, err
		}
	}
	return d.f.Write(p)
}

// Close closes the current file.
func (d *DailyFile) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return nil
	}
	err := d.f.Close()
	d.f = nil
	return err
}

func (d *DailyFile) rotate(day string) error {
	if d.f != nil {
		d.f.Close()
	}
	f, err := os.OpenFile(filepath.Join(d.dir, prefix+day+suffix), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	d.f, d.day = f, day
	d.prune()
	return nil
}

// prune deletes agent-*.log files dated more than keep days before today.
func (d *DailyFile) prune() {
	today, err := time.ParseInLocation("2006-01-02", d.day, time.Local)
	if err != nil {
		return
	}
	cutoff := today.AddDate(0, 0, -d.keep)
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix), time.Local)
		if err == nil && t.Before(cutoff) {
			os.Remove(filepath.Join(d.dir, name))
		}
	}
}

// ParseLevel parses debug, info, warn or error.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

// New returns a JSON logger writing to w at a level that can change later.
func New(w io.Writer, level *slog.LevelVar) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/notify/ ./internal/logging/`, then `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/notify/ ./internal/logging/`, then `CGO_ENABLED=0 GOOS=darwin go vet ./internal/notify/`
Expected: `ok`, and both vet runs are clean.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && go mod tidy && git add go.mod go.sum internal/notify internal/logging
git commit -m "feat(notify,logging): desktop notifications and daily rotating JSON logs" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Agent manager, workers and state machine

**Files:**
- Create: `internal/agent/manager.go`, `internal/agent/worker.go`, `internal/agent/service.go`, `internal/agent/bus.go`
- Modify: `internal/app/app.go` (thread-safe machine name)
- Test: `internal/agent/manager_test.go`

**Interfaces:**
- Consumes:
  - `app.App`, including Task 7's methods.
  - `engine.Runner`: `Sync`, `Resolve`, `ApproveDeletes`, `Snapshot`, `ListSnapshots`, `Restore`, `ConflictDetail`, `Action`.
  - `api` from Task 9, `notify` from Task 10, `logging.ParseLevel`.
- Produces:
  - `agent.Options{App, Notifier, Autostart, Triggers, Backoff, PollInterval, LogLevel, Version, Log}`.
  - `agent.Autostart` interface `{Enable() error; Disable() error; Enabled() (bool, error)}`.
  - `agent.DefaultBackoff` and `agent.NotifyAfterFailures = 3`.
  - `agent.New(Options) (*Manager, error)`, `(*Manager).Start() error` and `(*Manager).Close() error`.
  - `*Manager` implements `api.Service`.
  - Worker hooks for Task 13: `(*worker).startTriggers()`, `stopTriggers()`, `refreshTriggers(model.Action)` and `trigger(reason string)`. This task stubs them as no-ops.
  - In `app`: `(*App).CurrentMachineName() string`, guarded by a mutex that `SetMachineName` also takes. Every internal use of `a.MachineName` inside `app` goes through it.
- State machine (spec 6.9 + §13):
  - States: `idle`, `running` (the `Running` flag), `paused-conflict`, `paused-deletes`, `paused` (user) and `error`.
  - Automatic triggers are ignored in every paused state, in a permanent error, and in a transient error (the retry timer drives those).
  - Manual API calls always run.
  - Transient failures retry with `Backoff[min(n-1, len-1)]` and notify once when the count reaches 3. Permanent failures notify at once and never retry.
  - Entering `paused-conflict` or `paused-deletes` notifies once.
  - The initial state comes from the stored pause flag, the saved conflicts and unapproved pending deletions.

- [ ] **Step 1: Make the machine name thread-safe in app**

In `internal/app/app.go`, add a field `nameMu sync.RWMutex` to `App` and:
```go
// CurrentMachineName returns the machine's friendly name; safe for concurrent use.
func (a *App) CurrentMachineName() string {
	a.nameMu.RLock()
	defer a.nameMu.RUnlock()
	return a.MachineName
}
```
Make `SetMachineName` assign `a.MachineName` under `a.nameMu.Lock()`. Replace every read of `a.MachineName` inside package `app` (`deps`, `updateShared`, `CreateAction`, `Join`) with `a.CurrentMachineName()`. Run `go test ./internal/app/` and expect `ok`.

- [ ] **Step 2: Write the failing tests**

Create `internal/agent/manager_test.go`:
```go
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
	if p, err := e.m.PendingDeletes(ctx, act.ID); err != nil || p == nil || len(p.Paths) != 5 {
		t.Fatalf("pending = %+v, %v", p, err)
	}
	if msgs := e.rec.Messages(); len(msgs) != 1 || !strings.Contains(msgs[0], "approval") {
		t.Fatalf("notifications = %v", msgs)
	}
	res, err = e.m.ApproveDeletes(ctx, act.ID)
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

func (f *fakeAutostart) Enable() error            { f.on = true; return nil }
func (f *fakeAutostart) Disable() error           { f.on = false; return nil }
func (f *fakeAutostart) Enabled() (bool, error)   { return f.on, nil }

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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/agent/`
Expected: FAIL to compile (`undefined: Options`, `New`, and similar).

- [ ] **Step 4: Implement the event bus**

Create `internal/agent/bus.go`:
```go
package agent

import (
	"context"
	"sync"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
)

// bus fans events out to subscribers. A subscriber that falls behind loses
// events rather than blocking the agent.
type bus struct {
	mu   sync.Mutex
	subs map[chan api.Event]struct{}
}

func newBus() *bus { return &bus{subs: map[chan api.Event]struct{}{}} }

func (b *bus) publish(e api.Event) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// subscribe returns a channel closed when ctx or stop ends.
func (b *bus) subscribe(ctx context.Context, stop <-chan struct{}) <-chan api.Event {
	ch := make(chan api.Event, 128)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		b.mu.Lock()
		delete(b.subs, ch)
		close(ch)
		b.mu.Unlock()
	}()
	return ch
}
```

- [ ] **Step 5: Implement the manager**

Create `internal/agent/manager.go`:
```go
// Package agent runs s3-anysync in the background: one worker per action
// serialises its runs and changes, drives its state machine, reacts to
// triggers, notifies the user and publishes events. Manager implements
// api.Service for the CLI, tray, IPC server and GUI.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/notify"
)

// Autostart turns starting the agent at login on or off.
type Autostart interface {
	Enable() error
	Disable() error
	Enabled() (bool, error)
}

// Options configure a Manager.
type Options struct {
	App *app.App
	// Notifier shows desktop notifications; nil disables them.
	Notifier notify.Notifier
	// Autostart manages starting at login; nil means unsupported here.
	Autostart Autostart
	// Triggers starts file watchers, schedules and remote polls (the agent);
	// the CLI's in-process mode leaves them off.
	Triggers bool
	// Backoff is the retry schedule after failures; nil means DefaultBackoff.
	Backoff []time.Duration
	// PollInterval overrides every action's remote poll interval (tests).
	PollInterval time.Duration
	// LogLevel, if set, follows the log level setting.
	LogLevel *slog.LevelVar
	Version  string
	Log      *slog.Logger
}

// DefaultBackoff is the retry schedule after consecutive failures.
var DefaultBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}

// NotifyAfterFailures is the failure count that triggers a notification.
const NotifyAfterFailures = 3

// errShuttingDown is returned to callers waiting on a job when the agent stops.
var errShuttingDown = errors.New("agent: shutting down")

// Manager owns the per-action workers.
type Manager struct {
	opts    Options
	app     *app.App
	log     *slog.Logger
	bus     *bus
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	workers map[string]*worker
	started bool
}

var _ api.Service = (*Manager)(nil)

// New returns a Manager; call Start to begin.
func New(opts Options) (*Manager, error) {
	if opts.App == nil {
		return nil, errors.New("agent: Options.App is required")
	}
	if len(opts.Backoff) == 0 {
		opts.Backoff = DefaultBackoff
	}
	if opts.Notifier == nil {
		opts.Notifier = notify.Nop{}
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{opts: opts, app: opts.App, log: log, bus: newBus(), ctx: ctx, cancel: cancel, workers: map[string]*worker{}}, nil
}

// Start starts one worker per local action.
func (m *Manager) Start() error {
	acts, err := m.app.Actions()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return errors.New("agent: already started")
	}
	m.started = true
	for _, a := range acts {
		m.startWorkerLocked(a.ID)
	}
	return nil
}

func (m *Manager) startWorkerLocked(id string) {
	if _, ok := m.workers[id]; ok {
		return
	}
	w := newWorker(m, id)
	m.workers[id] = w
	go w.loop()
}

func (m *Manager) startWorker(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startWorkerLocked(id)
}

// Close stops every worker. A running job is interrupted through its
// context; Close waits for the workers to exit.
func (m *Manager) Close() error {
	m.cancel()
	m.mu.Lock()
	ws := make([]*worker, 0, len(m.workers))
	for _, w := range m.workers {
		ws = append(ws, w)
	}
	m.mu.Unlock()
	for _, w := range ws {
		<-w.done
	}
	return nil
}

func (m *Manager) worker(id string) (*worker, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workers[id]
	if !ok {
		return nil, fmt.Errorf("action %s: %w", id, api.ErrNotFound)
	}
	return w, nil
}

// removeWorker stops and forgets an action's worker.
func (m *Manager) removeWorker(id string) {
	m.mu.Lock()
	w, ok := m.workers[id]
	delete(m.workers, id)
	m.mu.Unlock()
	if ok {
		w.cancel()
		<-w.done
	}
}

func (m *Manager) backoff(failures int) time.Duration {
	i := failures - 1
	if i >= len(m.opts.Backoff) {
		i = len(m.opts.Backoff) - 1
	}
	if i < 0 {
		i = 0
	}
	return m.opts.Backoff[i]
}

func (m *Manager) notify(message string) {
	if err := m.opts.Notifier.Notify("s3-anysync", message); err != nil {
		m.log.Warn("notification failed", "err", err)
	}
}
```

- [ ] **Step 6: Implement the worker**

Create `internal/agent/worker.go`:
```go
package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// errInvalidSchedule marks an action whose schedule cannot be compiled.
var errInvalidSchedule = errors.New("invalid schedule")

// permanentErrs are never retried automatically: they need the user.
var permanentErrs = []error{
	engine.ErrDecrypt, engine.ErrPassphraseMissing, engine.ErrKeyCheckMissing, engine.ErrRootMissing,
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
	retry  *time.Timer
	trig   *triggerSet

	mu sync.Mutex
	st wstate
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
			v, err := j.fn(w.ctx)
			j.done <- jobResult{v, err}
		case reason := <-w.wake:
			w.autoSync(reason)
		case <-retryC:
			w.retry = nil
			w.autoSync("retry")
		}
	}
}

// trigger asks for a sync; repeated requests while one is queued coalesce.
func (w *worker) trigger(reason string) {
	select {
	case w.wake <- reason:
	default:
	}
}

// do runs fn on the worker goroutine and waits for its result.
func (w *worker) do(ctx context.Context, fn func(ctx context.Context) (any, error)) (any, error) {
	j := &job{fn: fn, done: make(chan jobResult, 1)}
	select {
	case w.jobs <- j:
	case <-w.ctx.Done():
		return nil, errShuttingDown
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-j.done:
		return r.v, r.err
	case <-w.ctx.Done():
		return nil, errShuttingDown
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// autoSync runs a sync for a trigger unless the state forbids it.
func (w *worker) autoSync(reason string) {
	w.mu.Lock()
	st := w.st
	w.mu.Unlock()
	switch st.state {
	case api.StatePausedUser, api.StatePausedConflict, api.StatePausedDeletes:
		return
	case api.StateError:
		if st.permanent || reason != "retry" {
			return
		}
	}
	w.m.log.Info("automatic sync", "action", w.id, "reason", reason)
	w.runSync(func(ctx context.Context, r *engine.Runner) (model.SyncResult, error) { return r.Sync(ctx) })
}

func (w *worker) getRunner() (*engine.Runner, error) {
	if w.runner == nil {
		r, err := w.m.app.Runner(w.id)
		if err != nil {
			return nil, err
		}
		w.runner = r
	}
	return w.runner, nil
}

// runSync runs a sync-like engine call on the worker goroutine and updates
// the state machine from its outcome.
func (w *worker) runSync(f func(context.Context, *engine.Runner) (model.SyncResult, error)) (model.SyncResult, error) {
	r, err := w.getRunner()
	if err != nil {
		w.afterRun(model.SyncResult{}, err)
		return model.SyncResult{}, err
	}
	w.setRunning(true)
	res, err := f(w.ctx, r)
	w.setRunning(false)
	if w.ctx.Err() != nil {
		return res, err // shutting down: don't record an interruption as a failure
	}
	w.refreshTriggers(r.Action())
	w.afterRun(res, err)
	return res, err
}

func (w *worker) setRunning(on bool) {
	w.mu.Lock()
	w.st.running = on
	w.mu.Unlock()
	w.publishState()
}

// afterRun applies a run's outcome to the state machine.
func (w *worker) afterRun(res model.SyncResult, err error) {
	name := w.actionName()
	now := time.Now().UTC()
	var message string
	retryIn := time.Duration(0)
	w.mu.Lock()
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
			retryIn = w.m.backoff(st.failures)
			st.nextRun = now.Add(retryIn)
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
	if paused, perr := w.m.app.Paused(w.id); perr == nil && paused {
		st.state = api.StatePausedUser
	}
	w.mu.Unlock()

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
	w.mu.Lock()
	f(&w.st)
	w.mu.Unlock()
	w.publishState()
}

func (w *worker) status(a model.Action) api.ActionStatus {
	w.mu.Lock()
	st := w.st
	w.mu.Unlock()
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

// Trigger hooks; Task 13 replaces these with the real implementation.
type triggerSet struct{}

func (w *worker) startTriggers()                {}
func (w *worker) stopTriggers()                 {}
func (w *worker) refreshTriggers(model.Action)  {}
func (w *worker) nextScheduled() time.Time      { return time.Time{} }
```

- [ ] **Step 7: Implement api.Service**

Create `internal/agent/service.go`:
```go
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

func (m *Manager) ApproveDeletes(ctx context.Context, id string) (model.SyncResult, error) {
	return m.syncJob(ctx, id, func(c context.Context, r *engine.Runner) (model.SyncResult, error) { return r.ApproveDeletes(c) })
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
```

Note: `Resume` calls `w.trigger("resume")`. In this task `startTriggers` is a no-op, so `autoSync` will run for a resumed action (it is not paused any more). That is intended: resuming syncs once.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/agent/` and `go test -count=1 -race ./...`
Expected: `ok`. The tests are timing-tolerant (5s wait limits), so run the agent package 5 times with `-count=5 -race` to check for flakiness.

- [ ] **Step 9: Commit**

```bash
gofmt -w internal && git add internal
git commit -m "feat(agent): manager, per-action workers, state machine and api.Service" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: File watcher with debounce

**Files:**
- Create: `internal/trigger/watch.go`
- Test: `internal/trigger/watch_test.go`

**Interfaces:**
- Consumes: `paths.TempFilePrefix`.
- Produces:
  - `trigger.Root{Path string; Dir bool}` and `trigger.WatchOptions{Roots, Debounce, Match func(abs string) bool, Fire func(), Rescan time.Duration, Log *slog.Logger}`.
  - `trigger.Watch(WatchOptions) (*Watcher, error)` and `(*Watcher).Close() error`.
- Behaviour:
  - Dir roots are watched recursively. The configured root and its symlink-resolved target are both watched, but symlinked subdirectories are not followed. New subdirectories are added as they appear.
  - A file root watches its parent directory (and the parent of its symlink target), and only that file's events count.
  - Roots that don't exist yet are retried every `Rescan` (default 30s).
  - Events for temp files (`paths.TempFilePrefix`), chmod-only events, and paths for which `Match` returns false are ignored.
  - Relevant events reset a single debounce timer, which calls `Fire` once things settle.

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/fsnotify/fsnotify@v1.10.1
```

- [ ] **Step 2: Write the failing tests**

Create `internal/trigger/watch_test.go`:
```go
package trigger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func watchFor(t *testing.T, roots []Root, match func(string) bool) chan struct{} {
	t.Helper()
	fired := make(chan struct{}, 16)
	w, err := Watch(WatchOptions{Roots: roots, Debounce: 100 * time.Millisecond, Match: match, Fire: func() { fired <- struct{}{} }, Rescan: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	time.Sleep(50 * time.Millisecond) // let the watches settle
	return fired
}

func expectFire(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("no fire for %s", what)
	}
}

func expectQuiet(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("unexpected fire: %s", what)
	case <-time.After(400 * time.Millisecond):
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBurstFiresOnce(t *testing.T) {
	dir := t.TempDir()
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, nil)
	for i := 0; i < 5; i++ {
		write(t, filepath.Join(dir, "a.json"), strings.Repeat("x", i+1))
	}
	expectFire(t, fired, "burst")
	expectQuiet(t, fired, "burst should coalesce into one fire")
}

func TestNewSubdirectoriesAreWatched(t *testing.T) {
	dir := t.TempDir()
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, nil)
	write(t, filepath.Join(dir, "sub", "deep", "a.json"), "a")
	expectFire(t, fired, "new subdirectory")
	time.Sleep(100 * time.Millisecond)
	write(t, filepath.Join(dir, "sub", "deep", "b.json"), "b")
	expectFire(t, fired, "write inside the new subdirectory")
}

func TestMatchAndTempFilesFilterEvents(t *testing.T) {
	dir := t.TempDir()
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, func(p string) bool { return !strings.HasSuffix(p, ".log") })
	write(t, filepath.Join(dir, "x.log"), "ignored")
	write(t, filepath.Join(dir, ".s3-anysync-tmp-123"), "ignored")
	expectQuiet(t, fired, "excluded and temp files")
	write(t, filepath.Join(dir, "x.json"), "counted")
	expectFire(t, fired, "matching file")
}

func TestFileRootWatchesOnlyThatFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	write(t, target, "{}")
	fired := watchFor(t, []Root{{Path: target}}, nil)
	write(t, filepath.Join(dir, "other.json"), "x")
	expectQuiet(t, fired, "sibling of a file root")
	write(t, target, `{"a":1}`)
	expectFire(t, fired, "the file root itself")
}

func TestMissingRootIsPickedUpLater(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "later")
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, nil)
	os.MkdirAll(dir, 0o755)
	time.Sleep(200 * time.Millisecond) // > Rescan
	write(t, filepath.Join(dir, "a.json"), "a")
	expectFire(t, fired, "root created after the watcher")
}

func TestCloseStopsFiring(t *testing.T) {
	dir := t.TempDir()
	fired := make(chan struct{}, 4)
	w, err := Watch(WatchOptions{Roots: []Root{{Path: dir, Dir: true}}, Debounce: 50 * time.Millisecond, Fire: func() { fired <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "a.json"), "a")
	expectQuiet(t, fired, "after Close")
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/trigger/`
Expected: FAIL with `undefined: Watch`.

- [ ] **Step 4: Implement**

Create `internal/trigger/watch.go`:
```go
// Package trigger starts syncs: a file watcher with debounce, a schedule
// loop and a remote poller.
package trigger

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/niceit/s3-anysync/internal/paths"
)

// Root is one watched location: a directory tree, or a single file (whose
// parent directory is watched).
type Root struct {
	Path string
	Dir  bool
}

// WatchOptions configure a Watcher.
type WatchOptions struct {
	Roots    []Root
	Debounce time.Duration
	// Match reports whether a changed path matters; nil means every path.
	Match func(abs string) bool
	Fire  func()
	// Rescan is how often roots that don't exist yet are retried (default 30s).
	Rescan time.Duration
	Log    *slog.Logger
}

// Watcher calls Fire once changes under its roots settle.
type Watcher struct {
	o       WatchOptions
	fsw     *fsnotify.Watcher
	mu      sync.Mutex
	watched map[string]bool
	files   map[string]bool // file roots (configured and resolved)
	dirs    []string        // dir roots (configured and resolved)
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

// Watch starts watching.
func Watch(o WatchOptions) (*Watcher, error) {
	if o.Rescan <= 0 {
		o.Rescan = 30 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{o: o, fsw: fsw, watched: map[string]bool{}, files: map[string]bool{}, stop: make(chan struct{}), done: make(chan struct{})}
	w.addRoots()
	go w.run()
	return w, nil
}

// Close stops the watcher; Fire is not called afterwards.
func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.stop)
		<-w.done
		err = w.fsw.Close()
	})
	return err
}

func withResolved(p string) []string {
	out := []string{filepath.Clean(p)}
	if real, err := filepath.EvalSymlinks(p); err == nil && real != out[0] {
		out = append(out, real)
	}
	return out
}

// addRoots (re)adds every root that exists; called at start and on rescans.
func (w *Watcher) addRoots() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dirs = w.dirs[:0]
	for _, r := range w.o.Roots {
		for _, p := range withResolved(r.Path) {
			if r.Dir {
				w.dirs = append(w.dirs, p)
				w.addTreeLocked(p)
			} else {
				w.files[p] = true
				w.addDirLocked(filepath.Dir(p))
			}
		}
	}
}

func (w *Watcher) addDirLocked(dir string) {
	if w.watched[dir] {
		return
	}
	if err := w.fsw.Add(dir); err == nil {
		w.watched[dir] = true
	}
}

// addTreeLocked watches dir and its subdirectories, not following symlinks.
func (w *Watcher) addTreeLocked(dir string) {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // missing or unreadable: retried on the next rescan
		}
		if d.IsDir() {
			w.addDirLocked(p)
		}
		return nil
	})
}

// relevant reports whether an event path belongs to a root and matters.
func (w *Watcher) relevant(p string) bool {
	if strings.HasPrefix(filepath.Base(p), paths.TempFilePrefix) {
		return false
	}
	w.mu.Lock()
	inRoot := w.files[p]
	for _, d := range w.dirs {
		if p == d || strings.HasPrefix(p, d+string(filepath.Separator)) {
			inRoot = true
			break
		}
	}
	w.mu.Unlock()
	if !inRoot {
		return false
	}
	return w.o.Match == nil || w.o.Match(p)
}

func (w *Watcher) run() {
	defer close(w.done)
	rescan := time.NewTicker(w.o.Rescan)
	defer rescan.Stop()
	var debounce *time.Timer
	var fireC <-chan time.Time
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-w.stop:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			p := filepath.Clean(ev.Name)
			if ev.Op == fsnotify.Chmod {
				continue
			}
			if ev.Has(fsnotify.Create) {
				if info, err := os.Lstat(p); err == nil && info.IsDir() {
					w.mu.Lock()
					w.addTreeLocked(p)
					w.mu.Unlock()
				}
			}
			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				w.mu.Lock()
				delete(w.watched, p)
				w.mu.Unlock()
			}
			if !w.relevant(p) {
				continue
			}
			if debounce == nil {
				debounce = time.NewTimer(w.o.Debounce)
			} else {
				debounce.Reset(w.o.Debounce)
			}
			fireC = debounce.C
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.o.Log.Warn("file watcher", "err", err)
		case <-rescan.C:
			w.addRoots()
		case <-fireC:
			fireC = nil
			w.o.Fire()
		}
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/trigger/`, then repeat with `-count=5`, then `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/trigger/`
Expected: `ok` on every run, and the vet run is clean.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && go mod tidy && git add go.mod go.sum internal/trigger
git commit -m "feat(trigger): recursive file watcher with debounce" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Schedule and poll loops wired into the workers

**Files:**
- Create: `internal/trigger/loops.go`, `internal/agent/triggers.go`
- Modify: `internal/agent/worker.go` (delete the stub trigger hooks and the empty `triggerSet`)
- Test: `internal/trigger/loops_test.go`, `internal/agent/triggers_test.go`

**Interfaces:**
- Consumes: Task 12's `trigger.Watch`, `schedule.Compile`, `engine.BuildMappings`, `Mappings.RemotePathFor`, `Runner.ManifestETag`, `app.Runner`, and Task 11's worker hooks.
- Produces:
  - `trigger.Scheduler` interface `{Next(time.Time) time.Time}`.
  - `trigger.RunSchedule(ctx, sched Scheduler, now func() time.Time, fire func())`.
  - `trigger.RunPoller(ctx, interval, check func(context.Context) (bool, error), fire func(), onErr func(error))`.
  - `trigger.ETagChecker(get func(context.Context) (string, error)) func(context.Context) (bool, error)`. Its first call only records the ETag.
  - The real worker hooks: `startTriggers`, `stopTriggers`, `refreshTriggers` and `nextScheduled`.
- Behaviour (spec 6.8):
  - `onChange` watches the mapped paths.
  - A schedule runs a sync at each time.
  - A remote poll every `remotePollMinutes` (or `Options.PollInterval` when set) runs only when `onChange` is on and there is no schedule.
  - At start, an action with any trigger syncs once (catch-up).
  - Triggers are rebuilt, followed by one sync, when the action's triggers, excludes or this machine's paths change, whether locally or as picked up by a sync from another machine.
  - An invalid schedule puts the action into a permanent error.

- [ ] **Step 1: Write the failing tests**

Create `internal/trigger/loops_test.go`:
```go
package trigger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type every time.Duration

func (e every) Next(t time.Time) time.Time { return t.Add(time.Duration(e)) }

func TestRunScheduleFiresUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	done := make(chan struct{})
	go func() { RunSchedule(ctx, every(30*time.Millisecond), nil, func() { n.Add(1) }); close(done) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	if got := n.Load(); got < 3 {
		t.Fatalf("fired %d times", got)
	}
}

func TestRunPollerAndETagChecker(t *testing.T) {
	var etag atomic.Value
	etag.Store("a")
	var calls atomic.Int32
	check := ETagChecker(func(context.Context) (string, error) {
		if calls.Add(1) == 3 {
			return "", errors.New("transient")
		}
		return etag.Load().(string), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fired := make(chan struct{}, 4)
	var errs atomic.Int32
	go RunPoller(ctx, 20*time.Millisecond, check, func() { fired <- struct{}{} }, func(error) { errs.Add(1) })
	time.Sleep(120 * time.Millisecond)
	select {
	case <-fired:
		t.Fatal("fired without a change")
	default:
	}
	etag.Store("b")
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("no fire after the ETag changed")
	}
	if errs.Load() != 1 {
		t.Fatalf("errors reported = %d", errs.Load())
	}
}
```

Create `internal/agent/triggers_test.go`:
```go
package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/model"
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/trigger/ ./internal/agent/`
Expected: FAIL. `RunSchedule` is undefined, and the agent trigger tests time out (the hooks are still stubs).

- [ ] **Step 3: Implement the loops**

Create `internal/trigger/loops.go`:
```go
package trigger

import (
	"context"
	"time"
)

// Scheduler is the part of a cron schedule RunSchedule needs.
type Scheduler interface {
	Next(time.Time) time.Time
}

// RunSchedule calls fire at every time sched gives, until ctx ends.
func RunSchedule(ctx context.Context, sched Scheduler, now func() time.Time, fire func()) {
	if now == nil {
		now = time.Now
	}
	for {
		t := now()
		timer := time.NewTimer(sched.Next(t).Sub(t))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			fire()
		}
	}
}

// RunPoller calls check every interval and fire when it reports a change;
// errors go to onErr (may be nil) and polling continues.
func RunPoller(ctx context.Context, interval time.Duration, check func(context.Context) (bool, error), fire func(), onErr func(error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		changed, err := check(ctx)
		if err != nil {
			if onErr != nil {
				onErr(err)
			}
			continue
		}
		if changed {
			fire()
		}
	}
}

// ETagChecker reports a change whenever the ETag differs from the last one
// seen; the first successful call only records it.
func ETagChecker(get func(context.Context) (string, error)) func(context.Context) (bool, error) {
	var last string
	seen := false
	return func(ctx context.Context) (bool, error) {
		etag, err := get(ctx)
		if err != nil {
			return false, err
		}
		changed := seen && etag != last
		last, seen = etag, true
		return changed, nil
	}
}
```

- [ ] **Step 4: Implement the worker hooks**

Delete from `internal/agent/worker.go` the stub block (the empty `triggerSet` type and the four no-op hook functions). Create `internal/agent/triggers.go`:
```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
	"github.com/niceit/s3-anysync/internal/schedule"
	"github.com/niceit/s3-anysync/internal/trigger"
)

// triggerSet is the running triggers of one action.
type triggerSet struct {
	key     string
	cancel  context.CancelFunc
	watcher *trigger.Watcher
	sched   cron.Schedule
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
	w.buildTriggers(a)
	if hasTriggers(a) {
		w.trigger("startup")
	}
}

func hasTriggers(a model.Action) bool {
	return a.Triggers.OnChange || (a.Triggers.Schedule.Kind != "" && a.Triggers.Schedule.Kind != model.ScheduleNone)
}

func (w *worker) stopTriggers() {
	if w.trig == nil {
		return
	}
	w.trig.cancel()
	if w.trig.watcher != nil {
		w.trig.watcher.Close()
	}
	w.trig = nil
}

// refreshTriggers rebuilds the triggers if what they depend on changed
// (rules, excludes or this machine's paths), then syncs once so newly
// watched or newly mapped content is picked up without waiting for an edit.
func (w *worker) refreshTriggers(a model.Action) {
	if !w.m.opts.Triggers {
		return
	}
	if w.trig != nil && w.trig.key == triggerKey(a, w.m.app.MachineID) {
		return
	}
	w.stopTriggers()
	w.buildTriggers(a)
	if hasTriggers(a) {
		w.trigger("rules changed")
	}
}

func (w *worker) buildTriggers(a model.Action) {
	ctx, cancel := context.WithCancel(w.ctx)
	ts := &triggerSet{key: triggerKey(a, w.m.app.MachineID), cancel: cancel}
	w.trig = ts
	sched, err := schedule.Compile(a.Triggers.Schedule)
	if err != nil {
		w.afterRun(model.SyncResult{}, fmt.Errorf("%w: %v", errInvalidSchedule, err))
		return
	}
	ts.sched = sched
	if sched != nil {
		go trigger.RunSchedule(ctx, sched, nil, func() { w.trigger("schedule") })
	}
	if !a.Triggers.OnChange {
		return
	}
	if ms, err := engine.BuildMappings(a, w.m.app.MachineID, paths.Expand); err != nil {
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
		// A separate runner: ManifestETag only reads the bucket, and the
		// worker's own runner belongs to the worker goroutine.
		poller, err := w.m.app.Runner(w.id)
		if err != nil {
			w.m.log.Warn("remote poller", "action", w.id, "err", err)
			return
		}
		go trigger.RunPoller(ctx, interval, trigger.ETagChecker(poller.ManifestETag), func() { w.trigger("remote") },
			func(err error) { w.m.log.Debug("remote poll", "action", w.id, "err", err) })
	}
}

// nextScheduled is the next scheduled run, or zero.
func (w *worker) nextScheduled() time.Time {
	if w.trig == nil || w.trig.sched == nil {
		return time.Time{}
	}
	return w.trig.sched.Next(time.Now())
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/trigger/ ./internal/agent/`, then `go test -count=3 -race ./internal/agent/`, then `go test -count=1 -race ./...`
Expected: `ok` on every run.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal && go mod tidy && git add go.mod go.sum internal
git commit -m "feat(agent): file-change, schedule and remote-poll triggers per action" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Local IPC (transport, token, server, client)

**Files:**
- Create: `internal/ipc/transport_unix.go` (`//go:build !windows`), `internal/ipc/transport_windows.go` (`//go:build windows`), `internal/ipc/token.go`, `internal/ipc/server.go`, `internal/ipc/client.go`
- Test: `internal/ipc/ipc_test.go` (package `ipc_test`), `internal/ipc/transport_unix_test.go` (`//go:build !windows`)

**Interfaces:**
- Consumes: `api.Service`, `api.Code`, `api.HTTPStatus`, `api.RemoteError`, and `agent.Manager` (tests only).
- Produces:
  - `ipc.Listen(dataDir) (net.Listener, error)`, `ipc.Dial(ctx, dataDir) (net.Conn, error)` and `ipc.Cleanup(dataDir)`.
  - `ipc.NewToken(dataDir) (string, error)` and `ipc.ReadToken(dataDir) (string, error)`.
  - `ipc.NewServer(svc api.Service, token string, log *slog.Logger) *Server`, with `(*Server).Serve(net.Listener) error` and `(*Server).Shutdown(ctx) error`.
  - `ipc.NewClient(dataDir, token string) *Client` and `ipc.Connect(ctx, dataDir) (*Client, error)`, which reads the token and calls `Status`. `*Client` implements `api.Service` and has `Close()`.
  - The HTTP routes listed in `server.go` below.

- [ ] **Step 1: Add the dependencies**

```bash
go get github.com/Microsoft/go-winio@v0.6.2 golang.org/x/sys@latest
```

- [ ] **Step 2: Write the failing tests**

Create `internal/ipc/ipc_test.go`:
```go
package ipc_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

var ctx = context.Background()

// shortDir returns a temp dir with a short path (Unix socket paths are limited).
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// serve starts a Manager behind an IPC server and returns a connected client.
func serve(t *testing.T) (*ipc.Client, string) {
	t.Helper()
	dir := shortDir(t)
	a, err := app.Open(dir, secrets.NewMemory(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewMemory()
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
	a.LockSettle = 0
	m, err := agent.New(agent.Options{App: a, Version: "ipc-test"})
	if err != nil {
		t.Fatal(err)
	}
	m.Start()
	tok, err := ipc.NewToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer(m, tok, nil)
	go srv.Serve(l)
	c, err := ipc.Connect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		ipc.Cleanup(dir)
		m.Close()
		a.Close()
	})
	return c, dir
}

func TestEndToEndOverIPC(t *testing.T) {
	c, _ := serve(t)
	st, err := c.Status(ctx)
	if err != nil || st.Version != "ipc-test" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	p, err := c.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s3cr3t-value"})
	if err != nil {
		t.Fatal(err)
	}
	act, err := c.CreateAction(ctx, api.CreateActionRequest{ProfileID: p.ID, Action: model.Action{Name: "Editor", PropagateDeletes: true}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "snippets")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.json"), []byte("a"), 0o644)
	if _, err := c.AddItem(ctx, act.ID, api.AddItemRequest{Item: model.Item{RemoteName: "snippets", Type: model.ItemDir}, LocalPath: dir}); err != nil {
		t.Fatal(err)
	}
	res, err := c.Sync(ctx, act.ID)
	if err != nil || res.Uploaded != 1 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	if _, err := c.Snapshot(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if snaps, err := c.Snapshots(ctx, act.ID); err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %v, %v", snaps, err)
	}
	if p, err := c.PendingDeletes(ctx, act.ID); err != nil || p != nil {
		t.Fatalf("pending = %+v, %v", p, err)
	}
	rules := act.Rules()
	rules.Name = "Renamed"
	if a, err := c.UpdateRules(ctx, act.ID, rules); err != nil || a.Name != "Renamed" {
		t.Fatalf("rules = %+v, %v", a, err)
	}
	if h, err := c.History(ctx, act.ID, 10); err != nil || len(h) == 0 {
		t.Fatalf("history = %v, %v", h, err)
	}
	if err := c.Pause(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.Action(ctx, act.ID); s.State != api.StatePausedUser {
		t.Fatalf("state = %s", s.State)
	}
	if err := c.Resume(ctx, act.ID); err != nil {
		t.Fatal(err)
	}

	// Typed errors survive the trip.
	if _, err := c.Sync(ctx, "nope"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	os.RemoveAll(dir)
	if _, err := c.Sync(ctx, act.ID); !errors.Is(err, engine.ErrRootMissing) {
		t.Fatalf("root missing: %v", err)
	}
	if err := c.LeaveAction(ctx, act.ID, false); err != nil {
		t.Fatal(err)
	}
	if acts, _ := c.Actions(ctx); len(acts) != 0 {
		t.Fatalf("actions after leave = %+v", acts)
	}
}

func TestSecretsNeverInResponses(t *testing.T) {
	_, dir := serve(t)
	tok, _ := ipc.ReadToken(dir)
	c := ipc.NewClient(dir, tok)
	defer c.Close()
	if _, err := c.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s3cr3t-value"}); err != nil {
		t.Fatal(err)
	}
	body := rawGet(t, dir, tok, "/profiles")
	if strings.Contains(body, "s3cr3t-value") {
		t.Fatalf("secret in response: %s", body)
	}
}

func rawGet(t *testing.T, dir, token, path string) string {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(c context.Context, _, _ string) (net.Conn, error) { return ipc.Dial(c, dir) }}}
	req, _ := http.NewRequest("GET", "http://agent"+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.Status + " " + string(b)
}

func TestWrongTokenIsRejected(t *testing.T) {
	_, dir := serve(t)
	if got := rawGet(t, dir, "wrong", "/status"); !strings.HasPrefix(got, "401") {
		t.Fatalf("wrong token: %s", got)
	}
	if got := rawGet(t, dir, "", "/status"); !strings.HasPrefix(got, "401") {
		t.Fatalf("no token: %s", got)
	}
	c := ipc.NewClient(dir, "wrong")
	defer c.Close()
	if _, err := c.Status(ctx); err == nil {
		t.Fatal("client with a wrong token succeeded")
	}
}

func TestEventsStreamOverIPC(t *testing.T) {
	c, _ := serve(t)
	p, _ := c.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s"})
	act, _ := c.CreateAction(ctx, api.CreateActionRequest{ProfileID: p.ID, Action: model.Action{Name: "E"}})
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := c.Subscribe(sctx)
	if err != nil {
		t.Fatal(err)
	}
	go c.Sync(ctx, act.ID)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("stream closed early")
			}
			if ev.Type == api.EventResult && ev.ActionID == act.ID {
				return
			}
		case <-timeout:
			t.Fatal("no result event over IPC")
		}
	}
}

func TestSecondListenerIsRefused(t *testing.T) {
	_, dir := serve(t)
	if _, err := ipc.Listen(dir); err == nil {
		t.Fatal("second Listen on the same data dir succeeded")
	}
}
```
Create `internal/ipc/transport_unix_test.go`:
```go
//go:build !windows

package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPathLimitAndMode(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	if _, err := Listen(long); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("long path: %v", err)
	}
	dir, _ := os.MkdirTemp("", "sa")
	defer os.RemoveAll(dir)
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	info, err := os.Stat(filepath.Join(dir, "agent.sock"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v", info.Mode(), err)
	}
	// A stale socket file (no listener) is replaced.
	l.Close()
	l2, err := Listen(dir)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	l2.Close()
}

func TestTokenFile(t *testing.T) {
	dir := t.TempDir()
	tok, err := NewToken(dir)
	if err != nil || len(tok) != 64 {
		t.Fatalf("token = %q, %v", tok, err)
	}
	got, err := ReadToken(dir)
	if err != nil || got != tok {
		t.Fatalf("read = %q, %v", got, err)
	}
	info, _ := os.Stat(filepath.Join(dir, "ipc.token"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %v", info.Mode())
	}
	tok2, _ := NewToken(dir)
	if tok2 == tok {
		t.Fatal("token not rotated")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/ipc/`
Expected: FAIL with `undefined: ipc.Listen`, and similar.

- [ ] **Step 4: Implement the transports and token**

Create `internal/ipc/transport_unix.go`:
```go
//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const sockName = "agent.sock"

// maxSockPath is the usable length of sun_path on macOS (104 bytes incl. NUL).
const maxSockPath = 103

func socketPath(dataDir string) string { return filepath.Join(dataDir, sockName) }

// Listen opens the agent socket in dataDir (mode 0600), replacing a stale one.
func Listen(dataDir string) (net.Listener, error) {
	p := socketPath(dataDir)
	if len(p) > maxSockPath {
		return nil, fmt.Errorf("ipc: socket path %q is too long (%d > %d bytes); use a shorter S3_ANYSYNC_DATA_DIR", p, len(p), maxSockPath)
	}
	if c, err := net.DialTimeout("unix", p, time.Second); err == nil {
		c.Close()
		return nil, errors.New("ipc: another agent is already listening on " + p)
	}
	os.Remove(p) // stale socket of a crashed agent
	l, err := net.Listen("unix", p)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Dial connects to the agent of dataDir.
func Dial(ctx context.Context, dataDir string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath(dataDir))
}

// Cleanup removes the socket file after the server stopped.
func Cleanup(dataDir string) { os.Remove(socketPath(dataDir)) }
```

Create `internal/ipc/transport_windows.go`:
```go
//go:build windows

package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// pipeName is unique per data folder.
func pipeName(dataDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	return `\\.\pipe\s3-anysync-` + hex.EncodeToString(sum[:])[:16]
}

func currentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// Listen opens the agent's named pipe, accessible only to the current user.
func Listen(dataDir string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(pipeName(dataDir), &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
}

// Dial connects to the agent of dataDir.
func Dial(ctx context.Context, dataDir string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName(dataDir))
}

// Cleanup is a no-op: named pipes disappear with their listener.
func Cleanup(string) {}
```

Create `internal/ipc/token.go`:
```go
package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

const tokenFile = "ipc.token"

// NewToken writes a fresh random token to dataDir (mode 0600) and returns it.
// On Windows the file is protected by the user profile's permissions.
func NewToken(dataDir string) (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	p := filepath.Join(dataDir, tokenFile)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tok, nil
}

// ReadToken returns the token the running agent wrote.
func ReadToken(dataDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, tokenFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
```

- [ ] **Step 5: Implement the server**

Create `internal/ipc/server.go`:
```go
// Package ipc carries api.Service between the CLI/GUI and the agent over a
// Unix socket (macOS) or a named pipe (Windows), as JSON over HTTP/1.1 with a
// bearer token and a Server-Sent Events stream.
package ipc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/model"
)

// Server serves an api.Service.
type Server struct {
	svc   api.Service
	token string
	log   *slog.Logger
	hs    *http.Server
}

// NewServer returns a server for svc that accepts only the given token.
func NewServer(svc api.Service, token string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{svc: svc, token: token, log: log}
	s.hs = &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	return s
}

// Serve serves on l until Shutdown; it then returns http.ErrServerClosed.
func (s *Server) Serve(l net.Listener) error { return s.hs.Serve(l) }

// Shutdown stops accepting requests and waits for running ones (open event
// streams are closed).
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.hs.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return s.hs.Close()
	}
	return err
}

type handlerFunc func(r *http.Request) (any, error)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn handlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := fn(r)
			writeResult(w, v, err)
		})
	}
	svc := s.svc
	id := func(r *http.Request) string { return r.PathValue("id") }

	h("GET /status", func(r *http.Request) (any, error) { return svc.Status(r.Context()) })
	h("GET /settings", func(r *http.Request) (any, error) { return svc.Settings(r.Context()) })
	h("PUT /settings", func(r *http.Request) (any, error) {
		var in api.Settings
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.UpdateSettings(r.Context(), in)
	})

	h("GET /profiles", func(r *http.Request) (any, error) { return svc.Profiles(r.Context()) })
	h("POST /profiles", func(r *http.Request) (any, error) {
		var in api.AddProfileRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.AddProfile(r.Context(), in)
	})
	h("POST /profiles/{id}/test", func(r *http.Request) (any, error) { return svc.TestProfile(r.Context(), id(r)) })
	h("GET /profiles/{id}/discover", func(r *http.Request) (any, error) { return svc.Discover(r.Context(), id(r)) })

	h("GET /actions", func(r *http.Request) (any, error) { return svc.Actions(r.Context()) })
	h("POST /actions", func(r *http.Request) (any, error) {
		var in api.CreateActionRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.CreateAction(r.Context(), in)
	})
	h("POST /actions/join", func(r *http.Request) (any, error) {
		var in api.JoinActionRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.JoinAction(r.Context(), in)
	})
	h("GET /actions/{id}", func(r *http.Request) (any, error) { return svc.Action(r.Context(), id(r)) })
	h("DELETE /actions/{id}", func(r *http.Request) (any, error) {
		return nil, svc.LeaveAction(r.Context(), id(r), r.URL.Query().Get("force") == "1")
	})
	h("PUT /actions/{id}/rules", func(r *http.Request) (any, error) {
		var in model.Rules
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.UpdateRules(r.Context(), id(r), in)
	})
	h("PUT /actions/{id}/direction", func(r *http.Request) (any, error) {
		var in struct {
			Direction model.Direction `json:"direction"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return nil, svc.SetMachineDirection(r.Context(), id(r), in.Direction)
	})
	h("POST /actions/{id}/items", func(r *http.Request) (any, error) {
		var in api.AddItemRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.AddItem(r.Context(), id(r), in)
	})
	h("PUT /actions/{id}/items/{item}/path", func(r *http.Request) (any, error) {
		var in struct {
			LocalPath string `json:"localPath"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return nil, svc.MapItem(r.Context(), id(r), r.PathValue("item"), in.LocalPath)
	})
	h("DELETE /actions/{id}/items/{item}/path", func(r *http.Request) (any, error) {
		return nil, svc.UnmapItem(r.Context(), id(r), r.PathValue("item"))
	})
	h("DELETE /actions/{id}/items/{item}", func(r *http.Request) (any, error) {
		return nil, svc.RemoveItem(r.Context(), id(r), r.PathValue("item"))
	})

	h("POST /actions/{id}/sync", func(r *http.Request) (any, error) { return svc.Sync(r.Context(), id(r)) })
	h("POST /actions/{id}/snapshot", func(r *http.Request) (any, error) { return svc.Snapshot(r.Context(), id(r)) })
	h("POST /actions/{id}/pause", func(r *http.Request) (any, error) { return nil, svc.Pause(r.Context(), id(r)) })
	h("POST /actions/{id}/resume", func(r *http.Request) (any, error) { return nil, svc.Resume(r.Context(), id(r)) })
	h("GET /actions/{id}/pending-deletes", func(r *http.Request) (any, error) { return svc.PendingDeletes(r.Context(), id(r)) })
	h("POST /actions/{id}/approve-deletes", func(r *http.Request) (any, error) { return svc.ApproveDeletes(r.Context(), id(r)) })
	h("GET /actions/{id}/snapshots", func(r *http.Request) (any, error) { return svc.Snapshots(r.Context(), id(r)) })
	h("POST /actions/{id}/restore", func(r *http.Request) (any, error) {
		var in api.RestoreRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.Restore(r.Context(), id(r), in)
	})
	h("GET /actions/{id}/conflicts", func(r *http.Request) (any, error) { return svc.Conflicts(r.Context(), id(r)) })
	h("GET /actions/{id}/conflicts/diff", func(r *http.Request) (any, error) {
		return svc.ConflictDetail(r.Context(), id(r), r.URL.Query().Get("path"))
	})
	h("POST /actions/{id}/resolve", func(r *http.Request) (any, error) {
		var in struct {
			Choices map[string]model.Side `json:"choices"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.Resolve(r.Context(), id(r), in.Choices)
	})
	h("GET /actions/{id}/history", func(r *http.Request) (any, error) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		return svc.History(r.Context(), id(r), limit)
	})
	mux.HandleFunc("GET /events", s.events)
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeJSON(w, http.StatusUnauthorized, api.RemoteError{Code: "unauthorized", Message: "missing or wrong IPC token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, api.RemoteError{Code: "internal", Message: "streaming unsupported"})
		return
	}
	ch, err := s.svc.Subscribe(r.Context())
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		return fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	return nil
}

func writeResult(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := api.Code(err)
		writeJSON(w, api.HTTPStatus(code), api.RemoteError{Code: code, Message: err.Error()})
		return
	}
	if v == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 6: Implement the client**

Create `internal/ipc/client.go`:
```go
package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// Client is api.Service over IPC.
type Client struct {
	hc    *http.Client
	token string
}

var _ api.Service = (*Client)(nil)

// NewClient returns a client for the agent of dataDir.
func NewClient(dataDir, token string) *Client {
	tr := &http.Transport{
		DialContext:        func(ctx context.Context, _, _ string) (net.Conn, error) { return Dial(ctx, dataDir) },
		DisableCompression: true,
	}
	return &Client{hc: &http.Client{Transport: tr}, token: token}
}

// Connect reads the agent's token and checks that the agent answers.
func Connect(ctx context.Context, dataDir string) (*Client, error) {
	tok, err := ReadToken(dataDir)
	if err != nil {
		return nil, fmt.Errorf("ipc: read token: %w", err)
	}
	c := NewClient(dataDir, tok)
	if _, err := c.Status(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

func esc(s string) string { return url.PathEscape(s) }

func (c *Client) request(ctx context.Context, method, path string, in any) (*http.Response, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ipc: %w", err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var re api.RemoteError
		if json.NewDecoder(resp.Body).Decode(&re) == nil && re.Code != "" {
			return nil, &re
		}
		return nil, fmt.Errorf("ipc: %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	resp, err := c.request(ctx, method, path, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Status(ctx context.Context) (api.Status, error) {
	var v api.Status
	return v, c.call(ctx, "GET", "/status", nil, &v)
}

func (c *Client) Settings(ctx context.Context) (api.Settings, error) {
	var v api.Settings
	return v, c.call(ctx, "GET", "/settings", nil, &v)
}

func (c *Client) UpdateSettings(ctx context.Context, s api.Settings) (api.Settings, error) {
	var v api.Settings
	return v, c.call(ctx, "PUT", "/settings", s, &v)
}

func (c *Client) Profiles(ctx context.Context) ([]model.Profile, error) {
	var v []model.Profile
	return v, c.call(ctx, "GET", "/profiles", nil, &v)
}

func (c *Client) AddProfile(ctx context.Context, req api.AddProfileRequest) (model.Profile, error) {
	var v model.Profile
	return v, c.call(ctx, "POST", "/profiles", req, &v)
}

func (c *Client) TestProfile(ctx context.Context, id string) (storage.ProbeResult, error) {
	var v storage.ProbeResult
	return v, c.call(ctx, "POST", "/profiles/"+esc(id)+"/test", nil, &v)
}

func (c *Client) Discover(ctx context.Context, profileID string) ([]model.Action, error) {
	var v []model.Action
	return v, c.call(ctx, "GET", "/profiles/"+esc(profileID)+"/discover", nil, &v)
}

func (c *Client) Actions(ctx context.Context) ([]api.ActionStatus, error) {
	var v []api.ActionStatus
	return v, c.call(ctx, "GET", "/actions", nil, &v)
}

func (c *Client) Action(ctx context.Context, id string) (api.ActionStatus, error) {
	var v api.ActionStatus
	return v, c.call(ctx, "GET", "/actions/"+esc(id), nil, &v)
}

func (c *Client) CreateAction(ctx context.Context, req api.CreateActionRequest) (model.Action, error) {
	var v model.Action
	return v, c.call(ctx, "POST", "/actions", req, &v)
}

func (c *Client) JoinAction(ctx context.Context, req api.JoinActionRequest) (model.Action, error) {
	var v model.Action
	return v, c.call(ctx, "POST", "/actions/join", req, &v)
}

func (c *Client) UpdateRules(ctx context.Context, id string, rules model.Rules) (model.Action, error) {
	var v model.Action
	return v, c.call(ctx, "PUT", "/actions/"+esc(id)+"/rules", rules, &v)
}

func (c *Client) SetMachineDirection(ctx context.Context, id string, d model.Direction) error {
	return c.call(ctx, "PUT", "/actions/"+esc(id)+"/direction", map[string]model.Direction{"direction": d}, nil)
}

func (c *Client) LeaveAction(ctx context.Context, id string, force bool) error {
	path := "/actions/" + esc(id)
	if force {
		path += "?force=1"
	}
	return c.call(ctx, "DELETE", path, nil, nil)
}

func (c *Client) AddItem(ctx context.Context, id string, req api.AddItemRequest) (model.Item, error) {
	var v model.Item
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/items", req, &v)
}

func (c *Client) MapItem(ctx context.Context, id, itemID, localPath string) error {
	return c.call(ctx, "PUT", "/actions/"+esc(id)+"/items/"+esc(itemID)+"/path", map[string]string{"localPath": localPath}, nil)
}

func (c *Client) UnmapItem(ctx context.Context, id, itemID string) error {
	return c.call(ctx, "DELETE", "/actions/"+esc(id)+"/items/"+esc(itemID)+"/path", nil, nil)
}

func (c *Client) RemoveItem(ctx context.Context, id, itemID string) error {
	return c.call(ctx, "DELETE", "/actions/"+esc(id)+"/items/"+esc(itemID), nil, nil)
}

func (c *Client) Sync(ctx context.Context, id string) (model.SyncResult, error) {
	var v model.SyncResult
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/sync", nil, &v)
}

func (c *Client) Snapshot(ctx context.Context, id string) (model.SnapshotInfo, error) {
	var v model.SnapshotInfo
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/snapshot", nil, &v)
}

func (c *Client) Pause(ctx context.Context, id string) error {
	return c.call(ctx, "POST", "/actions/"+esc(id)+"/pause", nil, nil)
}

func (c *Client) Resume(ctx context.Context, id string) error {
	return c.call(ctx, "POST", "/actions/"+esc(id)+"/resume", nil, nil)
}

func (c *Client) PendingDeletes(ctx context.Context, id string) (*model.PendingDeletes, error) {
	var v *model.PendingDeletes
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/pending-deletes", nil, &v)
}

func (c *Client) ApproveDeletes(ctx context.Context, id string) (model.SyncResult, error) {
	var v model.SyncResult
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/approve-deletes", nil, &v)
}

func (c *Client) Snapshots(ctx context.Context, id string) ([]model.SnapshotInfo, error) {
	var v []model.SnapshotInfo
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/snapshots", nil, &v)
}

func (c *Client) Restore(ctx context.Context, id string, req api.RestoreRequest) (engine.RestoreResult, error) {
	var v engine.RestoreResult
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/restore", req, &v)
}

func (c *Client) Conflicts(ctx context.Context, id string) ([]model.Conflict, error) {
	var v []model.Conflict
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/conflicts", nil, &v)
}

func (c *Client) ConflictDetail(ctx context.Context, id, path string) (engine.ConflictDetail, error) {
	var v engine.ConflictDetail
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/conflicts/diff?path="+url.QueryEscape(path), nil, &v)
}

func (c *Client) Resolve(ctx context.Context, id string, choices map[string]model.Side) (model.SyncResult, error) {
	var v model.SyncResult
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/resolve", map[string]any{"choices": choices}, &v)
}

func (c *Client) History(ctx context.Context, id string, limit int) ([]model.HistoryEntry, error) {
	var v []model.HistoryEntry
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/history?limit="+strconv.Itoa(limit), nil, &v)
}

// Subscribe streams the agent's events until ctx ends or the agent stops.
func (c *Client) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	resp, err := c.request(ctx, "GET", "/events", nil)
	if err != nil {
		return nil, err
	}
	ch := make(chan api.Event, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var ev api.Event
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -count=1 -race ./internal/ipc/` and `go test -count=1 -race ./...`. Then `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/ipc/` (the Windows transport must compile).
Expected: `ok`, and the vet run is clean.

- [ ] **Step 8: Commit**

```bash
gofmt -w internal && go mod tidy && git add go.mod go.sum internal/ipc
git commit -m "feat(ipc): socket/pipe transport, token auth, HTTP server with SSE, typed client" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Autostart at login

**Files:**
- Create: `internal/autostart/autostart.go` (no build tag), `internal/autostart/autostart_darwin.go`, `internal/autostart/autostart_windows.go`, `internal/autostart/autostart_other.go` (`//go:build !darwin && !windows`)
- Test: `internal/autostart/autostart_test.go`, `internal/autostart/autostart_darwin_test.go`

**Interfaces:**
- Produces:
  - `autostart.Manager` interface `{Enable() error; Disable() error; Enabled() (bool, error)}`, which satisfies `agent.Autostart`.
  - `autostart.New(exe string, args []string) (Manager, error)`, which errors on unsupported OSes.
  - Constants `Label = "com.niceit.s3-anysync.agent"` and `RunValue = "s3-anysync"`.
  - Pure helpers, tested on every OS: `plistContent(exe, args) string` and `commandLine(exe, args) string` (Windows quoting).
- Behaviour (spec §13): enabling writes the LaunchAgent plist or the HKCU Run value; disabling removes it. Neither starts nor stops the running agent.

- [ ] **Step 1: Write the failing tests**

Create `internal/autostart/autostart_test.go`:
```go
package autostart

import (
	"strings"
	"testing"
)

func TestPlistContent(t *testing.T) {
	p := plistContent("/Applications/s3 & sync.app/Contents/MacOS/s3-anysync-agent", []string{"run"})
	for _, want := range []string{
		"<key>Label</key>", "<string>com.niceit.s3-anysync.agent</string>",
		"<string>/Applications/s3 &amp; sync.app/Contents/MacOS/s3-anysync-agent</string>", "<string>run</string>",
		"<key>RunAtLoad</key>", "<true/>", "<key>ProcessType</key>", "<string>Interactive</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
	if !strings.HasPrefix(p, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Error("missing XML header")
	}
}

func TestCommandLine(t *testing.T) {
	cases := map[string]struct {
		exe  string
		args []string
	}{
		`C:\s3\agent.exe run`:                         {`C:\s3\agent.exe`, []string{"run"}},
		`"C:\Program Files\s3\agent.exe" run`:          {`C:\Program Files\s3\agent.exe`, []string{"run"}},
		`C:\a.exe "two words" "say \"hi\"" ""`:         {`C:\a.exe`, []string{"two words", `say "hi"`, ""}},
		`"C:\dir with space\\" run`:                    {`C:\dir with space\`, []string{"run"}},
	}
	for want, c := range cases {
		if got := commandLine(c.exe, c.args); got != want {
			t.Errorf("commandLine(%q, %q) = %s, want %s", c.exe, c.args, got, want)
		}
	}
}
```
The last case checks that a trailing backslash before the closing quote is doubled (Windows argv rules).

Create `internal/autostart/autostart_darwin_test.go`:
```go
//go:build darwin

package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentEnableDisable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "LaunchAgents")
	m := newAt(dir, "/usr/local/bin/s3-anysync-agent", []string{"run"})
	if on, err := m.Enabled(); err != nil || on {
		t.Fatalf("enabled before Enable: %v %v", on, err)
	}
	if err := m.Enable(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, Label+".plist"))
	if err != nil || !strings.Contains(string(data), "/usr/local/bin/s3-anysync-agent") {
		t.Fatalf("plist = %s, %v", data, err)
	}
	if on, _ := m.Enabled(); !on {
		t.Fatal("not enabled after Enable")
	}
	if err := m.Disable(); err != nil {
		t.Fatal(err)
	}
	if on, _ := m.Enabled(); on {
		t.Fatal("still enabled after Disable")
	}
	if err := m.Disable(); err != nil {
		t.Fatalf("second Disable: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/autostart/`
Expected: FAIL with `undefined: plistContent`.

- [ ] **Step 3: Implement**

Create `internal/autostart/autostart.go`:
```go
// Package autostart starts the agent at login: a per-user LaunchAgent on
// macOS, a HKCU Run value on Windows. Enabling or disabling never starts or
// stops the running agent.
package autostart

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// Manager turns starting at login on or off.
type Manager interface {
	Enable() error
	Disable() error
	Enabled() (bool, error)
}

// Label is the macOS LaunchAgent label (and plist file name).
const Label = "com.niceit.s3-anysync.agent"

// RunValue is the Windows HKCU Run value name.
const RunValue = "s3-anysync"

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// plistContent is the LaunchAgent that runs exe with args at login.
func plistContent(exe string, args []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("\t<key>Label</key>\n\t<string>" + Label + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range append([]string{exe}, args...) {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>ProcessType</key>\n\t<string>Interactive</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// commandLine quotes exe and args the way Windows (CommandLineToArgvW) parses them.
// Example: `C:\dir with space\` becomes "C:\dir with space\\".
func commandLine(exe string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, a := range append([]string{exe}, args...) {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes*2+1))
			slashes = 0
			b.WriteByte(c)
			continue
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
			slashes = 0
			b.WriteByte(c)
			continue
		}
	}
	b.WriteString(strings.Repeat(`\`, slashes*2))
	b.WriteByte('"')
	return b.String()
}
```
Note on `quoteArg`: backslashes are counted and written out when a non-backslash character follows: doubled (+1) before a quote, kept as is before other characters, and doubled before the closing quote.

Create `internal/autostart/autostart_darwin.go`:
```go
//go:build darwin

package autostart

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type launchAgent struct {
	dir  string
	exe  string
	args []string
}

// New returns the LaunchAgent manager for exe run with args.
func New(exe string, args []string) (Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return newAt(filepath.Join(home, "Library", "LaunchAgents"), exe, args), nil
}

func newAt(dir, exe string, args []string) *launchAgent {
	return &launchAgent{dir: dir, exe: exe, args: args}
}

func (l *launchAgent) path() string { return filepath.Join(l.dir, Label+".plist") }

func (l *launchAgent) Enable() error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	tmp := l.path() + ".tmp"
	if err := os.WriteFile(tmp, []byte(plistContent(l.exe, l.args)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path())
}

func (l *launchAgent) Disable() error {
	if err := os.Remove(l.path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *launchAgent) Enabled() (bool, error) {
	_, err := os.Stat(l.path())
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
```

Create `internal/autostart/autostart_windows.go`:
```go
//go:build windows

package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

type runEntry struct{ cmd string }

// New returns the HKCU Run manager for exe run with args.
func New(exe string, args []string) (Manager, error) {
	return &runEntry{cmd: commandLine(exe, args)}, nil
}

func (r *runEntry) Enable() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(RunValue, r.cmd)
}

func (r *runEntry) Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(RunValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func (r *runEntry) Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	_, _, err = k.GetStringValue(RunValue)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
```

Create `internal/autostart/autostart_other.go`:
```go
//go:build !darwin && !windows

package autostart

import "errors"

// New reports that autostart is not supported on this OS.
func New(string, []string) (Manager, error) {
	return nil, errors.New("autostart is not supported on this OS")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 ./internal/autostart/`, then `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/autostart/`, then `GOOS=linux go vet ./internal/autostart/`
Expected: `ok`, and both vet runs are clean.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal && go mod tidy && git add go.mod go.sum internal/autostart
git commit -m "feat(autostart): LaunchAgent on macOS, HKCU Run on Windows" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: Tray icon and menu

**Files:**
- Create:
  - `internal/tray/menu.go` (no tag: labels and icon choice)
  - `internal/tray/icons.go` (no tag: generated PNG icons)
  - `internal/tray/tray.go` (`//go:build cgo || windows`: the systray glue)
  - `internal/tray/tray_stub.go` (`//go:build !cgo && !windows`)
- Test: `internal/tray/menu_test.go`

**Interfaces:**
- Consumes: `api.Service` (`Status`, `Subscribe`, `Sync`, `Snapshot`, `Pause`, `Resume`).
- Produces:
  - `tray.Options{OnQuit func(); OpenGUI func() error (nil hides "Open"); Log *slog.Logger}`.
  - `tray.Run(ctx, svc api.Service, o Options) error`. It blocks on the main thread until Quit is clicked (it then calls `OnQuit`) or ctx ends. The stub just waits for ctx.
  - `tray.Available() bool`.
  - Pure helpers: `iconFor(api.Status) iconKind`, `actionTitle(api.ActionStatus) string`, `tooltip(api.Status) string`, `iconPNG(iconKind) []byte`.
- Menu (spec §7):
  - a disabled status line;
  - "Open" (only if `OpenGUI` is set);
  - "Sync all now", "Snapshot all", "Pause all", "Resume all";
  - an "Actions" submenu with, per action: its status title, then "Sync now", "Snapshot now" and "Pause"/"Resume";
  - "Quit s3-anysync".

- [ ] **Step 1: Add the dependency**

```bash
go get fyne.io/systray@v1.12.2
```

- [ ] **Step 2: Write the failing tests**

Create `internal/tray/menu_test.go`:
```go
package tray

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/model"
)

func as(state string, mod func(*api.ActionStatus)) api.ActionStatus {
	s := api.ActionStatus{Action: model.Action{ID: "a", Name: "Editor"}, State: state}
	if mod != nil {
		mod(&s)
	}
	return s
}

func TestIconFor(t *testing.T) {
	cases := []struct {
		acts []api.ActionStatus
		want iconKind
	}{
		{nil, iconIdle},
		{[]api.ActionStatus{as(api.StateIdle, nil)}, iconIdle},
		{[]api.ActionStatus{as(api.StateIdle, func(s *api.ActionStatus) { s.Running = true })}, iconSyncing},
		{[]api.ActionStatus{as(api.StatePausedUser, nil)}, iconIdle},
		{[]api.ActionStatus{as(api.StatePausedConflict, nil), as(api.StateIdle, func(s *api.ActionStatus) { s.Running = true })}, iconAttention},
		{[]api.ActionStatus{as(api.StatePausedDeletes, nil)}, iconAttention},
		{[]api.ActionStatus{as(api.StateError, nil), as(api.StatePausedConflict, nil)}, iconError},
	}
	for i, c := range cases {
		if got := iconFor(api.Status{Actions: c.acts}); got != c.want {
			t.Errorf("case %d: icon %v, want %v", i, got, c.want)
		}
	}
}

func TestActionTitle(t *testing.T) {
	cases := map[string]api.ActionStatus{
		"Editor: idle":                         as(api.StateIdle, nil),
		"Editor: syncing…":                     as(api.StateIdle, func(s *api.ActionStatus) { s.Running = true }),
		"Editor: 2 conflict(s)":                as(api.StatePausedConflict, func(s *api.ActionStatus) { s.Conflicts = 2 }),
		"Editor: 5 deletion(s) to approve":     as(api.StatePausedDeletes, func(s *api.ActionStatus) { s.PendingDeletes = 5 }),
		"Editor: paused":                       as(api.StatePausedUser, nil),
		"Editor: error (network is down)":      as(api.StateError, func(s *api.ActionStatus) { s.LastError = "network is down" }),
	}
	for want, s := range cases {
		if got := actionTitle(s); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	long := as(api.StateError, func(s *api.ActionStatus) { s.LastError = string(make([]byte, 200)) })
	if got := actionTitle(long); len([]rune(got)) > 80 {
		t.Errorf("title not shortened: %d runes", len([]rune(got)))
	}
}

func TestTooltip(t *testing.T) {
	st := api.Status{Actions: []api.ActionStatus{as(api.StateIdle, nil), as(api.StatePausedConflict, nil), as(api.StateError, nil)}}
	if got := tooltip(st); got != "s3-anysync: 3 action(s), 2 need attention" {
		t.Fatalf("tooltip = %q", got)
	}
	if got := tooltip(api.Status{}); got != "s3-anysync: no actions" {
		t.Fatalf("tooltip = %q", got)
	}
}

func TestIconsArePNG(t *testing.T) {
	for _, k := range []iconKind{iconIdle, iconSyncing, iconAttention, iconError} {
		img, err := png.Decode(bytes.NewReader(iconPNG(k)))
		if err != nil || img.Bounds().Dx() != 32 {
			t.Fatalf("icon %v: %v", k, err)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./internal/tray/`
Expected: FAIL with `undefined: iconFor`.

- [ ] **Step 4: Implement the pure parts**

Create `internal/tray/menu.go`:
```go
// Package tray shows the agent's status icon and menu. The real tray needs
// CGO on macOS; builds without it get a no-op stub.
package tray

import (
	"fmt"
	"log/slog"

	"github.com/niceit/s3-anysync/internal/api"
)

// Options configure the tray.
type Options struct {
	// OnQuit is called when the user picks Quit.
	OnQuit func()
	// OpenGUI opens the settings window; nil hides the Open item.
	OpenGUI func() error
	Log     *slog.Logger
}

type iconKind int

const (
	iconIdle iconKind = iota
	iconSyncing
	iconAttention
	iconError
)

// iconFor picks the icon: error beats attention beats syncing beats idle.
func iconFor(st api.Status) iconKind {
	kind := iconIdle
	for _, a := range st.Actions {
		switch {
		case a.State == api.StateError:
			return iconError
		case a.State == api.StatePausedConflict || a.State == api.StatePausedDeletes:
			kind = iconAttention
		case a.Running && kind == iconIdle:
			kind = iconSyncing
		}
	}
	return kind
}

const maxTitle = 80

// actionTitle is the menu line for one action.
func actionTitle(a api.ActionStatus) string {
	var s string
	switch {
	case a.Running:
		s = "syncing…"
	case a.State == api.StatePausedConflict:
		s = fmt.Sprintf("%d conflict(s)", a.Conflicts)
	case a.State == api.StatePausedDeletes:
		s = fmt.Sprintf("%d deletion(s) to approve", a.PendingDeletes)
	case a.State == api.StatePausedUser:
		s = "paused"
	case a.State == api.StateError:
		s = "error (" + a.LastError + ")"
	default:
		s = "idle"
	}
	title := []rune(a.Action.Name + ": " + s)
	if len(title) > maxTitle {
		title = append(title[:maxTitle-1], '…')
	}
	return string(title)
}

// tooltip summarises all actions.
func tooltip(st api.Status) string {
	if len(st.Actions) == 0 {
		return "s3-anysync: no actions"
	}
	attention := 0
	for _, a := range st.Actions {
		switch a.State {
		case api.StateError, api.StatePausedConflict, api.StatePausedDeletes:
			attention++
		}
	}
	if attention == 0 {
		return fmt.Sprintf("s3-anysync: %d action(s), all fine", len(st.Actions))
	}
	return fmt.Sprintf("s3-anysync: %d action(s), %d need attention", len(st.Actions), attention)
}
```

Create `internal/tray/icons.go`:
```go
package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

var iconColors = map[iconKind]color.RGBA{
	iconIdle:      {0x2e, 0x9e, 0x5b, 0xff}, // green
	iconSyncing:   {0x2f, 0x6f, 0xd6, 0xff}, // blue
	iconAttention: {0xe0, 0x8e, 0x1b, 0xff}, // orange
	iconError:     {0xd0, 0x31, 0x31, 0xff}, // red
}

// iconPNG draws a 32×32 filled circle in the state's colour.
func iconPNG(k iconKind) []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	c := iconColors[k]
	r := float64(size)/2 - 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-float64(size)/2+0.5, float64(y)-float64(size)/2+0.5
			if dx*dx+dy*dy <= r*r {
				img.SetRGBA(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}
```

- [ ] **Step 5: Implement the tray and the stub**

Create `internal/tray/tray_stub.go`:
```go
//go:build !cgo && !windows

package tray

import (
	"context"

	"github.com/niceit/s3-anysync/internal/api"
)

// Available reports whether this build has a real tray.
func Available() bool { return false }

// Run has no tray to show in this build: it waits for ctx.
func Run(ctx context.Context, svc api.Service, o Options) error {
	<-ctx.Done()
	return nil
}
```

Create `internal/tray/tray.go`:
```go
//go:build cgo || windows

package tray

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/niceit/s3-anysync/internal/api"
)

// Available reports whether this build has a real tray.
func Available() bool { return true }

// Run shows the tray and blocks until Quit is picked or ctx ends. On macOS
// it must be called from the main goroutine (the process's main thread).
func Run(ctx context.Context, svc api.Service, o Options) error {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	t := &tray{svc: svc, o: o, actions: map[string]*actionMenu{}}
	systray.Run(func() { t.ready(ctx) }, func() {})
	return nil
}

type actionMenu struct {
	title, sync, snap, pause *systray.MenuItem
	paused                   bool
	stop                     chan struct{}
}

type tray struct {
	svc     api.Service
	o       Options
	mu      sync.Mutex
	status  *systray.MenuItem
	parent  *systray.MenuItem
	actions map[string]*actionMenu
	icon    iconKind
	iconSet bool
}

func (t *tray) ready(ctx context.Context) {
	systray.SetIcon(iconPNG(iconIdle))
	systray.SetTooltip("s3-anysync")
	t.status = systray.AddMenuItem("s3-anysync", "")
	t.status.Disable()
	var open *systray.MenuItem
	if t.o.OpenGUI != nil {
		open = systray.AddMenuItem("Open s3-anysync", "Open the settings window")
	}
	systray.AddSeparator()
	syncAll := systray.AddMenuItem("Sync all now", "")
	snapAll := systray.AddMenuItem("Snapshot all", "")
	pauseAll := systray.AddMenuItem("Pause all", "")
	resumeAll := systray.AddMenuItem("Resume all", "")
	systray.AddSeparator()
	t.parent = systray.AddMenuItem("Actions", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit s3-anysync", "Stop the background agent")

	t.refresh(ctx)
	go t.watch(ctx)

	var openC <-chan struct{}
	if open != nil {
		openC = open.ClickedCh
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				systray.Quit()
				return
			case <-quit.ClickedCh:
				if t.o.OnQuit != nil {
					t.o.OnQuit()
				}
				systray.Quit()
				return
			case <-openC:
				if err := t.o.OpenGUI(); err != nil {
					t.o.Log.Warn("open window", "err", err)
				}
			case <-syncAll.ClickedCh:
				go t.forAll(ctx, func(id string) error { _, err := t.svc.Sync(ctx, id); return err })
			case <-snapAll.ClickedCh:
				go t.forAll(ctx, func(id string) error { _, err := t.svc.Snapshot(ctx, id); return err })
			case <-pauseAll.ClickedCh:
				go t.forAll(ctx, func(id string) error { return t.svc.Pause(ctx, id) })
			case <-resumeAll.ClickedCh:
				go t.forAll(ctx, func(id string) error { return t.svc.Resume(ctx, id) })
			}
		}
	}()
}

func (t *tray) forAll(ctx context.Context, f func(id string) error) {
	st, err := t.svc.Status(ctx)
	if err != nil {
		t.o.Log.Warn("tray status", "err", err)
		return
	}
	for _, a := range st.Actions {
		if err := f(a.Action.ID); err != nil {
			t.o.Log.Info("tray action", "action", a.Action.ID, "err", err)
		}
	}
}

// watch refreshes the menu on events, at most every 250ms.
func (t *tray) watch(ctx context.Context) {
	ch, err := t.svc.Subscribe(ctx)
	if err != nil {
		t.o.Log.Warn("tray subscribe", "err", err)
		return
	}
	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if pending == nil {
				pending = time.After(250 * time.Millisecond)
			}
		case <-pending:
			pending = nil
			t.refresh(ctx)
		}
	}
}

func (t *tray) refresh(ctx context.Context) {
	st, err := t.svc.Status(ctx)
	if err != nil {
		t.o.Log.Warn("tray status", "err", err)
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if k := iconFor(st); !t.iconSet || k != t.icon {
		systray.SetIcon(iconPNG(k))
		t.icon, t.iconSet = k, true
	}
	tip := tooltip(st)
	systray.SetTooltip(tip)
	t.status.SetTitle(tip)
	seen := map[string]bool{}
	for _, a := range st.Actions {
		id := a.Action.ID
		seen[id] = true
		m, ok := t.actions[id]
		if !ok {
			m = t.addAction(ctx, id)
			t.actions[id] = m
		}
		m.title.SetTitle(actionTitle(a))
		m.paused = a.State == api.StatePausedUser
		if m.paused {
			m.pause.SetTitle("Resume")
		} else {
			m.pause.SetTitle("Pause")
		}
	}
	for id, m := range t.actions {
		if !seen[id] {
			close(m.stop)
			m.title.Remove()
			delete(t.actions, id)
		}
	}
}

func (t *tray) addAction(ctx context.Context, id string) *actionMenu {
	m := &actionMenu{stop: make(chan struct{})}
	m.title = t.parent.AddSubMenuItem(id, "")
	m.sync = m.title.AddSubMenuItem("Sync now", "")
	m.snap = m.title.AddSubMenuItem("Snapshot now", "")
	m.pause = m.title.AddSubMenuItem("Pause", "")
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stop:
				return
			case <-m.sync.ClickedCh:
				go t.svc.Sync(ctx, id)
			case <-m.snap.ClickedCh:
				go t.svc.Snapshot(ctx, id)
			case <-m.pause.ClickedCh:
				t.mu.Lock()
				paused := m.paused
				t.mu.Unlock()
				if paused {
					go t.svc.Resume(ctx, id)
				} else {
					go t.svc.Pause(ctx, id)
				}
			}
		}
	}()
	return m
}
```

- [ ] **Step 6: Run the tests and builds**

Run:
- `CGO_ENABLED=0 go test -count=1 ./internal/tray/`: the pure parts plus the stub.
- `CGO_ENABLED=1 go build ./internal/tray/`: the real tray on macOS; needs Xcode command-line tools.
- `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./internal/tray/`: the real tray on Windows, no CGO.

Expected: the tests report `ok`, and both builds succeed. The systray glue can't be unit tested; Task 18 checks it by hand.

- [ ] **Step 7: Commit**

```bash
gofmt -w internal && go mod tidy && git add go.mod go.sum internal/tray
git commit -m "feat(tray): status icon and menu (CGO on macOS, stub without)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: CLI on api.Service, with automatic agent detection and new commands

**Files:**
- Rewrite: `cmd/agent/cli.go`, `cmd/agent/main.go`
- Create: `cmd/agent/connect.go`, `cmd/agent/rules.go`
- Modify: `cmd/agent/cli_test.go` (adapt the harness)
- Test: `cmd/agent/cli2_test.go` (new commands), `cmd/agent/rules_test.go`

**Interfaces:**
- Consumes: `api.Service`, `agent.New`, `ipc.Connect`, `datalock.Acquire`, `autostart.New`, `schedule.Validate` and `schedule.NextRuns`.
- Produces:
  - `type connector func(ctx context.Context) (api.Service, func(), error)` and `run(args, stdout, stderr, connector) int`.
  - `connectTo(ctx, dataDir string, sec secrets.Store, auto agent.Autostart, configure func(*app.App)) (api.Service, func(), error)`. It takes the data-folder lock and serves in-process (an `agent.Manager` without triggers). If the agent holds the lock (`datalock.ErrBusy`), it uses `ipc.Connect` instead.
  - `parseSchedule(spec) (model.Schedule, error)`.
  - New commands: `status`, `settings`, `autostart`, `action rules`, `action leave`, `item unmap`, `item remove`, `pause`, `resume`, `approve-deletes`, `diff` and `history`.
- Every existing command keeps its flags and output lines, so the existing CLI tests keep their assertions.

- [ ] **Step 1: Write the schedule-parsing test**

Create `cmd/agent/rules_test.go`:
```go
package main

import (
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func TestParseSchedule(t *testing.T) {
	ok := map[string]model.Schedule{
		"none":                {Kind: model.ScheduleNone},
		"every:15":            {Kind: model.ScheduleInterval, EveryMinutes: 15},
		"daily:09:30":         {Kind: model.ScheduleDaily, At: "09:30"},
		"weekly:1@10:00":      {Kind: model.ScheduleWeekly, Weekday: 1, At: "10:00"},
		"cron:*/10 * * * *":   {Kind: model.ScheduleCron, Cron: "*/10 * * * *"},
	}
	for in, want := range ok {
		got, err := parseSchedule(in)
		if err != nil || got != want {
			t.Errorf("parseSchedule(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "hourly", "every:0", "every:x", "daily:9:30", "weekly:8@10:00", "weekly:1", "cron:nope"} {
		if _, err := parseSchedule(in); err == nil {
			t.Errorf("parseSchedule(%q) accepted", in)
		}
	}
	if got := describeSchedule(model.Schedule{Kind: model.ScheduleWeekly, Weekday: 1, At: "10:00"}); got != "weekly on Monday at 10:00" {
		t.Errorf("describe = %q", got)
	}
}
```

- [ ] **Step 2: Adapt the existing test harness**

In `cmd/agent/cli_test.go`, replace `opener` with the version below, and change every `open func() (*app.App, error)` parameter (in `runOK`, `runFail` and similar helpers) to `open connector`:
```go
// opener simulates one machine in the CLI's in-process mode: its own data
// dir and secrets, sharing one bucket with the other machines.
func opener(t *testing.T, store storage.Storage) connector {
	dir, sec := t.TempDir(), secrets.NewMemory()
	return func(ctx context.Context) (api.Service, func(), error) {
		return connectTo(ctx, dir, sec, nil, func(a *app.App) {
			a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
			a.LockSettle = 0
		})
	}
}
```
Tests that built an `open` function returning an error (for example `TestRunPrintsExplanation`) now return the error from a connector: `func(context.Context) (api.Service, func(), error) { return nil, nil, fmt.Errorf("open: %w", storage.ErrLocked) }`.

Tests that read the database directly (for example `TestCLIReceiverRestoresCloudFlag` asserting via `DB.GetAction`) must assert through the CLI instead. `action rules ACTION_ID` with no flags prints the rules, including the line `receiver restores cloud: yes`. Keep every other assertion unchanged.

- [ ] **Step 3: Write the failing tests for the new commands**

Create `cmd/agent/cli2_test.go`:
```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

// cliSetup creates a profile, an action and a dir item with n files on m.
func cliSetup(t *testing.T, m connector, n int) (actionID, itemID, dir string) {
	t.Helper()
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	p := valueAfter(t, runOK(t, m, "profile", "add", "--name", "p", "--bucket", "b", "--access-key", "k"), "profile created")
	actionID = valueAfter(t, runOK(t, m, "action", "create", "--profile", p, "--name", "Editor"), "action created")
	dir = filepath.Join(t.TempDir(), "snippets")
	os.MkdirAll(dir, 0o755)
	for i := 0; i < n; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("s%02d.json", i)), []byte(fmt.Sprintf("snippet %d", i)), 0o644)
	}
	itemID = valueAfter(t, runOK(t, m, "item", "add", "--remote-name", "snippets", "--type", "dir", "--path", dir, actionID), "item added")
	return
}

func TestCLIStatusPauseResumeHistory(t *testing.T) {
	m := opener(t, storage.NewMemory())
	act, _, _ := cliSetup(t, m, 1)
	runOK(t, m, "sync", act)
	out := runOK(t, m, "status")
	if !strings.Contains(out, "in this process") || !strings.Contains(out, "Editor") || !strings.Contains(out, "idle") {
		t.Fatalf("status:\n%s", out)
	}
	if out := runOK(t, m, "pause", act); !strings.Contains(out, "paused") {
		t.Fatalf("pause:\n%s", out)
	}
	if out := runOK(t, m, "status"); !strings.Contains(out, "paused") {
		t.Fatalf("status after pause:\n%s", out)
	}
	runOK(t, m, "resume", act)
	if out := runOK(t, m, "history", "--limit", "5", act); !strings.Contains(out, "sync") || !strings.Contains(out, "ok") {
		t.Fatalf("history:\n%s", out)
	}
}

func TestCLIRules(t *testing.T) {
	m := opener(t, storage.NewMemory())
	act, _, _ := cliSetup(t, m, 1)
	out := runOK(t, m, "action", "rules", "--on-change", "--schedule", "daily:09:30", "--exclude", "*.log", "--delete-guard-files", "10", "--receiver-restores-cloud", act)
	for _, want := range []string{"rules updated", "on change: yes", "daily at 09:30", "next runs:", "excludes: *.log", "delete guard: 10 file(s) and 50%", "receiver restores cloud: yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("rules output lacks %q:\n%s", want, out)
		}
	}
	if out := runOK(t, m, "action", "rules", "--on-change=false", "--no-excludes", act); !strings.Contains(out, "on change: no") || !strings.Contains(out, "excludes: none") {
		t.Fatalf("second update:\n%s", out)
	}
	if out := runFail(t, m, "action", "rules", "--schedule", "daily:9:30", act); !strings.Contains(out, "HH:MM") {
		t.Fatalf("invalid schedule:\n%s", out)
	}
	if out := runOK(t, m, "action", "rules", act); !strings.Contains(out, "name: Editor") {
		t.Fatalf("show rules:\n%s", out)
	}
}

func TestCLIApproveDeletes(t *testing.T) {
	m := opener(t, storage.NewMemory())
	act, _, dir := cliSetup(t, m, 8)
	runOK(t, m, "sync", act)
	for i := 0; i < 5; i++ {
		os.Remove(filepath.Join(dir, fmt.Sprintf("s%02d.json", i)))
	}
	out := runOK(t, m, "sync", act)
	if !strings.Contains(out, "deletes-pending") || !strings.Contains(out, "pending deletion: snippets/s00.json") || !strings.Contains(out, "approve-deletes") {
		t.Fatalf("sync:\n%s", out)
	}
	if out := runOK(t, m, "approve-deletes", "--list", act); strings.Count(out, "pending deletion:") != 5 {
		t.Fatalf("list:\n%s", out)
	}
	if out := runOK(t, m, "approve-deletes", act); !strings.Contains(out, "deleted in cloud 5") {
		t.Fatalf("approve:\n%s", out)
	}
	if out := runOK(t, m, "approve-deletes", act); !strings.Contains(out, "no deletions are waiting") {
		t.Fatalf("nothing pending:\n%s", out)
	}
}

func TestCLIDiffAndItemsAndLeave(t *testing.T) {
	store := storage.NewMemory()
	a, b := opener(t, store), opener(t, store)
	act, item, dirA := cliSetup(t, a, 1)
	runOK(t, a, "sync", act)
	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "p", "--bucket", "b", "--access-key", "k"), "profile created")
	runOK(t, b, "action", "join", "--profile", pb, act)
	dirB := filepath.Join(t.TempDir(), "snippets")
	runOK(t, b, "item", "map", "--path", dirB, act, item)
	runOK(t, b, "sync", act)
	os.WriteFile(filepath.Join(dirA, "s00.json"), []byte("from A"), 0o644)
	runOK(t, a, "sync", act)
	os.WriteFile(filepath.Join(dirB, "s00.json"), []byte("from B!"), 0o644)
	runOK(t, b, "sync", act)
	out := runOK(t, b, "diff", act, "snippets/s00.json")
	if !strings.Contains(out, "from B!") || !strings.Contains(out, "from A") || !strings.Contains(out, "this machine") {
		t.Fatalf("diff:\n%s", out)
	}
	runOK(t, b, "resolve", "--all", "cloud", act)

	if out := runOK(t, b, "item", "unmap", act, item); !strings.Contains(out, "unmapped") {
		t.Fatalf("unmap:\n%s", out)
	}
	if out := runOK(t, a, "item", "remove", act, item); !strings.Contains(out, "removed") {
		t.Fatalf("remove:\n%s", out)
	}
	if out := runOK(t, b, "action", "leave", act); !strings.Contains(out, "left action") {
		t.Fatalf("leave:\n%s", out)
	}
	if out := runOK(t, b, "action", "list"); strings.Contains(out, act) {
		t.Fatalf("action still listed after leave:\n%s", out)
	}
}

func TestCLISettingsAndAutostartUnsupported(t *testing.T) {
	m := opener(t, storage.NewMemory())
	if out := runOK(t, m, "settings", "--log-level", "debug"); !strings.Contains(out, "log level: debug") {
		t.Fatalf("settings:\n%s", out)
	}
	if out := runOK(t, m, "autostart", "status"); !strings.Contains(out, "not supported") {
		t.Fatalf("autostart status:\n%s", out)
	}
	if out := runFail(t, m, "autostart", "enable"); !strings.Contains(out, "not supported") {
		t.Fatalf("autostart enable:\n%s", out)
	}
}

func TestCLIThroughTheAgentOverIPC(t *testing.T) {
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	a, err := app.Open(dir, secrets.NewMemory(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewMemory()
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
	a.LockSettle = 0
	m, _ := agent.New(agent.Options{App: a, Triggers: true, Version: "v-test"})
	m.Start()
	tok, _ := ipc.NewToken(dir)
	l, err := ipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer(m, tok, nil)
	go srv.Serve(l)
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		ipc.Cleanup(dir)
		m.Close()
		a.Close()
	}()
	remote := func(ctx context.Context) (api.Service, func(), error) {
		c, err := ipc.Connect(ctx, dir)
		if err != nil {
			return nil, nil, err
		}
		return c, c.Close, nil
	}
	if out := runOK(t, remote, "status"); !strings.Contains(out, "background agent v-test") {
		t.Fatalf("status over IPC:\n%s", out)
	}
	act, _, _ := cliSetup(t, remote, 1)
	if out := runOK(t, remote, "sync", act); !strings.Contains(out, "uploaded 1,") {
		t.Fatalf("sync over IPC:\n%s", out)
	}
}
```
If `runFail` does not exist in `cli_test.go`, add it next to `runOK`: it runs the command, fails the test when the exit code is 0, and returns stderr.

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./cmd/agent/`
Expected: FAIL to compile (`undefined: connector`, `connectTo`, `parseSchedule`).

- [ ] **Step 5: Implement the connector and main**

Create `cmd/agent/connect.go`:
```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/secrets"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// connector opens the service for one CLI command; the returned func closes it.
type connector func(ctx context.Context) (api.Service, func(), error)

// connectTo serves the command in this process when it can take the data
// folder, or through the running agent when the agent owns it. configure
// adjusts an in-process App (tests replace its storage).
func connectTo(ctx context.Context, dataDir string, sec secrets.Store, auto agent.Autostart, configure func(*app.App)) (api.Service, func(), error) {
	lock, err := datalock.Acquire(dataDir)
	if errors.Is(err, datalock.ErrBusy) {
		c, err := ipc.Connect(ctx, dataDir)
		if err != nil {
			return nil, nil, fmt.Errorf("the agent owns the data folder but does not answer (it may be starting or stopping): %w", err)
		}
		return c, c.Close, nil
	}
	if err != nil {
		return nil, nil, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a, err := app.Open(dataDir, sec, log)
	if err != nil {
		lock.Release()
		return nil, nil, err
	}
	if configure != nil {
		configure(a)
	}
	m, err := agent.New(agent.Options{App: a, Autostart: auto, Version: version, Log: log})
	if err == nil {
		err = m.Start()
	}
	if err != nil {
		a.Close()
		lock.Release()
		return nil, nil, err
	}
	return m, func() { m.Close(); a.Close(); lock.Release() }, nil
}
```

Rewrite `cmd/agent/main.go`:
```go
// Command s3-anysync-agent is the s3-anysync background agent and its CLI.
// "s3-anysync-agent run" starts the agent; every other command talks to the
// running agent, or works in-process when no agent runs.
package main

import (
	"context"
	"os"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/autostart"
	"github.com/niceit/s3-anysync/internal/secrets"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, connectDefault))
}

// connectDefault connects to the user's real data folder and keychain.
func connectDefault(ctx context.Context) (api.Service, func(), error) {
	dir, err := app.DefaultDataDir()
	if err != nil {
		return nil, nil, err
	}
	return connectTo(ctx, dir, secrets.NewKeyring(), defaultAutostart(), nil)
}

// defaultAutostart registers this executable with "run"; nil if unsupported.
func defaultAutostart() agent.Autostart {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	m, err := autostart.New(exe, []string{"run"})
	if err != nil {
		return nil
	}
	return m
}
```

- [ ] **Step 6: Implement the schedule helpers**

Create `cmd/agent/rules.go`:
```go
package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/schedule"
)

// parseSchedule parses none, every:MINUTES, daily:HH:MM,
// weekly:DAY@HH:MM (DAY 0=Sunday … 6=Saturday) or cron:SPEC.
func parseSchedule(spec string) (model.Schedule, error) {
	kind, rest, _ := strings.Cut(spec, ":")
	var s model.Schedule
	switch kind {
	case "none":
		s = model.Schedule{Kind: model.ScheduleNone}
	case "every":
		n, err := strconv.Atoi(rest)
		if err != nil {
			return s, fmt.Errorf("--schedule every:MINUTES, got %q", spec)
		}
		s = model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: n}
	case "daily":
		s = model.Schedule{Kind: model.ScheduleDaily, At: rest}
	case "weekly":
		day, at, ok := strings.Cut(rest, "@")
		d, err := strconv.Atoi(day)
		if !ok || err != nil {
			return s, fmt.Errorf("--schedule weekly:DAY@HH:MM, got %q", spec)
		}
		s = model.Schedule{Kind: model.ScheduleWeekly, Weekday: d, At: at}
	case "cron":
		s = model.Schedule{Kind: model.ScheduleCron, Cron: rest}
	default:
		return s, errors.New("--schedule must be none, every:MINUTES, daily:HH:MM, weekly:DAY@HH:MM or cron:SPEC")
	}
	return s, schedule.Validate(s)
}

// describeSchedule is a human description of s.
func describeSchedule(s model.Schedule) string {
	switch s.Kind {
	case model.ScheduleInterval:
		return fmt.Sprintf("every %d minute(s)", s.EveryMinutes)
	case model.ScheduleDaily:
		return "daily at " + s.At
	case model.ScheduleWeekly:
		return fmt.Sprintf("weekly on %s at %s", time.Weekday(s.Weekday), s.At)
	case model.ScheduleCron:
		return "cron " + s.Cron
	}
	return "none"
}
```

- [ ] **Step 7: Rewrite cli.go**

Replace `cmd/agent/cli.go` with:
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

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/schedule"
	"github.com/niceit/s3-anysync/internal/storage"
)

const usage = `usage: s3-anysync-agent <command> [flags] [arguments]

Flags always come before arguments. When the agent is running, commands go
through it; otherwise they run in this process.

  run [--no-tray] [--verbose]          start the background agent
  status
  machine [--name NAME]
  settings [--log-level debug|info|warn|error]
  autostart enable|disable|status
  profile add --name N --bucket B --access-key K [--provider P] [--endpoint URL] [--region R] [--prefix P] [--path-style]
              (secret key from S3_ANYSYNC_SECRET)
  profile list
  profile test PROFILE_ID
  action create --profile ID --name N [--direction D] [--no-propagate-deletes] [--receiver-restores-cloud] [--encrypt] [--exclude GLOB]... [--retention N]
              (passphrase from S3_ANYSYNC_PASSPHRASE when --encrypt)
  action list
  action discover --profile ID
  action join --profile ID ACTION_ID      (passphrase from S3_ANYSYNC_PASSPHRASE for encrypted actions)
  action direction ACTION_ID two-way|local-to-cloud|cloud-to-local|default
  action rules [--name N] [--direction D] [--on-change[=false]] [--debounce SECONDS] [--schedule SPEC] [--poll MINUTES]
               [--propagate-deletes[=false]] [--receiver-restores-cloud[=false]] [--exclude GLOB]... [--no-excludes]
               [--retention N] [--delete-guard-files N] [--delete-guard-percent P] ACTION_ID
               SPEC: none | every:MINUTES | daily:HH:MM | weekly:DAY@HH:MM (DAY 0=Sunday) | cron:SPEC
               without flags it shows the rules
  action leave [--force] ACTION_ID
  item add --remote-name NAME --type file|dir --path LOCAL_PATH [--exclude GLOB]... ACTION_ID
  item map --path LOCAL_PATH ACTION_ID ITEM_ID
  item unmap ACTION_ID ITEM_ID
  item remove ACTION_ID ITEM_ID
  sync ACTION_ID
  pause ACTION_ID
  resume ACTION_ID
  approve-deletes [--list] ACTION_ID
  snapshot ACTION_ID
  snapshots ACTION_ID
  restore --to local|cloud|both [--path P]... ACTION_ID TS
  conflicts ACTION_ID
  diff ACTION_ID PATH
  resolve (--all local|cloud | --path P=local|cloud ...) ACTION_ID
  history [--limit N] ACTION_ID

Environment: S3_ANYSYNC_DATA_DIR overrides the data folder.
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type cli struct {
	svc api.Service
	out io.Writer
}

type command func(*cli, context.Context, []string) error

func run(args []string, stdout, stderr io.Writer, connect connector) int {
	commands := map[string]command{
		"status": (*cli).status, "machine": (*cli).machine, "settings": (*cli).settings, "autostart": (*cli).autostart,
		"profile": (*cli).profile, "action": (*cli).action, "item": (*cli).item,
		"sync": (*cli).sync, "pause": (*cli).pause, "resume": (*cli).resume, "approve-deletes": (*cli).approveDeletes,
		"snapshot": (*cli).snapshot, "snapshots": (*cli).snapshots, "restore": (*cli).restore,
		"conflicts": (*cli).conflicts, "diff": (*cli).diff, "resolve": (*cli).resolve, "history": (*cli).history,
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
	ctx := context.Background()
	svc, closeFn, err := connect(ctx)
	if err != nil {
		reportError(stderr, err)
		return 1
	}
	defer closeFn()
	if err := cmd(&cli{svc: svc, out: stdout}, ctx, args[1:]); err != nil {
		reportError(stderr, err)
		return 1
	}
	return 0
}

func reportError(w io.Writer, err error) {
	fmt.Fprintln(w, "error:", err)
	if hint := explain(err); hint != "" {
		fmt.Fprintln(w, hint)
	}
}

// explain returns a one-line explanation of well-known errors, or "".
func explain(err error) string {
	switch {
	case errors.Is(err, engine.ErrKeyCheckMissing):
		return "  hint: the keycheck file (.meta/keycheck) of this action was lost in the bucket, so the passphrase cannot be verified; restore that object from a backup of the bucket or ask the machine that created the action."
	case errors.Is(err, engine.ErrDecrypt):
		return "  hint: the passphrase is wrong or the cloud data is damaged; check S3_ANYSYNC_PASSPHRASE."
	case errors.Is(err, engine.ErrPassphraseMissing):
		return "  hint: this action is encrypted; set S3_ANYSYNC_PASSPHRASE and re-run the same command (action create / action join / sync)."
	case errors.Is(err, engine.ErrRootMissing):
		return "  hint: an item's folder is missing (unmounted drive or renamed folder?); restore or re-mount it. If it was removed on purpose, unmap the item and map it again."
	case errors.Is(err, storage.ErrLocked):
		return "  hint: another machine is syncing this action; try again in a minute."
	case errors.Is(err, storage.ErrLockLost):
		return "  hint: this machine lost the cloud lock during the run (it took too long or the connection dropped); nothing is corrupted, run the sync again."
	case errors.Is(err, datalock.ErrBusy):
		return "  hint: another s3-anysync process owns the data folder; if the agent is starting or stopping, try again in a moment."
	}
	return ""
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

func when(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (c *cli) status(ctx context.Context, args []string) error {
	if _, err := parseArgs(flag.NewFlagSet("status", flag.ContinueOnError), args); err != nil {
		return err
	}
	st, err := c.svc.Status(ctx)
	if err != nil {
		return err
	}
	served := "this process (the agent is not running)"
	if st.Agent {
		served = "background agent " + st.Version
	}
	fmt.Fprintf(c.out, "machine: %s (%s)\nserved by: %s\n", st.MachineName, st.MachineID, served)
	w := c.table()
	fmt.Fprintln(w, "ID\tNAME\tSTATE\tLAST RUN\tNEXT RUN\tDETAILS")
	for _, a := range st.Actions {
		state := a.State
		if a.Running {
			state += " (running)"
		}
		details := a.LastSummary
		if a.LastError != "" {
			details = "error: " + a.LastError
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a.Action.ID, a.Action.Name, state, when(a.LastRun), when(a.NextRun), details)
	}
	return w.Flush()
}

func (c *cli) machine(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("machine", flag.ContinueOnError)
	name := fs.String("name", "", "new friendly name")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	s, err := c.svc.Settings(ctx)
	if err != nil {
		return err
	}
	if *name != "" {
		s.MachineName = *name
		if s, err = c.svc.UpdateSettings(ctx, s); err != nil {
			return err
		}
	}
	fmt.Fprintf(c.out, "machine id: %s\nmachine name: %s\n", s.MachineID, s.MachineName)
	return nil
}

func (c *cli) settings(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	level := fs.String("log-level", "", "debug, info, warn or error")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	s, err := c.svc.Settings(ctx)
	if err != nil {
		return err
	}
	if *level != "" {
		s.LogLevel = *level
		if s, err = c.svc.UpdateSettings(ctx, s); err != nil {
			return err
		}
	}
	auto := "not supported on this system"
	if s.AutostartSupported {
		auto = yesNo(s.Autostart)
	}
	fmt.Fprintf(c.out, "machine name: %s\nlog level: %s\nautostart: %s\n", s.MachineName, s.LogLevel, auto)
	return nil
}

func (c *cli) autostart(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("autostart: expected enable, disable or status")
	}
	s, err := c.svc.Settings(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
	case "enable", "disable":
		s.Autostart = args[0] == "enable"
		if s, err = c.svc.UpdateSettings(ctx, s); err != nil {
			return err
		}
	default:
		return fmt.Errorf("autostart: unknown subcommand %q", args[0])
	}
	if !s.AutostartSupported {
		fmt.Fprintln(c.out, "autostart: not supported on this system")
		return nil
	}
	fmt.Fprintf(c.out, "autostart: %s\n", yesNo(s.Autostart))
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
		if p.AccessKeyID == "" {
			return errors.New("profile add: --access-key is required")
		}
		secret := os.Getenv("S3_ANYSYNC_SECRET")
		if secret == "" {
			return errors.New("set S3_ANYSYNC_SECRET to the secret access key")
		}
		p, err := c.svc.AddProfile(ctx, api.AddProfileRequest{Profile: p, Secret: secret})
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "profile created: %s\n", p.ID)
		return c.printProbe(ctx, p.ID)
	case "list":
		if _, err := parseArgs(flag.NewFlagSet("profile list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		profiles, err := c.svc.Profiles(ctx)
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
	res, err := c.svc.TestProfile(ctx, id)
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

func (c *cli) machineID(ctx context.Context) (string, error) {
	s, err := c.svc.Settings(ctx)
	return s.MachineID, err
}

func (c *cli) action(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("action: expected create, list, discover, join, direction, rules or leave")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("action create", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		var a model.Action
		fs.StringVar(&a.Name, "name", "", "action name")
		dir := fs.String("direction", string(model.TwoWay), "default direction")
		noDel := fs.Bool("no-propagate-deletes", false, "never delete on the other side")
		fs.BoolVar(&a.ReceiverRestoresCloud, "receiver-restores-cloud", false, "with --no-propagate-deletes: a cloud-to-local machine uploads a file back that the cloud deleted (default: it never uploads)")
		fs.BoolVar(&a.Encrypt, "encrypt", false, "encrypt files")
		var excludes multiFlag
		fs.Var(&excludes, "exclude", "exclude glob (repeatable)")
		fs.IntVar(&a.SnapshotRetention, "retention", model.DefaultSnapshotRetention, "manual snapshots to keep")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		a.Direction = model.Direction(*dir)
		a.PropagateDeletes = !*noDel
		a.Excludes = excludes
		created, err := c.svc.CreateAction(ctx, api.CreateActionRequest{ProfileID: *profile, Action: a, Passphrase: os.Getenv("S3_ANYSYNC_PASSPHRASE")})
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "action created: %s\n", created.ID)
		return nil
	case "list":
		if _, err := parseArgs(flag.NewFlagSet("action list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		mid, err := c.machineID(ctx)
		if err != nil {
			return err
		}
		actions, err := c.svc.Actions(ctx)
		if err != nil {
			return err
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tDIRECTION (THIS MACHINE)\tITEMS\tENCRYPTED\tSTATE")
		for _, st := range actions {
			a := st.Action
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%v\t%s\n", a.ID, a.Name, a.DirectionFor(mid), len(a.Items), a.Encrypt, st.State)
		}
		return w.Flush()
	case "discover":
		fs := flag.NewFlagSet("action discover", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		found, err := c.svc.Discover(ctx, *profile)
		if err != nil {
			return err
		}
		local, err := c.svc.Actions(ctx)
		if err != nil {
			return err
		}
		joined := map[string]bool{}
		for _, st := range local {
			joined[st.Action.ID] = true
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tITEMS\tJOINED")
		for _, a := range found {
			fmt.Fprintf(w, "%s\t%s\t%d\t%v\n", a.ID, a.Name, len(a.Items), joined[a.ID])
		}
		return w.Flush()
	case "join":
		fs := flag.NewFlagSet("action join", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID")
		if err != nil {
			return err
		}
		a, err := c.svc.JoinAction(ctx, api.JoinActionRequest{ProfileID: *profile, ActionID: pos[0], Passphrase: os.Getenv("S3_ANYSYNC_PASSPHRASE")})
		if err != nil {
			return err
		}
		mid, err := c.machineID(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "joined action: %s\n", a.ID)
		w := c.table()
		fmt.Fprintln(w, "ITEM_ID\tREMOTE NAME\tTYPE\tLOCAL PATH")
		for _, it := range a.Items {
			local := it.LocalPaths[mid]
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
		if err := c.svc.SetMachineDirection(ctx, pos[0], d); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "direction updated")
		return nil
	case "rules":
		return c.rules(ctx, args[1:])
	case "leave":
		fs := flag.NewFlagSet("action leave", flag.ContinueOnError)
		force := fs.Bool("force", false, "leave locally even if the bucket cannot be updated")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID")
		if err != nil {
			return err
		}
		if err := c.svc.LeaveAction(ctx, pos[0], *force); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "left action: %s (local files and the cloud copy were kept)\n", pos[0])
		return nil
	}
	return fmt.Errorf("action: unknown subcommand %q", args[0])
}

func (c *cli) rules(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action rules", flag.ContinueOnError)
	name := fs.String("name", "", "action name")
	dir := fs.String("direction", "", "default direction")
	onChange := fs.Bool("on-change", false, "sync when local files change")
	debounce := fs.Int("debounce", 0, "seconds to wait for changes to settle")
	sched := fs.String("schedule", "", "none | every:MINUTES | daily:HH:MM | weekly:DAY@HH:MM | cron:SPEC")
	poll := fs.Int("poll", 0, "minutes between cloud checks (on-change without schedule)")
	prop := fs.Bool("propagate-deletes", true, "copy deletions to the other side")
	recv := fs.Bool("receiver-restores-cloud", false, "cloud-to-local machines upload files the cloud deleted (when deletions are not copied)")
	var excludes multiFlag
	fs.Var(&excludes, "exclude", "exclude glob (repeatable; replaces the list)")
	noEx := fs.Bool("no-excludes", false, "clear the exclude list")
	ret := fs.Int("retention", 0, "manual snapshots to keep")
	gFiles := fs.Int("delete-guard-files", 0, "delete guard: minimum files")
	gPct := fs.Int("delete-guard-percent", 0, "delete guard: minimum percent of synced files")
	pos, err := parseArgs(fs, args, "ACTION_ID")
	if err != nil {
		return err
	}
	st, err := c.svc.Action(ctx, pos[0])
	if err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if len(set) == 0 {
		c.printRules(st.Action)
		return nil
	}
	if set["exclude"] && *noEx {
		return errors.New("action rules: use either --exclude or --no-excludes")
	}
	r := st.Action.Rules()
	if set["name"] {
		r.Name = *name
	}
	if set["direction"] {
		r.Direction = model.Direction(*dir)
	}
	if set["on-change"] {
		r.Triggers.OnChange = *onChange
	}
	if set["debounce"] {
		r.Triggers.DebounceSeconds = *debounce
	}
	if set["schedule"] {
		s, err := parseSchedule(*sched)
		if err != nil {
			return err
		}
		r.Triggers.Schedule = s
	}
	if set["poll"] {
		r.Triggers.RemotePollMinutes = *poll
	}
	if set["propagate-deletes"] {
		r.PropagateDeletes = *prop
	}
	if set["receiver-restores-cloud"] {
		r.ReceiverRestoresCloud = *recv
	}
	if set["exclude"] {
		r.Excludes = excludes
	}
	if *noEx {
		r.Excludes = nil
	}
	if set["retention"] {
		r.SnapshotRetention = *ret
	}
	if set["delete-guard-files"] {
		r.DeleteGuardMinFiles = *gFiles
	}
	if set["delete-guard-percent"] {
		r.DeleteGuardPercent = *gPct
	}
	a, err := c.svc.UpdateRules(ctx, pos[0], r)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "rules updated")
	c.printRules(a)
	return nil
}

func (c *cli) printRules(a model.Action) {
	ex := "none"
	if len(a.Excludes) > 0 {
		ex = strings.Join(a.Excludes, ", ")
	}
	fmt.Fprintf(c.out, "name: %s\ndirection: %s\non change: %s (debounce %ds)\nschedule: %s\n",
		a.Name, a.Direction, yesNo(a.Triggers.OnChange), a.Triggers.DebounceSeconds, describeSchedule(a.Triggers.Schedule))
	if runs, err := schedule.NextRuns(a.Triggers.Schedule, time.Now(), 3); err == nil && len(runs) > 0 {
		var s []string
		for _, r := range runs {
			s = append(s, r.Format("2006-01-02 15:04"))
		}
		fmt.Fprintf(c.out, "next runs: %s\n", strings.Join(s, ", "))
	}
	fmt.Fprintf(c.out, "cloud check: every %d minute(s)\npropagate deletes: %s\nreceiver restores cloud: %s\nexcludes: %s\nmanual snapshots kept: %d\ndelete guard: %d file(s) and %d%%\n",
		a.Triggers.RemotePollMinutes, yesNo(a.PropagateDeletes), yesNo(a.ReceiverRestoresCloud), ex,
		a.SnapshotRetention, a.DeleteGuardMinFiles, a.DeleteGuardPercent)
}

func (c *cli) item(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("item: expected add, map, unmap or remove")
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
		if *typ != string(model.ItemFile) && *typ != string(model.ItemDir) {
			return errors.New("item add: --type must be file or dir")
		}
		it.Type = model.ItemType(*typ)
		it.Excludes = excludes
		added, err := c.svc.AddItem(ctx, pos[0], api.AddItemRequest{Item: it, LocalPath: *local})
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
		if err := c.svc.MapItem(ctx, pos[0], pos[1], *local); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "item mapped")
		return nil
	case "unmap":
		pos, err := parseArgs(flag.NewFlagSet("item unmap", flag.ContinueOnError), args[1:], "ACTION_ID", "ITEM_ID")
		if err != nil {
			return err
		}
		if err := c.svc.UnmapItem(ctx, pos[0], pos[1]); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "item unmapped on this machine (its files stay where they are)")
		return nil
	case "remove":
		pos, err := parseArgs(flag.NewFlagSet("item remove", flag.ContinueOnError), args[1:], "ACTION_ID", "ITEM_ID")
		if err != nil {
			return err
		}
		if err := c.svc.RemoveItem(ctx, pos[0], pos[1]); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "item removed from the action on every machine (files stay where they are)")
		return nil
	}
	return fmt.Errorf("item: unknown subcommand %q", args[0])
}

// actionArg parses a command whose only argument is ACTION_ID (plus extras).
func actionArg(fs *flag.FlagSet, args []string, extra ...string) ([]string, error) {
	return parseArgs(fs, args, append([]string{"ACTION_ID"}, extra...)...)
}

func (c *cli) printResult(res model.SyncResult) {
	fmt.Fprintf(c.out, "%s: %s\n", res.Outcome, res.Summary())
	for _, cf := range res.Conflicts {
		fmt.Fprintf(c.out, "  conflict: %s\n", cf.Path)
	}
	for _, p := range res.Deferred {
		fmt.Fprintf(c.out, "  deferred (changed during sync, will be re-planned): %s\n", p)
	}
	for _, p := range res.PendingDeletes {
		fmt.Fprintf(c.out, "  pending deletion: %s\n", p)
	}
	if res.Outcome == model.OutcomeDeletesPending {
		fmt.Fprintln(c.out, "These deletions are held back by the delete guard. Check them, then run: approve-deletes ACTION_ID (or restore the files).")
	}
	for _, p := range res.Unsupported {
		fmt.Fprintf(c.out, "  not synced here (name not allowed on this system): %s\n", p)
	}
}

// printConflictHint lists conflicts and tells the user how to continue.
func (c *cli) printConflictHint(list []model.Conflict) {
	for _, cf := range list {
		fmt.Fprintf(c.out, "  conflict: %s\n", cf.Path)
	}
	fmt.Fprintln(c.out, "To continue: review the conflicts (conflicts ACTION_ID), then run resolve again with a choice for every one.")
}

func (c *cli) sync(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("sync", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	res, err := c.svc.Sync(ctx, pos[0])
	if err != nil {
		return err
	}
	c.printResult(res)
	return nil
}

func (c *cli) pause(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("pause", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if err := c.svc.Pause(ctx, pos[0]); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "paused (automatic syncs stop until resume; manual commands still work)")
	return nil
}

func (c *cli) resume(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("resume", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if err := c.svc.Resume(ctx, pos[0]); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "resumed")
	return nil
}

func (c *cli) approveDeletes(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("approve-deletes", flag.ContinueOnError)
	list := fs.Bool("list", false, "only list the pending deletions")
	pos, err := actionArg(fs, args)
	if err != nil {
		return err
	}
	p, err := c.svc.PendingDeletes(ctx, pos[0])
	if err != nil {
		return err
	}
	if p == nil {
		fmt.Fprintln(c.out, "no deletions are waiting for approval")
		return nil
	}
	for _, x := range p.Paths {
		fmt.Fprintf(c.out, "  pending deletion: %s\n", x)
	}
	if *list {
		return nil
	}
	res, err := c.svc.ApproveDeletes(ctx, pos[0])
	if err != nil {
		return err
	}
	c.printResult(res)
	return nil
}

func (c *cli) snapshot(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("snapshot", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	info, err := c.svc.Snapshot(ctx, pos[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "snapshot taken: %s (%d files)\n", info.TS, len(info.Files))
	return nil
}

// machines returns the action's machine names (for display).
func (c *cli) machines(ctx context.Context, id string) map[string]string {
	st, err := c.svc.Action(ctx, id)
	if err != nil {
		return nil
	}
	return st.Action.Machines
}

func (c *cli) snapshots(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("snapshots", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	list, err := c.svc.Snapshots(ctx, pos[0])
	if err != nil {
		return err
	}
	machines := c.machines(ctx, pos[0])
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
	pos, err := actionArg(fs, args, "TS")
	if err != nil {
		return err
	}
	target := engine.RestoreTarget(*to)
	if target != engine.RestoreLocal && target != engine.RestoreCloud && target != engine.RestoreBoth {
		return fmt.Errorf("restore: --to must be local, cloud or both (got %q)", *to)
	}
	res, err := c.svc.Restore(ctx, pos[0], api.RestoreRequest{TS: pos[1], Paths: only, To: target})
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "restored %d file(s)\n", len(res.Restored))
	if target == engine.RestoreLocal {
		fmt.Fprintln(c.out, "note: restored locally only; a two-way machine will upload these files again at the next sync, and a cloud-to-local machine will re-download the cloud version.")
	}
	for _, p := range res.SkippedUnmapped {
		fmt.Fprintf(c.out, "  skipped (not mapped on this machine): %s\n", p)
	}
	return nil
}

func (c *cli) conflicts(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("conflicts", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	list, err := c.svc.Conflicts(ctx, pos[0])
	if err != nil {
		return err
	}
	machines := c.machines(ctx, pos[0])
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

func (c *cli) diff(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("diff", flag.ContinueOnError), args, "PATH")
	if err != nil {
		return err
	}
	d, err := c.svc.ConflictDetail(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	switch {
	case d.TooLarge:
		fmt.Fprintln(c.out, "a side is larger than 1 MiB; no text shown")
	case d.Binary:
		fmt.Fprintln(c.out, "binary content; no text shown")
	default:
		fmt.Fprintf(c.out, "--- this machine (%s)\n%s\n+++ cloud (%s)\n%s\n", describe(d.Conflict.Local), d.LocalText, describe(d.Conflict.Remote), d.RemoteText)
	}
	return nil
}

func (c *cli) resolve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	all := fs.String("all", "", "resolve every conflict: local or cloud")
	var each multiFlag
	fs.Var(&each, "path", "PATH=local|cloud (repeatable)")
	pos, err := actionArg(fs, args)
	if err != nil {
		return err
	}
	choices := map[string]model.Side{}
	if *all != "" {
		list, err := c.svc.Conflicts(ctx, pos[0])
		if err != nil {
			return err
		}
		for _, cf := range list {
			choices[cf.Path] = model.Side(*all)
		}
	}
	for _, kv := range each {
		p, side, ok := cutLast(kv)
		if !ok {
			return fmt.Errorf("--path %q: expected PATH=local or PATH=cloud", kv)
		}
		choices[p] = model.Side(side)
	}
	if *all == "" && len(each) == 0 {
		return errors.New("nothing to resolve: pass --all local|cloud or --path PATH=local|cloud")
	}
	res, err := c.svc.Resolve(ctx, pos[0], choices)
	if err != nil {
		return err
	}
	c.printResult(res)
	if res.Outcome == model.OutcomeConflict {
		c.printConflictHint(res.Conflicts)
	}
	return nil
}

func (c *cli) history(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "entries to show")
	pos, err := actionArg(fs, args)
	if err != nil {
		return err
	}
	list, err := c.svc.History(ctx, pos[0], *limit)
	if err != nil {
		return err
	}
	w := c.table()
	fmt.Fprintln(w, "STARTED\tKIND\tOUTCOME\tDETAILS")
	for _, h := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", when(h.StartedAt), h.Kind, h.Outcome, h.Message)
	}
	return w.Flush()
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

// cutLast splits s at the last '=' so paths may contain '='.
func cutLast(s string) (before, after string, ok bool) {
	i := strings.LastIndex(s, "=")
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test -count=1 -race ./cmd/agent/` and `go test -count=1 -race ./...`. Then `CGO_ENABLED=0 go build -o bin/s3-anysync-agent ./cmd/agent && ./bin/s3-anysync-agent; echo exit=$?`
Expected: the tests pass; the binary prints the usage and `exit=2`.

- [ ] **Step 9: Commit**

```bash
gofmt -w cmd internal && git add cmd
git commit -m "feat(cli): commands go through the agent when it runs; status, rules, pause, approve-deletes, diff, history" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 18: The `run` mode, end-to-end test, docs and final verification

**Files:**
- Create: `cmd/agent/agentmain.go`
- Modify: `cmd/agent/main.go` (dispatch `run`, lock the main thread), `README.md`, `docs/superpowers/specs/2026-10-01-s3-anysync-design.md` (section 7 and 13 cross-references)
- Test: `cmd/agent/agent_e2e_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `runAgent(ctx, agentEnv) error`. It takes the data-folder lock, opens daily logs, opens the app, starts the Manager with triggers, rotates the token, serves IPC, then shows the tray (or waits when there is none) and shuts down cleanly.
  - `runAgentCommand(args, stderr) int`, for `s3-anysync-agent run [--no-tray] [--verbose]`.
  - `errAgentRunning`.
  - `guiOpener() func() error`, which returns nil when the GUI executable is not next to the agent (the tray then hides "Open").

- [ ] **Step 1: Write the failing end-to-end test**

Create `cmd/agent/agent_e2e_test.go`:
```go
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/notify"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func memoryStorage(store storage.Storage) func(*app.App) {
	return func(a *app.App) {
		a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
		a.LockSettle = 0
	}
}

// startAgent runs a real agent (no tray) on a fresh short data dir.
func startAgent(t *testing.T, store storage.Storage) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runAgent(ctx, agentEnv{dataDir: dir, secrets: secrets.NewMemory(), notifier: notify.Nop{}, configure: memoryStorage(store), pollInterval: 200 * time.Millisecond, stderr: io.Discard})
	}()
	waitUntil(t, "the agent to answer", func() bool {
		c, err := ipc.Connect(ctx, dir)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})
	stopped := false
	return dir, func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Errorf("agent stopped with %v", err)
		}
		os.RemoveAll(dir)
	}
}

// cliFor is the real CLI connector for dir: it uses the agent when it runs.
func cliFor(dir string, store storage.Storage) connector {
	return func(ctx context.Context) (api.Service, func(), error) {
		return connectTo(ctx, dir, secrets.NewMemory(), nil, memoryStorage(store))
	}
}

func TestAgentEndToEnd(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	store := storage.NewMemory()
	dirA, stopA := startAgent(t, store)
	defer stopA()
	dirB, stopB := startAgent(t, store)
	defer stopB()
	a, b := cliFor(dirA, store), cliFor(dirB, store)

	if out := runOK(t, a, "status"); !strings.Contains(out, "background agent") {
		t.Fatalf("status:\n%s", out)
	}
	pa := valueAfter(t, runOK(t, a, "profile", "add", "--name", "p", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, a, "action", "create", "--profile", pa, "--name", "Editor"), "action created")
	filesA := filepath.Join(t.TempDir(), "snippets")
	os.MkdirAll(filesA, 0o755)
	os.WriteFile(filepath.Join(filesA, "a.json"), []byte("v1"), 0o644)
	item := valueAfter(t, runOK(t, a, "item", "add", "--remote-name", "snippets", "--type", "dir", "--path", filesA, act), "item added")
	runOK(t, a, "action", "rules", "--on-change", "--debounce", "1", act)
	waitUntil(t, "A's automatic upload", func() bool { return strings.Contains(runOK(t, a, "status"), "uploaded 1") })

	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "p", "--bucket", "b", "--access-key", "k"), "profile created")
	runOK(t, b, "action", "join", "--profile", pb, act)
	filesB := filepath.Join(t.TempDir(), "snippets")
	runOK(t, b, "item", "map", "--path", filesB, act, item)
	waitUntil(t, "B's automatic download", func() bool {
		data, err := os.ReadFile(filepath.Join(filesB, "a.json"))
		return err == nil && string(data) == "v1"
	})

	os.WriteFile(filepath.Join(filesA, "a.json"), []byte("v2 from A"), 0o644)
	waitUntil(t, "the edit to travel A -> cloud -> B", func() bool {
		data, _ := os.ReadFile(filepath.Join(filesB, "a.json"))
		return string(data) == "v2 from A"
	})

	// A second agent on the same data folder is refused.
	err := runAgent(context.Background(), agentEnv{dataDir: dirA, secrets: secrets.NewMemory(), stderr: io.Discard})
	if !errors.Is(err, errAgentRunning) {
		t.Fatalf("second agent: %v", err)
	}

	// With no agent running on a folder, the CLI works in-process there.
	stopA()
	reopened, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(reopened)
	if out := runOK(t, cliFor(reopened, store), "status"); !strings.Contains(out, "this process") {
		t.Fatalf("status without agent:\n%s", out)
	}
}

func TestRunCommandFlags(t *testing.T) {
	var errb bytes.Buffer
	if code := runAgentCommand([]string{"--bogus"}, &errb); code != 2 {
		t.Fatalf("bad flag: exit %d (%s)", code, errb.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/agent/ -run 'AgentEndToEnd|RunCommandFlags'`
Expected: FAIL to compile (`undefined: runAgent`, `agentEnv`).

- [ ] **Step 3: Implement**

Create `cmd/agent/agentmain.go`:
```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/logging"
	"github.com/niceit/s3-anysync/internal/notify"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/tray"
)

var errAgentRunning = errors.New("the agent is already running for this data folder")

// agentEnv is what runAgent needs from the outside world; tests swap parts.
type agentEnv struct {
	dataDir      string
	secrets      secrets.Store
	autostart    agent.Autostart
	notifier     notify.Notifier
	configure    func(*app.App) // adjusts the App (tests: memory storage)
	tray         bool
	verbose      bool
	pollInterval time.Duration // tests only
	stderr       io.Writer
}

// runAgent runs the background agent until ctx ends or Quit is picked in the tray.
func runAgent(ctx context.Context, env agentEnv) error {
	lock, err := datalock.Acquire(env.dataDir)
	if errors.Is(err, datalock.ErrBusy) {
		return errAgentRunning
	}
	if err != nil {
		return err
	}
	defer lock.Release()

	logs, err := logging.OpenDaily(filepath.Join(env.dataDir, "logs"), 7, nil)
	if err != nil {
		return err
	}
	defer logs.Close()
	level := new(slog.LevelVar)
	var w io.Writer = logs
	if env.verbose && env.stderr != nil {
		w = io.MultiWriter(logs, env.stderr)
	}
	log := logging.New(w, level)

	a, err := app.Open(env.dataDir, env.secrets, log)
	if err != nil {
		return err
	}
	defer a.Close()
	if env.configure != nil {
		env.configure(a)
	}
	if name, err := a.LogLevel(); err == nil {
		if l, err := logging.ParseLevel(name); err == nil {
			level.Set(l)
		}
	}

	m, err := agent.New(agent.Options{
		App: a, Notifier: env.notifier, Autostart: env.autostart, Triggers: true,
		PollInterval: env.pollInterval, LogLevel: level, Version: version, Log: log,
	})
	if err != nil {
		return err
	}
	if err := m.Start(); err != nil {
		return err
	}
	defer m.Close()

	token, err := ipc.NewToken(env.dataDir)
	if err != nil {
		return err
	}
	l, err := ipc.Listen(env.dataDir)
	if err != nil {
		return err
	}
	defer ipc.Cleanup(env.dataDir)
	srv := ipc.NewServer(m, token, log)
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("ipc server stopped", "err", err)
		}
	}()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("agent started", "version", version, "dataDir", env.dataDir)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if env.tray && tray.Available() {
		return tray.Run(ctx, m, tray.Options{OnQuit: cancel, OpenGUI: guiOpener(), Log: log})
	}
	<-ctx.Done()
	log.Info("agent stopping")
	return nil
}

// runAgentCommand is "s3-anysync-agent run".
func runAgentCommand(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	noTray := fs.Bool("no-tray", false, "run without the tray icon")
	verbose := fs.Bool("verbose", false, "also log to stderr")
	if _, err := parseArgs(fs, args); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	dir, err := app.DefaultDataDir()
	if err != nil {
		reportError(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = runAgent(ctx, agentEnv{
		dataDir: dir, secrets: secrets.NewKeyring(), autostart: defaultAutostart(), notifier: notify.NewDesktop(),
		tray: !*noTray, verbose: *verbose, stderr: stderr,
	})
	if err != nil {
		reportError(stderr, err)
		return 1
	}
	return 0
}

// guiOpener starts the GUI executable installed next to the agent (both live
// in Contents/MacOS of the .app bundle on macOS), or returns nil when there
// is none, which hides "Open" in the tray.
func guiOpener() func() error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	name := "s3-anysync"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	gui := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(gui); err != nil {
		return nil
	}
	return func() error { return exec.Command(gui).Start() }
}
```

In `cmd/agent/main.go`:
- Add `"runtime"` to the imports.
- Add the following before `main`:
```go
// The tray must run on the process's main thread (macOS AppKit).
func init() { runtime.LockOSThread() }
```
- Make `main` dispatch `run`:
```go
func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "run" {
		os.Exit(runAgentCommand(args[1:], os.Stderr))
	}
	os.Exit(run(args, os.Stdout, os.Stderr, connectDefault))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -count=1 -race ./cmd/agent/`, then `go test -count=3 -race ./cmd/agent/ -run AgentEndToEnd`, then `go test -count=1 -race ./...`
Expected: `ok` on every run, with no flakes.

- [ ] **Step 5: Update the docs**

In `README.md`:
- Replace the "Status" section: Plan 2 is done (background agent, triggers, tray, notifications, IPC, autostart). The GUI and installers come in Plans 3 and 4.
- Add a section "Running the agent":
  - `s3-anysync-agent run` (and `--no-tray`, `--verbose`), where logs go (`<data dir>/logs`), and how to stop it (tray Quit, Ctrl-C, SIGTERM).
  - `s3-anysync-agent autostart enable|disable|status`.
  - That every other command talks to the running agent automatically (or works in-process when none runs).
  - `status`, `pause`, `resume`, `history`.
- Add "Triggers": `action rules --on-change`, `--schedule` specs, the remote poll every `--poll` minutes when on-change has no schedule, and that triggers pause in every paused state.
- Add "Delete guard": the defaults, `approve-deletes [--list]`, and per-action `--delete-guard-files/--delete-guard-percent`.
- Add "Snapshot retention": the manual vs automatic rules from spec §13.
- Update "Known limitations":
  - Remove "no background agent", "no mass-delete threshold", "two processes on one data dir" (now prevented by `agent.lock`), "re-pointing while a sync runs" (the agent serialises it), and "Windows-unsafe names" (now skipped and reported).
  - Keep the rest.
  - Add: the macOS tray needs a CGO build; there is no GUI yet ("Open" is hidden in the tray).

In the spec, add one sentence at the start of section 7: "Plan 2 implemented this section with the decisions in section 13 (own autostart instead of `kardianos/service`; CGO only for the tray)."

- [ ] **Step 6: Final verification**

Run each command and check its expected result:

| Command | Expected |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | clean |
| `go test -count=1 -race ./...` | every package `ok` |
| `CGO_ENABLED=1 go build -o bin/s3-anysync-agent ./cmd/agent` | builds on macOS with the real tray (needs Xcode command-line tools) |
| `CGO_ENABLED=0 go build -o bin/s3-anysync-agent-headless ./cmd/agent` | builds (stub tray) |
| `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/s3-anysync-agent.exe ./cmd/agent` | builds (real tray, named pipe, HKCU Run) |
| `./bin/s3-anysync-agent; echo exit=$?` | usage, `exit=2` |

S3-compatible check, when Docker is available:
- Start or restart the MinIO test container:
  ```bash
  docker start s3-anysync-minio || docker run -d --rm --name s3-anysync-minio -p 9000:9000 -e MINIO_ROOT_USER=minio -e MINIO_ROOT_PASSWORD=minio12345 quay.io/minio/minio server /data
  ```
- Run:
  ```bash
  S3_TEST_ENDPOINT=http://localhost:9000 S3_TEST_KEY=minio S3_TEST_SECRET=minio12345 S3_TEST_BUCKET=s3-anysync-test go test -count=1 -race ./internal/storage/ ./internal/engine/
  ```
  This includes the new delete-guard scenarios against real S3 semantics. Report the scenario count.
- Stop the container afterwards with `docker stop s3-anysync-minio`.
- If Docker is not available, say so in the report; do not skip silently.

**Do NOT** run the agent against the user's real data folder, real keychain or real login items (`autostart enable`, `run` without `S3_ANYSYNC_DATA_DIR`). Checking the tray, notifications and autostart by hand needs the user's go-ahead: report that this check is pending, and the controller will ask the user.

- [ ] **Step 7: Commit**

```bash
gofmt -w cmd internal && git add cmd internal README.md docs/superpowers/specs
git commit -m "feat(agent): run mode with IPC, triggers, tray and logs; docs" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review notes (for the executor)

- Tasks 1–5 touch engine code that the Plan 1 reviews hardened. Read the current files first; where the plan's anchor text differs slightly (comments, variable names), apply the change at the equivalent place and say so in the report.
- Every test that waits on background activity uses generous timeouts (5–10s). If one is flaky under `-race` on a loaded machine, raise its timeout. Don't add sleeps to production code.
- The tray's systray glue and desktop notifications can only be checked by hand. Plan 3 (GUI) and Plan 4 (packaging) build on `api.Service` and `ipc.Client` as defined here.
