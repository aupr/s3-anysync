# s3-anysync

Keep application settings files and config folders in sync across macOS and Windows machines through your own S3-compatible bucket (AWS S3, Wasabi, Backblaze B2, Cloudflare R2, MinIO, ...).

Design: [docs/superpowers/specs/2026-10-01-s3-anysync-design.md](docs/superpowers/specs/2026-10-01-s3-anysync-design.md)

## Status

Plans 1 and 2 are done: the sync engine, the command line, and the background agent (triggers, tray, notifications, IPC, autostart). The desktop GUI (Plan 3) is implemented; the installers come in Plan 4.

## Concepts

- **Profile**: a bucket connection. The secret key is read from `S3_ANYSYNC_SECRET` when you add the profile and is stored in the OS keychain (macOS Keychain, Windows Credential Manager), never in the database, logs or bucket.
- **Action**: a named set of items (files or folders) kept in sync under `<prefix>/<actionId>/` in the bucket.
- **Direction**: each machine has its own direction per action: `two-way`, `local-to-cloud` or `cloud-to-local`. The action has a default; `action direction ACTION_ID <dir>` overrides it for this machine (`default` clears the override).
- **Deletes**: by default deletes propagate. `--no-propagate-deletes` stops that; one-way modes never delete a file on the destination that was never synced. With `--receiver-restores-cloud`, a cloud-to-local machine uploads a file back that the cloud deleted (default: it never uploads).
- **Encrypted actions**: `action create --encrypt` encrypts file contents with AES-256-GCM. The passphrase is read from `S3_ANYSYNC_PASSPHRASE` (also for `action join`) and is kept in the keychain.
- **Conflicts**: when both sides changed a file, `sync` reports `conflict: <path>` and leaves both copies alone and pauses the whole action: non-conflicting changes wait too until the conflicts are resolved. `resolve` needs a choice for every conflict: either `--all local|cloud` or one `--path P=local|cloud` per conflicting path. `resolve` also sends the versions (content hashes) of the conflicts it just listed; if a conflict was saved again with new content in the meantime, it is refused with `conflict-changed` and nothing is applied: list the conflicts again with `conflicts` and choose again.
- **Snapshots and restore**: `snapshot` stores a point-in-time copy of the action in the bucket (a `pre-resolve` snapshot is taken before resolving only when the resolution overwrites or deletes cloud files; local files that lose are copied to `<data dir>/local-backups/<actionId>/<ts>/`, newest 20 kept). Every sync that overwrites or deletes cloud files also takes an `auto-pre-sync` snapshot. Retention is split: manual snapshots keep the newest 20; automatic ones keep the newest 50 or those younger than 7 days; automatic snapshots that record deletions are kept at least 30 days. `snapshots` lists them; `restore --to local|cloud|both [--path P]... ACTION_ID TS` restores from one.
- **Data dir**: machine id, state and baseline live in `os.UserConfigDir()/s3-anysync`. Set `S3_ANYSYNC_DATA_DIR` to use another folder, which is how you simulate two machines on one computer.

## Build

Requires Go 1.27.

```bash
# macOS with the real tray icon (needs the Xcode command-line tools)
CGO_ENABLED=1 go build -o bin/s3-anysync-agent ./cmd/agent
# headless (no tray icon; also fine for servers and tests)
CGO_ENABLED=0 go build -o bin/s3-anysync-agent ./cmd/agent
# Windows (real tray, named pipe, HKCU Run)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/s3-anysync-agent.exe ./cmd/agent
```

## Try it with two "machines" on one computer

Each data folder acts as a separate machine. This writes the secret key to your OS keychain.

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

Run `./bin/s3-anysync-agent` with no arguments for every command. Flags always come before arguments.

## Running the agent

```bash
s3-anysync-agent run [--no-tray] [--no-notify] [--verbose]
```

The agent syncs in the background, shows a status icon and menu in the tray (unless `--no-tray`, or the build has no tray), and sends desktop notifications for conflicts, pending deletions and repeated failures. Logs are JSON files in `<data dir>/logs/agent-YYYY-MM-DD.log`, kept 7 days; `--verbose` also writes them to stderr. `--no-notify` posts no desktop notifications and only logs them at Info level (for development runs). The level follows the log level setting. Stop it with Quit in the tray, Ctrl-C or SIGTERM; after IPC shutdown it cancels running syncs (waiting at most 5 s), then exits.

