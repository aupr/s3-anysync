# s3-anysync: design

Date: 2026-10-01
Status: approved in brainstorming, pending spec review

## 1. Purpose

A desktop tool for macOS and Windows that keeps application settings files and config directories in sync through an S3-compatible bucket. The user creates **actions** (sync rules) and adds **items** (files or directories) to each action. A background agent runs the syncs; a GUI configures them.

Out of scope for v1: Linux, code signing, detecting whether a target app is running, scheduled snapshots, automated GUI tests.

## 2. Process architecture

One Go module, `github.com/niceit/s3-anysync`, builds two binaries that share `internal/` packages.

| Binary | Role | Runs |
|---|---|---|
| `s3-anysync-agent` | Sync engine, triggers, tray, IPC server | Always; starts at user login |
| `s3-anysync` (Wails + React) | Settings and management UI | Only when opened |

The GUI never touches local files or S3 directly. Every operation goes through the agent over local IPC, so closing or crashing the GUI never affects a sync. If the GUI starts and the agent is not running, the GUI launches it.

Two binaries are used because Wails and the system tray both need the macOS main thread, and because the agent should not carry the WebView runtime.

### Libraries

| Concern | Library |
|---|---|
| Autostart (per-user LaunchAgent / HKCU Run) | `kardianos/service` (user mode) |
| Tray | `fyne-io/systray` |
| IPC transport on Windows | `Microsoft/go-winio` |
| File watching | `fsnotify/fsnotify` |
| Cron | `robfig/cron/v3` |
| S3 | `aws-sdk-go-v2` |
| State store | `modernc.org/sqlite` (pure Go, no CGO) |
| Secrets | `zalando/go-keyring` |
| Notifications | `gen2brain/beeep` |
| Logging | `log/slog` + rotating file writer |
| GUI | Wails v2, React, TypeScript, Tailwind |

App data directory (`<appdata>`): `~/Library/Application Support/s3-anysync` on macOS, `%APPDATA%\s3-anysync` on Windows.

## 3. Data model

### Profile (local only)
An S3 connection: `id`, `name`, `provider` preset (AWS, Wasabi, DigitalOcean Spaces, Backblaze B2, Cloudflare R2, MinIO/custom), `endpoint`, `region`, `bucket`, `rootPrefix`, `accessKeyId`, `pathStyle`. The secret access key is stored in the OS keychain under the profile id.

### Machine
On first run the agent generates a `machineId` (a UUID) and a default friendly name (the hostname), which the user can edit in Settings. Both are stored locally.

### Action (shared through the bucket)
| Field | Meaning |
|---|---|
| `id` | UUID; also the remote folder name |
| `name` | Display name |
| `profileId` | Local reference to the profile (not shared) |
| `direction` | Default direction: `two-way`, `local-to-cloud`, `cloud-to-local` |
| `machineDirections` | Map from `machineId` to a direction that overrides the default for that machine (for example: work Mac is `local-to-cloud`, home PC is `cloud-to-local`) |
| `triggers.onChange` | bool; debounce seconds (default 3) |
| `triggers.schedule` | none, preset (every N minutes or hours, daily at HH:MM, weekly on a day at HH:MM) or raw cron |
| `triggers.remotePollMinutes` | default 5; used when `onChange` is on and no schedule is set |
| `propagateDeletes` | bool, default true |
| `receiverRestoresCloud` | bool, default false. Only matters when `propagateDeletes` is off: if the cloud deleted a file that a `cloud-to-local` machine still has, false leaves the local file alone (a receive-only machine never uploads); true uploads it back |
| `encrypt` | bool, fixed when the action is created; the passphrase is in the keychain under the action id |
| `excludes` | glob patterns applied to every item |
| `snapshotRetention` | keep the newest N snapshots (default 20) |

### Item (shared through the bucket)
| Field | Meaning |
|---|---|
| `id` | UUID |
| `remoteName` | Path under `current/`, e.g. `settings.json` or `snippets/` |
| `type` | `file` or `dir` |
| `excludes` | Glob patterns for this item only |
| `localPaths` | Map from `machineId` to an absolute local path; may contain `~`, `$HOME`, `%APPDATA%`, `%USERPROFILE%`, `%LOCALAPPDATA%` |

An item with no entry for the current machine is skipped on that machine, and the GUI shows it as "not mapped on this machine".

## 4. Remote layout

