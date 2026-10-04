# s3-anysync Plan 3: GUI design

Status: approved 2026-10-04. Builds on `2026-10-01-s3-anysync-design.md` (sections 9 and 13). That spec stays authoritative for the engine and agent; this document covers the GUI and the agent API additions it needs.

## 1. Scope

In scope:
- agent API additions: a dev-only secrets file, profile edit and remove, cancel a running job, rule-edit conflict detection, `Action.ProfileID` over IPC, a distinct `protected-path` error code, token-rotation retry in the IPC client
- the Wails v2 GUI (`cmd/gui`) and the React frontend (`frontend/`) with every screen in section 4
- automated tests for the Go bindings and for frontend logic and key screens; a hand-check of every screen against a temporary data folder and MinIO

Out of scope (Plan 4 or later): the Windows `-H=windowsgui` build and console/CLI split, signing, installers, `.app` bundling, end-to-end UI automation.

## 2. Agent API additions (first task)

| Addition | Behavior |
|---|---|
| `UpdateProfile(ctx, req UpdateProfileRequest) (model.Profile, error)` | Replaces the profile's fields. `Secret == ""` keeps the stored secret; a non-empty secret replaces it in the keychain. Unknown ID → `not-found`. Invalid fields → `invalid`. Changing `Bucket` or `RootPrefix` while an action uses the profile is refused with `in-use`. Changing endpoint, bucket or prefix resets `ConditionalWrites` to false (unknown until the next Test connection). Running workers of actions that use the profile are rebuilt with the new settings (same path as other runner rebuilds). |
| `RemoveProfile(ctx, id) error` | Refused with new code `in-use` (HTTP 409, sentinel `api.ErrInUse`) while any joined action uses the profile; the message names the actions. Otherwise deletes the profile row and its keychain secret (a missing secret is not an error). |
| `CancelSync(ctx, actionID) error` | Cancels the context of the action's running job, if any (no-op, no error, when idle). A pending automatic trigger of that action is dropped; requests queued by other callers still run. A cancelled job does not count as a failure, does not start backoff and does not notify; the action returns to its previous non-error state; the result outcome is `cancelled`. The engine's existing cancellation paths (lock release, baseline only after a full apply) are relied on and covered by a test. |
| `UpdateRules(ctx, req UpdateRulesRequest)` | `UpdateRulesRequest{Rules, ExpectedUpdatedAt}`; `ExpectedUpdatedAt` is the `Action.UpdatedAt` the editor loaded. The check runs inside `engine.UpdateSharedAction`, under the action lock, against the bucket copy. If the bucket copy's `UpdatedAt` differs, nothing is written and the new code `conflict-edit` (HTTP 409, sentinel `engine.ErrEditConflict`) is returned. A zero `ExpectedUpdatedAt` is rejected as `invalid` (the CLI passes the value it just read). |
| `ActionStatus.ProfileID` / `Action.ProfileID` | `ActionStatus.ProfileID` is always filled by the agent (today it can be lost over IPC because `model.Action.ProfileID` is `json:"-"`). The GUI uses only `ActionStatus.ProfileID`. |
| `Resolve(ctx, actionID, ResolveRequest)` | `ResolveRequest{Choices, Reviewed}`; `Reviewed` maps each path to `ConflictVersion{LocalHash, RemoteHash}` (`""` for a deleted side), the versions the choices were made on. Under the action lock the engine compares it with the saved conflicts before applying anything: a missing or extra entry is `invalid`, a differing hash is the new code `conflict-changed` (HTTP 409, sentinel `engine.ErrConflictChanged`) and nothing is applied or consumed. Not counted as a failure. |
| `protected-path` code | `engine.ErrProtectedPath` gets its own code (HTTP 400) instead of `invalid`. |
| Dev secrets file | Env `S3_ANYSYNC_SECRETS=file` makes the agent and CLI store secrets in `<data dir>/secrets.json` (mode 0600, written atomically) instead of the OS keychain. Any other non-empty value is an error at start. For development and tests only; README marks it insecure. Implemented as a `secrets.Store` in `internal/secrets`. |
| IPC client 401 retry | On `unauthorized`, the client re-reads `ipc.token` once and retries the request; a second 401 is returned as `unauthorized`. |

IPC routes: `PUT /profiles/{id}`, `DELETE /profiles/{id}`, `POST /actions/{id}/cancel`; `PUT /actions/{id}/rules` body becomes `{"rules":…, "expectedUpdatedAt":…}`. The CLI gains `profile-edit`, `profile-remove`, `cancel`; `rules` passes the `UpdatedAt` it read.