Only one process owns a data folder (`<data dir>/agent.lock`). If you start a second agent, or start one while a CLI command is running in-process, it exits with an error: retry in a moment.

Autostart at login (macOS LaunchAgent, Windows HKCU Run entry; both run `<agent executable> run`):

```bash
s3-anysync-agent autostart enable|disable|status
```

Enabling never starts or stops the running agent. When the agent starts with autostart enabled, it rewrites the entry only if the stored executable no longer exists, and never when `S3_ANYSYNC_DATA_DIR` is set (dev runs).

Every other command talks to the running agent automatically (the same API the GUI will use), or works in-process when no agent runs. Useful commands: `status` (agent state, each action's state and last result), `pause ACTION_ID` and `resume ACTION_ID` (a pause survives restarts), `cancel ACTION_ID` (stops the running sync, snapshot or restore; a cancelled run is not a failure: nothing is half-applied, no backoff, no notification), `history [--limit N] ACTION_ID`.

## Triggers

Set them per action with `action rules` (without flags it shows the rules):

- `--on-change` syncs after local files change, once they have been quiet for `--debounce SECONDS`.
- `--schedule SPEC`: `none`, `every:MINUTES`, `daily:HH:MM`, `weekly:DAY@HH:MM` (0 = Sunday) or `cron:SPEC`.
- When `--on-change` is on and there is no schedule, the agent also checks the bucket for remote changes every `--poll MINUTES`.
- Triggers do nothing while the action is paused in any way: paused by you, on a conflict, or on pending deletions. While an action is in `error`, change, schedule and poll triggers wait for the backoff retry. Failed syncs are retried after 30 s, 2 min, 10 min, 30 min, then hourly (never for errors a retry cannot fix, such as a wrong passphrase, a missing folder or denied access).

## Delete guard

A sync that would delete at least 5 files AND at least 50% of the files synced so far, on either side, applies nothing. The action pauses (`paused-deletes`) and notifies you. Review and approve:

```bash
s3-anysync-agent approve-deletes ACTION_ID                          # lists the paths and a review digest
s3-anysync-agent approve-deletes --yes --digest DIGEST ACTION_ID    # approves exactly what you reviewed
```

The approval needs the digest of the list you reviewed, so a list that changed in the meantime is refused. Per action: `action rules --delete-guard-files N --delete-guard-percent P ...`. Deletions you choose when resolving a conflict do not count.

## Snapshot retention

- Manual snapshots: the newest `--retention` (default 20) are kept.
- Automatic snapshots (`auto-pre-sync`, `pre-resolve`, `pre-restore`): kept while they are among the newest 50 or younger than 7 days; those that hold files about to be deleted are kept at least 30 days.

## Desktop GUI

A window over the running agent (Wails, React). Closing it never stops syncing; it starts the agent if none is running. It never opens the data folder itself: everything goes through the agent's IPC.

Build (macOS needs the Xcode command-line tools); the output is in `cmd/gui/build/bin/`:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
cd frontend && npm install && npm run build   # fills cmd/gui/dist
cd ../cmd/gui && wails build
```

A plain `go build ./cmd/gui` on a fresh clone embeds an empty `cmd/gui/dist` (only `.keep`), so run the frontend build first to get a working GUI binary.

On macOS `wails build` produces `cmd/gui/build/bin/s3-anysync.app`: place the agent binary in `s3-anysync.app/Contents/MacOS/` next to the GUI binary. On other platforms it must sit next to the GUI binary. Alternatively set `S3_ANYSYNC_AGENT` to its path.

Development: run the frontend tests with `cd frontend && npm install && npm test`. To run the GUI against a throwaway data folder, start an agent first, then the GUI from `cmd/gui`, both with the same data folder and the file secret store:

```bash
go build -o bin/s3-anysync-agent ./cmd/agent
S3_ANYSYNC_DATA_DIR=/tmp/s3as-dev S3_ANYSYNC_SECRETS=file ./bin/s3-anysync-agent run --no-tray --no-notify
cd cmd/gui && S3_ANYSYNC_DATA_DIR=/tmp/s3as-dev S3_ANYSYNC_SECRETS=file wails dev
```

Screens:

- **Actions**: list of actions with their state; create, join, sync now, pause, cancel; each action has Overview, Items, Rules, Snapshots and History tabs.
- **Profiles**: bucket connections; add, edit, test and remove (removal is refused while an action uses the profile).
- **Conflicts**: shows both versions with a diff and applies your local/cloud choice per path.
- **Deletions to review**: lists the paths a guarded sync wanted to delete; approve exactly what you reviewed.
- **Activity**: sync results, state changes and errors, with a filter by action.
- **Settings**: log level, machine name and autostart at login.

Deletions you choose when resolving a conflict bypass the delete guard; the GUI asks you to confirm them in the resolver.

Limitations specific to the GUI are listed under Known limitations.

## Tests

```bash
go test ./...
```

The S3 integration tests (storage adapter and every engine scenario against S3) run when an S3-compatible server is configured:

```bash
docker run -d --rm --name s3-anysync-minio -p 9000:9000 -e MINIO_ROOT_USER=minio -e MINIO_ROOT_PASSWORD=minio12345 quay.io/minio/minio server /data
```

```bash
S3_TEST_ENDPOINT=http://localhost:9000 S3_TEST_KEY=minio S3_TEST_SECRET=minio12345 S3_TEST_BUCKET=s3-anysync-test go test ./...
```

## Bucket permissions

The access key needs `s3:ListBucket` on the bucket and `s3:GetObject`, `s3:PutObject` and `s3:DeleteObject` on its objects.

## Development

`S3_ANYSYNC_SECRETS=file` keeps secrets unencrypted in `<data dir>/secrets.json` (mode 0600) instead of the keychain. Only for development and tests; any other value is an error. Use the same value for the agent and for `wails dev` so both see the same secrets (see Desktop GUI). With this setting the profile form's hint "Stored in the system keychain" does not apply.

## Known limitations

- `restore --to cloud` on a local-to-cloud machine is undone by that machine's next sync.
- After a lost lock, other machines' syncs may fail until the original machine syncs again.
- Whole files are held in memory while hashing, encrypting and transferring; large files are not supported.
- The remote lock is renewed over the network; a long stall can let it expire and another machine take it.
- Restore to local writes the snapshot's files into the mapped local paths (backing up what it overwrites); it is not an exact rollback and does not remove local files that are absent from the snapshot.
- A missing, renamed or unmounted item folder is refused with an error (`ErrRootMissing`) instead of deleting; fix the folder or unmap and map the item again.
- The macOS tray needs a CGO build; a CGO-off build runs headless. "Open" in the tray starts the GUI only when its executable sits next to the agent; otherwise it is hidden.
- The Windows build is a console app, so the HKCU Run autostart opens a console window until Plan 4 ships a `windowsgui` build.
- Autostart is per user, not per data folder: it starts the default data folder. The agent rewrites the entry at start only when the stored executable no longer exists and `S3_ANYSYNC_DATA_DIR` is not set.
- Items cannot contain the agent's own data folder (it would sync the agent's state): `item add`/`item map` refuse such a path, and a sync of an existing item that does is refused with an error.
- On macOS a very large watched tree is only partially watched (cap: 5000 files; the rest is noticed by schedules, cloud polls and manual syncs).
- A slow mass deletion spread over more than the watcher's MaxWait can stay under the delete guard threshold in each run (snapshots keep deletions for 30 days).
- The Windows pieces (named pipe with its owner check, tray, HKCU Run autostart) are cross-compiled but UNTESTED on Windows; the pipe-owner check in particular has never run there.
- The tray, desktop notifications and autostart have not been checked by hand on a real desktop yet (pending).
- The GUI is not covered by automated end-to-end tests (only Go and frontend unit tests); the hand-check of the GUI is performed as part of the release checklist and is not automated.
- Whenever `S3_ANYSYNC_DATA_DIR` is set, the Settings autostart switch is hidden and autostart is refused (CLI and GUI), so a development data folder never touches the real per-user login item.
- If removing an action's passphrase fails after you leave the action, a stale worker can stay in the agent until it restarts.
