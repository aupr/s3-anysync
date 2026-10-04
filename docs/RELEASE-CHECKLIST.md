# Release checklist

A fill-in checklist for one release. Copy it (or tick the boxes in a branch), and keep the filled copy with the release notes. The background and the build details are in [RELEASE.md](RELEASE.md); user-facing install steps are in [INSTALL.md](INSTALL.md).

**Release:** `v____`  **Commit:** `________`  **Date:** `________`  **Checked by:** `________`

Rules for the whole checklist:

- Test the exact artifacts that will ship (Step 0). If anything fails, fix it, delete the local tag (`git tag -d vX.Y.Z`) and start again from Step 0.
- Steps marked **real machine** use your real data folder, Keychain or Credential Manager, login items, tray and notifications. Do them on a machine and account you are happy to change.
- Nothing here needs to be run with `S3_ANYSYNC_DATA_DIR` set, except Step 1.

## Step 0: Build the candidate

- [ ] Working tree is clean and everything is committed (`git status`).
- [ ] Local tag created, not pushed: `git tag -a vX.Y.Z -m "s3-anysync X.Y.Z"`.
- [ ] `scripts/release.sh` finished without errors (it refuses a dirty tree and verifies at the end).
- [ ] `scripts/verify-dist.sh dist X.Y.Z` prints only `ok` lines (`skip ... (stage removed)` lines are fine) and exits 0.
- [ ] `dist/` contains the three artifacts and `SHA256SUMS`:
  - [ ] `s3-anysync-X.Y.Z-macos-universal.dmg`
  - [ ] `s3-anysync-X.Y.Z-macos-universal.zip`
  - [ ] `s3-anysync-X.Y.Z-windows-amd64-installer.exe`
- [ ] Version is right: the macOS app shows `X.Y.Z` in Settings, "App version".

## Step 1: GUI hand-check (temporary data folder and MinIO)

Does not touch your real data. Start MinIO (`docker run -d --name s3-anysync-minio -p 9000:9000 -e MINIO_ROOT_USER=minio -e MINIO_ROOT_PASSWORD=minio12345 quay.io/minio/minio server /data`) and create the bucket `s3-anysync-test`. Run the agent and the GUI with `S3_ANYSYNC_DATA_DIR=<tmp> S3_ANYSYNC_SECRETS=file` and the agent with `run --no-tray --no-notify` (see the README, Desktop GUI). Remove the container and the temp folders afterwards.

- [ ] Startup: header shows "Agent connected"; stopping the agent shows the "not reachable" banner, and it clears when the agent is back.
- [ ] Profiles: MinIO preset fills the endpoint; the saved dialog shows the connection test; edit with a blank secret keeps it; remove works when unused and is refused while an action uses the profile.
- [ ] New action wizard creates an action; items are listed; Sync now shows counts; Activity records it.
- [ ] Rules: the schedule preview shows 3 runs; save works; after the CLI changes the action, the GUI shows "Changed elsewhere" and "Reload and re-apply my edits" keeps both changes.
- [ ] Items: a path overlapping the data folder is refused with a clear message.
- [ ] Conflicts: with both machines changed, the diff shows both texts; Apply stays disabled until every file has a choice; a resolution that deletes asks for confirmation.
- [ ] Delete guard: deleting 5 of 8 files pauses the action ("Deletions to review"); the review lists them; Approve applies them.
- [ ] Snapshots: Restore has no target preselected and is disabled until one is chosen; restoring selected files to this machine works.
- [ ] Cancel: Cancel on a running sync stops it, the state returns to idle and a "Sync cancelled" message shows.
- [ ] Settings: machine name and log level save; the start-at-login switch is hidden with the development folder.
- [ ] Theme: light, dark and system work and survive a reload.
- [ ] Leave action returns to the Actions list.
- [ ] Browser console shows no errors from the app (the Wails dev-mode `ipc.js` message is not ours).

## Step 2: macOS (real machine)

Use a Mac running macOS 13 or newer. Uninstall any earlier s3-anysync first (see INSTALL.md).

- [ ] Open the DMG: it shows `s3-anysync.app` and the Applications link; drag the app to Applications.
- [ ] Gatekeeper first launch:
  - macOS 13–14: right-click → Open → Open.
  - macOS 15 and later: open the app, then System Settings → Privacy & Security → Open Anyway.
  - Alternative on any version: `xattr -dr com.apple.quarantine /Applications/s3-anysync.app` before the first launch.