## 3. Architecture

### 3.1 Go side (`cmd/gui`)

- `main.go`: Wails v2 app setup (window 1100×720, min 900×600; single-instance lock via Wails `SingleInstanceLock` that focuses the existing window); embeds `frontend/dist`.
- `app.go`: type `App` with one `api.Service` field. Every `api.Service` method is bound 1:1 (same name, same request/response types) and returns the error as a JSON-encoded `api.RemoteError` (`{"code","message"}`) so the frontend gets stable codes. No logic beyond that.
- Native helpers bound on `App`: `PickFile()`, `PickFolder()` (Wails dialogs; return "" on cancel), `OpenDataFolder()`, `OpenLogFolder()`, `AgentInfo()` (connected, version, data dir).
- **Connection manager** (`connect.go`, pure Go, testable):
  1. `ipc.Connect(dataDir)`; on success use it.
  2. If no agent runs, start the agent executable with `run` (detached), then poll `ipc.Connect` every 250 ms for up to 10 s. Agent executable lookup: env `S3_ANYSYNC_AGENT`, else `s3-anysync-agent[.exe]` next to the GUI executable (symlinks resolved). Data dir: `S3_ANYSYNC_DATA_DIR`, else `app.DefaultDataDir()`, and the agent is started with the same environment.
  3. If that fails, the frontend shows the "agent not running" page with the reason and Retry.
  - The launcher is an interface so tests use a fake. The GUI never opens the data folder's state itself.
- **Events:** after connecting, `App` calls `Subscribe` and forwards each `api.Event` with `runtime.EventsEmit("agent:event", ev)`. When the stream ends or a call fails with a connection error, `App` emits `agent:connection` `{connected:false, reason}`, retries every 2 s, and on success emits `{connected:true}`; the frontend then refetches everything (events are not replayed).
- Errors from a lost connection map to client-side code `unreachable`.
- Build: `cmd/gui` compiles with plain `go build ./...` and `go test ./...` without a built frontend: the embed uses a `frontend/dist` that contains a committed placeholder `index.html` (the real build overwrites it; `.gitignore` excludes the rest of `dist`). CGO is required for the GUI on macOS (Wails WebKit), as already allowed for the agent; Windows stays CGO-free.

### 3.2 Frontend (`frontend/`)

- React 18, TypeScript (strict), Vite, Tailwind CSS; Vitest + React Testing Library + jsdom. No other UI framework. Generated Wails bindings (`frontend/wailsjs/`) are committed.
- `src/api.ts`: thin typed wrapper over the bindings that parses the `{"code","message"}` error into an `AgentError` class.
- `src/store.ts`: one reducer-based store (React context + `useReducer`): `connection`, `status`, `actions: Record<id, ActionStatus>`, `profiles`, `activity` (ring buffer, last 500 entries). Event handling: `action.state` replaces `actions[id]`; `action.result` appends to activity and refetches `Action(id)`; `actions.changed` refetches `Actions()`; reconnect refetches everything.
- `src/errors.ts`: code → user message map (section 5).
- `src/lib/schedule.ts` maps presets to `model.Schedule`; the next-runs preview is the bound Go method `PreviewSchedule` (uses `internal/schedule`).
- Routing: in-app state router (no URL routing library): `actions`, `action/:id/:tab`, `conflicts/:id`, `profiles`, `activity`, `settings`.
- Themes: `system` (default), `light`, `dark`, stored in `localStorage`; Tailwind `dark:` classes.

## 4. Screens

Layout: left sidebar (Actions, Profiles, Activity, Settings); header with machine name, agent connection status and theme toggle. A banner shows while the agent is disconnected/reconnecting.

1. **Profiles**
   - List; form with provider presets that prefill endpoint, region and path-style: AWS S3, Cloudflare R2, Backblaze B2, MinIO, Wasabi, Custom.
   - Secret field is write-only; blank on edit means keep.
   - **Test connection** shows the `ProbeResult` as a checklist (list, put, get, delete, conditional writes).
   - Remove is disabled while an action uses the profile (reason shown); the agent's `in-use` error is also handled.