```
<rootPrefix>/<actionId>/.meta/action.json     action + items + every machine's paths and names; no secrets
<rootPrefix>/<actionId>/.meta/manifest.json   cloud state: path → {hash, size, mtime, etag, writerMachineId}
<rootPrefix>/<actionId>/.meta/lock            run lock: {machineId, expiresAt}
<rootPrefix>/<actionId>/.meta/keycheck        encrypted actions only: a known string encrypted with the passphrase
<rootPrefix>/<actionId>/current/<remoteName>…  live files
<rootPrefix>/<actionId>/snapshots/<ts>/…        snapshot copies
<rootPrefix>/<actionId>/snapshots/<ts>.json     snapshot index: {reason, machineId, createdAt, files[]}
```

`<ts>` is UTC `YYYYMMDDTHHMMSSZ`.

### Sharing actions between machines
- **Create:** the agent writes `.meta/action.json`.
- **Join:** after the user adds a profile, `discover-actions` lists every `*/.meta/action.json` under the root prefix. The user chooses one, clicks **Join**, and maps each item to a local path. The agent writes the updated `localPaths` back to `action.json` while holding the lock.
- An edit to the rules on any machine updates `action.json`. Other machines pick it up on their next run.

### Lock
- The lock is taken with a conditional write (`PutObject` with `If-None-Match: *`) and has a 2-minute TTL.
- An expired lock may be replaced with a conditional write that matches its ETag (`If-Match`).
- If the provider rejects conditional writes, the agent falls back to a best-effort lock: write, read it back, compare the owner. **Test connection** reports which mode is in use.

## 5. Encryption

Optional, per action. The agent ports the Tabby plugin's v2 envelope to Go:
- AES-256-GCM.
- scrypt key derivation with N=16384, r=8, p=1, a 16-byte salt and a 12-byte IV.
- A JSON envelope with `format: "s3-anysync"`.

When encryption is on:
- Every file under `current/` and `snapshots/` is encrypted.
- `action.json`, `manifest.json` and the snapshot indexes are not encrypted, because they contain only paths, hashes and metadata.
- The SHA-256 hash of the plaintext is stored in the object's user metadata (`x-amz-meta-sha256`), so files can be compared without decrypting them.
- Every machine that joins the action must enter the same passphrase. Join and every run decrypt `.meta/keycheck` first, so a wrong passphrase is rejected before anything is uploaded. A wrong passphrase (an authentication failure on decrypt) stops the action immediately with no retry.
- Encryption cannot be switched on or off after the action is created, because existing objects would then be in the wrong format.

## 6. Sync engine

Each action has one worker goroutine with a job queue, so runs of the same action never overlap. Jobs: `sync`, `snapshot`, `resolve`, `restore`.

### 6.1 Scan
- **Local:** walk each mapped item, applying the action and item excludes. A file whose size and mtime match the baseline reuses its stored hash; every other file is re-hashed with SHA-256.
- **Remote:** GET `manifest.json`. If it is missing or fails to parse, rebuild it from `ListObjectsV2` together with each object's `x-amz-meta-sha256`.

### 6.2 Plan

Each path is compared across local, remote and the baseline. The baseline is stored per action in local SQLite: path → {hash, size, mtime}.

| Local vs baseline | Remote vs baseline | two-way | local-to-cloud | cloud-to-local |
|---|---|---|---|---|
| same | same | nothing | nothing | nothing |
| changed | same | upload | upload | download (restore local) |
| same | changed | download | upload (overwrite cloud) | download |
| changed | changed, ≠ local | **conflict** | upload | download |
| changed | changed, = local | adopt as baseline | adopt | adopt |
| deleted | same | delete remote¹ | delete remote¹ | download (restore local) |
| same | deleted | delete local¹ | upload (restore cloud) | delete local¹ |
| deleted | deleted | drop from baseline | drop | drop |

¹ When `propagateDeletes` is off, the file is restored from the side that still has it. Exception (user decision): by default a `cloud-to-local` machine never writes to the cloud, so when the cloud deleted a file and `propagateDeletes` is off, the local file is left alone (no upload). The action switch `receiverRestoresCloud` turns this off: the machine then uploads the file back.

Paths that are new on one side are treated like "changed vs baseline" with an empty baseline hash.

**No baseline (first run on this machine for this action).** Paths that exist on only one side are copied to the other, within the limits of the direction. In `two-way` mode, a path that exists on both sides with different hashes is a **conflict**; there is no timestamp-based guessing.

