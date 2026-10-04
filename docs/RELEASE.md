# Releasing s3-anysync

## Prerequisites (build machine)

The build machine must be a Mac:

- macOS with the Xcode command-line tools
- Go 1.27 and Node 20
- the Wails CLI, pinned: `go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0` (installs to `~/go/bin/wails`; override with `WAILS=/path`)
- `brew install makensis` (Windows installer)
- network access: `npm ci` and the pinned `go-winres` (`go run github.com/tc-hib/go-winres@v0.3.3`) download packages

## Build

```bash
scripts/release.sh               # macOS and Windows
scripts/release.sh --mac-only
scripts/release.sh --windows-only
```

- The version comes from the git tag: on an exact tag `vX.Y.Z` it is `X.Y.Z`; otherwise it is a dev version `0.0.0-dev+<short sha>`.
- Cleaning is per platform: no flag removes all of `dist/`; `--mac-only` removes only `dist/*macos*`; `--windows-only` only `dist/*windows*`. `SHA256SUMS` and the stage directory are always rebuilt, and `SHA256SUMS` covers every `dist/s3-anysync-*` file present.
- `KEEP_STAGE=1 scripts/release.sh` keeps `dist/stage` (the staged executables) for inspection.
- The script never launches the built apps and runs `scripts/verify-dist.sh` at the end.

Outputs in `dist/` (git-ignored):

- `s3-anysync-<ver>-macos-universal.dmg`
- `s3-anysync-<ver>-macos-universal.zip`
- `s3-anysync-<ver>-windows-amd64-installer.exe`
- `SHA256SUMS`

`scripts/verify-dist.sh dist <version>` checks the artifacts without launching anything (every line must start with `ok`). Run it again after the final build.

## Checklist before tagging

1. `scripts/verify-dist.sh` reports only `ok` lines (release.sh runs it).
2. GUI hand-check with a temporary data folder and MinIO. Start an agent and the GUI with `S3_ANYSYNC_DATA_DIR=<tmp> S3_ANYSYNC_SECRETS=file` (see the README, Desktop GUI), then check:
   - startup: "Agent connected"; stopping the agent shows the "not reachable" banner and it clears when the agent is back;
   - profiles: MinIO preset, test dialog, edit with a blank secret, remove;
   - new action wizard, creating an action, items, Sync now, Activity entries;
   - rules: schedule preview, save, and the "Changed elsewhere" dialog when the CLI changed the action;
   - items: a path overlapping the data folder is refused;
   - conflicts: both machines change a file, the diff, Apply disabled until every file has a choice, a resolution that deletes asks to confirm;
   - delete guard: deleting 5 of 8 files pauses the action, review and approve;
   - snapshots: restore with a target, selected files only;
   - cancel a running sync; settings (machine name, log level); theme (light, dark, system) survives a reload; leave an action.
3. macOS on real use (your own account): install from the DMG; Gatekeeper first launch (right-click, Open); tray menu (Open, Sync all now, Pause all, Quit); a desktop notification (cause a conflict); accept the start-at-login prompt, log out and in, the agent is running; uninstall as described in INSTALL.md.
4. Windows on a real machine:
   - install without an administrator prompt; SmartScreen warning (More info, Run anyway);
   - the window opens and starts the agent with no console window; tray icon;
   - start at login: sign out and in, the agent is running without a console window;
   - `s3-anysync-cli status` after choosing the PATH option, also on an account whose user PATH had a single entry (the other entries must survive install and uninstall);
   - IPC pipe owner check: a second user account cannot talk to the agent's pipe;
   - upgrade: run the new installer over an existing install while the agent runs;
   - uninstall: the Run entry and the PATH entry are removed, `%APPDATA%\s3-anysync` is kept; also the silent uninstall `uninstall.exe /S`.
5. Tag and build the release:
   ```bash
   git tag -a vX.Y.Z -m "s3-anysync X.Y.Z"
   scripts/release.sh
   scripts/verify-dist.sh dist X.Y.Z
   ```
   Tag only after steps 1 to 4 pass. Publish the three files and `SHA256SUMS`.

## Known issues for the release notes

- The builds are unsigned: Gatekeeper (ad-hoc signed, not notarized) and SmartScreen warn on first launch.
- The Windows installer and uninstaller force-stop the agent with `taskkill /F` (there is no graceful stop).
- The Windows GUI executable's file version resource shows 0.0.0.
- The minimum macOS version is 13 (Ventura).