2. **Actions list**
   - Row: name, state badge, Running spinner, last run time + outcome + summary, next run, conflict and pending-delete counts (links to the resolver / delete review).
   - Row buttons: Sync now, Pause/Resume, Cancel (while running).
   - **New action** wizard: profile → name, direction, triggers → encryption + passphrase (fixed at creation; warning that it cannot be changed or recovered) → first items (optional).
   - **Join existing**: pick profile → list from `Discover` minus actions already joined → passphrase if encrypted → map items.
3. **Action detail** tabs:
   - **Overview:** state, last result, banners linking to the resolver or delete review; Sync, Snapshot now, Pause/Resume, Cancel, Leave (confirm dialog; if the agent refuses with a reason, offer force with a second confirm).
   - **Rules:** this machine's direction; triggers; schedule presets (every 15 min, hourly, daily at HH:MM) or cron with next 3 runs; delete propagation; receiverRestoresCloud; excludes; retention; delete-guard thresholds. Save sends `expectedUpdatedAt`; on `conflict-edit` a dialog offers "Reload (discard my edits)" or "Reload and re-apply my edits" (reapplies only the fields the user changed onto the fresh rules, then shows the form unsaved).
   - **Items:** each item's remote name, local path here or "not mapped on this machine", other machines' paths read-only; Add, Map/Remap, Unmap, Remove with native pickers; `protected-path` shown inline.
   - **Snapshots:** list (time, reason, file count); Restore dialog: all or selected files → target local/cloud/both → confirm.
   - **History:** table of past runs.
4. **Conflicts:** list with local/cloud size, mtime, hash; side-by-side text diff from `ConflictDetail` (binary or truncated → metadata only); per-file Keep local / Keep cloud; Apply disabled until every conflict has a choice; bulk "all local"/"all cloud" warn and list the files a bulk choice would delete, since resolution deletions are exempt from the delete guard.
5. **Delete review:** pending paths; Approve sends exactly that list; if the result is still `deletes-pending`, reload the list and say nothing was approved.
6. **Activity:** live feed of `action.result` and `action.state` events, filter by action, "Open log folder".
7. **Settings:** machine name, autostart (hidden when unsupported, including whenever `S3_ANYSYNC_DATA_DIR` is set), log level.

Unsaved forms (Rules, Profile) ask "Discard changes?" before navigating away.

## 5. Error handling

| Code | GUI behavior |
|---|---|
| `invalid`, `protected-path` | inline on the form (message) |
| `conflict-edit` | rules reload dialog |
| `in-use` | message naming the actions |
| `busy`, `locked` | toast "busy, try again" |
| `not-found` | toast; refetch lists |
| `unauthorized` | (client already retried once) treat as disconnected and reconnect |
| `unreachable` | disconnected banner; retry every 2 s |
| `conflict-pending`, `passphrase-missing`, `decrypt`, `keycheck-missing`, `root-missing`, `access-denied`, `no-such-bucket`, `lock-lost`, `unsupported`, `unavailable` | toast with a specific sentence each |
| anything else | toast with "Details" showing the raw message |

## 6. Testing

- Go: unit and `httptest` tests for every section 2 addition (incl. cancel during apply leaves the baseline and lock consistent, cancel is not a failure, `conflict-edit`, `in-use`, 401 retry). `cmd/gui` tests with a fake `api.Service` and fake launcher: binding pass-through and error encoding, auto-start, timeout, reconnect, event forwarding.
- Vitest: error mapping, store reducer, schedule preset mapper, profile form (blank secret keeps), rules form (`conflict-edit` flow incl. re-apply), conflict resolver (Apply gating, bulk-delete warning), delete review (fresh list).
- Hand-check: `wails dev` with `S3_ANYSYNC_DATA_DIR` set to a temp folder, `S3_ANYSYNC_SECRETS=file`, and the `s3-anysync-minio` container; every screen in section 4 walked once in the browser pane. The real data folder, Keychain and login items are not touched.

## 7. Task order

1. Agent API additions (section 2).
2. Wails scaffold and `cmd/gui` bindings, connection manager, events.
3. Frontend shell: layout, theme, store, events, error mapping, API wrapper.
4. Profiles screen.
5. Actions list, New action wizard, Join existing.
6. Action detail: Overview, Rules (with schedule preview), Items, Snapshots, History.
7. Conflicts resolver and delete review.
8. Activity and Settings.
9. README (GUI build/run) and the hand-check.

## 8. Toolchain

Wails v2 CLI installed with `go install github.com/wailsapp/wails/v2/cmd/wails@latest` (user approved); npm packages installed only inside `frontend/`. Node 20 is present.