The direction used is the machine's entry in `machineDirections`, or the action's default `direction` if there is none.

One-way modes never produce a conflict: the source side always wins. In `local-to-cloud` mode this means an edit made on another machine is overwritten (mirror semantics).

**Untracked files in one-way modes.** A file that exists only on the destination side and was never synced (not in the baseline) is left alone: `cloud-to-local` never deletes a local file it has never synced, and `local-to-cloud` never deletes a cloud file it has never synced.

**Symlinks.** Symlinks to files are followed. Symlinked directories are not descended into. Other special files are skipped.

**Unmapped or removed items.** Only paths that belong to an item mapped on this machine, and are not excluded, take part in planning. Removing a mapping therefore never deletes anything in the cloud.

### 6.3 Snapshot guard
Before executing, if the plan:
- **deletes or overwrites any remote file:** those files are copied server-side into `snapshots/<ts>/` (reason `auto-pre-sync`).
- **deletes or overwrites any local file:** those files are copied to `<appdata>/local-backups/<actionId>/<ts>/`. The newest 20 such backups are kept per action.

### 6.4 Execute
1. Acquire the lock.
2. Re-read the ETag of `manifest.json`. If it changed since the scan, release the lock and re-plan (at most 3 times).
3. Apply the plan:
   - **Upload:** PUT with metadata `sha256`, `mtime`, `machineId`.
   - **Download:** write to a temp file in the target directory, then rename it atomically into place and set its mtime.
   - **Delete.**
4. Commit (6.5), then release the lock.

### 6.5 Commit
Write the new baseline to SQLite and the new `manifest.json` to the bucket, and record a history row (counts, duration, outcome).

### 6.6 Conflicts
- The action enters `Paused (conflict)`. The whole action pauses, including its non-conflicting changes.
- The tray shows a conflict badge and an OS notification is sent.
- The resolver lists each conflicting file with:
  - local and cloud size and mtime
  - the machine that last wrote the cloud copy
  - a text diff for UTF-8 files under 1 MB
- The user chooses **local → cloud** or **cloud → local** per file, or one choice for all files.
- The losing side is snapshotted (remote) or backed up (local) before it is overwritten.
- After resolution the action runs a normal sync and returns to `Idle`.

### 6.7 Snapshots
- **Snapshot now:** in `two-way` and `local-to-cloud` modes, a sync runs first so local changes are included. Then every object under `current/` is copied server-side into `snapshots/<ts>/`, and the index is written with reason `manual`.
- **Automatic snapshots:** taken only by the guard (6.3) and by conflict resolution. There are no scheduled snapshots.
- **Retention:** after each snapshot, the oldest are deleted until `snapshotRetention` remain.
- **Restore:** from the GUI, the user picks a snapshot, then all files or selected files, then a target: local, cloud or both. Before restoring, the current state is snapshotted with reason `pre-restore`.

### 6.8 Triggers
- **On change:** recursive `fsnotify` watch of the mapped paths, debounced. On Windows each subdirectory is added explicitly. Changes made by the engine's own downloads are ignored.
- **Schedule:** `robfig/cron/v3`. Presets compile to cron expressions.
- **Remote poll:** when `onChange` is on and there is no schedule, the agent GETs `manifest.json` every `remotePollMinutes` and runs a sync if its ETag changed.
- **Manual:** from the GUI or the tray.

### 6.9 Action state machine
```
Idle ──trigger──▶ Running ──ok──▶ Idle
Running ──conflict──▶ Paused(conflict) ──resolved──▶ Running
Running ──error──▶ Error ──backoff retry──▶ Running
any ──user pause──▶ Paused(user) ──resume──▶ Idle
```
- Retry backoff: 30 s, 2 m, 10 m, 30 m, then every 1 h.
- An OS notification is sent after 3 consecutive failures.
- A decryption authentication failure or an S3 403 goes straight to `Error` with no automatic retry; it needs the user to act.

## 7. Agent

- **Autostart:** a per-user LaunchAgent on macOS, an HKCU Run entry on Windows. It is registered on the GUI's first run, and you can turn it off in Settings.
- **Single instance:** a lock file in `<appdata>` plus a liveness check on the IPC socket.
- **Tray:**
  - a status icon: idle, syncing, conflict, error
  - Open
  - Sync all now
  - Snapshot all
  - Pause all / Resume all
  - one submenu per action: Sync now, Snapshot now, Pause/Resume, plus its status line
  - Quit
