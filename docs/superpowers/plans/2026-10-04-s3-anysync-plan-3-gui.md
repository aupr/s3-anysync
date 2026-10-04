# s3-anysync Plan 3: GUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Wails v2 desktop GUI (`cmd/gui` + `frontend/`) that manages profiles, actions, items, rules, snapshots, conflicts and the delete guard through the running agent, plus the agent API additions it needs.

**Architecture:** The GUI is a thin client. `internal/gui` (pure Go, CGO-free, fully unit-tested) holds the connection manager (connect, auto-start the agent, reconnect, forward events) and the bound `App` whose methods mirror `api.Service` 1:1 and return errors as JSON `{"code","message"}`. `cmd/gui` is only the Wails entry point (CGO on macOS, stub without CGO). The React/TypeScript frontend talks only to the bound methods and the `agent:event` / `agent:connection` Wails events.

**Tech Stack:** Go 1.27, Wails v2 (latest v2.x), React 18, TypeScript 5 (strict), Vite 5, Tailwind CSS 3, Vitest 2 + React Testing Library + jsdom, `diff` (jsdiff).

**Spec:** `docs/superpowers/specs/2026-10-04-s3-anysync-gui-design.md` (and section 13 of `2026-10-01-s3-anysync-design.md` for the agent).

## Global Constraints

- Module `github.com/niceit/s3-anysync`; Go at `/opt/homebrew/bin/go`; work and commit on `main` (user-approved). Every commit ends with the trailer line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` (or the implementing model's own name/version line given in the dispatch).
- Every `internal/` package stays pure Go and builds/tests with `CGO_ENABLED=0`, except `internal/tray`. `cmd/gui` uses `//go:build cgo || windows` for the Wails entry and a stub for `!cgo && !windows`. Windows builds stay `CGO_ENABLED=0`.
- After every task these must pass: `go test -count=1 -race ./...`, `go vet ./...`, `gofmt -l .` (empty), `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...`. From Task 7 on also `cd frontend && npm run typecheck && npm test -- --run`.
- Tests are hermetic: temp dirs, `secrets.NewMemory()`, fake autostart, `storage.NewMemory()`. Never touch the real data folder, Keychain, login items, menu bar or notifications. Never run the real agent or GUI against the default data dir.
- Stable error codes (JSON `{"code","message"}`) are the contract between agent, GUI and CLI. New codes in this plan: `in-use` (409), `conflict-edit` (409), `cancelled` (409), `protected-path` (400). The GUI-only code `unreachable` means no agent connection.
- `Action.UpdatedAt` is passed back to the agent byte-for-byte as received (the frontend keeps it as the JSON string, never converts it to a JS `Date`).
- The GUI never opens the data folder's database, secrets or lock; everything goes through IPC.
- Frontend: no UI framework beyond React + Tailwind; no URL router library; dark mode via Tailwind `darkMode: 'class'`; theme `system|light|dark` stored in `localStorage` key `s3as.theme`.
- npm packages are installed only inside `frontend/`. The Wails CLI is installed with `go install github.com/wailsapp/wails/v2/cmd/wails@latest` (user-approved).
- `frontend/go.mod` exists only so `go ... ./...` skips `frontend/` (and `node_modules`, which can contain `.go` files).

## Decisions made while planning (deviations from the spec text, same user-visible behavior)

1. **Schedule preview runs in Go, not TypeScript.** The GUI process is Go, so it imports `internal/schedule` and exposes a bound `PreviewSchedule`. This removes the TS cron parser and the shared vector file (spec 3.2 `schedule.ts`/vectors): one implementation, identical results. The frontend keeps only a tiny preset→`model.Schedule` mapper.
2. **`CancelSync` drops a pending automatic trigger, not other callers' queued requests.** Dropping queued requests of other callers (e.g. a rules save) would lose user work; a queued explicit Sync from another click still runs.
3. **`UpdateRules` takes a request struct** `api.UpdateRulesRequest{Rules, ExpectedUpdatedAt}`; the check runs inside `engine.UpdateSharedAction` against the bucket copy, under the action lock (stronger than comparing the local copy).
4. **Profile edit refuses changing `Bucket` or `RootPrefix` while an action uses the profile** (code `in-use`): it would silently point joined actions at a place where they do not exist. Endpoint, region, path style, access key, secret and name can change. Changing endpoint/bucket/prefix resets `ConditionalWrites` to false (unknown until the next Test connection).
5. **`ActionStatus.ProfileID` already travels over IPC** (only `model.Action.ProfileID` is `json:"-"`); Task 5 adds the regression test, no code change.

Task 1 updates the GUI spec text for decisions 1–4.

## File structure

Backend (modify): `internal/secrets/{file.go,env.go}` (new), `internal/app/{profiles.go (new),manage.go}`, `internal/engine/{deps.go}`, `internal/api/{api.go,errors.go}`, `internal/agent/{service.go,worker.go}`, `internal/ipc/{server.go,client.go}`, `internal/model/model.go`, `cmd/agent/{main.go,agentmain.go,cli.go}`.

GUI Go (new): `internal/gui/conn.go` (connection manager), `internal/gui/app.go` (bound methods), `internal/gui/errors.go` (error encoding), `internal/gui/launch.go` + `launch_unix.go` + `launch_windows.go` (find/start the agent), `internal/gui/open.go` + `open_*.go` (open a folder), `cmd/gui/main.go` (Wails, `cgo || windows`), `cmd/gui/main_stub.go`, `cmd/gui/runtime.go` (Wails dialogs/events adapter), `cmd/gui/wails.json`, `cmd/gui/dist/.keep`.

Frontend (new, `frontend/`):
```
package.json  vite.config.ts  tsconfig.json  tailwind.config.js  postcss.config.js  index.html  go.mod
src/main.tsx  src/index.css  src/App.tsx
src/lib/types.ts       JSON types mirroring the Go API
src/lib/bridge.ts      the ONLY importer of wailsjs/ (bindings + EventsOn); mocked in tests
src/lib/api.ts         typed calls, AgentError
src/lib/errors.ts      code -> message, describeError
src/lib/store.tsx      reducer, StoreProvider, useStore, agent event wiring
src/lib/router.tsx     Route type, RouterProvider, useRouter, useDirtyGuard
src/lib/theme.ts       theme persistence + apply
src/lib/schedule.ts    presets <-> model.Schedule
src/lib/format.ts      dates, sizes, state labels
src/components/ui.tsx  Button, Badge, Field, Input, Select, Checkbox, Modal, ConfirmDialog, Spinner, EmptyState
src/components/toast.tsx  ToastProvider, useToast
src/components/Shell.tsx  sidebar + header + banners
src/screens/AgentDown.tsx  Profiles.tsx  ProfileForm.tsx  Actions.tsx  NewActionWizard.tsx  JoinWizard.tsx
src/screens/ActionDetail.tsx  src/screens/tabs/{Overview,Rules,Items,Snapshots,History}.tsx
src/screens/Conflicts.tsx  DeleteReview.tsx  Activity.tsx  Settings.tsx
src/test/setup.ts  src/test/mockBridge.ts
wailsjs/               generated by `wails generate module`, committed
```

---

### Task 1: Dev secrets file and spec decisions

**Files:**
- Create: `internal/secrets/file.go`, `internal/secrets/env.go`, `internal/secrets/file_test.go`
- Modify: `cmd/agent/main.go` (`connectDefault`), `cmd/agent/agentmain.go` (`runAgentCommand`), `README.md`, `docs/superpowers/specs/2026-10-04-s3-anysync-gui-design.md`

**Interfaces:**
- Produces: `secrets.NewFile(path string) *File` (a `secrets.Store`); `secrets.FromEnv(dataDir string) (secrets.Store, error)`; constants `secrets.EnvVar = "S3_ANYSYNC_SECRETS"`, `secrets.FileName = "secrets.json"`.

- [ ] **Step 1: Write the failing tests** (`internal/secrets/file_test.go`)

```go
package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.json")
	s := NewFile(p)
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
	if err := s.Set("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("k2", "v2"); err != nil {
		t.Fatal(err)
	}
	// A second store on the same file sees the values (persisted, not cached).
	if v, err := NewFile(p).Get("k"); err != nil || v != "v1" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("never-set"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if v, _ := s.Get("k2"); v != "v2" {
		t.Fatalf("k2 = %q", v)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v", fi.Mode(), err)
		}
	}
	if _, err := os.Stat(p + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temp file left behind")
	}
}

func TestFileStoreRejectsCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.json")
	os.WriteFile(p, []byte("{not json"), 0o600)
	if _, err := NewFile(p).Get("k"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("corrupt file: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvVar, "")
	if s, err := FromEnv(dir); err != nil {
		t.Fatal(err)
	} else if _, ok := s.(Keyring); !ok {
		t.Fatalf("default store = %T", s)
	}
	t.Setenv(EnvVar, "file")
	s, err := FromEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := s.(*File)
	if !ok || f.path != filepath.Join(dir, FileName) {
		t.Fatalf("file store = %#v", s)
	}
	t.Setenv(EnvVar, "plaintext")
	if _, err := FromEnv(dir); err == nil {
		t.Fatal("unknown backend accepted")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/secrets/`
Expected: FAIL (`undefined: NewFile`, `undefined: FromEnv`).

- [ ] **Step 3: Implement** `internal/secrets/file.go`

```go
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// File is a Store kept in a JSON file (mode 0600). It is for development and
// tests only: the secrets are stored unencrypted.
type File struct {
	mu   sync.Mutex
	path string
}

// NewFile returns a store backed by the file at path (created on first Set).
func NewFile(path string) *File { return &File{path: path} }

func (f *File) load() (map[string]string, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("secrets file %s: %w", f.path, err)
	}
	return m, nil
}

func (f *File) save(m map[string]string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fh, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = fh.Write(b)
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, f.path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func (f *File) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *File) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[key] = value
	return f.save(m)
}

func (f *File) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return f.save(m)
}
```

`internal/secrets/env.go`:

```go
package secrets

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvVar selects the secrets backend: unset or "" for the OS keychain,
// "file" for an unencrypted file in the data folder (development only).
const EnvVar = "S3_ANYSYNC_SECRETS"

// FileName is the development secrets file inside the data folder.
const FileName = "secrets.json"

// FromEnv returns the store selected by EnvVar for the data folder dataDir.
func FromEnv(dataDir string) (Store, error) {
	switch v := os.Getenv(EnvVar); v {
	case "":
		return NewKeyring(), nil
	case "file":
		return NewFile(filepath.Join(dataDir, FileName)), nil
	default:
		return nil, fmt.Errorf("%s=%q: use \"file\" or leave it unset", EnvVar, v)
	}
}
```

- [ ] **Step 4: Wire it into the agent and CLI**

`cmd/agent/main.go` `connectDefault`:
```go
func connectDefault(ctx context.Context) (api.Service, func(), error) {
	dir, err := app.DefaultDataDir()
	if err != nil {
		return nil, nil, err
	}
	sec, err := secrets.FromEnv(dir)
	if err != nil {
		return nil, nil, err
	}
	return connectTo(ctx, dir, sec, defaultAutostart(), nil)
}
```
`cmd/agent/agentmain.go` `runAgentCommand`, right after `dir` is resolved:
```go
	sec, err := secrets.FromEnv(dir)
	if err != nil {
		reportError(stderr, err)
		return 1
	}
```
and pass `secrets: sec` instead of `secrets.NewKeyring()`.

- [ ] **Step 5: Docs.** README "Known limitations"/dev section: add a short "Development: `S3_ANYSYNC_SECRETS=file` keeps secrets unencrypted in `<data dir>/secrets.json` (mode 0600) instead of the keychain. Only for development and tests; any other value is an error." In the GUI spec apply the four "Decisions made while planning" (section 2 `UpdateRules` row → request struct + bucket-copy check; `CancelSync` row → "A pending automatic trigger of that action is dropped; requests queued by other callers still run."; `UpdateProfile` row → bucket/prefix refusal with `in-use` and the ConditionalWrites reset; section 3.2 `schedule.ts` + vectors → "`src/lib/schedule.ts` maps presets to `model.Schedule`; the next-runs preview is the bound Go method `PreviewSchedule` (uses `internal/schedule`)", and section 6 remove "Shared schedule vectors run in Go and Vitest").

- [ ] **Step 6: Verify and commit**