- [ ] The window opens and shows "Agent connected"; the menu-bar icon appears.
- [ ] Menu-bar menu: Open, Sync all now, Pause all, Quit all work.
- [ ] The first-run question "Start s3-anysync at login?" appears once; answer Yes. Close and reopen the window: it does not ask again.
- [ ] Settings shows the start-at-login switch on; turning it off and on again works.
- [ ] Create a real profile and action against a test bucket; sync works; the secret is in the Keychain (Keychain Access, search `s3-anysync`) and not in the data folder.
- [ ] Cause a conflict: a desktop notification appears and the action pauses.
- [ ] Log out and log in: the agent is running (menu-bar icon present) without opening the window.
- [ ] CLI via the documented symlink (`sudo mkdir -p /usr/local/bin && sudo ln -s /Applications/s3-anysync.app/Contents/MacOS/s3-anysync-agent /usr/local/bin/s3-anysync`): `s3-anysync status` works while the agent runs.
- [ ] Uninstall as in INSTALL.md: turn off start at login, quit from the menu bar, move the app to the Trash, remove the symlink; the login item is gone after the next login; delete the Keychain entry and the data folder if you want a clean machine.

## Step 3: Windows (real machine)

Use Windows 10 or 11, 64-bit, a standard (non-admin) user, and a second user account for the pipe check.

- [ ] Run the installer: there is no administrator prompt. SmartScreen warns: More info → Run anyway.
- [ ] Installed under `%LOCALAPPDATA%\Programs\s3-anysync` with the three executables; a Start-menu shortcut exists; Settings → Apps lists s3-anysync with the right version.
- [ ] Open the app: it starts the agent with **no console window**; the tray icon appears; "Agent connected".
- [ ] The first-run question "Start s3-anysync at login?" appears once; answer Yes.
- [ ] Sign out and in: the agent is running **without a console window**; the tray icon is present.
- [ ] PATH option: re-run the installer with "Add the command-line tool to PATH" ticked; in a **new** terminal `s3-anysync-cli status` works. Also check an account whose user PATH has a single entry (for example only `%USERPROFILE%\AppData\Local\Microsoft\WindowsApps;`): after install, that entry is still intact and the install folder is added after it.
- [ ] Other user PATH entries (including ones with `%VARIABLES%`) are unchanged after install.
- [ ] IPC pipe owner check: while the agent runs for user A, user B cannot connect to its named pipe or run `s3-anysync-cli status` against it (it must fail or report the agent as not running).
- [ ] Tray menu: Open, Sync all now, Pause all, Quit all work; a notification appears for a conflict.
- [ ] Upgrade: run the (same or newer) installer over the existing install while the agent runs: it stops the agent, replaces the files, and the app opens again.
- [ ] Uninstall from Settings → Apps: the agent stops; `HKCU\...\Run` value `s3-anysync` is gone; if the PATH option was used the PATH entry is gone and the other entries are intact; the install folder is gone; `%APPDATA%\s3-anysync` is kept and the final message names it; the WebView2 cache folder `%APPDATA%\s3-anysync.exe` is removed.
- [ ] Silent uninstall: install again, then run `"%LOCALAPPDATA%\Programs\s3-anysync\uninstall.exe" /S`; it finishes without showing a dialog.
- [ ] If the WebView2 runtime is missing, the app offers to download it (only on a machine without it).

## Step 4: Release

- [ ] Every box above is ticked, or each skipped box has a written reason in the release notes.
- [ ] Re-run `scripts/verify-dist.sh dist X.Y.Z` on the final `dist/`.
- [ ] Push the tag: `git push origin vX.Y.Z`.
- [ ] Publish the three files and `SHA256SUMS`; the release notes include the known issues below.

## Known issues for the release notes

- The builds are unsigned: Gatekeeper (ad-hoc signed, not notarized) and SmartScreen warn on first launch.
- The Windows installer and uninstaller force-stop the agent with `taskkill /F` (there is no graceful stop).
- The Windows GUI executable's file version resource shows 0.0.0.
- The minimum macOS version is 13 (Ventura).

## Notes from this run

```
(failures, workarounds, machine and OS versions)
```