- **State store:** SQLite at `<appdata>/state.db`. Tables: profiles, actions (a local cache of `action.json` plus `profileId`), items, baselines, history, conflicts, settings.
- **Logs:** JSON `slog` output to `<appdata>/logs/agent-YYYY-MM-DD.log`, kept 7 days.

## 8. IPC

- **Transport:** HTTP/1.1. On macOS it runs over a Unix socket `<appdata>/agent.sock` with mode 0600. On Windows it runs over the named pipe `\\.\pipe\s3-anysync-<username>`, with a security descriptor that allows only the current user.
- **Authentication:** a random 32-byte token in `<appdata>/ipc.token`, readable only by the user. It is sent as `Authorization: Bearer`.
- **Endpoints (JSON):**
  - `GET/POST/PUT/DELETE /profiles`, and `POST /profiles/{id}/test`
  - `GET /profiles/{id}/discover`, and `POST /actions/join`
  - `GET/POST/PUT/DELETE /actions`, and `/actions/{id}/items`
  - `POST /actions/{id}/sync|snapshot|pause|resume`
  - `GET /actions/{id}/snapshots`, and `POST /actions/{id}/snapshots/{ts}/restore`
  - `GET /actions/{id}/conflicts`, `GET /actions/{id}/conflicts/{path}/diff`, and `POST /actions/{id}/conflicts/resolve`
  - `GET /actions/{id}/history`
  - `GET/PUT /settings`
- **Events:** `GET /events` is a Server-Sent Events stream of `action.state`, `action.progress`, `log` and `conflict` events.

## 9. GUI

The Go side is a thin IPC client bound to the frontend. Screens:

1. **Profiles:**
   - a list and form with provider presets that prefill the endpoint, region and path-style settings
   - **Test connection**, which checks list, put, get and delete permissions and whether conditional writes are supported
2. **Actions:** a list with status, last sync and next run. Buttons for **New action** and **Join existing**.
3. **Action detail, with tabs:**
   - **Rules:** direction, triggers, schedule presets or cron with a preview of the next 3 runs, delete propagation, encryption with the passphrase, excludes and retention.
   - **Items:** add a file or folder with the native picker, edit the remote name and excludes, and see each machine's mapping.
   - **Snapshots:** a list, and restore.
   - **History.**
4. **Conflicts:** the resolver described in 6.6, with a diff view.
5. **Activity:** a live log from `/events`.
6. **Settings:** machine name, autostart, log level.

Dark and light themes.

## 10. Project layout

```
cmd/agent/           agent entry point
cmd/gui/             Wails entry point (main.go, app.go)
internal/model/      shared types
internal/engine/     scan, plan, execute, commit, snapshot, restore
internal/storage/    Storage port + S3 adapter
internal/crypto/     v2 envelope encryption
internal/paths/      variable expansion, exclude matching
internal/trigger/    watcher, scheduler, remote poller
internal/state/      SQLite repository + migrations
internal/secrets/    keychain wrapper
internal/ipc/        server, client, SSE events
internal/agent/      action manager, state machine, tray, autostart
frontend/            React app
build/               packaging scripts
docs/
```

## 11. Testing

- **Planner:** table-driven unit tests covering every row of 6.2 in all three directions, with deletes on and off and with no baseline. The planner is a pure function.
- **Unit tests:** crypto round-trip and tamper detection, path expansion, exclude matching, cron preset compilation, backoff schedule.
- **Integration tests** (enabled when `S3_TEST_ENDPOINT` is set; MinIO in Docker):
  - the S3 adapter, including the conditional-write lock
  - the end-to-end engine, with two simulated machines on one bucket: propagation, conflicts, delete with a snapshot, encrypted actions, restore
- **IPC handlers:** tested with `httptest`.
- **GUI:** checked by hand in v1.

## 12. Delivery phases

Each phase ends in something runnable.

1. **Core:** model, SQLite, S3 adapter, crypto, paths, and the engine (scan, plan, execute, commit, snapshot, restore), plus a debug CLI (`s3-anysync-agent sync <actionId>`).
2. **Agent:** triggers, the action manager and state machine, IPC with events, tray, autostart, notifications.
3. **GUI:** every screen in section 9.
4. **Packaging:**
   - macOS: an unsigned `.app` bundle with the agent in `Contents/MacOS`.
   - Windows: the Wails NSIS per-user installer.
   - Install notes for Gatekeeper and SmartScreen.