Run: `go test -count=1 ./internal/secrets/ ./cmd/agent/` → PASS, then the Global Constraints checks.
```bash
git add internal/secrets cmd/agent README.md docs/superpowers/specs/2026-10-04-s3-anysync-gui-design.md
git commit -m "feat(secrets): S3_ANYSYNC_SECRETS=file development store" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Profile edit and remove

**Files:**
- Create: `internal/app/profiles.go`, `internal/app/profiles_test.go`
- Modify: `internal/app/app.go` (`AddProfile` validation errors wrap `engine.ErrInvalidInput`; move nothing else), `internal/api/api.go`, `internal/api/errors.go`, `internal/api/errors_test.go`, `internal/agent/service.go`, `internal/agent/manager_test.go`, `internal/ipc/server.go`, `internal/ipc/client.go`, `internal/ipc/routes_test.go`, `cmd/agent/cli.go`, `cmd/agent/cli2_test.go`

**Interfaces:**
- Produces:
  - `app.ErrInUse = errors.New("in use")`; `(*app.App).UpdateProfile(p model.Profile, secret string) (model.Profile, error)`; `(*app.App).RemoveProfile(id string) error`
  - `api.UpdateProfileRequest{Profile model.Profile \`json:"profile"\`; Secret string \`json:"secret,omitempty"\`}`
  - `api.Service` gains `UpdateProfile(ctx context.Context, req UpdateProfileRequest) (model.Profile, error)` and `RemoveProfile(ctx context.Context, id string) error`
  - error code `in-use` (HTTP 409) for `app.ErrInUse`
  - IPC: `PUT /profiles/{id}` (body `UpdateProfileRequest`; the path id wins over the body), `DELETE /profiles/{id}`
  - CLI: `profile edit PROFILE_ID [--name --provider --endpoint --region --bucket --prefix --access-key --path-style]` (secret from `S3_ANYSYNC_SECRET` when set), `profile remove PROFILE_ID`

- [ ] **Step 1: Failing app tests** (`internal/app/profiles_test.go`)

```go
package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

func TestUpdateProfile(t *testing.T) {
	a := newTestApp(t, storage.NewMemory())
	p, err := a.AddProfile(model.Profile{Name: "p", Bucket: "b", RootPrefix: "cfg", AccessKeyID: "k"}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.TestProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	// Blank secret keeps the stored one; name/region change keep ConditionalWrites.
	p.Name, p.Region = "renamed", "eu-1"
	got, err := a.UpdateProfile(p, "")
	if err != nil || got.Name != "renamed" || !got.ConditionalWrites {
		t.Fatalf("UpdateProfile = %+v, %v", got, err)
	}
	if s, _ := a.Secrets.Get(secrets.ProfileSecretKey(p.ID)); s != "s1" {
		t.Fatalf("secret = %q", s)
	}

	// A new secret replaces it; an endpoint change resets ConditionalWrites.
	p.Endpoint = "http://other:9000"
	got, err = a.UpdateProfile(p, "s2")
	if err != nil || got.ConditionalWrites {
		t.Fatalf("UpdateProfile = %+v, %v", got, err)
	}
	if s, _ := a.Secrets.Get(secrets.ProfileSecretKey(p.ID)); s != "s2" {
		t.Fatalf("secret = %q", s)
	}
	if saved, _ := a.DB.GetProfile(p.ID); saved.Endpoint != "http://other:9000" {
		t.Fatalf("saved = %+v", saved)
	}

	// Validation and unknown IDs.
	bad := p
	bad.Bucket = " "
	if _, err := a.UpdateProfile(bad, ""); !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("empty bucket: %v", err)
	}
	if _, err := a.UpdateProfile(model.Profile{ID: "nope", Name: "x", Bucket: "b"}, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestProfileInUse(t *testing.T) {
	a := newTestApp(t, storage.NewMemory())
	p, _ := a.AddProfile(model.Profile{Name: "p", Bucket: "b", RootPrefix: "cfg", AccessKeyID: "k"}, "s")
	if _, err := a.CreateAction(ctx, p.ID, model.Action{Name: "Editor"}, ""); err != nil {
		t.Fatal(err)
	}
	moved := p
	moved.Bucket = "other"
	if _, err := a.UpdateProfile(moved, ""); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "Editor") {
		t.Fatalf("bucket change in use: %v", err)
	}
	moved = p
	moved.RootPrefix = "elsewhere"
	if _, err := a.UpdateProfile(moved, ""); !errors.Is(err, ErrInUse) {
		t.Fatalf("prefix change in use: %v", err)
	}
	same := p
	same.RootPrefix = "/cfg/" // normalizes to the same prefix: allowed
	if _, err := a.UpdateProfile(same, ""); err != nil {
		t.Fatalf("same prefix: %v", err)
	}
	if err := a.RemoveProfile(p.ID); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "Editor") {
		t.Fatalf("remove in use: %v", err)
	}
}

func TestRemoveProfile(t *testing.T) {
	a := newTestApp(t, storage.NewMemory())
	p, _ := a.AddProfile(model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, "s")
	if err := a.RemoveProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.GetProfile(p.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("profile still there: %v", err)
	}
	if _, err := a.Secrets.Get(secrets.ProfileSecretKey(p.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("secret still there: %v", err)
	}
	if err := a.RemoveProfile(p.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ -run 'Profile'` → FAIL (`a.UpdateProfile undefined`).

- [ ] **Step 3: Implement** `internal/app/profiles.go`

```go
package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
)

// ErrInUse: the profile is used by actions on this machine.
var ErrInUse = errors.New("in use")

// normalizePrefix cleans a user-typed folder inside the bucket.
func normalizePrefix(s string) string {
	return strings.Trim(strings.ReplaceAll(strings.TrimSpace(s), `\`, "/"), "/")
}

// actionsUsing returns the names of local actions that use the profile.
func (a *App) actionsUsing(profileID string) ([]string, error) {
	acts, err := a.DB.ListActions()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, act := range acts {
		if act.ProfileID == profileID {
			names = append(names, act.Name)
		}
	}
	return names, nil
}

// UpdateProfile changes a profile; an empty secret keeps the stored one.
// Bucket and RootPrefix cannot change while an action uses the profile.
// Changing the endpoint, bucket or prefix makes conditional-write support
// unknown again (false until the next TestProfile).
func (a *App) UpdateProfile(p model.Profile, secret string) (model.Profile, error) {
	cur, err := a.DB.GetProfile(p.ID)
	if err != nil {
		return p, err
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Bucket) == "" {
		return p, fmt.Errorf("%w: profile name and bucket are required", engine.ErrInvalidInput)
	}
	p.RootPrefix = normalizePrefix(p.RootPrefix)
	if p.Bucket != cur.Bucket || p.RootPrefix != cur.RootPrefix {
		names, err := a.actionsUsing(p.ID)
		if err != nil {
			return p, err
		}
		if len(names) > 0 {
			return p, fmt.Errorf("%w: the bucket and folder cannot change while actions use this profile: %s", ErrInUse, strings.Join(names, ", "))
		}
	}
	p.ConditionalWrites = cur.ConditionalWrites
	if p.Endpoint != cur.Endpoint || p.Bucket != cur.Bucket || p.RootPrefix != cur.RootPrefix {
		p.ConditionalWrites = false
	}
	if secret != "" {
		if err := a.Secrets.Set(secrets.ProfileSecretKey(p.ID), secret); err != nil {
			return p, fmt.Errorf("store secret key: %w", err)
		}
	}
	return p, a.DB.PutProfile(p)
}

// RemoveProfile deletes a profile and its secret; refused while actions use it.
func (a *App) RemoveProfile(id string) error {
	if _, err := a.DB.GetProfile(id); err != nil {
		return err
	}
	names, err := a.actionsUsing(id)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		return fmt.Errorf("%w: actions use this profile: %s", ErrInUse, strings.Join(names, ", "))
	}
	if err := a.DB.DeleteProfile(id); err != nil {
		return err
	}
	return a.Secrets.Delete(secrets.ProfileSecretKey(id))
}
```

In `app.go` `AddProfile`: use `normalizePrefix(p.RootPrefix)` and wrap both validation errors: `fmt.Errorf("%w: profile name and bucket are required", engine.ErrInvalidInput)` and `fmt.Errorf("%w: secret access key is required", engine.ErrInvalidInput)`.

- [ ] **Step 4: Run** `go test ./internal/app/` → PASS.

- [ ] **Step 5: API contract.** In `internal/api/api.go` add to `Service` after `AddProfile`:
```go
	UpdateProfile(ctx context.Context, req UpdateProfileRequest) (model.Profile, error)
	// RemoveProfile deletes a profile and its secret; code in-use while actions use it.
	RemoveProfile(ctx context.Context, id string) error
```
and the type:
```go
// UpdateProfileRequest replaces a profile's fields; an empty Secret keeps the stored one.
type UpdateProfileRequest struct {
	Profile model.Profile `json:"profile"`
	Secret  string        `json:"secret,omitempty"`
}
```
In `internal/api/errors.go` import `internal/app` and add before `"busy"`: `{"in-use", http.StatusConflict, []error{app.ErrInUse}},`. In `errors_test.go` add a case asserting `Code(fmt.Errorf("x: %w", app.ErrInUse)) == "in-use"`, `HTTPStatus("in-use") == 409`, and that `(&RemoteError{Code: "in-use"})` satisfies `errors.Is(_, app.ErrInUse)`.

- [ ] **Step 6: Agent.** `internal/agent/service.go`:
```go
func (m *Manager) UpdateProfile(ctx context.Context, req api.UpdateProfileRequest) (model.Profile, error) {
	p, err := m.app.UpdateProfile(req.Profile, req.Secret)
	if err != nil {
		return p, err
	}
	m.resetRunners() // runners rebuild their storage with the new settings
	return p, nil
}

func (m *Manager) RemoveProfile(ctx context.Context, id string) error { return m.app.RemoveProfile(id) }
```
Test in `manager_test.go`:
```go
func TestUpdateProfileRebuildsRunners(t *testing.T) {
	e := newEnv(t, storage.NewMemory())
	act, _, _ := e.newAction(t, 1)
	ps, _ := e.m.Profiles(ctx)
	before := e.m.nameGen.Load()
	p := ps[0]
	p.Region = "eu-2"
	if _, err := e.m.UpdateProfile(ctx, api.UpdateProfileRequest{Profile: p}); err != nil {
		t.Fatal(err)
	}
	if e.m.nameGen.Load() == before {
		t.Fatal("runners not reset")
	}
	if _, err := e.m.Sync(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.m.RemoveProfile(ctx, p.ID); api.Code(err) != "in-use" {
		t.Fatalf("remove in use: %v", err)
	}
}
```

- [ ] **Step 7: IPC.** `server.go` after `POST /profiles`:
```go
	h("PUT /profiles/{id}", func(r *http.Request) (any, error) {
		var in api.UpdateProfileRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		in.Profile.ID = id(r)
		return svc.UpdateProfile(r.Context(), in)
	})
	h("DELETE /profiles/{id}", func(r *http.Request) (any, error) { return nil, svc.RemoveProfile(r.Context(), id(r)) })
```
`client.go`:
```go
func (c *Client) UpdateProfile(ctx context.Context, req api.UpdateProfileRequest) (model.Profile, error) {
	var v model.Profile
	return v, c.call(ctx, "PUT", "/profiles/"+esc(req.Profile.ID), req, &v)
}

func (c *Client) RemoveProfile(ctx context.Context, id string) error {
	return c.call(ctx, "DELETE", "/profiles/"+esc(id), nil, nil)
}
```
`routes_test.go`: add `rec` methods
```go
func (r *rec) UpdateProfile(_ context.Context, q api.UpdateProfileRequest) (model.Profile, error) {
	return model.Profile{}, r.log("UpdateProfile", q)
}
func (r *rec) RemoveProfile(_ context.Context, id string) error { return r.log("RemoveProfile", id) }
```
and table rows:
```go
		{"UpdateProfile", func() error {
			_, e := c.UpdateProfile(ctx, api.UpdateProfileRequest{Profile: model.Profile{ID: "a/b", Name: "p"}, Secret: "s"})
			return e
		}, `UpdateProfile ` + mj(api.UpdateProfileRequest{Profile: model.Profile{ID: "a/b", Name: "p"}, Secret: "s"})},
		{"RemoveProfile", func() error { return c.RemoveProfile(ctx, weird) }, `RemoveProfile "a b?c"`},
```
Any other type implementing `api.Service` must gain the methods (the compiler lists them).

- [ ] **Step 8: CLI.** In `cmd/agent/cli.go` profile switch add:
```go
	case "edit":
		fs := flag.NewFlagSet("profile edit", flag.ContinueOnError)
		name := fs.String("name", "", "profile name")
		provider := fs.String("provider", "", "provider label")
		endpoint := fs.String("endpoint", "", "endpoint URL (empty for AWS)")
		region := fs.String("region", "", "region")
		bucket := fs.String("bucket", "", "bucket")
		prefix := fs.String("prefix", "", "folder inside the bucket")
		access := fs.String("access-key", "", "access key id")
		pathStyle := fs.Bool("path-style", false, "path-style addressing")
		pos, err := parseArgs(fs, args[1:], "PROFILE_ID")
		if err != nil {
			return err
		}
		profiles, err := c.svc.Profiles(ctx)
		if err != nil {
			return err
		}
		var p *model.Profile
		for i := range profiles {
			if profiles[i].ID == pos[0] {
				p = &profiles[i]
			}
		}
		if p == nil {
			return fmt.Errorf("profile %s: %w", pos[0], api.ErrNotFound)
		}
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if set["name"] {
			p.Name = *name
		}
		if set["provider"] {
			p.Provider = *provider
		}
		if set["endpoint"] {
			p.Endpoint = *endpoint
		}
		if set["region"] {
			p.Region = *region
		}
		if set["bucket"] {
			p.Bucket = *bucket
		}
		if set["prefix"] {
			p.RootPrefix = *prefix
		}
		if set["access-key"] {
			p.AccessKeyID = *access
		}
		if set["path-style"] {
			p.PathStyle = *pathStyle
		}
		if _, err := c.svc.UpdateProfile(ctx, api.UpdateProfileRequest{Profile: *p, Secret: os.Getenv("S3_ANYSYNC_SECRET")}); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "profile updated")
		return nil
	case "remove":
		pos, err := parseArgs(flag.NewFlagSet("profile remove", flag.ContinueOnError), args[1:], "PROFILE_ID")
		if err != nil {
			return err
		}
		if err := c.svc.RemoveProfile(ctx, pos[0]); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "profile removed")
		return nil
```
Add both lines to `usage` next to `profile add/list/test`, and an `explain` hint for code `in-use`: "the profile is used by actions; leave them first, or keep the bucket and folder". CLI test in `cli2_test.go` (use `opener`, `runOK`, `valueAfter` from `cli_test.go`): add profile, `profile edit ID --name renamed` prints `profile updated` and `profile list` shows `renamed`; create an action with it; `profile remove ID` exits non-zero with `in-use` hint text in stderr.

- [ ] **Step 9: Verify and commit**

Run the Global Constraints checks.
```bash
git add internal cmd
git commit -m "feat(api): edit and remove profiles" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Rule-edit conflict detection

**Files:**
- Modify: `internal/engine/deps.go`, `internal/app/manage.go`, `internal/app/manage_test.go`, `internal/api/api.go`, `internal/api/errors.go`, `internal/api/errors_test.go`, `internal/agent/service.go`, `internal/agent/manager_test.go`, `internal/ipc/server.go`, `internal/ipc/client.go`, `internal/ipc/routes_test.go`, `cmd/agent/cli.go`

**Interfaces:**
- Produces:
  - `engine.ErrEditConflict = errors.New("the action was changed elsewhere since it was loaded")`
  - `(*app.App).UpdateRules(ctx context.Context, actionID string, r model.Rules, expected time.Time) (model.Action, error)` — zero `expected` → wraps `engine.ErrInvalidInput`; version mismatch → wraps `engine.ErrEditConflict` and the local copy is replaced by the bucket's copy (ProfileID kept)
  - `api.UpdateRulesRequest{Rules model.Rules \`json:"rules"\`; ExpectedUpdatedAt time.Time \`json:"expectedUpdatedAt"\`}`
  - `api.Service.UpdateRules(ctx context.Context, actionID string, req UpdateRulesRequest) (model.Action, error)` (signature change)
  - error code `conflict-edit` (HTTP 409)
  - IPC `PUT /actions/{id}/rules` body is `UpdateRulesRequest`

- [ ] **Step 1: Failing test** in `internal/app/manage_test.go`:

```go
func TestUpdateRulesDetectsEditConflict(t *testing.T) {
	a, store, act, _, _, _ := manageFixture(t, false)
	cur, _ := a.Action(act.ID)
	r := cur.Rules()
	r.Name = "first"
	first, err := a.UpdateRules(ctx, act.ID, r, cur.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	// An edit based on the old version is refused and changes nothing.
	r.Name = "second"
	if _, err := a.UpdateRules(ctx, act.ID, r, cur.UpdatedAt); !errors.Is(err, engine.ErrEditConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	if shared, _ := engine.FetchAction(ctx, store, "", act.ID); shared.Name != "first" {
		t.Fatalf("bucket copy = %q", shared.Name)
	}
	if _, err := a.UpdateRules(ctx, act.ID, r, time.Time{}); !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("zero version: %v", err)
	}

	// Another machine edits the bucket copy: the conflict refreshes the local copy.
	p, _ := a.DB.GetProfile(cur.ProfileID)
	d, err := a.deps(p, act.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := engine.UpdateSharedAction(ctx, d, p.RootPrefix, act.ID, func(x *model.Action) error { x.Name = "remote"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateRules(ctx, act.ID, r, first.UpdatedAt); !errors.Is(err, engine.ErrEditConflict) {
		t.Fatalf("edit after a remote change: %v", err)
	}
	local, _ := a.Action(act.ID)
	if local.Name != "remote" || !local.UpdatedAt.Equal(remote.UpdatedAt) || local.ProfileID != cur.ProfileID {
		t.Fatalf("local copy not refreshed: %+v", local)
	}
	if _, err := a.UpdateRules(ctx, act.ID, r, local.UpdatedAt); err != nil {
		t.Fatalf("edit on the fresh version: %v", err)
	}
}
```
Update the existing `TestUpdateRules` and `TestUpdateRulesRejectsInvalid` calls to pass the current `UpdatedAt` (read `a.Action(id)` before each call; the invalid-schedule calls may pass any non-zero time because validation runs first).

- [ ] **Step 2: Run** `go test ./internal/app/ -run UpdateRules` → FAIL (too many arguments).

- [ ] **Step 3: Implement.** `internal/engine/deps.go` next to the other sentinels:
```go
	// ErrEditConflict: the action changed (on any machine) after the editor loaded it.
	ErrEditConflict = errors.New("the action was changed elsewhere since it was loaded")
```
`internal/app/manage.go`:
```go
// UpdateRules changes an action's editable settings for every machine.
// expected is the UpdatedAt the editor loaded; if the bucket's copy has
// another version nothing is written, the local copy is replaced by the
// bucket's and engine.ErrEditConflict is returned.
func (a *App) UpdateRules(ctx context.Context, actionID string, r model.Rules, expected time.Time) (model.Action, error) {
	if expected.IsZero() {
		return model.Action{}, fmt.Errorf("%w: the version (updatedAt) of the edited action is required", engine.ErrInvalidInput)
	}
	if err := schedule.Validate(r.Triggers.Schedule); err != nil {
		return model.Action{}, err
	}
	var fresh model.Action
	updated, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		if !act.UpdatedAt.Equal(expected) {
			fresh = *act
			return fmt.Errorf("%w (now version %s)", engine.ErrEditConflict, act.UpdatedAt.Format(time.RFC3339Nano))
		}
		act.ApplyRules(r)
		return nil
	})
	if errors.Is(err, engine.ErrEditConflict) {
		if local, lerr := a.DB.GetAction(actionID); lerr == nil {
			fresh.ProfileID = local.ProfileID
			_ = a.savePutAction(fresh) // best effort: the conflict is reported either way
		}
	}
	return updated, err
}
```
(Keep the existing schedule validation error type; add imports `fmt`, `time`.)

- [ ] **Step 4: Run** `go test ./internal/app/` → PASS.

- [ ] **Step 5: API, agent, IPC, CLI.**
  - `api.go`: replace the `UpdateRules` method with `UpdateRules(ctx context.Context, actionID string, req UpdateRulesRequest) (model.Action, error)` documented "// UpdateRules fails with code conflict-edit when the action changed since req.ExpectedUpdatedAt." and add the request type.
  - `errors.go`: add `{"conflict-edit", http.StatusConflict, []error{engine.ErrEditConflict}},` before `"busy"`; test the code and status in `errors_test.go`.
  - `agent/service.go`:
    ```go
    func (m *Manager) UpdateRules(ctx context.Context, id string, req api.UpdateRulesRequest) (model.Action, error) {
    	v, err := m.mutate(ctx, id, func(c context.Context) (any, error) {
    		return m.app.UpdateRules(c, id, req.Rules, req.ExpectedUpdatedAt)
    	})
    	a, _ := v.(model.Action)
    	return a, err
    }
    ```
    and in `mutate`, right after `v, err := fn(c)`:
    ```go
    		if errors.Is(err, engine.ErrEditConflict) {
    			// The local copy was refreshed from the bucket: rebuild and announce it.
    			w.runner = nil
    			m.bus.publish(api.Event{Type: api.EventActions, ActionID: id})
    			w.publishState()
    		}
    ```
    Update `TestRulesAndItemsThroughManager` to pass `api.UpdateRulesRequest{Rules: ..., ExpectedUpdatedAt: <current UpdatedAt>}` and add an assertion that a second call with the old version returns `api.Code(err) == "conflict-edit"`.
  - `ipc/server.go`: decode `api.UpdateRulesRequest` instead of `model.Rules`. `ipc/client.go`:
    ```go
    func (c *Client) UpdateRules(ctx context.Context, id string, req api.UpdateRulesRequest) (model.Action, error) {
    	var v model.Action
    	return v, c.call(ctx, "PUT", "/actions/"+esc(id)+"/rules", req, &v)
    }
    ```
    `routes_test.go`: `rec.UpdateRules(_ context.Context, id string, q api.UpdateRulesRequest)` logs `("UpdateRules", id, q)`; the table row uses `req := api.UpdateRulesRequest{Rules: rules, ExpectedUpdatedAt: time.Date(2026, 10, 4, 1, 2, 3, 4, time.UTC)}` and expects `` `UpdateRules "a b?c"|` + mj(req) ``.
  - `cmd/agent/cli.go` `rules`: `c.svc.UpdateRules(ctx, pos[0], api.UpdateRulesRequest{Rules: r, ExpectedUpdatedAt: st.Action.UpdatedAt})`; `explain`: code `conflict-edit` → `"  hint: the action was changed (maybe on another machine) while you edited it; run the command again."` (match with `errors.Is(err, engine.ErrEditConflict)` like the other hints).

- [ ] **Step 6: Verify and commit** (Global Constraints checks)
```bash
git add internal cmd
git commit -m "feat(api): refuse rule edits based on an outdated action version" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Cancel a running job

**Files:**
- Modify: `internal/model/model.go`, `internal/api/api.go`, `internal/api/errors.go`, `internal/api/errors_test.go`, `internal/agent/worker.go`, `internal/agent/service.go`, `internal/agent/robust_test.go`, `internal/ipc/server.go`, `internal/ipc/client.go`, `internal/ipc/routes_test.go`, `cmd/agent/cli.go`, `README.md`

**Interfaces:**
- Produces:
  - `model.OutcomeCancelled = "cancelled"`
  - `api.ErrCancelled = errors.New("cancelled by the user")`, code `cancelled` (HTTP 409)
  - `api.Service.CancelSync(ctx context.Context, actionID string) error` — cancels the action's running job (sync, resolve, approve-deletes, snapshot, restore, snapshot list, conflict detail) and drops a pending automatic trigger; no-op when nothing runs; unknown action → `not-found`
  - IPC `POST /actions/{id}/cancel`; CLI `cancel ACTION_ID`
  - The cancelled caller gets `api.ErrCancelled`; an `action.result` event with `Result.Outcome == "cancelled"` is published; failures, backoff and notifications are untouched; state stays what it was before the run.

- [ ] **Step 1: Failing test** in `internal/agent/robust_test.go` (uses the existing `gateStore`, whose `"block"` mode parks `Put`s under `current/` until their context ends):

```go
func TestCancelSyncIsNotAFailure(t *testing.T) {
	g := &gateStore{Memory: storage.NewMemory(), entered: make(chan struct{}, 1)}
	e := newEnv(t, g)
	act, _, _ := e.newAction(t, 3)
	if err := e.m.CancelSync(ctx, act.ID); err != nil {
		t.Fatalf("cancel while idle: %v", err)
	}
	if err := e.m.CancelSync(ctx, "nope"); api.Code(err) != "not-found" {
		t.Fatalf("cancel unknown: %v", err)
	}
	sub, cancelSub := context.WithCancel(ctx)
	defer cancelSub()
	events, _ := e.m.Subscribe(sub)

	g.set("block")
	errc := make(chan error, 1)
	go func() { _, err := e.m.Sync(ctx, act.ID); errc <- err }()
	<-g.entered
	if err := e.m.CancelSync(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, api.ErrCancelled) {
			t.Fatalf("sync err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the sync")
	}
	st := e.state(t, act.ID)
	if st.State != api.StateIdle || st.Running || st.Failures != 0 || st.LastOutcome != model.OutcomeCancelled || st.LastError != "" {
		t.Fatalf("after cancel = %+v", st)
	}
	if msgs := e.rec.Messages(); len(msgs) != 0 {
		t.Fatalf("notified: %v", msgs)
	}
	sawCancelled := false
	for !sawCancelled {
		select {
		case ev := <-events:
			sawCancelled = ev.Type == api.EventResult && ev.Result != nil && ev.Result.Outcome == model.OutcomeCancelled
		case <-time.After(5 * time.Second):
			t.Fatal("no cancelled result event")
		}
	}

	// Nothing was left half-done: the lock is free and the next run converges.
	g.set("")
	res, err := e.m.Sync(ctx, act.ID)
	if err != nil || res.Uploaded != 3 {
		t.Fatalf("sync after cancel = %+v, %v", res, err)
	}
	res, err = e.m.Sync(ctx, act.ID)
	if err != nil || res.Outcome != model.OutcomeOK || res.Uploaded+res.Downloaded+res.DeletedLocal+res.DeletedRemote != 0 {
		t.Fatalf("third sync = %+v, %v", res, err)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/agent/ -run CancelSync` → FAIL (`e.m.CancelSync undefined`).

- [ ] **Step 3: Implement.**
  - `model.go` run outcomes: add `OutcomeCancelled = "cancelled"`.
  - `api.go`: `ErrCancelled` lives in `errors.go` next to `ErrUnavailable` (`// ErrCancelled: the user cancelled the running job.`); `Service` gains, after `Resume`:
    ```go
    	// CancelSync cancels the action's running job, if any; the cancelled
    	// caller gets code cancelled. A pending automatic trigger is dropped.
    	CancelSync(ctx context.Context, actionID string) error
    ```
    `errors.go` codes: `{"cancelled", http.StatusConflict, []error{ErrCancelled}},` (before `"busy"`); test code/status.
  - `worker.go`: add the field `jobCancel context.CancelFunc // guarded by mu: cancels the running job's context` to `worker`, and:
    ```go
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
    ```
    In `loop`: `case reason := <-w.wake:` → `w.guard(func() { w.cancellable(func(c context.Context) { w.autoSync(c, reason) }) })`; retry → same with `"retry"`. `runJob`: `w.guard(func() { w.cancellable(func(c context.Context) { v, err = j.fn(c) }) }, &err)`.
    `autoSync(ctx context.Context, reason string)` calls `w.runSync(ctx, func(c context.Context, r *engine.Runner) (model.SyncResult, error) { return r.Sync(c) })`.
    `runSync(ctx context.Context, f ...)` passes `ctx` to `callEngine(ctx, f, r)` (which calls `f(ctx, r)` instead of `f(w.ctx, r)`), and after the existing `if w.ctx.Err() != nil { return res, err }` add:
    ```go
    	if err != nil && ctx.Err() != nil {
    		w.afterCancel() // the user cancelled this run
    		return res, api.ErrCancelled
    	}
    ```
  - `service.go`: `syncJob` → `w.do(ctx, func(c context.Context) (any, error) { return w.runSync(c, f) })`. `runnerJob` after `f(c, r)`:
    ```go
    		v, err := f(c, r)
    		if err != nil && c.Err() != nil && w.ctx.Err() == nil {
    			return v, api.ErrCancelled
    		}
    		return v, err
    ```
    and:
    ```go
    func (m *Manager) CancelSync(ctx context.Context, id string) error {
    	w, err := m.worker(id)
    	if err != nil {
    		return err
    	}
    	if w.cancelRunning() {
    		m.log.Info("job cancelled by the user", "action", id)
    	}
    	return nil
    }
    ```
    Grep for every other caller of `runSync`/`autoSync`/`callEngine` and pass the job context.

- [ ] **Step 4: Run** `go test -race -count=3 ./internal/agent/` → PASS.

- [ ] **Step 5: IPC + CLI.** Server: `h("POST /actions/{id}/cancel", func(r *http.Request) (any, error) { return nil, svc.CancelSync(r.Context(), id(r)) })`. Client:
```go
func (c *Client) CancelSync(ctx context.Context, id string) error {
	return c.call(ctx, "POST", "/actions/"+esc(id)+"/cancel", nil, nil)
}
```
`rec.CancelSync` logs `("CancelSync", id)`; table row `{"CancelSync", func() error { return c.CancelSync(ctx, weird) }, `CancelSync "a b?c"`}`. CLI: top-level `cancel ACTION_ID` (next to `pause`/`resume`, same parsing) printing `cancel requested`; usage line `  cancel ACTION_ID                     stop the action's running sync, snapshot or restore`; `explain` for `api.ErrCancelled`: `"  hint: the run was cancelled; nothing is half-applied, the next sync continues."`. README: mention `cancel` in the CLI list and that a cancelled run is not a failure.

- [ ] **Step 6: Verify and commit**
```bash
git add internal cmd README.md
git commit -m "feat(agent): cancel a running sync, snapshot or restore" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: IPC hardening for the GUI (protected-path code, token retry, ProfileID)

**Files:**
- Modify: `internal/api/errors.go`, `internal/api/errors_test.go`, `internal/ipc/client.go`, `internal/ipc/ipc_test.go`, `internal/agent/manager_test.go`

**Interfaces:**
- Produces: code `protected-path` (HTTP 400) for `engine.ErrProtectedPath` (removed from `invalid`); `ipc.Client` re-reads `ipc.token` once on a 401 and retries.

- [ ] **Step 1: Failing tests.**
  - `errors_test.go`: `Code(fmt.Errorf("x: %w", engine.ErrProtectedPath)) == "protected-path"`, `HTTPStatus("protected-path") == 400`.
  - `ipc_test.go`:
    ```go
    func TestClientRetriesOnceWithRotatedToken(t *testing.T) {
    	r := &rec{}
    	dir := startStub(t, r, "new-token", nil)
    	c := ipc.NewClient(dir, "old-token")
    	defer c.Close()
    	// The agent restarted and wrote a new token; the client still has the old one.
    	if err := os.WriteFile(filepath.Join(dir, "ipc.token"), []byte("new-token\n"), 0o600); err != nil {
    		t.Fatal(err)
    	}
    	if _, err := c.Status(ctx); err != nil {
    		t.Fatalf("status after rotation: %v", err)
    	}
    	// A token that is still wrong after re-reading is reported, not retried forever.
    	os.WriteFile(filepath.Join(dir, "ipc.token"), []byte("stale\n"), 0o600)
    	c2 := ipc.NewClient(dir, "stale")
    	defer c2.Close()
    	var re *api.RemoteError
    	if _, err := c2.Status(ctx); !errors.As(err, &re) || re.Code != "unauthorized" {
    		t.Fatalf("wrong token: %v", err)
    	}
    }
    ```
    (`rec` and `startStub` are in `routes_test.go`, same package `ipc_test`; if `startStub` writes its own token file, write the rotated token after it.)
  - `manager_test.go`: `TestActionStatusCarriesProfileID` — create an action through `newAction`, assert `e.m.Actions(ctx)` and `e.m.Action(ctx, id)` have `ProfileID == <profile id>`, and that `json.Marshal` of the `api.ActionStatus` contains `"profileId":"<id>"`.

- [ ] **Step 2: Run** → the errors and token tests FAIL; the ProfileID test passes already (regression guard).

- [ ] **Step 3: Implement.** `errors.go`: add `{"protected-path", http.StatusBadRequest, []error{engine.ErrProtectedPath}},` before `"invalid"` and drop `engine.ErrProtectedPath` from the `invalid` entry. `client.go`: store `dataDir` and guard the token with a mutex:
```go
type Client struct {
	hc      *http.Client
	dataDir string
	mu      sync.Mutex
	token   string
}

func (c *Client) currentToken() string { c.mu.Lock(); defer c.mu.Unlock(); return c.token }

// refreshToken re-reads the agent's token; it reports whether it changed.
func (c *Client) refreshToken() bool {
	tok, err := ReadToken(c.dataDir)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if tok == c.token {
		return false
	}
	c.token = tok
	return true
}
```
`request` marshals the body once into `[]byte`, sends with `currentToken()`, and on `resp.StatusCode == http.StatusUnauthorized` (after closing that body) calls `refreshToken()`; if it changed, it sends the same request once more with a fresh `bytes.NewReader(b)`. Update the `Client` doc comment: "A 401 makes the client re-read the token file once (the agent rotates it at every start) and retry."

- [ ] **Step 4: Verify and commit**
```bash
git add internal
git commit -m "feat(ipc): protected-path code and token re-read after an agent restart" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: `internal/gui` — connection manager and bound methods

**Files:**
- Create: `internal/gui/errors.go`, `internal/gui/conn.go`, `internal/gui/app.go`, `internal/gui/launch.go`, `internal/gui/launch_unix.go` (`//go:build !windows`), `internal/gui/launch_windows.go`, `internal/gui/open.go`, `internal/gui/gui_test.go`, `internal/gui/launch_test.go`

**Interfaces:**
- Consumes: `api.Service` (incl. Tasks 2–4 methods), `api.RemoteError`, `api.Code`, `schedule.Validate/NextRuns`, `ipc.Connect` (only in `cmd/gui`).
- Produces (used by `cmd/gui` in Task 7 and, through Wails, by the frontend):
  - `gui.Client` (`api.Service` + `Close()`), `gui.ConnState{Connected bool "connected"; Reason string "reason,omitempty"}`, constants `gui.EventAgent = "agent:event"`, `gui.EventConnection = "agent:connection"`, `gui.ErrUnreachable`
  - `gui.NewConn(gui.ConnOptions) *gui.Conn`; `(*Conn).Run(ctx)`, `Service() (api.Service, error)`, `State() ConnState`, `Retry()`, `Lost()`
  - `gui.Dialogs` interface `{PickFile(title string) (string, error); PickFolder(title string) (string, error)}`
  - `gui.NewApp(conn *Conn, dlg Dialogs, dataDir string) *App` with bound methods: every `api.Service` method except `Subscribe`, same name, same parameters minus `ctx` (`Status`, `Settings`, `UpdateSettings`, `Profiles`, `AddProfile`, `UpdateProfile`, `RemoveProfile`, `TestProfile`, `Discover`, `Actions`, `Action`, `CreateAction`, `JoinAction`, `UpdateRules`, `SetMachineDirection`, `LeaveAction`, `AddItem`, `MapItem`, `UnmapItem`, `RemoveItem`, `Sync`, `CancelSync`, `Snapshot`, `Pause`, `Resume`, `PendingDeletes`, `ApproveDeletes`, `Snapshots`, `Restore`, `Conflicts`, `ConflictDetail`, `Resolve`, `History`), plus `PreviewSchedule(s model.Schedule) ([]string, error)` (RFC 3339, next 3), `PickFile(title) (string, error)`, `PickFolder(title) (string, error)`, `OpenDataFolder() error`, `OpenLogFolder() error`, `ConnectionState() ConnState`, `RetryConnect()`, `DataDir() string`
  - Every error returned by a bound method has as its message the JSON `{"code":"…","message":"…"}`.
  - `gui.AgentPath() (string, error)` (`$S3_ANYSYNC_AGENT`, else `s3-anysync-agent[.exe]` next to the resolved executable), `gui.Launcher(path string) func(dataDir string) error` (starts `path run`, detached, environment inherited — never sets `S3_ANYSYNC_DATA_DIR` itself, so a normally started agent still refreshes its login item), `gui.OpenPath(path string) error`.

- [ ] **Step 1: Failing tests** (`internal/gui/gui_test.go`)

```go
package gui

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
)

// fakeClient is an agent connection; only the methods below are used.
type fakeClient struct {
	api.Service
	events chan api.Event
	gone   chan struct{} // closed: the agent died
	closed atomic.Bool
	status api.Status
	err    error
}

func newFake() *fakeClient {
	return &fakeClient{events: make(chan api.Event), gone: make(chan struct{}), status: api.Status{MachineName: "mac"}}
}

func (f *fakeClient) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	out := make(chan api.Event)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-f.gone:
				return
			case ev := <-f.events:
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (f *fakeClient) Close()                                  { f.closed.Store(true) }
func (f *fakeClient) Status(context.Context) (api.Status, error) { return f.status, f.err }

type emitted struct {
	name string
	data any
}

type recorder struct {
	mu   sync.Mutex
	list []emitted
}

func (r *recorder) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, emitted{name, data})
}

func (r *recorder) find(f func(emitted) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.list {
		if f(e) {
			return true
		}
	}
	return false
}

func (r *recorder) count(f func(emitted) bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.list {
		if f(e) {
			n++
		}
	}
	return n
}

func connected(want bool) func(emitted) bool {
	return func(e emitted) bool {
		s, ok := e.data.(ConnState)
		return e.name == EventConnection && ok && s.Connected == want
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// start runs a Conn with fast timings; connect is called for each attempt.
func start(t *testing.T, connect func() (Client, error), launch func(string) error) (*Conn, *recorder) {
	t.Helper()
	rec := &recorder{}
	c := NewConn(ConnOptions{
		DataDir:   "/data",
		Connect:   func(context.Context, string) (Client, error) { return connect() },
		Launch:    launch,
		Emit:      rec.emit,
		Retry:     10 * time.Millisecond,
		StartWait: 60 * time.Millisecond,
		Poll:      5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return c, rec
}

func decodeErr(t *testing.T, err error) api.RemoteError {
	t.Helper()
	var re api.RemoteError
	if err == nil || json.Unmarshal([]byte(err.Error()), &re) != nil {
		t.Fatalf("not a JSON error: %v", err)
	}
	return re
}

func TestConnectsAndForwardsEvents(t *testing.T) {
	f := newFake()
	c, rec := start(t, func() (Client, error) { return f, nil }, nil)
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	a := NewApp(c, nil, "/data")
	if st, err := a.Status(); err != nil || st.MachineName != "mac" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	f.events <- api.Event{Type: api.EventState, ActionID: "a1"}
	waitFor(t, "event forwarded", func() bool {
		return rec.find(func(e emitted) bool {
			ev, ok := e.data.(api.Event)
			return e.name == EventAgent && ok && ev.ActionID == "a1"
		})
	})
	if !a.ConnectionState().Connected {
		t.Fatal("ConnectionState not connected")
	}
}

func TestStartsTheAgentOnce(t *testing.T) {
	var launched atomic.Int32
	f := newFake()
	c, rec := start(t, func() (Client, error) {
		if launched.Load() == 0 {
			return nil, errors.New("no agent")
		}
		return f, nil
	}, func(dir string) error {
		if dir != "/data" {
			t.Errorf("launch dir = %q", dir)
		}
		launched.Add(1)
		return nil
	})
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	if launched.Load() != 1 {
		t.Fatalf("launched %d times", launched.Load())
	}
	_ = c
}

func TestAgentThatNeverStarts(t *testing.T) {
	var launched atomic.Int32
	c, rec := start(t, func() (Client, error) { return nil, errors.New("no agent") },
		func(string) error { launched.Add(1); return nil })
	waitFor(t, "start timeout reported", func() bool {
		return rec.find(func(e emitted) bool {
			s, ok := e.data.(ConnState)
			return ok && !s.Connected && strings.Contains(s.Reason, "did not start")
		})
	})
	time.Sleep(50 * time.Millisecond) // several plain retries
	if n := launched.Load(); n != 1 {
		t.Fatalf("plain retries launched the agent: %d", n)
	}
	c.Retry()
	waitFor(t, "explicit retry launches again", func() bool { return launched.Load() == 2 })
	a := NewApp(c, nil, "/data")
	if _, err := a.Status(); decodeErr(t, err).Code != "unreachable" {
		t.Fatalf("Status while down: %v", err)
	}
}

func TestReconnectsAfterTheAgentDies(t *testing.T) {
	first, second := newFake(), newFake()
	var n atomic.Int32
	c, rec := start(t, func() (Client, error) {
		if n.Add(1) == 1 {
			return first, nil
		}
		return second, nil
	}, nil)
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	close(first.gone)
	waitFor(t, "disconnect reported", func() bool { return rec.find(connected(false)) })
	waitFor(t, "reconnected", func() bool { return rec.count(connected(true)) == 2 })
	if !first.closed.Load() {
		t.Fatal("dead client not closed")
	}
	if s, _ := c.Service(); s != second {
		t.Fatal("not using the new client")
	}
}

func TestTransportErrorDropsTheSession(t *testing.T) {
	f := newFake()
	f.err = &url.Error{Op: "Get", URL: "http://agent/status", Err: errors.New("connection refused")}
	c, rec := start(t, func() (Client, error) { return f, nil }, nil)
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	a := NewApp(c, nil, "/data")
	if _, err := a.Status(); decodeErr(t, err).Code != "unreachable" {
		t.Fatalf("Status = %v", err)
	}
	waitFor(t, "session dropped", func() bool { return rec.find(connected(false)) })
}

func TestErrorEncoding(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&api.RemoteError{Code: "in-use", Message: "used by Editor"}, "in-use"},
		{&api.RemoteError{Code: "unauthorized", Message: "bad token"}, "unauthorized"},
		{engine.ErrConflictPending, "conflict-pending"},
		{ErrUnreachable, "unreachable"},
		{errors.New("boom"), "internal"},
	}
	for _, tc := range cases {
		re := decodeErr(t, encode(tc.err))
		if re.Code != tc.code || re.Message != tc.err.Error() {
			t.Errorf("encode(%v) = %+v, want code %s", tc.err, re, tc.code)
		}
	}
	if encode(nil) != nil {
		t.Fatal("encode(nil) != nil")
	}
}

// Every api.Service method except Subscribe is bound with the same name and
// the same parameters minus the context.
func TestBindingsCoverTheService(t *testing.T) {
	svc := reflect.TypeOf((*api.Service)(nil)).Elem()
	app := reflect.TypeOf(&App{})
	for i := 0; i < svc.NumMethod(); i++ {
		m := svc.Method(i)
		if m.Name == "Subscribe" {
			continue
		}
		b, ok := app.MethodByName(m.Name)
		if !ok {
			t.Errorf("App lacks %s", m.Name)
			continue
		}
		// b.Type has the receiver first; m.Type has ctx first.
		if b.Type.NumIn()-1 != m.Type.NumIn()-1 {
			t.Errorf("%s: %d params, want %d", m.Name, b.Type.NumIn()-1, m.Type.NumIn()-1)
			continue
		}
		for j := 1; j < m.Type.NumIn(); j++ {
			if b.Type.In(j) != m.Type.In(j) {
				t.Errorf("%s param %d = %v, want %v", m.Name, j, b.Type.In(j), m.Type.In(j))
			}
		}
		if b.Type.NumOut() != m.Type.NumOut() {
			t.Errorf("%s: %d results, want %d", m.Name, b.Type.NumOut(), m.Type.NumOut())
		}
	}
}

func TestPreviewSchedule(t *testing.T) {
	a := NewApp(NewConn(ConnOptions{}), nil, "/data")
	a.now = func() time.Time { return time.Date(2026, 10, 4, 8, 0, 0, 0, time.Local) }
	runs, err := a.PreviewSchedule(model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: 15})
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	want := time.Date(2026, 10, 4, 8, 15, 0, 0, time.Local).Format(time.RFC3339)
	if runs[0] != want {
		t.Fatalf("first run %s, want %s", runs[0], want)
	}
	for _, bad := range []model.Schedule{{Kind: model.ScheduleCron, Cron: "nope"}, {Kind: model.ScheduleCron, Cron: "@every 1s"}} {
		if _, err := a.PreviewSchedule(bad); decodeErr(t, err).Code != "invalid" {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if runs, err := a.PreviewSchedule(model.Schedule{Kind: model.ScheduleNone}); err != nil || len(runs) != 0 {
		t.Fatalf("none = %v, %v", runs, err)
	}
}

type fakeDialogs struct{ titles []string }

func (d *fakeDialogs) PickFile(title string) (string, error)   { d.titles = append(d.titles, title); return "/f", nil }
func (d *fakeDialogs) PickFolder(title string) (string, error) { d.titles = append(d.titles, title); return "/d", nil }

func TestLocalHelpers(t *testing.T) {
	d := &fakeDialogs{}
	a := NewApp(NewConn(ConnOptions{}), d, "/data")
	var opened []string
	a.open = func(p string) error { opened = append(opened, p); return nil }
	if p, _ := a.PickFolder("Pick"); p != "/d" {
		t.Fatal(p)
	}
	if p, _ := a.PickFile("Pick"); p != "/f" {
		t.Fatal(p)
	}
	a.OpenDataFolder()
	a.OpenLogFolder()
	if len(opened) != 2 || opened[0] != "/data" || !strings.HasSuffix(opened[1], "logs") {
		t.Fatalf("opened = %v", opened)
	}
	if a.DataDir() != "/data" {
		t.Fatal(a.DataDir())
	}
}
```

(`api.Service` must already contain the Task 2–4 methods; the coverage test then forces them onto `App`.)

- [ ] **Step 2: Run** `go test ./internal/gui/` → FAIL (package does not compile).

- [ ] **Step 3: Implement** `internal/gui/errors.go`

```go
// Package gui is the Go side of the desktop GUI: the connection to the agent
// and the methods bound to the frontend. It never opens the data folder.
package gui

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"

	"github.com/niceit/s3-anysync/internal/api"
)

// ErrUnreachable: there is no connection to the agent.
var ErrUnreachable = errors.New("the s3-anysync agent is not running or not reachable")

// encode turns err into an error whose message is the JSON {"code","message"}
// that the frontend parses.
func encode(err error) error {
	if err == nil {
		return nil
	}
	re := api.RemoteError{Code: api.Code(err), Message: err.Error()}
	var remote *api.RemoteError
	switch {
	case errors.As(err, &remote):
		re.Code = remote.Code
	case errors.Is(err, ErrUnreachable):
		re.Code = "unreachable"
	}
	b, _ := json.Marshal(re)
	return errors.New(string(b))
}

// lostConnection reports whether err means the connection to the agent is
// gone (as opposed to the agent answering with an error). unauthorized
// counts: the client already re-read the token once.
func lostConnection(err error) bool {
	var remote *api.RemoteError
	if errors.As(err, &remote) {
		return remote.Code == "unauthorized"
	}
	var ue *url.Error
	var ne net.Error
	return errors.As(err, &ue) || errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}
```

`internal/gui/conn.go`

```go
package gui

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
)

// Client is one connection to the agent.
type Client interface {
	api.Service
	Close()
}

// ConnState is sent to the frontend with EventConnection.
type ConnState struct {
	Connected bool   `json:"connected"`
	Reason    string `json:"reason,omitempty"`
}

// Events emitted to the frontend.
const (
	EventAgent      = "agent:event"      // data: api.Event
	EventConnection = "agent:connection" // data: ConnState
)

// ConnOptions configures a Conn. Zero durations take the defaults.
type ConnOptions struct {
	DataDir string
	Connect func(ctx context.Context, dataDir string) (Client, error)
	// Launch starts the agent (nil: never). It is used on the first attempt
	// and after Retry, never by the automatic reconnects.
	Launch    func(dataDir string) error
	Emit      func(event string, data any)
	Retry     time.Duration // between reconnect attempts (2s)
	StartWait time.Duration // how long a started agent may take to answer (10s)
	Poll      time.Duration // between attempts while waiting for it (250ms)
	Log       *slog.Logger
}

// Conn keeps the GUI connected to the agent.
type Conn struct {
	o    ConnOptions
	kick chan bool // wakes the reconnect wait; true also starts the agent

	mu     sync.Mutex
	client Client
	drop   context.CancelFunc // ends the current session
	state  ConnState
}

// NewConn returns a Conn; call Run to connect.
func NewConn(o ConnOptions) *Conn {
	if o.Retry <= 0 {
		o.Retry = 2 * time.Second
	}
	if o.StartWait <= 0 {
		o.StartWait = 10 * time.Second
	}
	if o.Poll <= 0 {
		o.Poll = 250 * time.Millisecond
	}
	if o.Emit == nil {
		o.Emit = func(string, any) {}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Conn{o: o, kick: make(chan bool, 1), state: ConnState{Reason: "connecting to the agent"}}
}

// Run connects (starting the agent on the first attempt), forwards events
// and reconnects until ctx ends.
func (c *Conn) Run(ctx context.Context) {
	launch := true
	for ctx.Err() == nil {
		cl, err := c.connect(ctx, launch)
		launch = false
		if err == nil {
			c.session(ctx, cl)
		} else if ctx.Err() == nil {
			c.o.Log.Info("agent not reachable", "err", err)
			c.set(nil, nil, ConnState{Reason: err.Error()})
		}
		select {
		case <-ctx.Done():
		case launch = <-c.kick:
		case <-time.After(c.o.Retry):
		}
	}
	c.set(nil, nil, ConnState{Reason: "the GUI is closing"})
}

// session forwards the agent's events until the stream ends.
func (c *Conn) session(ctx context.Context, cl Client) {
	defer cl.Close()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := cl.Subscribe(sctx)
	if err != nil {
		c.set(nil, nil, ConnState{Reason: err.Error()})
		return
	}
	c.set(cl, cancel, ConnState{Connected: true})
	for ev := range events {
		c.o.Emit(EventAgent, ev)
	}
	if ctx.Err() == nil {
		c.set(nil, nil, ConnState{Reason: "the connection to the agent was lost"})
	}
}

func (c *Conn) connect(ctx context.Context, launch bool) (Client, error) {
	cl, err := c.try(ctx)
	if err == nil || !launch || c.o.Launch == nil {
		return cl, err
	}
	if lerr := c.o.Launch(c.o.DataDir); lerr != nil {
		return nil, fmt.Errorf("start the agent: %w", lerr)
	}
	deadline := time.Now().Add(c.o.StartWait)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.o.Poll):
		}
		if cl, err = c.try(ctx); err == nil {
			return cl, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the agent did not start within %s: %w", c.o.StartWait, err)
		}
	}
}

func (c *Conn) try(ctx context.Context) (Client, error) {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.o.Connect(tctx, c.o.DataDir)
}

// set changes the connection and announces a changed state.
func (c *Conn) set(cl Client, drop context.CancelFunc, st ConnState) {
	c.mu.Lock()
	changed := c.state != st
	c.client, c.drop, c.state = cl, drop, st
	c.mu.Unlock()
	if changed {
		c.o.Emit(EventConnection, st)
	}
}

// Service returns the current connection, or ErrUnreachable.
func (c *Conn) Service() (api.Service, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return nil, ErrUnreachable
	}
	return c.client, nil
}

// State returns the connection state.
func (c *Conn) State() ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Retry reconnects now and starts the agent if it still does not answer.
func (c *Conn) Retry() {
	select {
	case c.kick <- true:
	default:
	}
}

// Lost ends the current session after a failed call; Run reconnects.
func (c *Conn) Lost() {
	c.mu.Lock()
	drop := c.drop
	c.mu.Unlock()
	if drop != nil {
		drop()
	}
	select {
	case c.kick <- false:
	default:
	}
}
```

`internal/gui/app.go`

```go
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
	open    func(path string) error
	now     func() time.Time
}

// NewApp returns the bound object.
func NewApp(conn *Conn, dlg Dialogs, dataDir string) *App {
	return &App{conn: conn, dlg: dlg, dataDir: dataDir, open: OpenPath, now: time.Now}
}

// do calls the agent. Calls are not cut short by the GUI: a sync may take long.
func do[T any](a *App, f func(ctx context.Context, s api.Service) (T, error)) (T, error) {
	var zero T
	s, err := a.conn.Service()
	if err != nil {
		return zero, encode(err)
	}
	v, err := f(context.Background(), s)
	if err != nil {
		if lostConnection(err) {
			a.conn.Lost()
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
	return do(a, func(c context.Context, s api.Service) (*model.PendingDeletes, error) { return s.PendingDeletes(c, actionID) })
}

func (a *App) ApproveDeletes(actionID string, reviewed []string) (model.SyncResult, error) {
	return do(a, func(c context.Context, s api.Service) (model.SyncResult, error) { return s.ApproveDeletes(c, actionID, reviewed) })
}

func (a *App) Snapshots(actionID string) ([]model.SnapshotInfo, error) {
	return do(a, func(c context.Context, s api.Service) ([]model.SnapshotInfo, error) { return s.Snapshots(c, actionID) })
}

func (a *App) Restore(actionID string, req api.RestoreRequest) (engine.RestoreResult, error) {
	return do(a, func(c context.Context, s api.Service) (engine.RestoreResult, error) { return s.Restore(c, actionID, req) })
}

func (a *App) Conflicts(actionID string) ([]model.Conflict, error) {
	return do(a, func(c context.Context, s api.Service) ([]model.Conflict, error) { return s.Conflicts(c, actionID) })
}

func (a *App) ConflictDetail(actionID, path string) (engine.ConflictDetail, error) {
	return do(a, func(c context.Context, s api.Service) (engine.ConflictDetail, error) { return s.ConflictDetail(c, actionID, path) })
}

func (a *App) Resolve(actionID string, choices map[string]model.Side) (model.SyncResult, error) {
	return do(a, func(c context.Context, s api.Service) (model.SyncResult, error) { return s.Resolve(c, actionID, choices) })
}

func (a *App) History(actionID string, limit int) ([]model.HistoryEntry, error) {
	return do(a, func(c context.Context, s api.Service) ([]model.HistoryEntry, error) { return s.History(c, actionID, limit) })
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
```

`internal/gui/open.go`

```go
package gui

import (
	"os/exec"
	"runtime"
)

// OpenPath shows a folder in the system file manager.
func OpenPath(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() //nolint:errcheck // explorer exits 1 even on success
	return nil
}
```

`internal/gui/launch.go`

```go
package gui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// AgentEnv overrides where the GUI finds the agent executable.
const AgentEnv = "S3_ANYSYNC_AGENT"

func agentName() string {
	if runtime.GOOS == "windows" {
		return "s3-anysync-agent.exe"
	}
	return "s3-anysync-agent"
}

// AgentPath returns $S3_ANYSYNC_AGENT, else the agent next to this executable.
func AgentPath() (string, error) {
	if p := os.Getenv(AgentEnv); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	p := filepath.Join(filepath.Dir(exe), agentName())
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("the agent is not installed next to the GUI (%s); set %s: %w", p, AgentEnv, err)
	}
	return p, nil
}

// Launcher returns a function that starts the agent at path with "run",
// detached from the GUI. The agent inherits the GUI's environment (so
// S3_ANYSYNC_DATA_DIR and S3_ANYSYNC_SECRETS match); the data dir argument
// is that same folder and is not passed again.
func Launcher(path string) func(dataDir string) error {
	return func(string) error {
		cmd := exec.Command(path, "run")
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait() //nolint:errcheck // reaps the agent if it exits while the GUI runs
		return nil
	}
}
```

`launch_unix.go`:
```go
//go:build !windows

package gui

import (
	"os/exec"
	"syscall"
)

// detach starts the agent in its own session so it outlives the GUI.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
```
`launch_windows.go`:
```go
package gui

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detach starts the agent without a console, outside the GUI's process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup, HideWindow: true}
}
```

- [ ] **Step 4: Launcher test** (`internal/gui/launch_test.go`) — re-executes the test binary as a fake agent:

```go
package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if out := os.Getenv("GUI_TEST_FAKE_AGENT"); out != "" {
		// Acting as the agent: record the arguments and environment, then exit.
		os.WriteFile(out, []byte(strings.Join(os.Args[1:], " ")+"\n"+os.Getenv("S3_ANYSYNC_SECRETS")), 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLauncherStartsAgentWithRun(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args")
	t.Setenv("GUI_TEST_FAKE_AGENT", out)
	t.Setenv("S3_ANYSYNC_SECRETS", "file")
	if err := Launcher(os.Args[0])("/ignored"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			if got := string(b); got != "run\nfile" {
				t.Fatalf("fake agent saw %q", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent was not started")
}

func TestAgentPath(t *testing.T) {
	t.Setenv(AgentEnv, "/opt/agent")
	if p, err := AgentPath(); err != nil || p != "/opt/agent" {
		t.Fatalf("override = %q, %v", p, err)
	}
	t.Setenv(AgentEnv, "")
	// The test binary has no agent next to it.
	if _, err := AgentPath(); err == nil || !strings.Contains(err.Error(), AgentEnv) {
		t.Fatalf("missing agent: %v", err)
	}
}
```

- [ ] **Step 5: Run** `go test -race -count=3 ./internal/gui/` → PASS; `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./internal/gui/` clean.

- [ ] **Step 6: Commit**
```bash
git add internal/gui
git commit -m "feat(gui): agent connection manager and bound methods" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Wails entry point and frontend scaffold

**Files:**
- Create: `cmd/gui/main.go`, `cmd/gui/runtime.go`, `cmd/gui/main_stub.go`, `cmd/gui/wails.json`, `cmd/gui/dist/.keep`, `frontend/{package.json,package-lock.json,vite.config.ts,tsconfig.json,tailwind.config.js,postcss.config.js,index.html,go.mod}`, `frontend/src/{main.tsx,index.css,App.tsx,App.test.tsx}`, `frontend/src/test/setup.ts`, `frontend/wailsjs/**` (generated)
- Modify: `.gitignore`, `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `gui.NewConn`, `gui.NewApp`, `gui.AgentPath`, `gui.Launcher`, `gui.Dialogs`, `ipc.Connect`, `app.DefaultDataDir`.
- Produces: generated bindings `frontend/wailsjs/go/gui/App.{js,d.ts}` and `frontend/wailsjs/runtime/runtime.{js,d.ts}`; npm scripts `dev`, `build`, `typecheck`, `test`; build output in `cmd/gui/dist/` (embedded).

- [ ] **Step 1: Toolchain.** `go install github.com/wailsapp/wails/v2/cmd/wails@latest`, then `~/go/bin/wails doctor` (report its summary; it must find Xcode CLT and npm). `go get github.com/wailsapp/wails/v2@<same version as the CLI>`.

- [ ] **Step 2: Wails entry.** `cmd/gui/main.go`:

```go
//go:build cgo || windows

// Command s3-anysync is the desktop GUI. It talks only to the agent.
package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/gui"
	"github.com/niceit/s3-anysync/internal/ipc"
)

//go:embed all:dist
var assets embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "s3-anysync:", err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := app.DefaultDataDir()
	if err != nil {
		return err
	}
	rt := &wailsRuntime{}
	var launch func(string) error
	if p, err := gui.AgentPath(); err == nil {
		launch = gui.Launcher(p)
	} else {
		slog.Warn("cannot start the agent automatically", "err", err)
	}
	conn := gui.NewConn(gui.ConnOptions{
		DataDir: dir,
		Connect: func(ctx context.Context, d string) (gui.Client, error) {
			c, err := ipc.Connect(ctx, d)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		Launch: launch,
		Emit:   rt.emit,
	})
	bound := gui.NewApp(conn, rt, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sum := sha256.Sum256([]byte(dir))
	return wails.Run(&options.App{
		Title:       "s3-anysync",
		Width:       1100,
		Height:      720,
		MinWidth:    900,
		MinHeight:   600,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup: func(wctx context.Context) {
			rt.setContext(wctx)
			go conn.Run(ctx)
		},
		OnShutdown: func(context.Context) { cancel() },
		Bind:       []interface{}{bound},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com.niceit.s3-anysync.gui." + hex.EncodeToString(sum[:8]),
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { rt.focus() },
		},
	})
}
```

`cmd/gui/runtime.go`:

```go
//go:build cgo || windows

package main

import (
	"context"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// wailsRuntime adapts the Wails runtime to gui.Dialogs and event emission.
type wailsRuntime struct {
	mu  sync.Mutex
	ctx context.Context
}

func (r *wailsRuntime) setContext(c context.Context) { r.mu.Lock(); r.ctx = c; r.mu.Unlock() }

func (r *wailsRuntime) get() context.Context { r.mu.Lock(); defer r.mu.Unlock(); return r.ctx }

func (r *wailsRuntime) emit(name string, data any) {
	if c := r.get(); c != nil {
		wruntime.EventsEmit(c, name, data)
	}
}

func (r *wailsRuntime) PickFile(title string) (string, error) {
	return wruntime.OpenFileDialog(r.get(), wruntime.OpenDialogOptions{Title: title, ShowHiddenFiles: true})
}

func (r *wailsRuntime) PickFolder(title string) (string, error) {
	return wruntime.OpenDirectoryDialog(r.get(), wruntime.OpenDialogOptions{Title: title, ShowHiddenFiles: true, CanCreateDirectories: true})
}

func (r *wailsRuntime) focus() {
	if c := r.get(); c != nil {
		wruntime.WindowUnminimise(c)
		wruntime.WindowShow(c)
	}
}
```

`cmd/gui/main_stub.go`:
```go
//go:build !cgo && !windows

// Command s3-anysync is the desktop GUI; this build has no CGO and no GUI.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "s3-anysync: this build has no GUI; build with CGO enabled (Xcode command-line tools on macOS)")
	os.Exit(1)
}
```

`cmd/gui/wails.json`:
```json
{
  "$schema": "https://wails.io/schemas/config.v2.json",
  "name": "s3-anysync",
  "outputfilename": "s3-anysync",
  "frontend:dir": "../../frontend",
  "frontend:install": "npm install",
  "frontend:build": "npm run build",
  "frontend:dev:watcher": "npm run dev",
  "frontend:dev:serverUrl": "auto",
  "author": { "name": "niceit" }
}
```
`cmd/gui/dist/.keep`: empty file. `.gitignore`: replace `/frontend/dist/` with
```
/cmd/gui/dist/*
!/cmd/gui/dist/.keep
/cmd/gui/build/bin/
```

- [ ] **Step 3: Frontend scaffold.** In `frontend/`:
```bash
npm init -y
npm install react@18 react-dom@18 diff@5
npm install -D typescript@5 vite@5 @vitejs/plugin-react@4 tailwindcss@3 postcss@8 autoprefixer@10 vitest@2 jsdom@25 @testing-library/react@16 @testing-library/dom@10 @testing-library/user-event@14 @testing-library/jest-dom@6 @types/react@18 @types/react-dom@18 @types/diff@5
```
`package.json` (keep the installed dependency versions npm wrote; set the rest to):
```json
{
  "name": "s3-anysync-gui",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && node -e \"require('fs').rmSync('../cmd/gui/dist/assets',{recursive:true,force:true})\" && vite build",
    "typecheck": "tsc --noEmit",
    "test": "vitest"
  }
}
```
`vite.config.ts`:
```ts
/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // Built into the Go package that embeds it; .keep must survive.
  build: { outDir: '../cmd/gui/dist', emptyOutDir: false },
  test: { environment: 'jsdom', setupFiles: ['./src/test/setup.ts'], globals: true },
})
```
`tsconfig.json`:
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "skipLibCheck": true,
    "isolatedModules": true,
    "noEmit": true,
    "types": ["vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src", "wailsjs", "vite.config.ts"]
}
```
`tailwind.config.js`:
```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: 'class',
  theme: { extend: {} },
  plugins: [],
}
```
`postcss.config.js`: `export default { plugins: { tailwindcss: {}, autoprefixer: {} } }`
`index.html`:
```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>s3-anysync</title>
  </head>
  <body class="bg-white text-zinc-900 dark:bg-zinc-900 dark:text-zinc-100">
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```
`src/index.css`: `@tailwind base;\n@tailwind components;\n@tailwind utilities;\n\nhtml, body, #root { height: 100%; }`
`src/main.tsx`:
```tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import './index.css'
import App from './App'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
```
`src/App.tsx` (replaced in Task 8): `export default function App() { return <h1 className="p-4 text-xl font-semibold">s3-anysync</h1> }`
`src/App.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import App from './App'

test('renders the app name', () => {
  render(<App />)
  expect(screen.getByText('s3-anysync')).toBeInTheDocument()
})
```
`src/test/setup.ts`: `import '@testing-library/jest-dom/vitest'`
`frontend/go.mod`:
```
// This file only stops "go ... ./..." from descending into frontend/
// (node_modules can contain .go files). There is no Go code here.
module github.com/niceit/s3-anysync/frontend-not-go

go 1.27
```

- [ ] **Step 4: Generate bindings and build.**
```bash
cd cmd/gui && ~/go/bin/wails generate module   # writes ../../frontend/wailsjs
cd ../../frontend && npm run typecheck && npm test -- --run && npm run build
cd ../cmd/gui && ~/go/bin/wails build -clean     # produces cmd/gui/build/bin/s3-anysync.app (macOS)
```
Expected: generated `frontend/wailsjs/go/gui/App.d.ts` lists every bound method; the build succeeds. Commit the Wails-generated `cmd/gui/build/` assets except `build/bin/`. Do NOT launch the built app (it would start the real agent on the real data folder).

- [ ] **Step 5: Global Constraints checks** (CGO-off build uses the stub; Windows vet compiles the Wails entry), then commit:
```bash
git add .gitignore go.mod go.sum cmd/gui frontend
git commit -m "feat(gui): Wails entry point and React/TypeScript frontend scaffold" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Frontend core — types, API wrapper, errors, store, router, theme, shell

**Files:**
- Create: `frontend/src/lib/{types.ts,bridge.ts,api.ts,errors.ts,store.tsx,router.tsx,theme.ts,format.ts}`, `frontend/src/components/{ui.tsx,toast.tsx,Shell.tsx}`, `frontend/src/screens/{AgentDown.tsx,index.tsx}`, `frontend/src/test/mockBridge.ts`, tests `frontend/src/lib/{api.test.ts,errors.test.ts,store.test.tsx,router.test.tsx}`
- Modify: `frontend/src/App.tsx`, `frontend/src/App.test.tsx`, `frontend/src/test/setup.ts`

**Interfaces:**
- Consumes: the Task 6 bound methods (via `wailsjs/go/gui/App`) and events `agent:event` (data `AgentEvent`), `agent:connection` (data `ConnState`).
- Produces (every later frontend task uses these exact names):
  - `src/lib/types.ts`: all types below.
  - `src/lib/api.ts`: `api.<method>` as listed, `AgentError {code, message}`, `toAgentError(e)`.
  - `src/lib/errors.ts`: `describeError(e): Described`, `fieldCodes`.
  - `src/lib/store.tsx`: `StoreProvider`, `useStore(): { state, dispatch, refresh, reloadProfiles }`, `reducer`, `initialState`, `ACTIVITY_LIMIT = 500`.
  - `src/lib/router.tsx`: `Route`, `Tab`, `RouterProvider`, `useRouter(): { route, navigate }`, `useDirtyGuard(dirty: boolean)`.
  - `src/lib/format.ts`: `formatTime`, `isZeroTime`, `formatSize`, `stateLabels`, `stateTone`, `summarizeResult`, `directionLabels`.
  - `src/lib/theme.ts`: `Theme`, `useTheme()`.
  - `src/components/ui.tsx`: `Button`, `Badge`, `Field`, `Input`, `Select`, `Checkbox`, `Modal`, `ConfirmDialog`, `Spinner`, `EmptyState`, `Table` helpers as defined below.
  - `src/components/toast.tsx`: `ToastProvider`, `useToast(): { show(message, opts?), error(e) }`.
  - `src/screens/index.tsx`: `Screen({ route })` — the route switch later tasks extend.
  - Tests mock the bridge with `vi.mock('<relative>/lib/bridge', () => import('<relative>/test/mockBridge'))` and use `bindings`, `emit`, `resetBridge` from `src/test/mockBridge.ts`.

- [ ] **Step 1: Types and bridge.** `src/lib/types.ts`:

```ts
// JSON shapes of the Go API (internal/api, internal/model, internal/engine,
// internal/storage, internal/gui). Times are RFC 3339 strings; never turn
// Action.updatedAt into a Date (it is sent back verbatim).
export type Direction = 'two-way' | 'local-to-cloud' | 'cloud-to-local'
export type ScheduleKind = 'none' | 'interval' | 'daily' | 'weekly' | 'cron'
export type Side = 'local' | 'cloud'
export type RestoreTarget = 'local' | 'cloud' | 'both'
export type ActionState = 'idle' | 'running' | 'paused-conflict' | 'paused-deletes' | 'paused' | 'error'

export interface Schedule { kind: ScheduleKind; everyMinutes?: number; at?: string; weekday?: number; cron?: string }
export interface Triggers { onChange: boolean; debounceSeconds: number; schedule: Schedule; remotePollMinutes: number }
export interface Item {
  id: string
  remoteName: string
  type: 'file' | 'dir'
  excludes: string[] | null
  localPaths: Record<string, string> | null
}
export interface Action {
  id: string
  name: string
  direction: Direction
  machineDirections: Record<string, Direction> | null
  triggers: Triggers
  propagateDeletes: boolean
  receiverRestoresCloud: boolean
  encrypt: boolean
  excludes: string[] | null
  snapshotRetention: number
  deleteGuardMinFiles: number
  deleteGuardPercent: number
  items: Item[] | null
  machines: Record<string, string> | null
  updatedAt: string
}
export interface Rules {
  name: string
  direction: Direction
  triggers: Triggers
  propagateDeletes: boolean
  receiverRestoresCloud: boolean
  excludes: string[]
  snapshotRetention: number
  deleteGuardMinFiles: number
  deleteGuardPercent: number
}
export interface ActionStatus {
  action: Action
  profileId: string
  state: ActionState
  running: boolean
  lastRun?: string
  lastOutcome?: string
  lastSummary?: string
  lastError?: string
  failures: number
  nextRun?: string
  conflicts: number
  pendingDeletes: number
}
export interface Status { machineId: string; machineName: string; version: string; agent: boolean; actions: ActionStatus[] | null }
export interface Settings { machineId: string; machineName: string; autostart: boolean; autostartSupported: boolean; logLevel: string }
export interface Profile {
  id: string
  name: string
  provider: string
  endpoint: string
  region: string
  bucket: string
  rootPrefix: string
  accessKeyId: string
  pathStyle: boolean
  conditionalWrites: boolean
}
export interface ProbeResult { list: boolean; put: boolean; get: boolean; delete: boolean; conditionalWrites: boolean }
export interface FileEntry { path: string; hash: string; size: number; mtime: number; etag?: string; writer?: string }
export interface Conflict { path: string; local: FileEntry | null; remote: FileEntry | null; detectedAt: string }
export interface ConflictDetail { conflict: Conflict; localText: string; remoteText: string; binary: boolean; tooLarge: boolean }
export interface SnapshotInfo { ts: string; reason: string; machineId: string; createdAt: string; files: FileEntry[] | null; deletes?: string[] }
export interface SyncResult {
  outcome: string
  uploaded: number
  downloaded: number
  deletedRemote: number
  deletedLocal: number
  adopted: number
  conflicts: Conflict[] | null
  deferred: string[] | null
  pendingDeletes: string[] | null
  unsupported: string[] | null
}
export interface PendingDeletes { paths: string[] | null; approved: boolean; detectedAt: string }
export interface HistoryEntry { id: number; actionId: string; kind: string; startedAt: string; finishedAt: string; outcome: string; message: string }
export interface RestoreResult { restored: string[] | null; skippedUnmapped: string[] | null }
export interface RestoreRequest { ts: string; paths?: string[]; to: RestoreTarget }
export interface NewItem { remoteName: string; type: 'file' | 'dir'; excludes: string[] }
export interface CreateActionRequest { profileId: string; action: Partial<Action> & { name: string }; passphrase?: string }
export interface JoinActionRequest { profileId: string; actionId: string; passphrase?: string }
export interface AgentEvent {
  type: 'action.state' | 'action.result' | 'actions.changed' | 'log'
  actionId?: string
  status?: ActionStatus
  result?: SyncResult
  error?: string
  message?: string
  time: string
}
export interface ConnState { connected: boolean; reason?: string }
```

`src/lib/bridge.ts` (the only file importing `wailsjs/`):
```ts
import * as App from '../../wailsjs/go/gui/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'

export const bindings: Record<string, (...args: unknown[]) => Promise<unknown>> =
  App as unknown as Record<string, (...args: unknown[]) => Promise<unknown>>

// onEvent subscribes to a Go event; it returns the unsubscribe function.
export function onEvent(name: string, cb: (data: any) => void): () => void {
  return EventsOn(name, cb)
}
```

`src/test/mockBridge.ts`:
```ts
import { vi } from 'vitest'

type Fn = ReturnType<typeof vi.fn>
const fns: Record<string, Fn> = {}
// Any binding is a vi.fn resolving undefined until a test sets it.
export const bindings: Record<string, Fn> = new Proxy(fns, {
  get(target, name: string) {
    if (!(name in target)) target[name] = vi.fn(async () => undefined)
    return target[name]
  },
})

const handlers = new Map<string, Set<(d: any) => void>>()

export function onEvent(name: string, cb: (d: any) => void): () => void {
  if (!handlers.has(name)) handlers.set(name, new Set())
  handlers.get(name)!.add(cb)
  return () => handlers.get(name)?.delete(cb)
}

export function emit(name: string, data: unknown) {
  handlers.get(name)?.forEach((h) => h(data))
}

export function resetBridge() {
  handlers.clear()
  for (const k of Object.keys(fns)) delete fns[k]
}
```
`src/test/setup.ts`:
```ts
import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'
import { resetBridge } from './mockBridge'

afterEach(() => {
  cleanup()
  resetBridge()
})
```

- [ ] **Step 2: Failing tests for api and errors.** `src/lib/api.test.ts`:
```ts
import { vi } from 'vitest'
vi.mock('./bridge', () => import('../test/mockBridge'))
import { bindings } from '../test/mockBridge'
import { api, AgentError, toAgentError } from './api'

test('toAgentError parses the Go JSON error', () => {
  const e = toAgentError('{"code":"in-use","message":"used by Editor"}')
  expect(e).toBeInstanceOf(AgentError)
  expect(e.code).toBe('in-use')
  expect(e.message).toBe('used by Editor')
  expect(toAgentError(new Error('{"code":"busy","message":"b"}')).code).toBe('busy')
  expect(toAgentError('plain failure')).toMatchObject({ code: 'internal', message: 'plain failure' })
})

test('calls reject with AgentError and pass arguments through', async () => {
  bindings.UpdateRules.mockRejectedValue('{"code":"conflict-edit","message":"changed"}')
  const rules = { name: 'x' } as never
  await expect(api.updateRules('a1', rules, '2026-10-04T01:02:03.000000004Z')).rejects.toMatchObject({ code: 'conflict-edit' })
  expect(bindings.UpdateRules).toHaveBeenCalledWith('a1', { rules, expectedUpdatedAt: '2026-10-04T01:02:03.000000004Z' })
})

test('null lists become empty arrays', async () => {
  bindings.Profiles.mockResolvedValue(null)
  expect(await api.profiles()).toEqual([])
})
```
`src/lib/errors.test.ts`:
```ts
import { describeError } from './errors'
import { AgentError } from './api'

test('known codes get a friendly message and keep the raw one as details', () => {
  const d = describeError(new AgentError('access-denied', 'AccessDenied: 403'))
  expect(d.message).toMatch(/refused access/)
  expect(d.details).toBe('AccessDenied: 403')
  expect(d.field).toBe(false)
})

test('field codes show the agent message inline', () => {
  expect(describeError(new AgentError('invalid', 'name is empty'))).toMatchObject({ field: true, message: 'name is empty' })
  expect(describeError(new AgentError('protected-path', 'contains the data folder'))).toMatchObject({ field: true })
})

test('in-use shows the agent message; unknown codes are generic with details', () => {
  expect(describeError(new AgentError('in-use', 'used by Editor')).message).toBe('used by Editor')
  expect(describeError('{"code":"weird","message":"x"}')).toMatchObject({ message: 'Something went wrong.', details: 'x' })
})
```
Run `cd frontend && npx vitest run src/lib` → FAIL (modules missing).

- [ ] **Step 3: Implement api and errors.** `src/lib/api.ts`:
```ts
import { bindings } from './bridge'
import type {
  Action, ActionStatus, ConflictDetail, Conflict, ConnState, CreateActionRequest, Direction, HistoryEntry, Item,
  JoinActionRequest, NewItem, PendingDeletes, ProbeResult, Profile, RestoreRequest, RestoreResult, Rules, Schedule,
  Settings, Side, SnapshotInfo, Status, SyncResult,
} from './types'

export class AgentError extends Error {
  constructor(public code: string, message: string) {
    super(message)
    this.name = 'AgentError'
  }
}

// toAgentError turns a rejected binding call into an AgentError. Go errors
// arrive as the JSON {"code","message"} (internal/gui/errors.go).
export function toAgentError(e: unknown): AgentError {
  if (e instanceof AgentError) return e
  const raw = typeof e === 'string' ? e : e instanceof Error ? e.message : String(e)
  try {
    const j = JSON.parse(raw)
    if (j && typeof j.code === 'string') return new AgentError(j.code, String(j.message ?? ''))
  } catch {
    // not JSON
  }
  return new AgentError('internal', raw)
}

function call<T>(name: string, ...args: unknown[]): Promise<T> {
  return (bindings[name](...args) as Promise<T>).catch((e: unknown) => {
    throw toAgentError(e)
  })
}

const orEmpty = <T,>(v: T[] | null | undefined): T[] => v ?? []

export const api = {
  status: () => call<Status>('Status'),
  settings: () => call<Settings>('Settings'),
  updateSettings: (s: Settings) => call<Settings>('UpdateSettings', s),
  profiles: () => call<Profile[] | null>('Profiles').then(orEmpty),
  addProfile: (profile: Profile, secret: string) => call<Profile>('AddProfile', { profile, secret }),
  updateProfile: (profile: Profile, secret: string) => call<Profile>('UpdateProfile', { profile, secret }),
  removeProfile: (id: string) => call<void>('RemoveProfile', id),
  testProfile: (id: string) => call<ProbeResult>('TestProfile', id),
  discover: (profileId: string) => call<Action[] | null>('Discover', profileId).then(orEmpty),
  actions: () => call<ActionStatus[] | null>('Actions').then(orEmpty),
  action: (id: string) => call<ActionStatus>('Action', id),
  createAction: (req: CreateActionRequest) => call<Action>('CreateAction', req),
  joinAction: (req: JoinActionRequest) => call<Action>('JoinAction', req),
  updateRules: (id: string, rules: Rules, expectedUpdatedAt: string) =>
    call<Action>('UpdateRules', id, { rules, expectedUpdatedAt }),
  // '' resets this machine to the action's default direction.
  setMachineDirection: (id: string, d: Direction | '') => call<void>('SetMachineDirection', id, d),
  leaveAction: (id: string, force: boolean) => call<void>('LeaveAction', id, force),
  addItem: (id: string, item: NewItem, localPath: string) => call<Item>('AddItem', id, { item, localPath }),
  mapItem: (id: string, itemId: string, localPath: string) => call<void>('MapItem', id, itemId, localPath),
  unmapItem: (id: string, itemId: string) => call<void>('UnmapItem', id, itemId),
  removeItem: (id: string, itemId: string) => call<void>('RemoveItem', id, itemId),
  sync: (id: string) => call<SyncResult>('Sync', id),
  cancelSync: (id: string) => call<void>('CancelSync', id),
  snapshot: (id: string) => call<SnapshotInfo>('Snapshot', id),
  pause: (id: string) => call<void>('Pause', id),
  resume: (id: string) => call<void>('Resume', id),
  pendingDeletes: (id: string) => call<PendingDeletes | null>('PendingDeletes', id),
  approveDeletes: (id: string, reviewed: string[]) => call<SyncResult>('ApproveDeletes', id, reviewed),
  snapshots: (id: string) => call<SnapshotInfo[] | null>('Snapshots', id).then(orEmpty),
  restore: (id: string, req: RestoreRequest) => call<RestoreResult>('Restore', id, req),
  conflicts: (id: string) => call<Conflict[] | null>('Conflicts', id).then(orEmpty),
  conflictDetail: (id: string, path: string) => call<ConflictDetail>('ConflictDetail', id, path),
  resolve: (id: string, choices: Record<string, Side>) => call<SyncResult>('Resolve', id, choices),
  history: (id: string, limit: number) => call<HistoryEntry[] | null>('History', id, limit).then(orEmpty),
  previewSchedule: (s: Schedule) => call<string[] | null>('PreviewSchedule', s).then(orEmpty),
  pickFile: (title: string) => call<string>('PickFile', title),
  pickFolder: (title: string) => call<string>('PickFolder', title),
  openDataFolder: () => call<void>('OpenDataFolder'),
  openLogFolder: () => call<void>('OpenLogFolder'),
  connectionState: () => call<ConnState>('ConnectionState'),
  retryConnect: () => call<void>('RetryConnect'),
  dataDir: () => call<string>('DataDir'),
}
```
`src/lib/errors.ts`:
```ts
import { toAgentError } from './api'

const messages: Record<string, string> = {
  busy: 'The agent is busy with this action. Try again in a moment.',
  locked: 'Another machine is syncing this action. Try again in a minute.',
  'not-found': 'It no longer exists; it may have been removed elsewhere.',
  'conflict-pending': 'This action has conflicts. Resolve them first.',
  'conflict-edit': 'The action was changed elsewhere while you edited it.',
  'passphrase-missing': 'This action is encrypted. Enter its passphrase.',
  decrypt: 'The passphrase is wrong or the cloud data is damaged.',
  'keycheck-missing': "The action's key check file is missing in the bucket, so the passphrase cannot be verified.",
  'root-missing': "An item's folder is missing (unmounted drive or renamed folder?).",
  'access-denied': 'The bucket refused access. Check the access key, the secret and the permissions.',
  'no-such-bucket': 'The bucket does not exist. Check the profile.',
  'lock-lost': 'This machine lost the cloud lock during the run. Nothing is damaged; run it again.',
  unsupported: 'This is not supported on this system.',
  unavailable: 'The agent is shutting down. Try again in a moment.',
  cancelled: 'Cancelled.',
  unreachable: 'The agent is not running.',
  unauthorized: 'The agent restarted. Reconnecting…',
}

// Codes whose agent message belongs next to the form field.
export const fieldCodes = new Set(['invalid', 'protected-path'])

export interface Described {
  code: string
  message: string
  details?: string
  field: boolean
}

export function describeError(e: unknown): Described {
  const err = toAgentError(e)
  if (fieldCodes.has(err.code)) return { code: err.code, message: err.message, field: true }
  if (err.code === 'in-use') return { code: err.code, message: err.message, field: false }
  const known = messages[err.code]
  if (known) return { code: err.code, message: known, details: err.message, field: false }
  return { code: err.code, message: 'Something went wrong.', details: err.message, field: false }
}
```
Run the two test files → PASS.

- [ ] **Step 4: Format and theme.** `src/lib/format.ts`:
```ts
import type { ActionState, Direction, SyncResult } from './types'

export const isZeroTime = (s?: string) => !s || s.startsWith('0001-01-01')

export function formatTime(s?: string): string {
  if (isZeroTime(s)) return '—'
  const d = new Date(s!)
  return Number.isNaN(d.getTime()) ? s! : d.toLocaleString()
}

export function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`
}

export const stateLabels: Record<ActionState, string> = {
  idle: 'Idle',
  running: 'Running',
  'paused-conflict': 'Conflicts',
  'paused-deletes': 'Deletions to review',
  paused: 'Paused',
  error: 'Error',
}

export type Tone = 'neutral' | 'ok' | 'warn' | 'error'

export function stateTone(s: ActionState): Tone {
  if (s === 'error') return 'error'
  if (s === 'paused-conflict' || s === 'paused-deletes') return 'warn'
  if (s === 'idle') return 'ok'
  return 'neutral'
}

export const directionLabels: Record<Direction, string> = {
  'two-way': 'Two-way',
  'local-to-cloud': 'This machine → cloud',
  'cloud-to-local': 'Cloud → this machine',
}

// summarizeResult mirrors model.SyncResult.Summary in Go.
export function summarizeResult(r: SyncResult): string {
  let s = `uploaded ${r.uploaded}, downloaded ${r.downloaded}, deleted in cloud ${r.deletedRemote}, deleted locally ${r.deletedLocal}, adopted ${r.adopted}, conflicts ${r.conflicts?.length ?? 0}`
  if (r.deferred?.length) s += `, deferred ${r.deferred.length}`
  if (r.pendingDeletes?.length) s += `, pending deletions ${r.pendingDeletes.length}`
  if (r.unsupported?.length) s += `, unsupported ${r.unsupported.length}`
  return s
}
```
`src/lib/theme.ts`:
```ts
import { useEffect, useState } from 'react'

export type Theme = 'system' | 'light' | 'dark'
const KEY = 's3as.theme'

export function loadTheme(): Theme {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // storage unavailable
  }
  return 'system'
}

function saveTheme(t: Theme) {
  try {
    localStorage.setItem(KEY, t)
  } catch {
    // storage unavailable
  }
}

export const isDark = (t: Theme, prefersDark: boolean) => t === 'dark' || (t === 'system' && prefersDark)

export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setTheme] = useState<Theme>(loadTheme)
  useEffect(() => {
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)')
    const apply = () => document.documentElement.classList.toggle('dark', isDark(theme, !!mq?.matches))
    apply()
    saveTheme(theme)
    mq?.addEventListener?.('change', apply)
    return () => mq?.removeEventListener?.('change', apply)
  }, [theme])
  return [theme, setTheme]
}
```

- [ ] **Step 5: Store — failing test** `src/lib/store.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('./bridge', () => import('../test/mockBridge'))
import { act, render, screen, waitFor } from '@testing-library/react'
import { bindings, emit } from '../test/mockBridge'
import { ACTIVITY_LIMIT, initialState, reducer, StoreProvider, useStore } from './store'
import type { ActionStatus } from './types'

const st = (id: string, state: ActionStatus['state'], name = 'Editor'): ActionStatus =>
  ({ action: { id, name } as ActionStatus['action'], profileId: 'p', state, running: false, failures: 0, conflicts: 0, pendingDeletes: 0 })

test('state events update the action and log only real state changes', () => {
  let s = reducer(initialState, { type: 'actions', list: [st('a', 'idle')] })
  s = reducer(s, { type: 'event', event: { type: 'action.state', actionId: 'a', status: { ...st('a', 'idle'), running: true }, time: 't1' } })
  expect(s.actions.a.running).toBe(true)
  expect(s.activity).toHaveLength(0)
  s = reducer(s, { type: 'event', event: { type: 'action.state', actionId: 'a', status: st('a', 'paused-conflict'), time: 't2' } })
  expect(s.activity[0]).toMatchObject({ kind: 'state', actionName: 'Editor', text: 'Idle → Conflicts' })
})

test('result events are logged newest first and capped', () => {
  let s = reducer(initialState, { type: 'actions', list: [st('a', 'idle')] })
  const result = { outcome: 'ok', uploaded: 2, downloaded: 0, deletedRemote: 0, deletedLocal: 0, adopted: 0, conflicts: null, deferred: null, pendingDeletes: null, unsupported: null }
  for (let i = 0; i < ACTIVITY_LIMIT + 5; i++) {
    s = reducer(s, { type: 'event', event: { type: 'action.result', actionId: 'a', result, time: `t${i}` } })
  }
  expect(s.activity).toHaveLength(ACTIVITY_LIMIT)
  expect(s.activity[0].time).toBe(`t${ACTIVITY_LIMIT + 4}`)
  expect(s.activity[0].text).toBe('ok: uploaded 2, downloaded 0, deleted in cloud 0, deleted locally 0, adopted 0, conflicts 0')
  s = reducer(s, { type: 'event', event: { type: 'action.result', actionId: 'a', error: 'network down', time: 'tx' } })
  expect(s.activity[0]).toMatchObject({ kind: 'error', text: 'network down' })
})

test('actions list replaces the map (left actions disappear)', () => {
  let s = reducer(initialState, { type: 'actions', list: [st('a', 'idle'), st('b', 'idle')] })
  s = reducer(s, { type: 'actions', list: [st('b', 'idle')] })
  expect(Object.keys(s.actions)).toEqual(['b'])
})

function Probe() {
  const { state } = useStore()
  return <div>{state.connection.connected ? `on:${Object.keys(state.actions).join(',')}` : 'off'}</div>
}

test('provider loads everything on connect and refetches on events', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: false, reason: 'connecting' })
  bindings.Status.mockResolvedValue({ machineId: 'm', machineName: 'mac', version: 'v', agent: true, actions: [st('a', 'idle')] })
  bindings.Profiles.mockResolvedValue([])
  render(<StoreProvider><Probe /></StoreProvider>)
  expect(await screen.findByText('off')).toBeInTheDocument()
  act(() => emit('agent:connection', { connected: true }))
  expect(await screen.findByText('on:a')).toBeInTheDocument()
  bindings.Actions.mockResolvedValue([st('a', 'idle'), st('b', 'idle')])
  act(() => emit('agent:event', { type: 'actions.changed', actionId: 'b', time: 't' }))
  expect(await screen.findByText('on:a,b')).toBeInTheDocument()
  bindings.Action.mockResolvedValue({ ...st('a', 'idle'), lastOutcome: 'ok' })
  act(() => emit('agent:event', { type: 'action.result', actionId: 'a', result: null, time: 't' }))
  await waitFor(() => expect(bindings.Action).toHaveBeenCalledWith('a'))
})
```

- [ ] **Step 6: Implement** `src/lib/store.tsx`:
```tsx
import { createContext, useCallback, useContext, useEffect, useReducer, type Dispatch, type ReactNode } from 'react'
import { api } from './api'
import { onEvent } from './bridge'
import { stateLabels, summarizeResult } from './format'
import type { ActionStatus, AgentEvent, ConnState, Profile, Status } from './types'

export const ACTIVITY_LIMIT = 500

export interface ActivityEntry {
  id: number
  time: string
  actionId?: string
  actionName: string
  kind: 'result' | 'state' | 'error'
  text: string
}

export interface State {
  connection: ConnState
  status: Status | null
  actions: Record<string, ActionStatus>
  profiles: Profile[]
  activity: ActivityEntry[]
  seq: number
}

export type Msg =
  | { type: 'connection'; conn: ConnState }
  | { type: 'status'; status: Status }
  | { type: 'actions'; list: ActionStatus[] }
  | { type: 'action'; status: ActionStatus }
  | { type: 'profiles'; list: Profile[] }
  | { type: 'event'; event: AgentEvent }

export const initialState: State = {
  connection: { connected: false, reason: 'connecting to the agent' },
  status: null,
  actions: {},
  profiles: [],
  activity: [],
  seq: 0,
}

const byId = (list: ActionStatus[]) => Object.fromEntries(list.map((a) => [a.action.id, a]))

function log(s: State, e: Omit<ActivityEntry, 'id'>): State {
  const entry = { ...e, id: s.seq + 1 }
  return { ...s, seq: entry.id, activity: [entry, ...s.activity].slice(0, ACTIVITY_LIMIT) }
}

export function reducer(s: State, m: Msg): State {
  switch (m.type) {
    case 'connection':
      return { ...s, connection: m.conn }
    case 'status':
      return { ...s, status: m.status, actions: byId(m.status.actions ?? []) }
    case 'actions':
      return { ...s, actions: byId(m.list) }
    case 'action':
      return { ...s, actions: { ...s.actions, [m.status.action.id]: m.status } }
    case 'profiles':
      return { ...s, profiles: m.list }
    case 'event': {
      const ev = m.event
      const id = ev.actionId
      const name = (id && (s.actions[id]?.action.name ?? ev.status?.action.name)) || id || ''
      if (ev.type === 'action.state' && id && ev.status) {
        const prev = s.actions[id]
        const next = { ...s, actions: { ...s.actions, [id]: ev.status } }
        if (prev && prev.state !== ev.status.state) {
          return log(next, { time: ev.time, actionId: id, actionName: name, kind: 'state', text: `${stateLabels[prev.state]} → ${stateLabels[ev.status.state]}` })
        }
        return next
      }
      if (ev.type === 'action.result') {
        if (ev.error) return log(s, { time: ev.time, actionId: id, actionName: name, kind: 'error', text: ev.error })
        if (ev.result) {
          return log(s, { time: ev.time, actionId: id, actionName: name, kind: 'result', text: `${ev.result.outcome}: ${summarizeResult(ev.result)}` })
        }
      }
      return s
    }
  }
}

interface StoreValue {
  state: State
  dispatch: Dispatch<Msg>
  refresh: () => Promise<void>
  reloadProfiles: () => Promise<void>
}

const Ctx = createContext<StoreValue | null>(null)

export function StoreProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, initialState)
  const reloadProfiles = useCallback(async () => {
    dispatch({ type: 'profiles', list: await api.profiles() })
  }, [])
  const refresh = useCallback(async () => {
    const [status, profiles] = await Promise.all([api.status(), api.profiles()])
    dispatch({ type: 'status', status })
    dispatch({ type: 'profiles', list: profiles })
  }, [])
  useEffect(() => {
    const quiet = () => {} // a failed background refresh shows up as a connection change
    const onConn = (conn: ConnState) => {
      dispatch({ type: 'connection', conn })
      if (conn.connected) refresh().catch(quiet)
    }
    const offConn = onEvent('agent:connection', onConn)
    const offEv = onEvent('agent:event', (ev: AgentEvent) => {
      dispatch({ type: 'event', event: ev })
      if (ev.type === 'action.result' && ev.actionId) {
        api.action(ev.actionId).then((status) => dispatch({ type: 'action', status })).catch(quiet)
      }
      if (ev.type === 'actions.changed') {
        api.actions().then((list) => dispatch({ type: 'actions', list })).catch(quiet)
      }
    })
    api.connectionState().then(onConn).catch(quiet)
    return () => {
      offConn()
      offEv()
    }
  }, [refresh])
  return <Ctx.Provider value={{ state, dispatch, refresh, reloadProfiles }}>{children}</Ctx.Provider>
}

export function useStore(): StoreValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useStore outside StoreProvider')
  return v
}
```
Run `npx vitest run src/lib/store.test.tsx` → PASS.

- [ ] **Step 7: UI kit and toasts.** `src/components/ui.tsx`:
```tsx
import { useEffect, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from 'react'
import type { Tone } from '../lib/format'

type Variant = 'primary' | 'secondary' | 'danger' | 'ghost'
const variants: Record<Variant, string> = {
  primary: 'bg-blue-600 text-white hover:bg-blue-700 disabled:bg-blue-300',
  secondary: 'border border-zinc-300 bg-white text-zinc-800 hover:bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800 dark:text-zinc-100 dark:hover:bg-zinc-700',
  danger: 'bg-red-600 text-white hover:bg-red-700 disabled:bg-red-300',
  ghost: 'text-zinc-700 hover:bg-zinc-100 dark:text-zinc-200 dark:hover:bg-zinc-800',
}

export function Button({ variant = 'secondary', className = '', ...p }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant }) {
  return <button type="button" className={`rounded-md px-3 py-1.5 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-60 ${variants[variant]} ${className}`} {...p} />
}

const tones: Record<Tone, string> = {
  neutral: 'bg-zinc-100 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300',
  ok: 'bg-green-100 text-green-800 dark:bg-green-900/40 dark:text-green-300',
  warn: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
  error: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300',
}

export function Badge({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${tones[tone]}`}>{children}</span>
}

export function Field({ label, hint, error, children }: { label: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-sm font-medium">{label}</span>
      {children}
      {hint && <span className="block text-xs text-zinc-500">{hint}</span>}
      {error && <span role="alert" className="block text-xs text-red-600">{error}</span>}
    </label>
  )
}

const control = 'w-full rounded-md border border-zinc-300 bg-white px-2 py-1.5 text-sm dark:border-zinc-600 dark:bg-zinc-800'

export function Input(p: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={control} {...p} />
}

export function Select(p: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className={control} {...p} />
}

export function Checkbox({ label, checked, onChange, disabled }: { label: ReactNode; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>{label}</span>
    </label>
  )
}

export function Modal({ title, children, footer, onClose, wide }: { title: string; children: ReactNode; footer?: ReactNode; onClose: () => void; wide?: boolean }) {
  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-6">
      <div role="dialog" aria-label={title} className={`flex max-h-full w-full flex-col rounded-lg bg-white shadow-xl dark:bg-zinc-900 ${wide ? 'max-w-4xl' : 'max-w-lg'}`}>
        <h2 className="border-b border-zinc-200 px-4 py-3 text-base font-semibold dark:border-zinc-700">{title}</h2>
        <div className="overflow-auto px-4 py-3">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-zinc-200 px-4 py-3 dark:border-zinc-700">{footer}</div>}
      </div>
    </div>
  )
}

export function ConfirmDialog(p: { title: string; message: ReactNode; confirmLabel: string; danger?: boolean; busy?: boolean; onConfirm: () => void; onCancel: () => void }) {
  return (
    <Modal title={p.title} onClose={p.onCancel} footer={
      <>
        <Button onClick={p.onCancel}>Cancel</Button>
        <Button variant={p.danger ? 'danger' : 'primary'} disabled={p.busy} onClick={p.onConfirm}>{p.confirmLabel}</Button>
      </>
    }>
      <div className="text-sm">{p.message}</div>
    </Modal>
  )
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return <span role="status" aria-label={label} className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-zinc-300 border-t-blue-600" />
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-zinc-300 p-8 text-center dark:border-zinc-700">
      <p className="font-medium">{title}</p>
      {children && <div className="mt-2 text-sm text-zinc-500">{children}</div>}
    </div>
  )
}

export const th = 'px-3 py-2 text-left text-xs font-semibold uppercase text-zinc-500'
export const td = 'px-3 py-2 text-sm align-top'
```
`src/components/toast.tsx`:
```tsx
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'
import { describeError } from '../lib/errors'

interface Toast { id: number; message: string; details?: string; tone: 'info' | 'error' }
interface ToastValue {
  show: (message: string, opts?: { details?: string; tone?: 'info' | 'error' }) => void
  error: (e: unknown) => void
}

const Ctx = createContext<ToastValue | null>(null)
let next = 1

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const [open, setOpen] = useState<number | null>(null)
  const dismiss = useCallback((id: number) => setToasts((t) => t.filter((x) => x.id !== id)), [])
  const show = useCallback<ToastValue['show']>((message, opts) => {
    const t: Toast = { id: next++, message, details: opts?.details, tone: opts?.tone ?? 'info' }
    setToasts((all) => [...all, t])
    setTimeout(() => dismiss(t.id), t.details ? 12000 : 6000)
  }, [dismiss])
  const error = useCallback((e: unknown) => {
    const d = describeError(e)
    show(d.message, { details: d.details, tone: 'error' })
  }, [show])
  // Stable, so screens can list the toast in effect dependencies.
  const value = useMemo(() => ({ show, error }), [show, error])
  return (
    <Ctx.Provider value={value}>
      {children}
      <div className="fixed bottom-4 right-4 z-50 flex w-96 flex-col gap-2">
        {toasts.map((t) => (
          <div key={t.id} role="alert" className={`rounded-md p-3 text-sm shadow-lg ${t.tone === 'error' ? 'bg-red-600 text-white' : 'bg-zinc-800 text-white'}`}>
            <div className="flex justify-between gap-2">
              <span>{t.message}</span>
              <button type="button" aria-label="Dismiss" onClick={() => dismiss(t.id)}>×</button>
            </div>
            {t.details && (
              <button type="button" className="mt-1 text-xs underline" onClick={() => setOpen(open === t.id ? null : t.id)}>Details</button>
            )}
            {t.details && open === t.id && <pre className="mt-1 whitespace-pre-wrap break-all text-xs">{t.details}</pre>}
          </div>
        ))}
      </div>
    </Ctx.Provider>
  )
}

export function useToast(): ToastValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useToast outside ToastProvider')
  return v
}
```

- [ ] **Step 8: Router — failing test** `src/lib/router.test.tsx`:
```tsx
import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { RouterProvider, useDirtyGuard, useRouter } from './router'

function Page() {
  const { route, navigate } = useRouter()
  const [dirty, setDirty] = useState(false)
  useDirtyGuard(dirty)
  return (
    <div>
      <span>at:{route.name}</span>
      <button onClick={() => setDirty(true)}>edit</button>
      <button onClick={() => navigate({ name: 'profiles' })}>go</button>
    </div>
  )
}

test('navigation asks before discarding unsaved changes', () => {
  render(<RouterProvider><Page /></RouterProvider>)
  fireEvent.click(screen.getByText('go'))
  expect(screen.getByText('at:profiles')).toBeInTheDocument()
})

test('cancel keeps the page, discard leaves it', () => {
  render(<RouterProvider><Page /></RouterProvider>)
  fireEvent.click(screen.getByText('edit'))
  fireEvent.click(screen.getByText('go'))
  expect(screen.getByText('Discard changes?')).toBeInTheDocument()
  fireEvent.click(screen.getByText('Cancel'))
  expect(screen.getByText('at:actions')).toBeInTheDocument()
  fireEvent.click(screen.getByText('go'))
  fireEvent.click(screen.getByText('Discard'))
  expect(screen.getByText('at:profiles')).toBeInTheDocument()
})
```
Implement `src/lib/router.tsx`:
```tsx
import { createContext, useCallback, useContext, useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { ConfirmDialog } from '../components/ui'

export type Tab = 'overview' | 'rules' | 'items' | 'snapshots' | 'history'
export type Route =
  | { name: 'actions' }
  | { name: 'action'; id: string; tab: Tab }
  | { name: 'conflicts'; id: string }
  | { name: 'deletes'; id: string }
  | { name: 'profiles' }
  | { name: 'activity' }
  | { name: 'settings' }

interface RouterValue {
  route: Route
  navigate: (r: Route) => void
  setDirty: (key: string, dirty: boolean) => void
}

const Ctx = createContext<RouterValue | null>(null)

export function RouterProvider({ initial = { name: 'actions' }, children }: { initial?: Route; children: ReactNode }) {
  const [route, setRoute] = useState<Route>(initial)
  const [pending, setPending] = useState<Route | null>(null)
  const dirty = useRef(new Set<string>())
  const setDirty = useCallback((key: string, d: boolean) => {
    if (d) dirty.current.add(key)
    else dirty.current.delete(key)
  }, [])
  const navigate = useCallback((r: Route) => {
    if (dirty.current.size > 0) setPending(r)
    else setRoute(r)
  }, [])
  return (
    <Ctx.Provider value={{ route, navigate, setDirty }}>
      {children}
      {pending && (
        <ConfirmDialog
          title="Discard changes?"
          message="You have unsaved changes. Leave this page and discard them?"
          confirmLabel="Discard"
          danger
          onCancel={() => setPending(null)}
          onConfirm={() => {
            dirty.current.clear()
            setRoute(pending)
            setPending(null)
          }}
        />
      )}
    </Ctx.Provider>
  )
}

export function useRouter(): RouterValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useRouter outside RouterProvider')
  return v
}

// useDirtyGuard makes navigation ask before leaving while dirty is true.
export function useDirtyGuard(dirty: boolean) {
  const { setDirty } = useRouter()
  const key = useId()
  useEffect(() => {
    setDirty(key, dirty)
    return () => setDirty(key, false)
  }, [dirty, key, setDirty])
}
```

- [ ] **Step 9: Shell, AgentDown, App.** `src/components/Shell.tsx`:
```tsx
import type { ReactNode } from 'react'
import { useRouter, type Route } from '../lib/router'
import { useStore } from '../lib/store'
import { useTheme, type Theme } from '../lib/theme'

const nav: { label: string; route: Route; match: Route['name'][] }[] = [
  { label: 'Actions', route: { name: 'actions' }, match: ['actions', 'action', 'conflicts', 'deletes'] },
  { label: 'Profiles', route: { name: 'profiles' }, match: ['profiles'] },
  { label: 'Activity', route: { name: 'activity' }, match: ['activity'] },
  { label: 'Settings', route: { name: 'settings' }, match: ['settings'] },
]

export function Shell({ children }: { children: ReactNode }) {
  const { state } = useStore()
  const { route, navigate } = useRouter()
  const [theme, setTheme] = useTheme()
  const conn = state.connection
  return (
    <div className="flex h-full">
      <nav className="w-48 shrink-0 border-r border-zinc-200 bg-zinc-50 p-3 dark:border-zinc-800 dark:bg-zinc-950">
        <div className="mb-4 px-2 text-lg font-semibold">s3-anysync</div>
        {nav.map((n) => (
          <button key={n.label} type="button" onClick={() => navigate(n.route)}
            className={`block w-full rounded-md px-2 py-1.5 text-left text-sm ${n.match.includes(route.name) ? 'bg-blue-100 font-medium text-blue-900 dark:bg-blue-900/40 dark:text-blue-100' : 'hover:bg-zinc-100 dark:hover:bg-zinc-800'}`}>
            {n.label}
          </button>
        ))}
      </nav>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-between border-b border-zinc-200 px-4 py-2 text-sm dark:border-zinc-800">
          <span>Machine: <strong>{state.status?.machineName ?? '…'}</strong></span>
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-1">
              <span className={`h-2 w-2 rounded-full ${conn.connected ? 'bg-green-500' : 'bg-amber-500'}`} />
              {conn.connected ? 'Agent connected' : 'Reconnecting…'}
            </span>
            <select aria-label="Theme" value={theme} onChange={(e) => setTheme(e.target.value as Theme)}
              className="rounded border border-zinc-300 bg-transparent px-1 py-0.5 dark:border-zinc-700">
              <option value="system">System</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select>
          </div>
        </header>
        {!conn.connected && (
          <div role="alert" className="bg-amber-100 px-4 py-2 text-sm text-amber-900 dark:bg-amber-900/40 dark:text-amber-100">
            The agent is not reachable{conn.reason ? ` (${conn.reason})` : ''}. Reconnecting every 2 seconds; changes cannot be saved until it is back.
          </div>
        )}
        <main className="min-h-0 flex-1 overflow-auto p-4">{children}</main>
      </div>
    </div>
  )
}
```
`src/screens/AgentDown.tsx`:
```tsx
import { useEffect, useState } from 'react'
import { Button, Spinner } from '../components/ui'
import { api } from '../lib/api'
import { useStore } from '../lib/store'

export function AgentDown() {
  const { state } = useStore()
  const [dir, setDir] = useState('')
  useEffect(() => {
    api.dataDir().then(setDir).catch(() => {})
  }, [])
  const reason = state.connection.reason ?? ''
  const connecting = reason.startsWith('connecting')
  return (
    <div className="flex h-full items-center justify-center p-8">
      <div className="max-w-lg space-y-4 text-center">
        <h1 className="text-xl font-semibold">{connecting ? 'Connecting to the agent…' : 'The s3-anysync agent is not running'}</h1>
        {connecting ? <Spinner /> : <p className="text-sm text-zinc-600 dark:text-zinc-400">{reason}</p>}
        {!connecting && (
          <>
            <Button variant="primary" onClick={() => api.retryConnect()}>Start the agent and retry</Button>
            <p className="text-xs text-zinc-500">
              Data folder: {dir || '…'}. The GUI starts <code>s3-anysync-agent</code> from its own folder, or from the path in
              <code> S3_ANYSYNC_AGENT</code>.
            </p>
          </>
        )}
      </div>
    </div>
  )
}
```
`src/screens/index.tsx` (later tasks add a `case` per screen):
```tsx
import { EmptyState } from '../components/ui'
import type { Route } from '../lib/router'

export function Screen({ route }: { route: Route }) {
  switch (route.name) {
    default:
      return <EmptyState title="This screen is added in a later task." />
  }
}
```
`src/App.tsx`:
```tsx
import { Shell } from './components/Shell'
import { ToastProvider } from './components/toast'
import { RouterProvider, useRouter } from './lib/router'
import { StoreProvider, useStore } from './lib/store'
import { AgentDown } from './screens/AgentDown'
import { Screen } from './screens'

function Main() {
  const { state } = useStore()
  const { route } = useRouter()
  // Before the first successful connection there is nothing to show.
  if (!state.connection.connected && !state.status) return <AgentDown />
  return (
    <Shell>
      <Screen route={route} />
    </Shell>
  )
}

export default function App() {
  return (
    <ToastProvider>
      <StoreProvider>
        <RouterProvider>
          <Main />
        </RouterProvider>
      </StoreProvider>
    </ToastProvider>
  )
}
```
`src/App.test.tsx` (replace):
```tsx
import { vi } from 'vitest'
vi.mock('./lib/bridge', () => import('./test/mockBridge'))
import { act, fireEvent, render, screen } from '@testing-library/react'
import { bindings, emit } from './test/mockBridge'
import App from './App'

test('shows the agent-down page with a retry button', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: false, reason: 'the agent did not start within 10s' })
  bindings.DataDir.mockResolvedValue('/tmp/data')
  render(<App />)
  expect(await screen.findByText('the agent did not start within 10s')).toBeInTheDocument()
  fireEvent.click(screen.getByText('Start the agent and retry'))
  expect(bindings.RetryConnect).toHaveBeenCalled()
})

test('shows the shell once connected, and a banner when the agent goes away', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm', machineName: 'studio-mac', version: 'v', agent: true, actions: [] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
  expect(await screen.findByText('studio-mac')).toBeInTheDocument()
  act(() => emit('agent:connection', { connected: false, reason: 'the connection to the agent was lost' }))
  expect(await screen.findByText(/not reachable/)).toBeInTheDocument()
  expect(screen.getByText('studio-mac')).toBeInTheDocument()
})
```

- [ ] **Step 10: Verify and commit.** `cd frontend && npm run typecheck && npm test -- --run` → all PASS; then the Go checks.
```bash
git add frontend
git commit -m "feat(gui): frontend core: API wrapper, errors, store, router, shell" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Profiles screen

**Files:**
- Create: `frontend/src/lib/providers.ts`, `frontend/src/screens/ProfileForm.tsx`, `frontend/src/screens/Profiles.tsx`, `frontend/src/screens/Profiles.test.tsx`
- Modify: `frontend/src/screens/index.tsx` (add `case 'profiles': return <Profiles />`)

**Interfaces:**
- Consumes: `api.profiles/addProfile/updateProfile/removeProfile/testProfile`, `useStore().state.{profiles,actions}`, `reloadProfiles`, `describeError`, `useToast`, UI kit.
- Produces: `ProfileForm({ profile?: Profile; onSaved(p: Profile): void; onCancel(): void })`, `providers` presets, `ProbeChecklist({ result })` (exported from `ProfileForm.tsx`, reused nowhere else).

- [ ] **Step 1: Failing test** `src/screens/Profiles.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings, emit } from '../test/mockBridge'
import App from '../App'
import type { Profile } from '../lib/types'

const prof: Profile = { id: 'p1', name: 'Home MinIO', provider: 'minio', endpoint: 'http://nas:9000', region: 'us-east-1', bucket: 'cfg', rootPrefix: 'sync', accessKeyId: 'AK', pathStyle: true, conditionalWrites: true }

async function openProfiles(profiles: Profile[], actions: unknown[] = []) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions })
  bindings.Profiles.mockResolvedValue(profiles)
  render(<App />)
  fireEvent.click(await screen.findByRole('button', { name: 'Profiles' }))
}

test('new profile: preset fills the endpoint, secret is required, test result is shown', async () => {
  await openProfiles([])
  fireEvent.click(screen.getByText('New profile'))
  const dlg = screen.getByRole('dialog')
  fireEvent.change(within(dlg).getByLabelText('Provider'), { target: { value: 'minio' } })
  expect(within(dlg).getByLabelText('Endpoint')).toHaveValue('http://localhost:9000')
  expect(within(dlg).getByLabelText('Path-style addressing')).toBeChecked()
  fireEvent.change(within(dlg).getByLabelText('Name'), { target: { value: 'Home' } })
  fireEvent.change(within(dlg).getByLabelText('Bucket'), { target: { value: 'cfg' } })
  fireEvent.change(within(dlg).getByLabelText('Access key ID'), { target: { value: 'AK' } })
  fireEvent.click(within(dlg).getByText('Save'))
  expect(await within(dlg).findByText('The secret access key is required.')).toBeInTheDocument()
  expect(bindings.AddProfile).not.toHaveBeenCalled()

  bindings.AddProfile.mockResolvedValue({ ...prof, id: 'new' })
  bindings.TestProfile.mockResolvedValue({ list: true, put: true, get: true, delete: true, conditionalWrites: false })
  fireEvent.change(within(dlg).getByLabelText('Secret access key'), { target: { value: 'SK' } })
  fireEvent.click(within(dlg).getByText('Save'))
  await waitFor(() => expect(bindings.AddProfile).toHaveBeenCalled())
  expect(bindings.AddProfile.mock.calls[0][0]).toMatchObject({ profile: { name: 'Home', bucket: 'cfg', endpoint: 'http://localhost:9000', pathStyle: true }, secret: 'SK' })
  expect(await screen.findByText(/Conditional writes: no/)).toBeInTheDocument()
})

test('edit with a blank secret keeps the stored one; agent field errors show inline', async () => {
  await openProfiles([prof])
  fireEvent.click(await screen.findByText('Edit'))
  const dlg = screen.getByRole('dialog')
  expect(within(dlg).getByLabelText('Secret access key')).toHaveValue('')
  bindings.UpdateProfile.mockRejectedValueOnce('{"code":"in-use","message":"the bucket and folder cannot change while actions use this profile: Editor"}')
  fireEvent.change(within(dlg).getByLabelText('Bucket'), { target: { value: 'other' } })
  fireEvent.click(within(dlg).getByText('Save'))
  expect(await within(dlg).findByText(/cannot change while actions use this profile: Editor/)).toBeInTheDocument()
  bindings.UpdateProfile.mockResolvedValueOnce(prof)
  fireEvent.change(within(dlg).getByLabelText('Bucket'), { target: { value: 'cfg' } })
  fireEvent.click(within(dlg).getByText('Save'))
  await waitFor(() => expect(bindings.UpdateProfile).toHaveBeenLastCalledWith({ profile: expect.objectContaining({ id: 'p1' }), secret: '' }))
})

test('remove is disabled while an action uses the profile', async () => {
  const used = { action: { id: 'a1', name: 'Editor' }, profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0 }
  await openProfiles([prof], [used])
  const remove = await screen.findByText('Remove')
  expect(remove).toBeDisabled()
  expect(remove).toHaveAttribute('title', 'Used by: Editor')
  act(() => emit('agent:event', { type: 'actions.changed', time: 't' }))
})
```

- [ ] **Step 2: Run** `npx vitest run src/screens/Profiles.test.tsx` → FAIL.

- [ ] **Step 3: Implement.** `src/lib/providers.ts`:
```ts
export interface ProviderPreset {
  id: string
  label: string
  endpoint: string
  region: string
  pathStyle: boolean
  hint?: string
}

// Endpoints with <…> parts must be completed by the user (checked on save).
export const providers: ProviderPreset[] = [
  { id: 'aws', label: 'AWS S3', endpoint: '', region: 'us-east-1', pathStyle: false, hint: 'Leave the endpoint empty for AWS.' },
  { id: 'r2', label: 'Cloudflare R2', endpoint: 'https://<account-id>.r2.cloudflarestorage.com', region: 'auto', pathStyle: true },
  { id: 'b2', label: 'Backblaze B2', endpoint: 'https://s3.<region>.backblazeb2.com', region: '<region>', pathStyle: false },
  { id: 'minio', label: 'MinIO', endpoint: 'http://localhost:9000', region: 'us-east-1', pathStyle: true },
  { id: 'wasabi', label: 'Wasabi', endpoint: 'https://s3.<region>.wasabisys.com', region: '<region>', pathStyle: false },
  { id: 'custom', label: 'Custom', endpoint: '', region: '', pathStyle: true },
]

export const providerLabel = (id: string) => providers.find((p) => p.id === id)?.label ?? id
```
`src/screens/ProfileForm.tsx`:
```tsx
import { useState } from 'react'
import { Button, Checkbox, Field, Input, Modal, Select } from '../components/ui'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { providers } from '../lib/providers'
import { useDirtyGuard } from '../lib/router'
import type { ProbeResult, Profile } from '../lib/types'

const blank: Profile = { id: '', name: '', provider: 'aws', endpoint: '', region: 'us-east-1', bucket: '', rootPrefix: '', accessKeyId: '', pathStyle: false, conditionalWrites: false }

export function ProbeChecklist({ result }: { result: ProbeResult }) {
  const row = (label: string, ok: boolean) => (
    <li key={label} className={ok ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}>
      {ok ? '✓' : '✗'} {label}
    </li>
  )
  return (
    <div className="space-y-1 text-sm">
      <ul>{[row('List', result.list), row('Upload', result.put), row('Download', result.get), row('Delete', result.delete)]}</ul>
      <p>
        Conditional writes: {result.conditionalWrites ? 'yes (safe cloud lock)' : 'no (best-effort cloud lock; avoid syncing the same action from two machines at the same moment)'}
      </p>
    </div>
  )
}

export function ProfileForm({ profile, onSaved, onCancel }: { profile?: Profile; onSaved: (p: Profile) => void; onCancel: () => void }) {
  const editing = !!profile
  const [p, setP] = useState<Profile>(profile ?? blank)
  const [secret, setSecret] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [probe, setProbe] = useState<ProbeResult | null>(null)
  const [saved, setSaved] = useState<Profile | null>(null)
  const dirty = !saved && (JSON.stringify(p) !== JSON.stringify(profile ?? blank) || secret !== '')
  useDirtyGuard(dirty)
  const set = <K extends keyof Profile>(k: K, v: Profile[K]) => setP((x) => ({ ...x, [k]: v }))

  const pickProvider = (id: string) => {
    const pre = providers.find((x) => x.id === id)!
    setP((x) => ({ ...x, provider: id, endpoint: pre.endpoint, region: pre.region, pathStyle: pre.pathStyle }))
  }

  const save = async () => {
    setError('')
    if (!p.name.trim() || !p.bucket.trim() || !p.accessKeyId.trim()) return setError('Name, bucket and access key ID are required.')
    if (!editing && !secret) return setError('The secret access key is required.')
    if (p.endpoint.includes('<') || p.region.includes('<')) return setError('Replace the <…> parts of the endpoint and region.')
    setBusy(true)
    try {
      const out = editing ? await api.updateProfile(p, secret) : await api.addProfile(p, secret)
      setSaved(out)
      if (!editing) setProbe(await api.testProfile(out.id).catch((e) => { setError(describeError(e).message); return null }))
      else onSaved(out)
    } catch (e) {
      const d = describeError(e)
      setError(d.details ? `${d.message} (${d.details})` : d.message)
    } finally {
      setBusy(false)
    }
  }

  if (saved && !editing) {
    return (
      <Modal title="Profile saved" onClose={() => onSaved(saved)} footer={<Button variant="primary" onClick={() => onSaved(saved)}>Done</Button>}>
        {probe ? <ProbeChecklist result={probe} /> : <p className="text-sm">Testing the connection…</p>}
        {error && <p role="alert" className="mt-2 text-sm text-red-600">{error}</p>}
      </Modal>
    )
  }

  const preset = providers.find((x) => x.id === p.provider)
  return (
    <Modal title={editing ? `Edit ${profile!.name}` : 'New profile'} onClose={onCancel} footer={
      <>
        <Button onClick={onCancel}>Cancel</Button>
        <Button variant="primary" disabled={busy} onClick={save}>Save</Button>
      </>
    }>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Name"><Input aria-label="Name" value={p.name} onChange={(e) => set('name', e.target.value)} /></Field>
        <Field label="Provider">
          <Select aria-label="Provider" value={p.provider} onChange={(e) => pickProvider(e.target.value)}>
            {providers.map((x) => <option key={x.id} value={x.id}>{x.label}</option>)}
          </Select>
        </Field>
        <Field label="Endpoint" hint={preset?.hint}><Input aria-label="Endpoint" value={p.endpoint} onChange={(e) => set('endpoint', e.target.value)} /></Field>
        <Field label="Region"><Input aria-label="Region" value={p.region} onChange={(e) => set('region', e.target.value)} /></Field>
        <Field label="Bucket"><Input aria-label="Bucket" value={p.bucket} onChange={(e) => set('bucket', e.target.value)} /></Field>
        <Field label="Folder in the bucket" hint="Optional."><Input aria-label="Folder in the bucket" value={p.rootPrefix} onChange={(e) => set('rootPrefix', e.target.value)} /></Field>
        <Field label="Access key ID"><Input aria-label="Access key ID" value={p.accessKeyId} onChange={(e) => set('accessKeyId', e.target.value)} /></Field>
        <Field label="Secret access key" hint={editing ? 'Leave empty to keep the stored secret.' : 'Stored in the system keychain.'}>
          <Input aria-label="Secret access key" type="password" autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} />
        </Field>
      </div>
      <div className="mt-3">
        <Checkbox label="Path-style addressing" checked={p.pathStyle} onChange={(v) => set('pathStyle', v)} />
      </div>
      {error && <p role="alert" className="mt-3 text-sm text-red-600">{error}</p>}
    </Modal>
  )
}
```
`Checkbox` must render an accessible label: the `<label>` wraps the input, so `getByLabelText('Path-style addressing')` works.

`src/screens/Profiles.tsx`:
```tsx
import { useState } from 'react'
import { useToast } from '../components/toast'
import { Button, ConfirmDialog, EmptyState, Modal, td, th } from '../components/ui'
import { api } from '../lib/api'
import { providerLabel } from '../lib/providers'
import { useStore } from '../lib/store'
import type { ProbeResult, Profile } from '../lib/types'
import { ProbeChecklist, ProfileForm } from './ProfileForm'

export function Profiles() {
  const { state, reloadProfiles } = useStore()
  const toast = useToast()
  const [editing, setEditing] = useState<Profile | 'new' | null>(null)
  const [removing, setRemoving] = useState<Profile | null>(null)
  const [probe, setProbe] = useState<{ p: Profile; r: ProbeResult | null } | null>(null)

  const usedBy = (id: string) => Object.values(state.actions).filter((a) => a.profileId === id).map((a) => a.action.name)
  const done = () => {
    setEditing(null)
    reloadProfiles().catch(toast.error)
  }
  const test = async (p: Profile) => {
    setProbe({ p, r: null })
    try {
      setProbe({ p, r: await api.testProfile(p.id) })
      reloadProfiles().catch(toast.error)
    } catch (e) {
      setProbe(null)
      toast.error(e)
    }
  }
  const remove = async (p: Profile) => {
    try {
      await api.removeProfile(p.id)
      setRemoving(null)
      await reloadProfiles()
    } catch (e) {
      setRemoving(null)
      toast.error(e)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Profiles</h1>
        <Button variant="primary" onClick={() => setEditing('new')}>New profile</Button>
      </div>
      {state.profiles.length === 0 ? (
        <EmptyState title="No profiles yet">A profile is a connection to an S3-compatible bucket.</EmptyState>
      ) : (
        <table className="w-full">
          <thead><tr><th className={th}>Name</th><th className={th}>Bucket</th><th className={th}>Endpoint</th><th className={th}>Cloud lock</th><th className={th} /></tr></thead>
          <tbody>
            {state.profiles.map((p) => {
              const used = usedBy(p.id)
              return (
                <tr key={p.id} className="border-t border-zinc-200 dark:border-zinc-800">
                  <td className={td}>{p.name}<div className="text-xs text-zinc-500">{providerLabel(p.provider)}</div></td>
                  <td className={td}>{p.bucket}{p.rootPrefix && `/${p.rootPrefix}`}</td>
                  <td className={td}>{p.endpoint || 'AWS default'}</td>
                  <td className={td}>{p.conditionalWrites ? 'Conditional' : 'Best effort'}</td>
                  <td className={`${td} space-x-2 whitespace-nowrap text-right`}>
                    <Button onClick={() => setEditing(p)}>Edit</Button>
                    <Button onClick={() => test(p)}>Test connection</Button>
                    <Button variant="danger" disabled={used.length > 0} title={used.length ? `Used by: ${used.join(', ')}` : undefined} onClick={() => setRemoving(p)}>Remove</Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {editing && <ProfileForm profile={editing === 'new' ? undefined : editing} onSaved={done} onCancel={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog title={`Remove ${removing.name}?`} danger confirmLabel="Remove"
          message="The profile and its stored secret are deleted from this machine. The bucket is not touched."
          onCancel={() => setRemoving(null)} onConfirm={() => remove(removing)} />
      )}
      {probe && (
        <Modal title={`Test ${probe.p.name}`} onClose={() => setProbe(null)} footer={<Button onClick={() => setProbe(null)}>Close</Button>}>
          {probe.r ? <ProbeChecklist result={probe.r} /> : <p className="text-sm">Testing…</p>}
        </Modal>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run** `npm run typecheck && npx vitest run` → PASS. Commit:
```bash
git add frontend
git commit -m "feat(gui): profiles screen with provider presets and connection test" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Actions list, schedule editor, New action and Join wizards

**Files:**
- Create: `frontend/src/lib/schedule.ts`, `frontend/src/lib/schedule.test.ts`, `frontend/src/components/ScheduleEditor.tsx`, `frontend/src/lib/useActionCommands.ts`, `frontend/src/screens/Actions.tsx`, `frontend/src/screens/NewActionWizard.tsx`, `frontend/src/screens/JoinWizard.tsx`, `frontend/src/screens/Actions.test.tsx`
- Modify: `frontend/src/screens/index.tsx` (add `case 'actions': return <Actions />`)

**Interfaces:**
- Consumes: `api.sync/pause/resume/cancelSync/createAction/addItem/discover/joinAction/mapItem/previewSchedule/pickFile/pickFolder`, store, router, toast.
- Produces:
  - `src/lib/schedule.ts`: `type Preset = 'none' | 'every15' | 'hourly' | 'interval' | 'daily' | 'weekly' | 'cron'`, `presetOf(s: Schedule): Preset`, `scheduleFor(p: Preset, cur: Schedule): Schedule`, `weekdays: string[]`
  - `ScheduleEditor({ value: Schedule; onChange(s: Schedule): void })` with a live preview of the next 3 runs
  - `useActionCommands(): { sync(id), pause(id), resume(id), cancel(id), snapshot(id) }` — calls with toasts (used again by the Overview tab)
  - `basename(path: string): string` exported from `NewActionWizard.tsx` (used by the Items tab)

- [ ] **Step 1: Failing tests.** `src/lib/schedule.test.ts`:
```ts
import { presetOf, scheduleFor } from './schedule'

test('presets round-trip', () => {
  expect(presetOf({ kind: 'none' })).toBe('none')
  expect(presetOf({ kind: 'interval', everyMinutes: 15 })).toBe('every15')
  expect(presetOf({ kind: 'interval', everyMinutes: 60 })).toBe('hourly')
  expect(presetOf({ kind: 'interval', everyMinutes: 45 })).toBe('interval')
  expect(presetOf({ kind: 'weekly', weekday: 1, at: '10:00' })).toBe('weekly')
  expect(scheduleFor('daily', { kind: 'none' })).toEqual({ kind: 'daily', at: '09:00' })
  expect(scheduleFor('daily', { kind: 'weekly', weekday: 3, at: '07:30' })).toEqual({ kind: 'daily', at: '07:30' })
  expect(scheduleFor('cron', { kind: 'none' })).toEqual({ kind: 'cron', cron: '0 * * * *' })
  expect(scheduleFor('every15', { kind: 'daily', at: '09:00' })).toEqual({ kind: 'interval', everyMinutes: 15 })
})
```
`src/screens/Actions.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import App from '../App'
import type { ActionStatus } from '../lib/types'

const action = (over: Partial<ActionStatus> = {}): ActionStatus => ({
  action: { id: 'a1', name: 'Editor', direction: 'two-way', machineDirections: null, triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, encrypt: false, excludes: null, snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50, items: [], machines: { m1: 'mac' }, updatedAt: '2026-10-04T01:02:03.000000004Z' },
  profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0, ...over,
})

function boot(actions: ActionStatus[]) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions })
  bindings.Profiles.mockResolvedValue([{ id: 'p1', name: 'Home', provider: 'minio', endpoint: '', region: '', bucket: 'b', rootPrefix: '', accessKeyId: 'k', pathStyle: true, conditionalWrites: true }])
  bindings.PreviewSchedule.mockResolvedValue([])
  render(<App />)
}

test('rows show state, counts as links, and the right buttons', async () => {
  boot([action({ state: 'paused-conflict', conflicts: 2 }), action({ action: { ...action().action, id: 'a2', name: 'Shell' }, running: true })])
  const editor = (await screen.findByText('Editor')).closest('tr')!
  expect(within(editor).getByText('Conflicts')).toBeInTheDocument()
  expect(within(editor).getByText('2 conflicts')).toBeInTheDocument()
  const shell = screen.getByText('Shell').closest('tr')!
  bindings.CancelSync.mockResolvedValue(undefined)
  fireEvent.click(within(shell).getByText('Cancel'))
  await waitFor(() => expect(bindings.CancelSync).toHaveBeenCalledWith('a2'))
})

test('sync now reports a cancelled run quietly', async () => {
  boot([action()])
  bindings.Sync.mockRejectedValue('{"code":"cancelled","message":"cancelled by the user"}')
  fireEvent.click(await screen.findByText('Sync now'))
  expect(await screen.findByText('Sync cancelled.')).toBeInTheDocument()
})

test('new action wizard creates the action and its first item', async () => {
  boot([])
  fireEvent.click(await screen.findByText('New action'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Next'))
  fireEvent.change(within(dlg).getByLabelText('Name'), { target: { value: 'VS Code' } })
  fireEvent.click(within(dlg).getByText('Next'))
  fireEvent.click(within(dlg).getByText('Next')) // not encrypted
  bindings.PickFolder.mockResolvedValue('/Users/me/Library/Application Support/Code/User')
  fireEvent.click(within(dlg).getByText('Add folder'))
  expect(await within(dlg).findByDisplayValue('User')).toBeInTheDocument()
  bindings.CreateAction.mockResolvedValue({ ...action().action, id: 'new', name: 'VS Code' })
  bindings.AddItem.mockResolvedValue({ id: 'i1', remoteName: 'User', type: 'dir', excludes: [], localPaths: {} })
  bindings.Action.mockResolvedValue(action({ action: { ...action().action, id: 'new', name: 'VS Code' } }))
  fireEvent.click(within(dlg).getByText('Create'))
  await waitFor(() => expect(bindings.AddItem).toHaveBeenCalledWith('new', { item: { remoteName: 'User', type: 'dir', excludes: [] }, localPath: '/Users/me/Library/Application Support/Code/User' }))
  expect(bindings.CreateAction.mock.calls[0][0]).toMatchObject({ profileId: 'p1', action: { name: 'VS Code', direction: 'two-way', propagateDeletes: true, triggers: { onChange: true, schedule: { kind: 'none' } } } })
})

test('join lists only actions this machine has not joined', async () => {
  boot([action()])
  bindings.Discover.mockResolvedValue([action().action, { ...action().action, id: 'a9', name: 'Terminal', encrypt: true }])
  fireEvent.click(await screen.findByText('Join existing'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByText('Find actions'))
  expect(await within(dlg).findByText('Terminal')).toBeInTheDocument()
  expect(within(dlg).queryByText('Editor')).toBeNull()
})
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** `src/lib/schedule.ts`:
```ts
import type { Schedule } from './types'

export type Preset = 'none' | 'every15' | 'hourly' | 'interval' | 'daily' | 'weekly' | 'cron'

export const presetLabels: Record<Preset, string> = {
  none: 'No schedule',
  every15: 'Every 15 minutes',
  hourly: 'Every hour',
  interval: 'Every N minutes',
  daily: 'Daily at…',
  weekly: 'Weekly on…',
  cron: 'Cron expression',
}

export const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

export function presetOf(s: Schedule): Preset {
  switch (s.kind) {
    case 'interval':
      return s.everyMinutes === 15 ? 'every15' : s.everyMinutes === 60 ? 'hourly' : 'interval'
    case 'daily':
    case 'weekly':
    case 'cron':
      return s.kind
    default:
      return 'none'
  }
}

export function scheduleFor(p: Preset, cur: Schedule): Schedule {
  switch (p) {
    case 'none':
      return { kind: 'none' }
    case 'every15':
      return { kind: 'interval', everyMinutes: 15 }
    case 'hourly':
      return { kind: 'interval', everyMinutes: 60 }
    case 'interval':
      return { kind: 'interval', everyMinutes: cur.kind === 'interval' && cur.everyMinutes ? cur.everyMinutes : 30 }
    case 'daily':
      return { kind: 'daily', at: cur.at ?? '09:00' }
    case 'weekly':
      return { kind: 'weekly', weekday: cur.weekday ?? 1, at: cur.at ?? '09:00' }
    case 'cron':
      return { kind: 'cron', cron: cur.cron ?? '0 * * * *' }
  }
}
```
`src/components/ScheduleEditor.tsx`:
```tsx
import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { formatTime } from '../lib/format'
import { presetLabels, presetOf, scheduleFor, weekdays, type Preset } from '../lib/schedule'
import type { Schedule } from '../lib/types'
import { Field, Input, Select } from './ui'

export function ScheduleEditor({ value, onChange }: { value: Schedule; onChange: (s: Schedule) => void }) {
  const preset = presetOf(value)
  const [runs, setRuns] = useState<string[]>([])
  const [error, setError] = useState('')
  useEffect(() => {
    let live = true
    const t = setTimeout(() => {
      api.previewSchedule(value)
        .then((r) => live && (setRuns(r), setError('')))
        .catch((e) => live && (setRuns([]), setError(describeError(e).message)))
    }, 300)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [value])
  return (
    <div className="space-y-2">
      <Field label="Schedule">
        <Select aria-label="Schedule" value={preset} onChange={(e) => onChange(scheduleFor(e.target.value as Preset, value))}>
          {(Object.keys(presetLabels) as Preset[]).map((p) => <option key={p} value={p}>{presetLabels[p]}</option>)}
        </Select>
      </Field>
      {preset === 'interval' && (
        <Field label="Minutes between runs">
          <Input aria-label="Minutes between runs" type="number" min={1} value={value.everyMinutes ?? 30}
            onChange={(e) => onChange({ ...value, everyMinutes: Number(e.target.value) })} />
        </Field>
      )}
      {preset === 'weekly' && (
        <Field label="Day">
          <Select aria-label="Day" value={value.weekday ?? 1} onChange={(e) => onChange({ ...value, weekday: Number(e.target.value) })}>
            {weekdays.map((d, i) => <option key={d} value={i}>{d}</option>)}
          </Select>
        </Field>
      )}
      {(preset === 'daily' || preset === 'weekly') && (
        <Field label="Time (24h, HH:MM)">
          <Input aria-label="Time" value={value.at ?? ''} onChange={(e) => onChange({ ...value, at: e.target.value })} />
        </Field>
      )}
      {preset === 'cron' && (
        <Field label="Cron expression" hint="Five fields: minute hour day-of-month month day-of-week.">
          <Input aria-label="Cron expression" value={value.cron ?? ''} onChange={(e) => onChange({ ...value, cron: e.target.value })} />
        </Field>
      )}
      {preset !== 'none' && (error
        ? <p role="alert" className="text-xs text-red-600">{error}</p>
        : <p className="text-xs text-zinc-500">Next runs: {runs.map(formatTime).join(' · ') || '…'}</p>)}
    </div>
  )
}
```
`src/lib/useActionCommands.ts`:
```ts
import { useToast } from '../components/toast'
import { api } from './api'
import { toAgentError } from './api'
import { summarizeResult } from './format'

export function useActionCommands() {
  const toast = useToast()
  const quietCancel = (what: string) => (e: unknown) => {
    if (toAgentError(e).code === 'cancelled') toast.show(`${what} cancelled.`)
    else toast.error(e)
  }
  return {
    sync: (id: string) =>
      api.sync(id).then((r) => {
        if (r.outcome === 'conflict') toast.show(`${r.conflicts?.length ?? 0} conflict(s) need your choice.`)
        else if (r.outcome === 'deletes-pending') toast.show(`${r.pendingDeletes?.length ?? 0} deletion(s) are waiting for your approval.`)
        else toast.show(`Synced: ${summarizeResult(r)}`)
      }, quietCancel('Sync')),
    snapshot: (id: string) => api.snapshot(id).then((s) => toast.show(`Snapshot ${s.ts} created.`), quietCancel('Snapshot')),
    pause: (id: string) => api.pause(id).catch(toast.error),
    resume: (id: string) => api.resume(id).catch(toast.error),
    cancel: (id: string) => api.cancelSync(id).catch(toast.error),
  }
}
```
`src/screens/Actions.tsx`:
```tsx
import { useState } from 'react'
import { Badge, Button, EmptyState, Spinner, td, th } from '../components/ui'
import { formatTime, isZeroTime, stateLabels, stateTone } from '../lib/format'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import { useActionCommands } from '../lib/useActionCommands'
import { JoinWizard } from './JoinWizard'
import { NewActionWizard } from './NewActionWizard'

export function Actions() {
  const { state } = useStore()
  const { navigate } = useRouter()
  const cmd = useActionCommands()
  const [wizard, setWizard] = useState<'new' | 'join' | null>(null)
  const list = Object.values(state.actions).sort((a, b) => a.action.name.localeCompare(b.action.name))
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Actions</h1>
        <div className="space-x-2">
          <Button onClick={() => setWizard('join')}>Join existing</Button>
          <Button variant="primary" onClick={() => setWizard('new')}>New action</Button>
        </div>
      </div>
      {list.length === 0 ? (
        <EmptyState title="No actions yet">Create an action for the settings you want to sync, or join one created on another machine.</EmptyState>
      ) : (
        <table className="w-full">
          <thead><tr><th className={th}>Action</th><th className={th}>State</th><th className={th}>Last sync</th><th className={th}>Next run</th><th className={th} /></tr></thead>
          <tbody>
            {list.map((a) => {
              const id = a.action.id
              const paused = a.state === 'paused'
              return (
                <tr key={id} className="border-t border-zinc-200 dark:border-zinc-800">
                  <td className={td}>
                    <button type="button" className="font-medium hover:underline" onClick={() => navigate({ name: 'action', id, tab: 'overview' })}>{a.action.name}</button>
                    {a.action.encrypt && <span className="ml-2 text-xs text-zinc-500">encrypted</span>}
                  </td>
                  <td className={`${td} space-y-1`}>
                    <div className="flex items-center gap-2"><Badge tone={stateTone(a.state)}>{stateLabels[a.state]}</Badge>{a.running && <Spinner label="Running" />}</div>
                    {a.conflicts > 0 && <button type="button" className="block text-xs text-amber-700 underline" onClick={() => navigate({ name: 'conflicts', id })}>{a.conflicts} conflicts</button>}
                    {a.pendingDeletes > 0 && <button type="button" className="block text-xs text-amber-700 underline" onClick={() => navigate({ name: 'deletes', id })}>{a.pendingDeletes} deletions to review</button>}
                  </td>
                  <td className={td}>
                    {formatTime(a.lastRun)}
                    {a.lastOutcome && <div className="text-xs text-zinc-500">{a.lastOutcome}{a.lastSummary ? `: ${a.lastSummary}` : ''}</div>}
                    {a.lastError && <div className="text-xs text-red-600">{a.lastError}</div>}
                  </td>
                  <td className={td}>{isZeroTime(a.nextRun) ? '—' : formatTime(a.nextRun)}</td>
                  <td className={`${td} space-x-2 whitespace-nowrap text-right`}>
                    {a.running
                      ? <Button onClick={() => cmd.cancel(id)}>Cancel</Button>
                      : <Button onClick={() => cmd.sync(id)} disabled={paused}>Sync now</Button>}
                    {paused ? <Button onClick={() => cmd.resume(id)}>Resume</Button> : <Button onClick={() => cmd.pause(id)}>Pause</Button>}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {wizard === 'new' && <NewActionWizard onClose={() => setWizard(null)} />}
      {wizard === 'join' && <JoinWizard onClose={() => setWizard(null)} />}
    </div>
  )
}
```
`src/screens/NewActionWizard.tsx`:
```tsx
import { useState } from 'react'
import { ScheduleEditor } from '../components/ScheduleEditor'
import { useToast } from '../components/toast'
import { Button, Checkbox, Field, Input, Modal, Select } from '../components/ui'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { directionLabels } from '../lib/format'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import type { Direction, Schedule } from '../lib/types'

export const basename = (p: string) => p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() ?? p

export interface DraftItem { remoteName: string; type: 'file' | 'dir'; localPath: string; excludes: string }

export const splitLines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)

const steps = ['Profile', 'Rules', 'Encryption', 'Items'] as const

export function NewActionWizard({ onClose }: { onClose: () => void }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const toast = useToast()
  const [step, setStep] = useState(0)
  const [profileId, setProfileId] = useState(state.profiles[0]?.id ?? '')
  const [name, setName] = useState('')
  const [direction, setDirection] = useState<Direction>('two-way')
  const [onChange, setOnChange] = useState(true)
  const [schedule, setSchedule] = useState<Schedule>({ kind: 'none' })
  const [encrypt, setEncrypt] = useState(false)
  const [pass, setPass] = useState('')
  const [pass2, setPass2] = useState('')
  const [items, setItems] = useState<DraftItem[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  if (state.profiles.length === 0) {
    return (
      <Modal title="New action" onClose={onClose} footer={<Button variant="primary" onClick={() => { onClose(); navigate({ name: 'profiles' }) }}>Go to Profiles</Button>}>
        <p className="text-sm">Create a profile (a bucket connection) first.</p>
      </Modal>
    )
  }

  const next = () => {
    setError('')
    if (step === 1 && !name.trim()) return setError('Give the action a name.')
    if (step === 2 && encrypt && (pass.length < 8 || pass !== pass2)) return setError('The passphrase must have at least 8 characters and both entries must match.')
    setStep(step + 1)
  }
  const pick = async (type: 'file' | 'dir') => {
    const p = type === 'dir' ? await api.pickFolder('Choose a folder to sync') : await api.pickFile('Choose a file to sync')
    if (p) setItems((x) => [...x, { remoteName: basename(p), type, localPath: p, excludes: '' }])
  }
  const create = async () => {
    setError('')
    setBusy(true)
    try {
      const act = await api.createAction({
        profileId,
        action: { name: name.trim(), direction, propagateDeletes: true, triggers: { onChange, debounceSeconds: 0, schedule, remotePollMinutes: 0 } },
        passphrase: encrypt ? pass : undefined,
      })
      const failed: string[] = []
      for (const it of items) {
        try {
          await api.addItem(act.id, { remoteName: it.remoteName, type: it.type, excludes: splitLines(it.excludes) }, it.localPath)
        } catch (e) {
          failed.push(`${it.remoteName}: ${describeError(e).message}`)
        }
      }
      if (failed.length) toast.show(`The action was created, but some items were not added: ${failed.join('; ')}`, { tone: 'error' })
      onClose()
      navigate({ name: 'action', id: act.id, tab: failed.length ? 'items' : 'overview' })
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={`New action — ${steps[step]}`} onClose={onClose} footer={
      <>
        {step > 0 && <Button onClick={() => setStep(step - 1)}>Back</Button>}
        {step < steps.length - 1 ? <Button variant="primary" onClick={next}>Next</Button> : <Button variant="primary" disabled={busy} onClick={create}>Create</Button>}
      </>
    }>
      <div className="space-y-3">
        {step === 0 && (
          <Field label="Profile">
            <Select aria-label="Profile" value={profileId} onChange={(e) => setProfileId(e.target.value)}>
              {state.profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
          </Field>
        )}
        {step === 1 && (
          <>
            <Field label="Name"><Input aria-label="Name" value={name} onChange={(e) => setName(e.target.value)} /></Field>
            <Field label="Direction">
              <Select aria-label="Direction" value={direction} onChange={(e) => setDirection(e.target.value as Direction)}>
                {(Object.keys(directionLabels) as Direction[]).map((d) => <option key={d} value={d}>{directionLabels[d]}</option>)}
              </Select>
            </Field>
            <Checkbox label="Sync when local files change" checked={onChange} onChange={setOnChange} />
            <ScheduleEditor value={schedule} onChange={setSchedule} />
          </>
        )}
        {step === 2 && (
          <>
            <Checkbox label="Encrypt files in the bucket" checked={encrypt} onChange={setEncrypt} />
            {encrypt && (
              <>
                <p className="text-sm text-amber-700">Encryption cannot be changed later, and a lost passphrase cannot be recovered. Every machine that joins needs it.</p>
                <Field label="Passphrase"><Input aria-label="Passphrase" type="password" value={pass} onChange={(e) => setPass(e.target.value)} /></Field>
                <Field label="Repeat passphrase"><Input aria-label="Repeat passphrase" type="password" value={pass2} onChange={(e) => setPass2(e.target.value)} /></Field>
              </>
            )}
          </>
        )}
        {step === 3 && (
          <>
            <p className="text-sm text-zinc-500">Optional: add the files or folders to sync now. You can add more later.</p>
            {items.map((it, i) => (
              <div key={i} className="rounded border border-zinc-200 p-2 dark:border-zinc-700">
                <div className="truncate text-xs text-zinc-500">{it.localPath}</div>
                <Field label="Name in the bucket">
                  <Input aria-label="Name in the bucket" value={it.remoteName} onChange={(e) => setItems((x) => x.map((y, j) => (j === i ? { ...y, remoteName: e.target.value } : y)))} />
                </Field>
                <Button variant="ghost" onClick={() => setItems((x) => x.filter((_, j) => j !== i))}>Remove</Button>
              </div>
            ))}
            <div className="space-x-2">
              <Button onClick={() => pick('dir')}>Add folder</Button>
              <Button onClick={() => pick('file')}>Add file</Button>
            </div>
          </>
        )}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      </div>
    </Modal>
  )
}
```
`src/screens/JoinWizard.tsx`:
```tsx
import { useState } from 'react'
import { useToast } from '../components/toast'
import { Button, Field, Input, Modal, Select } from '../components/ui'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import type { Action } from '../lib/types'

export function JoinWizard({ onClose }: { onClose: () => void }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const toast = useToast()
  const [profileId, setProfileId] = useState(state.profiles[0]?.id ?? '')
  const [found, setFound] = useState<Action[] | null>(null)
  const [chosen, setChosen] = useState<Action | null>(null)
  const [pass, setPass] = useState('')
  const [joined, setJoined] = useState<Action | null>(null)
  const [paths, setPaths] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const find = async () => {
    setError('')
    try {
      setFound((await api.discover(profileId)).filter((a) => !state.actions[a.id]))
    } catch (e) {
      setError(describeError(e).message)
    }
  }
  const join = async () => {
    if (!chosen) return
    setError('')
    setBusy(true)
    try {
      setJoined(await api.joinAction({ profileId, actionId: chosen.id, passphrase: chosen.encrypt ? pass : undefined }))
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(false)
    }
  }
  const pick = async (itemId: string, type: 'file' | 'dir') => {
    const p = type === 'dir' ? await api.pickFolder('Choose the local folder') : await api.pickFile('Choose the local file')
    if (p) setPaths((x) => ({ ...x, [itemId]: p }))
  }
  const finish = async () => {
    if (!joined) return
    setBusy(true)
    const failed: string[] = []
    for (const [itemId, p] of Object.entries(paths)) {
      try {
        await api.mapItem(joined.id, itemId, p)
      } catch (e) {
        failed.push(describeError(e).message)
      }
    }
    setBusy(false)
    if (failed.length) toast.show(`Some items were not mapped: ${failed.join('; ')}`, { tone: 'error' })
    onClose()
    navigate({ name: 'action', id: joined.id, tab: 'items' })
  }

  if (joined) {
    return (
      <Modal title={`Map ${joined.name} on this machine`} onClose={onClose} footer={<Button variant="primary" disabled={busy} onClick={finish}>Finish</Button>}>
        <p className="mb-2 text-sm text-zinc-500">Choose where each item lives on this machine. Items you skip are not synced here.</p>
        {(joined.items ?? []).map((it) => (
          <div key={it.id} className="mb-2 rounded border border-zinc-200 p-2 text-sm dark:border-zinc-700">
            <div className="font-medium">{it.remoteName} <span className="text-xs text-zinc-500">({it.type})</span></div>
            {Object.entries(it.localPaths ?? {}).map(([m, p]) => <div key={m} className="truncate text-xs text-zinc-500">{joined.machines?.[m] ?? m}: {p}</div>)}
            <div className="mt-1 flex items-center gap-2">
              <Button onClick={() => pick(it.id, it.type)}>Choose…</Button>
              <span className="truncate text-xs">{paths[it.id] ?? 'not mapped'}</span>
            </div>
          </div>
        ))}
      </Modal>
    )
  }

  return (
    <Modal title="Join an existing action" onClose={onClose} footer={
      <>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!chosen || busy || (chosen.encrypt && !pass)} onClick={join}>Join</Button>
      </>
    }>
      <div className="space-y-3">
        <div className="flex items-end gap-2">
          <Field label="Profile">
            <Select aria-label="Profile" value={profileId} onChange={(e) => { setProfileId(e.target.value); setFound(null); setChosen(null) }}>
              {state.profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
          </Field>
          <Button onClick={find}>Find actions</Button>
        </div>
        {found && found.length === 0 && <p className="text-sm text-zinc-500">No actions to join in this bucket.</p>}
        {found?.map((a) => (
          <label key={a.id} className="flex items-start gap-2 text-sm">
            <input type="radio" name="join" checked={chosen?.id === a.id} onChange={() => setChosen(a)} />
            <span>
              <span className="font-medium">{a.name}</span>
              <span className="block text-xs text-zinc-500">
                {(a.items ?? []).length} item(s) · machines: {Object.values(a.machines ?? {}).join(', ') || '—'}{a.encrypt ? ' · encrypted' : ''}
              </span>
            </span>
          </label>
        ))}
        {chosen?.encrypt && <Field label="Passphrase"><Input aria-label="Passphrase" type="password" value={pass} onChange={(e) => setPass(e.target.value)} /></Field>}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      </div>
    </Modal>
  )
}
```

- [ ] **Step 4: Run** `npm run typecheck && npx vitest run` → PASS. Commit:
```bash
git add frontend
git commit -m "feat(gui): actions list, new action and join wizards, schedule editor" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Action detail — Overview and Rules tabs

**Files:**
- Create: `frontend/src/lib/rules.ts`, `frontend/src/lib/rules.test.ts`, `frontend/src/screens/ActionDetail.tsx`, `frontend/src/screens/tabs/Overview.tsx`, `frontend/src/screens/tabs/Rules.tsx`, `frontend/src/screens/tabs/Rules.test.tsx`
- Modify: `frontend/src/screens/index.tsx` (add `case 'action': return <ActionDetail id={route.id} tab={route.tab} />`)

**Interfaces:**
- Consumes: `useActionCommands`, `ScheduleEditor`, `api.updateRules/setMachineDirection/action/leaveAction`, store, router.
- Produces: `rulesOf(a: Action): Rules`, `reapplyEdits(original: Rules, edited: Rules, fresh: Rules): Rules` (fields the user changed between `original` and `edited` are copied onto `fresh`, compared per top-level field by JSON value), `ActionDetail({ id, tab })` rendering tabs `overview | rules | items | snapshots | history` (Items/Snapshots/History are filled in Task 12; until then those tabs render `EmptyState`).

- [ ] **Step 1: Failing tests.** `src/lib/rules.test.ts`:
```ts
import { reapplyEdits, rulesOf } from './rules'
import type { Action, Rules } from './types'

const base: Rules = { name: 'A', direction: 'two-way', triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, excludes: [], snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50 }

test('rulesOf copies the editable fields and normalizes null lists', () => {
  const a = { ...base, id: 'x', excludes: null, encrypt: false, items: null, machines: null, machineDirections: null, updatedAt: 't' } as unknown as Action
  expect(rulesOf(a)).toEqual(base)
})

test('reapplyEdits keeps the other machine\'s changes and re-applies mine', () => {
  const mine = { ...base, name: 'Mine', excludes: ['*.log'] }
  const theirs = { ...base, snapshotRetention: 5, name: 'Theirs' }
  expect(reapplyEdits(base, mine, theirs)).toEqual({ ...theirs, name: 'Mine', excludes: ['*.log'] })
})
```
`src/screens/tabs/Rules.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('../../lib/bridge', () => import('../../test/mockBridge'))
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../../test/mockBridge'
import App from '../../App'
import type { ActionStatus } from '../../lib/types'

const V1 = '2026-10-04T01:02:03.000000004Z'
const st = (over: Partial<ActionStatus['action']> = {}): ActionStatus => ({
  action: { id: 'a1', name: 'Editor', direction: 'two-way', machineDirections: null, triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, encrypt: false, excludes: null, snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50, items: [], machines: { m1: 'mac' }, updatedAt: V1, ...over },
  profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0,
})

async function openRules() {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [st()] })
  bindings.Profiles.mockResolvedValue([])
  bindings.PreviewSchedule.mockResolvedValue([])
  render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'Rules' }))
}

test('save sends the loaded version verbatim', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Editor 2' } })
  bindings.UpdateRules.mockResolvedValue(st({ name: 'Editor 2', updatedAt: '2026-10-04T02:00:00Z' }).action)
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenCalled())
  const [id, req] = bindings.UpdateRules.mock.calls[0]
  expect(id).toBe('a1')
  expect(req.expectedUpdatedAt).toBe(V1)
  expect(req.rules.name).toBe('Editor 2')
  expect(bindings.SetMachineDirection).not.toHaveBeenCalled()
})

test('conflict-edit: reload and re-apply keeps my edit on top of the fresh rules', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Mine' } })
  bindings.UpdateRules.mockRejectedValueOnce('{"code":"conflict-edit","message":"changed"}')
  bindings.Action.mockResolvedValue(st({ snapshotRetention: 7, updatedAt: '2026-10-04T03:00:00Z' }))
  fireEvent.click(screen.getByText('Save'))
  const dlg = await screen.findByRole('dialog', { name: 'Changed elsewhere' })
  fireEvent.click(within(dlg).getByText('Reload and re-apply my edits'))
  await waitFor(() => expect(screen.getByLabelText('Manual snapshots to keep')).toHaveValue(7))
  expect(screen.getByLabelText('Name')).toHaveValue('Mine')
  bindings.UpdateRules.mockResolvedValueOnce(st({ name: 'Mine', updatedAt: '2026-10-04T04:00:00Z' }).action)
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.UpdateRules).toHaveBeenLastCalledWith('a1', expect.objectContaining({ expectedUpdatedAt: '2026-10-04T03:00:00Z' })))
})

test('machine direction is saved separately, after the rules', async () => {
  await openRules()
  fireEvent.change(screen.getByLabelText('This machine'), { target: { value: 'cloud-to-local' } })
  bindings.UpdateRules.mockResolvedValue(st().action)
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.SetMachineDirection).toHaveBeenCalledWith('a1', 'cloud-to-local'))
})
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** `src/lib/rules.ts`:
```ts
import type { Action, Rules } from './types'

export function rulesOf(a: Action): Rules {
  return {
    name: a.name,
    direction: a.direction,
    triggers: a.triggers,
    propagateDeletes: a.propagateDeletes,
    receiverRestoresCloud: a.receiverRestoresCloud,
    excludes: a.excludes ?? [],
    snapshotRetention: a.snapshotRetention,
    deleteGuardMinFiles: a.deleteGuardMinFiles,
    deleteGuardPercent: a.deleteGuardPercent,
  }
}

// reapplyEdits copies every field the user changed (original → edited) onto
// the fresh rules another machine saved meanwhile.
export function reapplyEdits(original: Rules, edited: Rules, fresh: Rules): Rules {
  const out: Rules = { ...fresh }
  for (const k of Object.keys(edited) as (keyof Rules)[]) {
    if (JSON.stringify(edited[k]) !== JSON.stringify(original[k])) {
      ;(out as unknown as Record<string, unknown>)[k] = edited[k]
    }
  }
  return out
}
```
`src/screens/ActionDetail.tsx`:
```tsx
import { Badge, Button, EmptyState } from '../components/ui'
import { stateLabels, stateTone } from '../lib/format'
import { useRouter, type Tab } from '../lib/router'
import { useStore } from '../lib/store'
import { Overview } from './tabs/Overview'
import { RulesTab } from './tabs/Rules'

const tabs: { id: Tab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'rules', label: 'Rules' },
  { id: 'items', label: 'Items' },
  { id: 'snapshots', label: 'Snapshots' },
  { id: 'history', label: 'History' },
]

export function ActionDetail({ id, tab }: { id: string; tab: Tab }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const st = state.actions[id]
  if (!st) {
    return (
      <EmptyState title="This action no longer exists on this machine.">
        <Button onClick={() => navigate({ name: 'actions' })}>Back to actions</Button>
      </EmptyState>
    )
  }
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <button type="button" className="text-sm text-zinc-500 hover:underline" onClick={() => navigate({ name: 'actions' })}>Actions /</button>
        <h1 className="text-xl font-semibold">{st.action.name}</h1>
        <Badge tone={stateTone(st.state)}>{stateLabels[st.state]}</Badge>
      </div>
      <div className="flex gap-1 border-b border-zinc-200 dark:border-zinc-800">
        {tabs.map((t) => (
          <button key={t.id} type="button" onClick={() => navigate({ name: 'action', id, tab: t.id })}
            className={`px-3 py-2 text-sm ${t.id === tab ? 'border-b-2 border-blue-600 font-medium' : 'text-zinc-500'}`}>{t.label}</button>
        ))}
      </div>
      {tab === 'overview' && <Overview st={st} />}
      {tab === 'rules' && <RulesTab st={st} />}
      {tab === 'items' && <EmptyState title="Items are added in Task 12." />}
      {tab === 'snapshots' && <EmptyState title="Snapshots are added in Task 12." />}
      {tab === 'history' && <EmptyState title="History is added in Task 12." />}
    </div>
  )
}
```
(Task 12 replaces the three `EmptyState` lines with the real tabs.)

`src/screens/tabs/Overview.tsx`:
```tsx
import { useState } from 'react'
import { useToast } from '../../components/toast'
import { Button, ConfirmDialog, Spinner } from '../../components/ui'
import { api, toAgentError } from '../../lib/api'
import { describeError } from '../../lib/errors'
import { directionLabels, formatTime, isZeroTime } from '../../lib/format'
import { useRouter } from '../../lib/router'
import { useStore } from '../../lib/store'
import type { ActionStatus } from '../../lib/types'
import { useActionCommands } from '../../lib/useActionCommands'

export function Overview({ st }: { st: ActionStatus }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const cmd = useActionCommands()
  const toast = useToast()
  const [leaving, setLeaving] = useState<'ask' | 'force' | null>(null)
  const [forceReason, setForceReason] = useState('')
  const id = st.action.id
  const machineId = state.status?.machineId ?? ''
  const dir = st.action.machineDirections?.[machineId] ?? st.action.direction

  const leave = async (force: boolean) => {
    try {
      await api.leaveAction(id, force)
      setLeaving(null)
      navigate({ name: 'actions' })
    } catch (e) {
      const code = toAgentError(e).code
      if (!force && !['busy', 'locked', 'unreachable', 'not-found'].includes(code)) {
        setForceReason(describeError(e).message)
        setLeaving('force')
      } else {
        setLeaving(null)
        toast.error(e)
      }
    }
  }

  return (
    <div className="space-y-4">
      {st.state === 'paused-conflict' && (
        <div className="flex items-center justify-between rounded-md bg-amber-100 p-3 text-sm text-amber-900 dark:bg-amber-900/40 dark:text-amber-100">
          <span>{st.conflicts} file(s) changed on both sides. The action is paused until you choose.</span>
          <Button onClick={() => navigate({ name: 'conflicts', id })}>Resolve conflicts</Button>
        </div>
      )}
      {st.state === 'paused-deletes' && (
        <div className="flex items-center justify-between rounded-md bg-amber-100 p-3 text-sm text-amber-900 dark:bg-amber-900/40 dark:text-amber-100">
          <span>{st.pendingDeletes} deletion(s) are held back by the delete guard.</span>
          <Button onClick={() => navigate({ name: 'deletes', id })}>Review deletions</Button>
        </div>
      )}
      {st.state === 'error' && (
        <div className="flex items-center justify-between rounded-md bg-red-100 p-3 text-sm text-red-900 dark:bg-red-900/40 dark:text-red-100">
          <span>{st.lastError} ({st.failures} failure(s){isZeroTime(st.nextRun) ? '' : `, next retry ${formatTime(st.nextRun)}`})</span>
          <Button onClick={() => cmd.resume(id)}>Retry now</Button>
        </div>
      )}
      <dl className="grid grid-cols-[12rem_1fr] gap-y-2 text-sm">
        <dt className="text-zinc-500">Direction on this machine</dt><dd>{directionLabels[dir]}</dd>
        <dt className="text-zinc-500">Last sync</dt><dd>{formatTime(st.lastRun)} {st.lastOutcome && `(${st.lastOutcome})`}</dd>
        <dt className="text-zinc-500">Summary</dt><dd>{st.lastSummary || '—'}</dd>
        <dt className="text-zinc-500">Next run</dt><dd>{isZeroTime(st.nextRun) ? '—' : formatTime(st.nextRun)}</dd>
        <dt className="text-zinc-500">Machines</dt><dd>{Object.values(st.action.machines ?? {}).join(', ')}</dd>
        <dt className="text-zinc-500">Encryption</dt><dd>{st.action.encrypt ? 'On' : 'Off'}</dd>
      </dl>
      <div className="flex flex-wrap items-center gap-2">
        {st.running ? <><Spinner label="Running" /><Button onClick={() => cmd.cancel(id)}>Cancel</Button></> : <Button variant="primary" disabled={st.state === 'paused'} onClick={() => cmd.sync(id)}>Sync now</Button>}
        <Button disabled={st.running} onClick={() => cmd.snapshot(id)}>Snapshot now</Button>
        {st.state === 'paused' ? <Button onClick={() => cmd.resume(id)}>Resume</Button> : <Button onClick={() => cmd.pause(id)}>Pause</Button>}
        <Button variant="danger" onClick={() => setLeaving('ask')}>Leave action</Button>
      </div>
      {leaving === 'ask' && (
        <ConfirmDialog title={`Leave ${st.action.name}?`} danger confirmLabel="Leave"
          message="This machine stops syncing the action. Local files and the cloud copy are kept; other machines keep syncing."
          onCancel={() => setLeaving(null)} onConfirm={() => leave(false)} />
      )}
      {leaving === 'force' && (
        <ConfirmDialog title="Leave anyway?" danger confirmLabel="Leave on this machine only"
          message={<>The bucket could not be updated: {forceReason}. Leave on this machine only? Other machines will still list this machine until the bucket is reachable.</>}
          onCancel={() => setLeaving(null)} onConfirm={() => leave(true)} />
      )}
    </div>
  )
}
```
`src/screens/tabs/Rules.tsx`:
```tsx
import { useEffect, useRef, useState } from 'react'
import { ScheduleEditor } from '../../components/ScheduleEditor'
import { useToast } from '../../components/toast'
import { Button, Checkbox, Field, Input, Modal, Select } from '../../components/ui'
import { api, toAgentError } from '../../lib/api'
import { describeError } from '../../lib/errors'
import { directionLabels } from '../../lib/format'
import { useDirtyGuard } from '../../lib/router'
import { reapplyEdits, rulesOf } from '../../lib/rules'
import { useStore } from '../../lib/store'
import type { ActionStatus, Direction, Rules } from '../../lib/types'

export function RulesTab({ st }: { st: ActionStatus }) {
  const { state } = useStore()
  const toast = useToast()
  const machineId = state.status?.machineId ?? ''
  const id = st.action.id
  const loadedMachineDir = st.action.machineDirections?.[machineId] ?? ''
  const [base, setBase] = useState<{ rules: Rules; version: string; machineDir: Direction | '' }>(
    { rules: rulesOf(st.action), version: st.action.updatedAt, machineDir: loadedMachineDir })
  const [form, setForm] = useState<Rules>(base.rules)
  const [machineDir, setMachineDir] = useState<Direction | ''>(loadedMachineDir)
  const [excludes, setExcludes] = useState((base.rules.excludes ?? []).join('\n'))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState(false)
  const edited: Rules = { ...form, excludes: excludes.split('\n').map((x) => x.trim()).filter(Boolean) }
  const dirty = JSON.stringify(edited) !== JSON.stringify(base.rules) || machineDir !== base.machineDir
  useDirtyGuard(dirty)

  const load = (rules: Rules, version: string, md: Direction | '') => {
    setBase({ rules, version, machineDir: md })
    setForm(rules)
    setExcludes(rules.excludes.join('\n'))
    setMachineDir(md)
  }
  // Follow changes from events while the user has not edited anything.
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  useEffect(() => {
    if (!dirtyRef.current && st.action.updatedAt !== base.version) load(rulesOf(st.action), st.action.updatedAt, loadedMachineDir)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [st.action.updatedAt])

  const set = <K extends keyof Rules>(k: K, v: Rules[K]) => setForm((f) => ({ ...f, [k]: v }))
  const setTrig = <K extends keyof Rules['triggers']>(k: K, v: Rules['triggers'][K]) => setForm((f) => ({ ...f, triggers: { ...f.triggers, [k]: v } }))

  const save = async () => {
    setError('')
    setBusy(true)
    try {
      const saved = await api.updateRules(id, edited, base.version)
      if (machineDir !== base.machineDir) await api.setMachineDirection(id, machineDir)
      load(rulesOf(saved), saved.updatedAt, machineDir)
      toast.show('Rules saved.')
    } catch (e) {
      if (toAgentError(e).code === 'conflict-edit') setConflict(true)
      else {
        const d = describeError(e)
        setError(d.details ? `${d.message} (${d.details})` : d.message)
      }
    } finally {
      setBusy(false)
    }
  }

  const reload = async (reapply: boolean) => {
    setConflict(false)
    try {
      const fresh = await api.action(id)
      const freshRules = rulesOf(fresh.action)
      const freshMd = fresh.action.machineDirections?.[machineId] ?? ''
      if (!reapply) return load(freshRules, fresh.action.updatedAt, freshMd)
      const merged = reapplyEdits(base.rules, edited, freshRules)
      setBase({ rules: freshRules, version: fresh.action.updatedAt, machineDir: freshMd })
      setForm(merged)
      setExcludes(merged.excludes.join('\n'))
    } catch (e) {
      toast.error(e)
    }
  }

  const num = (v: string) => (v === '' ? 0 : Number(v))
  return (
    <div className="max-w-2xl space-y-4">
      <Field label="Name"><Input aria-label="Name" value={form.name} onChange={(e) => set('name', e.target.value)} /></Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Default direction" hint="For every machine without its own setting.">
          <Select aria-label="Default direction" value={form.direction} onChange={(e) => set('direction', e.target.value as Direction)}>
            {(Object.keys(directionLabels) as Direction[]).map((d) => <option key={d} value={d}>{directionLabels[d]}</option>)}
          </Select>
        </Field>
        <Field label="This machine">
          <Select aria-label="This machine" value={machineDir} onChange={(e) => setMachineDir(e.target.value as Direction | '')}>
            <option value="">Use the default</option>
            {(Object.keys(directionLabels) as Direction[]).map((d) => <option key={d} value={d}>{directionLabels[d]}</option>)}
          </Select>
        </Field>
      </div>
      <fieldset className="space-y-2 rounded-md border border-zinc-200 p-3 dark:border-zinc-700">
        <legend className="px-1 text-sm font-medium">Triggers</legend>
        <Checkbox label="Sync when local files change" checked={form.triggers.onChange} onChange={(v) => setTrig('onChange', v)} />
        {form.triggers.onChange && (
          <Field label="Wait for changes to settle (seconds)">
            <Input aria-label="Debounce seconds" type="number" min={1} value={form.triggers.debounceSeconds} onChange={(e) => setTrig('debounceSeconds', num(e.target.value))} />
          </Field>
        )}
        <ScheduleEditor value={form.triggers.schedule} onChange={(s) => setTrig('schedule', s)} />
        {form.triggers.onChange && form.triggers.schedule.kind === 'none' && (
          <Field label="Check the cloud for changes every (minutes)">
            <Input aria-label="Cloud poll minutes" type="number" min={1} value={form.triggers.remotePollMinutes} onChange={(e) => setTrig('remotePollMinutes', num(e.target.value))} />
          </Field>
        )}
      </fieldset>
      <Checkbox label="Copy deletions to the other side (after a safety snapshot)" checked={form.propagateDeletes} onChange={(v) => set('propagateDeletes', v)} />
      {!form.propagateDeletes && (
        <Checkbox label="Cloud-to-local machines upload files the cloud deleted" checked={form.receiverRestoresCloud} onChange={(v) => set('receiverRestoresCloud', v)} />
      )}
      <Field label="Excludes" hint="One glob per line, e.g. *.log or cache/**">
        <textarea aria-label="Excludes" rows={4} value={excludes} onChange={(e) => setExcludes(e.target.value)}
          className="w-full rounded-md border border-zinc-300 bg-white px-2 py-1.5 font-mono text-sm dark:border-zinc-600 dark:bg-zinc-800" />
      </Field>
      <div className="grid grid-cols-3 gap-3">
        <Field label="Manual snapshots to keep">
          <Input aria-label="Manual snapshots to keep" type="number" min={1} value={form.snapshotRetention} onChange={(e) => set('snapshotRetention', num(e.target.value))} />
        </Field>
        <Field label="Delete guard: files">
          <Input aria-label="Delete guard files" type="number" min={1} value={form.deleteGuardMinFiles} onChange={(e) => set('deleteGuardMinFiles', num(e.target.value))} />
        </Field>
        <Field label="Delete guard: percent">
          <Input aria-label="Delete guard percent" type="number" min={1} max={100} value={form.deleteGuardPercent} onChange={(e) => set('deleteGuardPercent', num(e.target.value))} />
        </Field>
      </div>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <div className="flex gap-2">
        <Button variant="primary" disabled={!dirty || busy} onClick={save}>Save</Button>
        <Button disabled={!dirty || busy} onClick={() => load(base.rules, base.version, base.machineDir)}>Discard changes</Button>
      </div>
      {conflict && (
        <Modal title="Changed elsewhere" onClose={() => setConflict(false)} footer={
          <>
            <Button onClick={() => reload(false)}>Reload (discard my edits)</Button>
            <Button variant="primary" onClick={() => reload(true)}>Reload and re-apply my edits</Button>
          </>
        }>
          <p className="text-sm">These rules were changed (maybe on another machine) after you opened them. Nothing was saved.</p>
        </Modal>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run** `npm run typecheck && npx vitest run` → PASS. Commit:
```bash
git add frontend
git commit -m "feat(gui): action detail with overview and rules editor" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Items, Snapshots and History tabs

**Files:**
- Create: `frontend/src/screens/tabs/Items.tsx`, `frontend/src/screens/tabs/Snapshots.tsx`, `frontend/src/screens/tabs/History.tsx`, `frontend/src/screens/tabs/Items.test.tsx`, `frontend/src/screens/tabs/Snapshots.test.tsx`
- Modify: `frontend/src/screens/ActionDetail.tsx` (replace the three `EmptyState` tab lines with `<ItemsTab st={st} />`, `<SnapshotsTab st={st} />`, `<HistoryTab st={st} />`)

**Interfaces:**
- Consumes: `api.addItem/mapItem/unmapItem/removeItem/pickFile/pickFolder/snapshots/snapshot/restore/history`, `basename`, `splitLines` (from `NewActionWizard.tsx`), `useActionCommands`.
- Produces: `ItemsTab`, `SnapshotsTab`, `HistoryTab` (`{ st: ActionStatus }` props), `reasonLabels` (in `Snapshots.tsx`).

- [ ] **Step 1: Failing tests.** `src/screens/tabs/Items.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('../../lib/bridge', () => import('../../test/mockBridge'))
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../../test/mockBridge'
import App from '../../App'
import type { ActionStatus } from '../../lib/types'

const st: ActionStatus = {
  action: { id: 'a1', name: 'Editor', direction: 'two-way', machineDirections: null, triggers: { onChange: true, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 }, propagateDeletes: true, receiverRestoresCloud: false, encrypt: false, excludes: null, snapshotRetention: 20, deleteGuardMinFiles: 5, deleteGuardPercent: 50,
    items: [
      { id: 'i1', remoteName: 'snippets', type: 'dir', excludes: null, localPaths: { m1: '/Users/me/snippets', m2: 'C:\\Users\\me\\snippets' } },
      { id: 'i2', remoteName: 'settings.json', type: 'file', excludes: null, localPaths: { m2: 'C:\\x\\settings.json' } },
    ],
    machines: { m1: 'mac', m2: 'pc' }, updatedAt: 't' },
  profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0,
}

async function openItems() {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [st] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'Items' }))
}

test('shows this machine\'s path, other machines read-only, and maps an unmapped item with the right picker', async () => {
  await openItems()
  const snippets = screen.getByText('snippets').closest('tr')!
  expect(within(snippets).getByText('/Users/me/snippets')).toBeInTheDocument()
  expect(within(snippets).getByText(/pc: C:\\Users\\me\\snippets/)).toBeInTheDocument()
  const settings = screen.getByText('settings.json').closest('tr')!
  expect(within(settings).getByText('Not mapped on this machine')).toBeInTheDocument()
  bindings.PickFile.mockResolvedValue('/Users/me/settings.json')
  fireEvent.click(within(settings).getByText('Map…'))
  await waitFor(() => expect(bindings.MapItem).toHaveBeenCalledWith('a1', 'i2', '/Users/me/settings.json'))
})

test('a protected-path refusal is shown inside the add dialog', async () => {
  await openItems()
  fireEvent.click(screen.getByText('Add item'))
  const dlg = screen.getByRole('dialog')
  bindings.PickFolder.mockResolvedValue('/Users/me/Library/Application Support')
  fireEvent.click(within(dlg).getByText('Choose…'))
  expect(await within(dlg).findByDisplayValue('Application Support')).toBeInTheDocument()
  bindings.AddItem.mockRejectedValue('{"code":"protected-path","message":"this folder contains s3-anysync\'s own data folder"}')
  fireEvent.click(within(dlg).getByText('Add'))
  expect(await within(dlg).findByText(/own data folder/)).toBeInTheDocument()
})
```
`src/screens/tabs/Snapshots.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('../../lib/bridge', () => import('../../test/mockBridge'))
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../../test/mockBridge'
import App from '../../App'

test('restore selected files to the chosen target', async () => {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [
    { action: { id: 'a1', name: 'Editor', items: [], machines: { m1: 'mac' }, updatedAt: 't', triggers: { onChange: false, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 } }, profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0 },
  ] })
  bindings.Profiles.mockResolvedValue([])
  bindings.Snapshots.mockResolvedValue([
    { ts: '20261004T010203Z', reason: 'auto-pre-sync', machineId: 'm1', createdAt: '2026-10-04T01:02:03Z', files: [{ path: 'snippets/a.json', hash: 'h', size: 10, mtime: 0 }, { path: 'snippets/b.json', hash: 'h', size: 20, mtime: 0 }] },
  ])
  render(<App />)
  fireEvent.click(await screen.findByText('Editor'))
  fireEvent.click(screen.getByRole('button', { name: 'Snapshots' }))
  expect(await screen.findByText('Before a sync')).toBeInTheDocument()
  fireEvent.click(screen.getByText('Restore…'))
  const dlg = screen.getByRole('dialog')
  fireEvent.click(within(dlg).getByLabelText('Selected files only'))
  fireEvent.click(within(dlg).getByLabelText('snippets/b.json'))
  fireEvent.click(within(dlg).getByLabelText('This machine only'))
  bindings.Restore.mockResolvedValue({ restored: ['snippets/b.json'], skippedUnmapped: null })
  fireEvent.click(within(dlg).getByText('Restore'))
  await waitFor(() => expect(bindings.Restore).toHaveBeenCalledWith('a1', { ts: '20261004T010203Z', paths: ['snippets/b.json'], to: 'local' }))
  expect(await screen.findByText('Restored 1 file(s).')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** `src/screens/tabs/Items.tsx`:
```tsx
import { useState } from 'react'
import { useToast } from '../../components/toast'
import { Button, ConfirmDialog, EmptyState, Field, Input, Modal, Select, td, th } from '../../components/ui'
import { api } from '../../lib/api'
import { describeError } from '../../lib/errors'
import { useStore } from '../../lib/store'
import type { ActionStatus, Item } from '../../lib/types'
import { basename, splitLines } from '../NewActionWizard'

function AddItemDialog({ actionId, onClose }: { actionId: string; onClose: () => void }) {
  const [type, setType] = useState<'file' | 'dir'>('dir')
  const [localPath, setLocalPath] = useState('')
  const [remoteName, setRemoteName] = useState('')
  const [excludes, setExcludes] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const choose = async () => {
    const p = type === 'dir' ? await api.pickFolder('Choose a folder to sync') : await api.pickFile('Choose a file to sync')
    if (p) {
      setLocalPath(p)
      if (!remoteName) setRemoteName(basename(p))
    }
  }
  const add = async () => {
    setError('')
    setBusy(true)
    try {
      await api.addItem(actionId, { remoteName: remoteName.trim(), type, excludes: splitLines(excludes) }, localPath)
      onClose()
    } catch (e) {
      const d = describeError(e)
      setError(d.details ? `${d.message} (${d.details})` : d.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title="Add item" onClose={onClose} footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={busy || !localPath || !remoteName.trim()} onClick={add}>Add</Button></>}>
      <div className="space-y-3">
        <Field label="Type">
          <Select aria-label="Type" value={type} onChange={(e) => { setType(e.target.value as 'file' | 'dir'); setLocalPath('') }}>
            <option value="dir">Folder</option>
            <option value="file">File</option>
          </Select>
        </Field>
        <div className="flex items-center gap-2"><Button onClick={choose}>Choose…</Button><span className="truncate text-xs">{localPath || 'nothing chosen'}</span></div>
        <Field label="Name in the bucket" hint="The same name on every machine; each machine maps it to its own path.">
          <Input aria-label="Name in the bucket" value={remoteName} onChange={(e) => setRemoteName(e.target.value)} />
        </Field>
        {type === 'dir' && (
          <Field label="Excludes" hint="One glob per line.">
            <textarea aria-label="Excludes" rows={3} value={excludes} onChange={(e) => setExcludes(e.target.value)}
              className="w-full rounded-md border border-zinc-300 bg-white px-2 py-1.5 font-mono text-sm dark:border-zinc-600 dark:bg-zinc-800" />
          </Field>
        )}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      </div>
    </Modal>
  )
}

export function ItemsTab({ st }: { st: ActionStatus }) {
  const { state } = useStore()
  const toast = useToast()
  const machineId = state.status?.machineId ?? ''
  const id = st.action.id
  const [adding, setAdding] = useState(false)
  const [confirm, setConfirm] = useState<{ item: Item; op: 'unmap' | 'remove' } | null>(null)
  const items = st.action.items ?? []

  const map = async (it: Item) => {
    const p = it.type === 'dir' ? await api.pickFolder(`Where is "${it.remoteName}" on this machine?`) : await api.pickFile(`Where is "${it.remoteName}" on this machine?`)
    if (p) await api.mapItem(id, it.id, p).catch(toast.error)
  }
  const run = async () => {
    if (!confirm) return
    const { item, op } = confirm
    setConfirm(null)
    await (op === 'unmap' ? api.unmapItem(id, item.id) : api.removeItem(id, item.id)).catch(toast.error)
  }

  return (
    <div className="space-y-3">
      <Button variant="primary" onClick={() => setAdding(true)}>Add item</Button>
      {items.length === 0 ? <EmptyState title="No items yet" /> : (
        <table className="w-full">
          <thead><tr><th className={th}>Item</th><th className={th}>On this machine</th><th className={th}>Other machines</th><th className={th} /></tr></thead>
          <tbody>
            {items.map((it) => {
              const here = it.localPaths?.[machineId]
              const others = Object.entries(it.localPaths ?? {}).filter(([m]) => m !== machineId)
              return (
                <tr key={it.id} className="border-t border-zinc-200 dark:border-zinc-800">
                  <td className={td}><span className="font-medium">{it.remoteName}</span><div className="text-xs text-zinc-500">{it.type === 'dir' ? 'folder' : 'file'}{it.excludes?.length ? ` · excludes: ${it.excludes.join(', ')}` : ''}</div></td>
                  <td className={`${td} break-all`}>{here ?? <span className="text-zinc-500">Not mapped on this machine</span>}</td>
                  <td className={`${td} break-all text-xs text-zinc-500`}>{others.map(([m, p]) => <div key={m}>{st.action.machines?.[m] ?? m}: {p}</div>)}</td>
                  <td className={`${td} space-x-2 whitespace-nowrap text-right`}>
                    <Button onClick={() => map(it)}>{here ? 'Remap…' : 'Map…'}</Button>
                    {here && <Button onClick={() => setConfirm({ item: it, op: 'unmap' })}>Unmap</Button>}
                    <Button variant="danger" onClick={() => setConfirm({ item: it, op: 'remove' })}>Remove</Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {adding && <AddItemDialog actionId={id} onClose={() => setAdding(false)} />}
      {confirm && (
        <ConfirmDialog
          title={confirm.op === 'unmap' ? `Stop syncing ${confirm.item.remoteName} here?` : `Remove ${confirm.item.remoteName}?`}
          danger={confirm.op === 'remove'}
          confirmLabel={confirm.op === 'unmap' ? 'Unmap' : 'Remove'}
          message={confirm.op === 'unmap'
            ? 'This machine stops syncing the item. Local files and the cloud copy stay; other machines keep syncing.'
            : 'The item is removed from the action on every machine. Local files and the cloud copy are kept.'}
          onCancel={() => setConfirm(null)} onConfirm={run} />
      )}
    </div>
  )
}
```
`src/screens/tabs/Snapshots.tsx`:
```tsx
import { useCallback, useEffect, useState } from 'react'
import { useToast } from '../../components/toast'
import { Button, EmptyState, Input, Modal, Spinner, td, th } from '../../components/ui'
import { api, toAgentError } from '../../lib/api'
import { formatTime } from '../../lib/format'
import type { ActionStatus, RestoreTarget, SnapshotInfo } from '../../lib/types'
import { useActionCommands } from '../../lib/useActionCommands'

export const reasonLabels: Record<string, string> = {
  manual: 'Manual',
  'auto-pre-sync': 'Before a sync',
  'pre-resolve': 'Before resolving conflicts',
  'pre-restore': 'Before a restore',
}

const targets: { id: RestoreTarget; label: string; hint: string }[] = [
  { id: 'local', label: 'This machine only', hint: 'Overwrites the local files; the cloud keeps its version until the next sync.' },
  { id: 'cloud', label: 'The cloud only', hint: 'Other machines receive these versions on their next sync.' },
  { id: 'both', label: 'This machine and the cloud', hint: 'Everything goes back to the snapshot.' },
]

function RestoreDialog({ actionId, snap, onClose }: { actionId: string; snap: SnapshotInfo; onClose: () => void }) {
  const toast = useToast()
  const [selectedOnly, setSelectedOnly] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [filter, setFilter] = useState('')
  const [to, setTo] = useState<RestoreTarget>('both')
  const [busy, setBusy] = useState(false)
  const files = (snap.files ?? []).filter((f) => f.path.toLowerCase().includes(filter.toLowerCase()))
  const toggle = (p: string) => setSelected((s) => {
    const n = new Set(s)
    if (n.has(p)) n.delete(p)
    else n.add(p)
    return n
  })
  const restore = async () => {
    setBusy(true)
    try {
      const r = await api.restore(actionId, { ts: snap.ts, paths: selectedOnly ? [...selected].sort() : undefined, to })
      const skipped = r.skippedUnmapped?.length ? ` ${r.skippedUnmapped.length} file(s) belong to items not mapped here and were skipped locally.` : ''
      toast.show(`Restored ${r.restored?.length ?? 0} file(s).${skipped}`)
      onClose()
    } catch (e) {
      if (toAgentError(e).code === 'cancelled') toast.show('Restore cancelled.')
      else toast.error(e)
      setBusy(false)
    }
  }
  return (
    <Modal title={`Restore snapshot from ${formatTime(snap.createdAt)}`} wide onClose={onClose} footer={
      <><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={busy || (selectedOnly && selected.size === 0)} onClick={restore}>Restore</Button></>
    }>
      <div className="space-y-3 text-sm">
        <p>The current state is snapshotted first, so a restore can be undone.</p>
        <div className="space-y-1">
          <label className="flex items-center gap-2"><input type="radio" checked={!selectedOnly} onChange={() => setSelectedOnly(false)} />All {snap.files?.length ?? 0} files</label>
          <label className="flex items-center gap-2"><input type="radio" checked={selectedOnly} onChange={() => setSelectedOnly(true)} />Selected files only</label>
        </div>
        {selectedOnly && (
          <div className="space-y-1">
            <Input aria-label="Filter files" placeholder="Filter…" value={filter} onChange={(e) => setFilter(e.target.value)} />
            <div className="max-h-56 overflow-auto rounded border border-zinc-200 p-2 dark:border-zinc-700">
              {files.map((f) => (
                <label key={f.path} className="flex items-center gap-2"><input type="checkbox" checked={selected.has(f.path)} onChange={() => toggle(f.path)} />{f.path}</label>
              ))}
            </div>
          </div>
        )}
        <div className="space-y-1">
          {targets.map((t) => (
            <label key={t.id} className="flex items-start gap-2">
              <input type="radio" name="target" checked={to === t.id} onChange={() => setTo(t.id)} />
              <span>{t.label}<span className="block text-xs text-zinc-500">{t.hint}</span></span>
            </label>
          ))}
        </div>
      </div>
    </Modal>
  )
}

export function SnapshotsTab({ st }: { st: ActionStatus }) {
  const toast = useToast()
  const cmd = useActionCommands()
  const id = st.action.id
  const [list, setList] = useState<SnapshotInfo[] | null>(null)
  const [restoring, setRestoring] = useState<SnapshotInfo | null>(null)
  const load = useCallback(() => {
    api.snapshots(id).then((l) => setList([...l].sort((a, b) => b.createdAt.localeCompare(a.createdAt)))).catch(toast.error)
  }, [id, toast])
  useEffect(load, [load, st.lastRun])
  return (
    <div className="space-y-3">
      <Button variant="primary" disabled={st.running} onClick={() => cmd.snapshot(id).then(load)}>Snapshot now</Button>
      {list === null ? <Spinner /> : list.length === 0 ? <EmptyState title="No snapshots yet" /> : (
        <table className="w-full">
          <thead><tr><th className={th}>Taken</th><th className={th}>Reason</th><th className={th}>Machine</th><th className={th}>Files</th><th className={th} /></tr></thead>
          <tbody>
            {list.map((s) => (
              <tr key={s.ts} className="border-t border-zinc-200 dark:border-zinc-800">
                <td className={td}>{formatTime(s.createdAt)}</td>
                <td className={td}>{reasonLabels[s.reason] ?? s.reason}{s.deletes?.length ? <div className="text-xs text-zinc-500">before deleting {s.deletes.length} file(s)</div> : null}</td>
                <td className={td}>{st.action.machines?.[s.machineId] ?? s.machineId}</td>
                <td className={td}>{s.files?.length ?? 0}</td>
                <td className={`${td} text-right`}><Button onClick={() => setRestoring(s)}>Restore…</Button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {restoring && <RestoreDialog actionId={id} snap={restoring} onClose={() => { setRestoring(null); load() }} />}
    </div>
  )
}
```
`src/screens/tabs/History.tsx`:
```tsx
import { useEffect, useState } from 'react'
import { useToast } from '../../components/toast'
import { EmptyState, Spinner, td, th } from '../../components/ui'
import { api } from '../../lib/api'
import { formatTime } from '../../lib/format'
import type { ActionStatus, HistoryEntry } from '../../lib/types'

const kindLabels: Record<string, string> = {
  sync: 'Sync',
  resolve: 'Resolve',
  snapshot: 'Snapshot',
  restore: 'Restore',
  'approve-deletes': 'Approve deletions',
}

export function HistoryTab({ st }: { st: ActionStatus }) {
  const toast = useToast()
  const [rows, setRows] = useState<HistoryEntry[] | null>(null)
  useEffect(() => {
    api.history(st.action.id, 100).then(setRows).catch(toast.error)
  }, [st.action.id, st.lastRun, toast])
  if (rows === null) return <Spinner />
  if (rows.length === 0) return <EmptyState title="Nothing has run yet" />
  return (
    <table className="w-full">
      <thead><tr><th className={th}>Started</th><th className={th}>Kind</th><th className={th}>Outcome</th><th className={th}>Details</th></tr></thead>
      <tbody>
        {rows.map((h) => (
          <tr key={h.id} className="border-t border-zinc-200 dark:border-zinc-800">
            <td className={td}>{formatTime(h.startedAt)}</td>
            <td className={td}>{kindLabels[h.kind] ?? h.kind}</td>
            <td className={td}>{h.outcome}</td>
            <td className={`${td} break-all text-xs`}>{h.message}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
```

- [ ] **Step 4: Run** `npm run typecheck && npx vitest run` → PASS. Commit:
```bash
git add frontend
git commit -m "feat(gui): items, snapshots with restore, and history tabs" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Conflict resolver and delete review

**Files:**
- Create: `frontend/src/lib/conflicts.ts`, `frontend/src/lib/conflicts.test.ts`, `frontend/src/components/DiffView.tsx`, `frontend/src/screens/Conflicts.tsx`, `frontend/src/screens/DeleteReview.tsx`, `frontend/src/screens/Conflicts.test.tsx`
- Modify: `frontend/src/screens/index.tsx` (add `case 'conflicts': return <Conflicts id={route.id} />` and `case 'deletes': return <DeleteReview id={route.id} />`)

**Interfaces:**
- Consumes: `api.conflicts/conflictDetail/resolve/pendingDeletes/approveDeletes`, `diffLines` from `diff`.
- Produces: `deletionsFor(conflicts: Conflict[], choices: Record<string, Side>): { path: string; where: 'local' | 'cloud' }[]` (choosing a side whose entry is `null` deletes the file on the other side), `allChosen(conflicts, choices): boolean`, `DiffView({ left, right })`.

- [ ] **Step 1: Failing tests.** `src/lib/conflicts.test.ts`:
```ts
import { allChosen, deletionsFor } from './conflicts'
import type { Conflict } from './types'

const f = (path: string) => ({ path, hash: 'h', size: 1, mtime: 0 })
const cs: Conflict[] = [
  { path: 'a', local: f('a'), remote: f('a'), detectedAt: 't' },
  { path: 'b', local: null, remote: f('b'), detectedAt: 't' },
  { path: 'c', local: f('c'), remote: null, detectedAt: 't' },
]

test('deletionsFor lists files a choice deletes', () => {
  expect(deletionsFor(cs, { a: 'local', b: 'local', c: 'local' })).toEqual([{ path: 'b', where: 'cloud' }])
  expect(deletionsFor(cs, { a: 'cloud', b: 'cloud', c: 'cloud' })).toEqual([{ path: 'c', where: 'local' }])
})

test('allChosen needs a choice for every conflict', () => {
  expect(allChosen(cs, { a: 'local', b: 'cloud' })).toBe(false)
  expect(allChosen(cs, { a: 'local', b: 'cloud', c: 'cloud' })).toBe(true)
})
```
`src/screens/Conflicts.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import App from '../App'

const f = (path: string) => ({ path, hash: 'abcdef0123', size: 12, mtime: 1_700_000_000_000_000_000 })
const status = (state: string, extra = {}) => ({
  action: { id: 'a1', name: 'Editor', items: [], machines: { m1: 'mac' }, updatedAt: 't', triggers: { onChange: false, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 } },
  profileId: 'p1', state, running: false, failures: 0, conflicts: 0, pendingDeletes: 0, ...extra,
})

function boot(st: unknown) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [st] })
  bindings.Profiles.mockResolvedValue([])
  render(<App />)
}

test('apply needs every choice and warns about deletions before resolving', async () => {
  boot(status('paused-conflict', { conflicts: 2 }))
  bindings.Conflicts.mockResolvedValue([
    { path: 'a.json', local: f('a.json'), remote: f('a.json'), detectedAt: 't' },
    { path: 'gone.json', local: null, remote: f('gone.json'), detectedAt: 't' },
  ])
  bindings.ConflictDetail.mockResolvedValue({ conflict: {}, localText: 'x\n', remoteText: 'y\n', binary: false, tooLarge: false })
  fireEvent.click(await screen.findByText('2 conflicts'))
  const apply = await screen.findByText('Apply')
  expect(apply).toBeDisabled()
  fireEvent.click(screen.getByText('Keep all local'))
  expect(apply).toBeEnabled()
  fireEvent.click(apply)
  const dlg = await screen.findByRole('dialog', { name: 'Some files will be deleted' })
  expect(within(dlg).getByText(/gone.json/)).toBeInTheDocument()
  bindings.Resolve.mockResolvedValue({ outcome: 'ok', uploaded: 1, downloaded: 0, deletedRemote: 1, deletedLocal: 0, adopted: 0, conflicts: null, deferred: null, pendingDeletes: null, unsupported: null })
  fireEvent.click(within(dlg).getByText('Resolve'))
  await waitFor(() => expect(bindings.Resolve).toHaveBeenCalledWith('a1', { 'a.json': 'local', 'gone.json': 'local' }))
})

test('delete review approves exactly the shown list and reloads when it changed', async () => {
  boot(status('paused-deletes', { pendingDeletes: 2 }))
  bindings.PendingDeletes.mockResolvedValueOnce({ paths: ['b', 'a'], approved: false, detectedAt: 't' })
  fireEvent.click(await screen.findByText('2 deletions to review'))
  expect(await screen.findByText('a')).toBeInTheDocument()
  bindings.ApproveDeletes.mockResolvedValueOnce({ outcome: 'deletes-pending', uploaded: 0, downloaded: 0, deletedRemote: 0, deletedLocal: 0, adopted: 0, conflicts: null, deferred: null, pendingDeletes: ['a', 'b', 'c'], unsupported: null })
  bindings.PendingDeletes.mockResolvedValueOnce({ paths: ['a', 'b', 'c'], approved: false, detectedAt: 't' })
  fireEvent.click(screen.getByText('Approve 2 deletion(s)'))
  fireEvent.click(within(await screen.findByRole('dialog')).getByText('Approve'))
  await waitFor(() => expect(bindings.ApproveDeletes).toHaveBeenCalledWith('a1', ['a', 'b']))
  expect(await screen.findByText(/nothing was approved/)).toBeInTheDocument()
  expect(await screen.findByText('c')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** `src/lib/conflicts.ts`:
```ts
import type { Conflict, Side } from './types'

// deletionsFor lists the deletions a set of choices causes: keeping a side
// where the file was deleted deletes it on the other side. Resolution
// deletions are not held back by the delete guard, so the GUI confirms them.
export function deletionsFor(conflicts: Conflict[], choices: Record<string, Side>): { path: string; where: 'local' | 'cloud' }[] {
  const out: { path: string; where: 'local' | 'cloud' }[] = []
  for (const c of conflicts) {
    const side = choices[c.path]
    if (side === 'local' && !c.local) out.push({ path: c.path, where: 'cloud' })
    if (side === 'cloud' && !c.remote) out.push({ path: c.path, where: 'local' })
  }
  return out
}

export const allChosen = (conflicts: Conflict[], choices: Record<string, Side>) => conflicts.every((c) => !!choices[c.path])
```
`src/components/DiffView.tsx`:
```tsx
import { diffLines } from 'diff'

// DiffView shows local (left) and cloud (right) text side by side.
export function DiffView({ left, right }: { left: string; right: string }) {
  const rows: { l?: string; r?: string; kind: 'same' | 'left' | 'right' }[] = []
  for (const part of diffLines(left, right)) {
    const lines = part.value.replace(/\n$/, '').split('\n')
    for (const line of lines) {
      if (part.added) rows.push({ r: line, kind: 'right' })
      else if (part.removed) rows.push({ l: line, kind: 'left' })
      else rows.push({ l: line, r: line, kind: 'same' })
    }
  }
  const cell = 'whitespace-pre-wrap break-all px-2 font-mono text-xs'
  return (
    <div className="grid grid-cols-2 rounded border border-zinc-200 dark:border-zinc-700">
      <div className="border-b border-r border-zinc-200 px-2 py-1 text-xs font-semibold dark:border-zinc-700">This machine</div>
      <div className="border-b border-zinc-200 px-2 py-1 text-xs font-semibold dark:border-zinc-700">Cloud</div>
      {rows.map((row, i) => [
        <div key={`l${i}`} className={`${cell} border-r border-zinc-200 dark:border-zinc-700 ${row.kind === 'left' ? 'bg-red-50 dark:bg-red-900/30' : ''}`}>{row.l ?? ''}</div>,
        <div key={`r${i}`} className={`${cell} ${row.kind === 'right' ? 'bg-green-50 dark:bg-green-900/30' : ''}`}>{row.r ?? ''}</div>,
      ])}
    </div>
  )
}
```
`src/screens/Conflicts.tsx`:
```tsx
import { useCallback, useEffect, useState } from 'react'
import { DiffView } from '../components/DiffView'
import { useToast } from '../components/toast'
import { Button, ConfirmDialog, EmptyState, Spinner } from '../components/ui'
import { api } from '../lib/api'
import { allChosen, deletionsFor } from '../lib/conflicts'
import { formatSize, summarizeResult } from '../lib/format'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import type { Conflict, ConflictDetail, FileEntry, Side } from '../lib/types'

const describe = (e: FileEntry | null) =>
  e ? `${formatSize(e.size)} · ${new Date(e.mtime / 1e6).toLocaleString()} · ${e.hash.slice(0, 8)}` : 'deleted'

export function Conflicts({ id }: { id: string }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const toast = useToast()
  const [list, setList] = useState<Conflict[] | null>(null)
  const [choices, setChoices] = useState<Record<string, Side>>({})
  const [open, setOpen] = useState<string | null>(null)
  const [detail, setDetail] = useState<ConflictDetail | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const name = state.actions[id]?.action.name ?? id

  const load = useCallback(() => {
    setChoices({})
    api.conflicts(id).then(setList).catch(toast.error)
  }, [id, toast])
  useEffect(load, [load])
  useEffect(() => {
    setDetail(null)
    if (open) api.conflictDetail(id, open).then(setDetail).catch(toast.error)
  }, [id, open, toast])

  if (list === null) return <Spinner />
  if (list.length === 0) {
    return <EmptyState title="No conflicts"><Button onClick={() => navigate({ name: 'action', id, tab: 'overview' })}>Back to {name}</Button></EmptyState>
  }
  const deletions = deletionsFor(list, choices)
  const all = (side: Side) => setChoices(Object.fromEntries(list.map((c) => [c.path, side])))
  const resolve = async () => {
    setConfirm(false)
    setBusy(true)
    try {
      const r = await api.resolve(id, choices)
      if (r.outcome === 'conflict') {
        toast.show('New conflicts appeared while resolving; review them again.')
        load()
      } else {
        toast.show(`Resolved: ${summarizeResult(r)}`)
        navigate({ name: 'action', id, tab: 'overview' })
      }
    } catch (e) {
      toast.error(e)
      load() // the saved conflicts may have changed (stale choice)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Conflicts in {name}</h1>
        <div className="space-x-2">
          <Button onClick={() => all('local')}>Keep all local</Button>
          <Button onClick={() => all('cloud')}>Keep all cloud</Button>
          <Button variant="primary" disabled={!allChosen(list, choices) || busy} onClick={() => (deletions.length ? setConfirm(true) : resolve())}>Apply</Button>
        </div>
      </div>
      <p className="text-sm text-zinc-500">Each file changed on this machine and in the cloud. Choose the version to keep; the other one is saved in a snapshot first.</p>
      <ul className="divide-y divide-zinc-200 rounded border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
        {list.map((c) => (
          <li key={c.path} className="p-3">
            <div className="flex items-center justify-between gap-3">
              <button type="button" className="truncate text-left font-mono text-sm hover:underline" onClick={() => setOpen(open === c.path ? null : c.path)}>{c.path}</button>
              <div className="flex shrink-0 gap-3 text-sm">
                <label className="flex items-center gap-1"><input type="radio" name={c.path} checked={choices[c.path] === 'local'} onChange={() => setChoices((x) => ({ ...x, [c.path]: 'local' }))} />Keep local</label>
                <label className="flex items-center gap-1"><input type="radio" name={c.path} checked={choices[c.path] === 'cloud'} onChange={() => setChoices((x) => ({ ...x, [c.path]: 'cloud' }))} />Keep cloud</label>
              </div>
            </div>
            <div className="mt-1 grid grid-cols-2 text-xs text-zinc-500"><span>Local: {describe(c.local)}</span><span>Cloud: {describe(c.remote)}</span></div>
            {open === c.path && (
              <div className="mt-2">
                {!detail ? <Spinner /> : detail.binary || detail.tooLarge
                  ? <p className="text-sm text-zinc-500">{detail.binary ? 'Binary file: no text comparison.' : 'Too large to compare.'}</p>
                  : <DiffView left={detail.localText} right={detail.remoteText} />}
              </div>
            )}
          </li>
        ))}
      </ul>
      {confirm && (
        <ConfirmDialog title="Some files will be deleted" danger confirmLabel="Resolve" onCancel={() => setConfirm(false)} onConfirm={resolve}
          message={
            <>
              <p className="mb-2">Your choices delete these files (the delete guard does not hold back deletions you choose here):</p>
              <ul className="max-h-48 overflow-auto font-mono text-xs">{deletions.map((d) => <li key={d.path}>{d.path} — deleted {d.where === 'cloud' ? 'in the cloud' : 'on this machine'}</li>)}</ul>
            </>
          } />
      )}
    </div>
  )
}
```
`src/screens/DeleteReview.tsx`:
```tsx
import { useCallback, useEffect, useState } from 'react'
import { useToast } from '../components/toast'
import { Button, ConfirmDialog, EmptyState, Spinner } from '../components/ui'
import { api } from '../lib/api'
import { formatTime, summarizeResult } from '../lib/format'
import { useRouter } from '../lib/router'
import { useStore } from '../lib/store'
import type { PendingDeletes } from '../lib/types'

export function DeleteReview({ id }: { id: string }) {
  const { state } = useStore()
  const { navigate } = useRouter()
  const toast = useToast()
  const [pending, setPending] = useState<PendingDeletes | null | undefined>(undefined)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const name = state.actions[id]?.action.name ?? id
  const load = useCallback(() => {
    api.pendingDeletes(id).then(setPending).catch(toast.error)
  }, [id, toast])
  useEffect(load, [load])

  if (pending === undefined) return <Spinner />
  const paths = [...(pending?.paths ?? [])].sort()
  if (!pending || paths.length === 0 || pending.approved) {
    return <EmptyState title="Nothing to review"><Button onClick={() => navigate({ name: 'action', id, tab: 'overview' })}>Back to {name}</Button></EmptyState>
  }
  const approve = async () => {
    setConfirm(false)
    setBusy(true)
    try {
      const r = await api.approveDeletes(id, paths)
      if (r.outcome === 'deletes-pending') {
        toast.show('The list of deletions changed, so nothing was approved. Review the new list.')
        load()
      } else {
        toast.show(`Done: ${summarizeResult(r)}`)
        navigate({ name: 'action', id, tab: 'overview' })
      }
    } catch (e) {
      toast.error(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Deletions held back in {name}</h1>
      <p className="text-sm text-zinc-500">
        A sync on {formatTime(pending.detectedAt)} would delete many files at once, so nothing was applied. Approve only if these deletions are intended;
        a snapshot is taken before anything is deleted.
      </p>
      <ul className="max-h-[60vh] overflow-auto rounded border border-zinc-200 p-2 font-mono text-xs dark:border-zinc-800">
        {paths.map((p) => <li key={p}>{p}</li>)}
      </ul>
      <Button variant="danger" disabled={busy} onClick={() => setConfirm(true)}>{`Approve ${paths.length} deletion(s)`}</Button>
      {confirm && (
        <ConfirmDialog title="Approve the deletions?" danger confirmLabel="Approve" onCancel={() => setConfirm(false)} onConfirm={approve}
          message={`${paths.length} file(s) are deleted on the side where they still exist.`} />
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run** `npm run typecheck && npx vitest run` → PASS. Commit:
```bash
git add frontend
git commit -m "feat(gui): conflict resolver with diff and delete review" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Activity and Settings screens

**Files:**
- Create: `frontend/src/screens/Activity.tsx`, `frontend/src/screens/Settings.tsx`, `frontend/src/screens/Settings.test.tsx`
- Modify: `frontend/src/screens/index.tsx` (add `case 'activity': return <Activity />`, `case 'settings': return <SettingsScreen />`; the `default` branch now renders `<Actions />` — every route has a screen)

**Interfaces:**
- Consumes: `state.activity`, `api.settings/updateSettings/openLogFolder/openDataFolder/dataDir`.
- Produces: `Activity`, `SettingsScreen`.

- [ ] **Step 1: Failing test** `src/screens/Settings.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { bindings, emit } from '../test/mockBridge'
import App from '../App'

function boot() {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v1', agent: true, actions: [
    { action: { id: 'a1', name: 'Editor', items: [], machines: {}, updatedAt: 't', triggers: { onChange: false, debounceSeconds: 5, schedule: { kind: 'none' }, remotePollMinutes: 5 } }, profileId: 'p1', state: 'idle', running: false, failures: 0, conflicts: 0, pendingDeletes: 0 },
  ] })
  bindings.Profiles.mockResolvedValue([])
  bindings.DataDir.mockResolvedValue('/tmp/d')
  render(<App />)
}

test('settings: autostart hidden when unsupported; save sends the whole value', async () => {
  boot()
  bindings.Settings.mockResolvedValue({ machineId: 'm1', machineName: 'mac', autostart: false, autostartSupported: false, logLevel: 'info' })
  fireEvent.click(await screen.findByRole('button', { name: 'Settings' }))
  expect(await screen.findByDisplayValue('mac')).toBeInTheDocument()
  expect(screen.queryByLabelText('Start the agent when I log in')).toBeNull()
  fireEvent.change(screen.getByLabelText('Machine name'), { target: { value: 'studio' } })
  fireEvent.change(screen.getByLabelText('Log level'), { target: { value: 'debug' } })
  bindings.UpdateSettings.mockResolvedValue({ machineId: 'm1', machineName: 'studio', autostart: false, autostartSupported: false, logLevel: 'debug' })
  fireEvent.click(screen.getByText('Save'))
  await waitFor(() => expect(bindings.UpdateSettings).toHaveBeenCalledWith({ machineId: 'm1', machineName: 'studio', autostart: false, autostartSupported: false, logLevel: 'debug' }))
})

test('activity lists results and filters by action', async () => {
  boot()
  await screen.findByText('Editor')
  act(() => emit('agent:event', { type: 'action.result', actionId: 'a1', error: 'network is down', time: '2026-10-04T01:00:00Z' }))
  fireEvent.click(screen.getByRole('button', { name: 'Activity' }))
  expect(await screen.findByText('network is down')).toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('Filter by action'), { target: { value: 'other' } })
  expect(screen.queryByText('network is down')).toBeNull()
})
```

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** `src/screens/Activity.tsx`:
```tsx
import { useState } from 'react'
import { useToast } from '../components/toast'
import { Badge, Button, EmptyState, Select, td, th } from '../components/ui'
import { api } from '../lib/api'
import { formatTime } from '../lib/format'
import { useStore } from '../lib/store'

const kindTone = { result: 'ok', state: 'neutral', error: 'error' } as const

export function Activity() {
  const { state } = useStore()
  const toast = useToast()
  const [filter, setFilter] = useState('')
  const rows = state.activity.filter((a) => !filter || a.actionId === filter)
  const actions = Object.values(state.actions).sort((a, b) => a.action.name.localeCompare(b.action.name))
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Activity</h1>
        <div className="flex items-center gap-2">
          <Select aria-label="Filter by action" value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="">All actions</option>
            {actions.map((a) => <option key={a.action.id} value={a.action.id}>{a.action.name}</option>)}
            <option value="other">Other</option>
          </Select>
          <Button onClick={() => api.openLogFolder().catch(toast.error)}>Open log folder</Button>
        </div>
      </div>
      <p className="text-xs text-zinc-500">Live events since this window opened (the last 500). The full history is in the agent's log files.</p>
      {rows.length === 0 ? <EmptyState title="Nothing yet" /> : (
        <table className="w-full">
          <thead><tr><th className={th}>Time</th><th className={th}>Action</th><th className={th}>Event</th></tr></thead>
          <tbody>
            {rows.map((a) => (
              <tr key={a.id} className="border-t border-zinc-200 dark:border-zinc-800">
                <td className={`${td} whitespace-nowrap`}>{formatTime(a.time)}</td>
                <td className={td}>{a.actionName}</td>
                <td className={td}><Badge tone={kindTone[a.kind]}>{a.kind}</Badge> <span className="break-all">{a.text}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
```
`src/screens/Settings.tsx`:
```tsx
import { useEffect, useState } from 'react'
import { useToast } from '../components/toast'
import { Button, Checkbox, Field, Input, Select, Spinner } from '../components/ui'
import { api } from '../lib/api'
import { describeError } from '../lib/errors'
import { useDirtyGuard } from '../lib/router'
import { useStore } from '../lib/store'
import type { Settings } from '../lib/types'

export function SettingsScreen() {
  const { state, refresh } = useStore()
  const toast = useToast()
  const [loaded, setLoaded] = useState<Settings | null>(null)
  const [form, setForm] = useState<Settings | null>(null)
  const [dir, setDir] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    api.settings().then((s) => { setLoaded(s); setForm(s) }).catch(toast.error)
    api.dataDir().then(setDir).catch(() => {})
  }, [toast])
  useDirtyGuard(!!form && JSON.stringify(form) !== JSON.stringify(loaded))
  if (!form) return <Spinner />
  const save = async () => {
    setError('')
    try {
      const s = await api.updateSettings(form)
      setLoaded(s)
      setForm(s)
      toast.show('Settings saved.')
      refresh().catch(() => {})
    } catch (e) {
      setError(describeError(e).message)
    }
  }
  return (
    <div className="max-w-xl space-y-4">
      <h1 className="text-xl font-semibold">Settings</h1>
      <Field label="Machine name" hint="Shown to the other machines of your actions.">
        <Input aria-label="Machine name" value={form.machineName} onChange={(e) => setForm({ ...form, machineName: e.target.value })} />
      </Field>
      {form.autostartSupported && (
        <Checkbox label="Start the agent when I log in" checked={form.autostart} onChange={(v) => setForm({ ...form, autostart: v })} />
      )}
      <Field label="Log level">
        <Select aria-label="Log level" value={form.logLevel} onChange={(e) => setForm({ ...form, logLevel: e.target.value })}>
          {['debug', 'info', 'warn', 'error'].map((l) => <option key={l} value={l}>{l}</option>)}
        </Select>
      </Field>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <Button variant="primary" onClick={save}>Save</Button>
      <dl className="grid grid-cols-[10rem_1fr] gap-y-1 border-t border-zinc-200 pt-4 text-sm dark:border-zinc-800">
        <dt className="text-zinc-500">Machine ID</dt><dd className="font-mono text-xs">{form.machineId}</dd>
        <dt className="text-zinc-500">Agent version</dt><dd>{state.status?.version ?? '—'}</dd>
        <dt className="text-zinc-500">Data folder</dt><dd className="break-all">{dir} <Button variant="ghost" onClick={() => api.openDataFolder().catch(toast.error)}>Open</Button></dd>
      </dl>
    </div>
  )
}
```
`src/screens/index.tsx` final form:
```tsx
import type { Route } from '../lib/router'
import { ActionDetail } from './ActionDetail'
import { Actions } from './Actions'
import { Activity } from './Activity'
import { Conflicts } from './Conflicts'
import { DeleteReview } from './DeleteReview'
import { Profiles } from './Profiles'
import { SettingsScreen } from './Settings'

export function Screen({ route }: { route: Route }) {
  switch (route.name) {
    case 'action':
      return <ActionDetail id={route.id} tab={route.tab} />
    case 'conflicts':
      return <Conflicts id={route.id} />
    case 'deletes':
      return <DeleteReview id={route.id} />
    case 'profiles':
      return <Profiles />
    case 'activity':
      return <Activity />
    case 'settings':
      return <SettingsScreen />
    default:
      return <Actions />
  }
}
```

- [ ] **Step 4: Run** `npm run typecheck && npx vitest run && npm run build` → PASS. Commit:
```bash
git add frontend
git commit -m "feat(gui): activity feed and settings screens" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: `run --no-notify`, README, and the hand-check

**Files:**
- Modify: `cmd/agent/agentmain.go` (flag `--no-notify`), `cmd/agent/cli.go` (usage), a test in `cmd/agent/agent_e2e_test.go` or `cli2_test.go`, `README.md`

**Interfaces:**
- Produces: `s3-anysync-agent run [--no-tray] [--no-notify] [--verbose]` — `--no-notify` replaces the desktop notifier with one that only logs (`notify.Notifier` that writes the message at Info level), so development runs never post real notifications.

- [ ] **Step 1: Failing test.** Extend the existing `run` flag parsing test (search `runAgentCommand` tests) so `run --no-notify --no-tray` is accepted and the `agentEnv` built has a notifier that is not `notify.NewDesktop()` (expose the decision as a small pure function `pickNotifier(noNotify bool, log *slog.Logger) notify.Notifier` and test that `pickNotifier(true, log)` returns the log-only notifier type `logNotifier`). Check the existing `notify.Notifier` interface signature in `internal/notify/notify.go` first and match it.

- [ ] **Step 2: Implement** the flag, `logNotifier` (in `cmd/agent/agentmain.go`) and `pickNotifier`; add `[--no-notify]` to the `run` usage line.

- [ ] **Step 3: README** — new section "Desktop GUI":
  - what it is (a window over the running agent; closing it never stops syncing; it starts the agent if needed)
  - build: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`, then `cd cmd/gui && wails build` (macOS needs the Xcode command-line tools; output `cmd/gui/build/bin/`); the agent binary must sit next to the GUI binary, or set `S3_ANYSYNC_AGENT`
  - development: `cd frontend && npm install && npm test`; `S3_ANYSYNC_DATA_DIR=/tmp/s3as-dev S3_ANYSYNC_SECRETS=file wails dev` from `cmd/gui`, with an agent started first as `S3_ANYSYNC_DATA_DIR=/tmp/s3as-dev S3_ANYSYNC_SECRETS=file s3-anysync-agent run --no-tray --no-notify`
  - screens in one line each; conflicts and delete review explain that resolution deletions bypass the delete guard and are confirmed in the GUI
  - Known limitations: the GUI is not covered by automated end-to-end tests; the Settings autostart switch always changes the real per-user login item, even for a development data folder.

- [ ] **Step 4: Verify and commit** (all Global Constraints checks).
```bash
git add cmd/agent README.md
git commit -m "feat(agent): run --no-notify for development; docs: desktop GUI" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 5: Hand-check (controller, not a subagent).** With MinIO (`docker start s3-anysync-minio || docker run …` as in Plan 1), a temp data folder `D=$(mktemp -d)`, `S3_ANYSYNC_SECRETS=file`, build the agent (`go build -o /tmp/s3as-agent ./cmd/agent`), start `S3_ANYSYNC_DATA_DIR=$D S3_ANYSYNC_SECRETS=file /tmp/s3as-agent run --no-tray --no-notify` in the background, then `cd cmd/gui && S3_ANYSYNC_DATA_DIR=$D S3_ANYSYNC_SECRETS=file S3_ANYSYNC_AGENT=/tmp/s3as-agent wails dev` and open the dev server URL Wails prints (browser mode, bindings work there) in the browser pane. Walk once:
  1. Profiles: add a MinIO profile (endpoint `http://localhost:9000`, path style), see the test checklist; edit name with blank secret; remove refused while used.
  2. Actions: New action with a temp folder item; Sync now; result toast; Activity shows it.
  3. Rules: change schedule to daily 09:00 → preview shows 3 runs; save; edit the same action through the CLI (`S3_ANYSYNC_DATA_DIR=$D /tmp/s3as-agent action rules --name X <id>`) while the GUI form is dirty → Save shows "Changed elsewhere" → re-apply works.
  4. Items: add a second item; try mapping `$D` itself → protected-path message.
  5. Conflicts: second data folder `D2` joined via the CLI in-process mode to create a conflict; resolver shows the diff; Apply.
  6. Delete guard: delete ≥5 files (≥50%) locally; sync → "Deletions to review"; approve.
  7. Snapshots: restore one file to local.
  8. Cancel: a large folder sync → Cancel → state stays idle, toast "Sync cancelled.".
  9. Stop the agent → banner appears; start it again → banner clears and lists refresh.
  10. Theme toggle light/dark.
  Do NOT toggle autostart in Settings. Stop the agent, `wails dev`, and MinIO afterwards; delete `$D`/`$D2`. Record the outcome per step in the ledger.

---

## Self-review notes (for the executor)

- Spec coverage: section 2 → Tasks 1–5; 3.1 → Tasks 6–7; 3.2 → Task 8 (+10 schedule presets); 4.1 → Task 9; 4.2 → Task 10; 4.3 → Tasks 11–12; 4.4–4.5 → Task 13; 4.6–4.7 → Task 14; 5 → Task 8 `errors.ts` + per-screen handling; 6 → tests in every task + Task 15 hand-check; 8 → Task 7.
- Types are defined once in `src/lib/types.ts`; Go names come from `internal/api` and `internal/gui`.
