# s3-anysync

Keep application settings files and config folders in sync across macOS and Windows machines through your own S3-compatible bucket (AWS S3, Wasabi, Backblaze B2, Cloudflare R2, MinIO, ...).

Design: [docs/superpowers/specs/2026-10-01-s3-anysync-design.md](docs/superpowers/specs/2026-10-01-s3-anysync-design.md)

## Status

Plan 1 is done: the sync engine and a debug command line. The background agent (tray, schedules, file watching), the GUI and the installers come in later plans.

## Concepts

- **Profile**: a bucket connection. The secret key is read from `S3_ANYSYNC_SECRET` when you add the profile and is stored in the OS keychain (macOS Keychain, Windows Credential Manager), never in the database, logs or bucket.
- **Action**: a named set of items (files or folders) kept in sync under `<prefix>/<actionId>/` in the bucket.
- **Direction**: each machine has its own direction per action: `two-way`, `local-to-cloud` or `cloud-to-local`. The action has a default; `action direction ACTION_ID <dir>` overrides it for this machine (`default` clears the override).
- **Deletes**: by default deletes propagate. `--no-propagate-deletes` stops that; one-way modes never delete a file on the destination that was never synced. With `--receiver-restores-cloud`, a cloud-to-local machine uploads a file back that the cloud deleted (default: it never uploads).
- **Encrypted actions**: `action create --encrypt` encrypts file contents with AES-256-GCM. The passphrase is read from `S3_ANYSYNC_PASSPHRASE` (also for `action join`) and is kept in the keychain.
- **Conflicts**: when both sides changed a file, `sync` reports `conflict: <path>` and leaves both copies alone. `resolve` needs a choice for every conflict: either `--all local|cloud` or one `--path P=local|cloud` per conflicting path.
- **Snapshots and restore**: `snapshot` stores a point-in-time copy of the action in the bucket (a `pre-resolve` snapshot is taken before resolving). `snapshots` lists them; `restore --to local|cloud|both [--path P]... ACTION_ID TS` restores from one.
- **Data dir**: machine id, state and baseline live in `os.UserConfigDir()/s3-anysync`. Set `S3_ANYSYNC_DATA_DIR` to use another folder, which is how you simulate two machines on one computer.

## Build

Requires Go 1.27.

```bash
CGO_ENABLED=0 go build -o bin/s3-anysync-agent ./cmd/agent
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

## Known limitations of Plan 1

- No background agent or GUI yet: syncs run only when you invoke the CLI.
- No mass-delete guard: if a synced folder is emptied locally, the deletes propagate (unless `--no-propagate-deletes`).
- Whole files are held in memory while hashing, encrypting and transferring; large files are not supported.
- The remote lock is renewed over the network; a long stall can let it expire and another machine take it.
- Restore to local writes the snapshot's files into the mapped local paths (backing up what it overwrites); it is not an exact rollback and does not remove local files that are absent from the snapshot.
