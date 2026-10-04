# Installing s3-anysync

Downloads are named `s3-anysync-<version>-macos-universal.dmg` (and `.zip`) and `s3-anysync-<version>-windows-amd64-installer.exe`. The builds are not signed with a purchased certificate, so macOS and Windows warn on first launch (see below).

## macOS

Requires macOS 13 (Ventura) or newer. The app is universal (Apple Silicon and Intel).

1. Open the DMG and drag `s3-anysync.app` to Applications (or unzip the ZIP and move the app).
2. First launch: the app is only ad-hoc signed, not signed with an Apple Developer ID and not notarized, so Gatekeeper asks before it opens. Right-click the app, choose Open, then Open again (once). Or remove the quarantine flag in a terminal:
   ```bash
   xattr -dr com.apple.quarantine /Applications/s3-anysync.app
   ```
3. The window starts the background agent if none is running, and the tray icon appears in the menu bar. Closing the window does not stop syncing; use Quit in the tray menu to stop the agent.

**Command line.** The agent executable is also the command-line tool. To run it as `s3-anysync`:

```bash
sudo ln -s /Applications/s3-anysync.app/Contents/MacOS/s3-anysync-agent /usr/local/bin/s3-anysync
s3-anysync status
```

**Data.** `~/Library/Application Support/s3-anysync` holds settings, sync state and `logs/`. Set `S3_ANYSYNC_DATA_DIR` to use another folder; this is for development only and it disables start at login.

**Uninstall.** Turn off "Start the agent when I log in" in Settings, quit the agent from the tray icon, and move the app to the Trash. Delete the data folder to remove settings and sync state too.

## Windows

Requires 64-bit Windows (amd64).

1. Run `s3-anysync-<version>-windows-amd64-installer.exe`. SmartScreen warns because the installer is not signed: click More info, then Run anyway.
2. The installer is per user and needs no administrator rights. It installs into `%LOCALAPPDATA%\Programs\s3-anysync` and adds a Start menu shortcut.
3. Optional component "Add the command-line tool to PATH" (off by default). It only edits your user PATH; open a new terminal afterwards and run `s3-anysync-cli status`.

Programs installed:

- `s3-anysync.exe`: the window. It starts the agent if none is running.
- `s3-anysync-agent.exe`: the background agent (tray icon, no console window).
- `s3-anysync-cli.exe`: the command-line tool (console). It does not run the agent; start the agent with `s3-anysync-agent.exe run` or the window.

**Data.** `%APPDATA%\s3-anysync` holds settings, sync state and `logs\`. `S3_ANYSYNC_DATA_DIR` overrides it (development only; disables start at login).

**Uninstall.** Settings, Apps, s3-anysync, Uninstall. The uninstaller force-stops the agent and the window (there is no graceful stop), removes the login entry, the PATH entry (if you added it) and the WebView2 cache, and keeps `%APPDATA%\s3-anysync`. Delete that folder to remove settings and sync state too.

## Start at login

The first time the window starts it asks once whether to start s3-anysync at login. You can change it later with the "Start the agent when I log in" switch in Settings. With `S3_ANYSYNC_DATA_DIR` set the switch is hidden and start at login is refused.

## Upgrading

- macOS: quit the agent from the tray icon, then replace the app in Applications.
- Windows: run the new installer over the old one; it stops the running agent and window first.

## Verifying downloads

Publish `SHA256SUMS` next to the files. On macOS, in the download folder:

```bash
shasum -a 256 -c SHA256SUMS
```

On Windows (PowerShell), compare with the matching line of `SHA256SUMS`:

```powershell
Get-FileHash .\s3-anysync-<version>-windows-amd64-installer.exe -Algorithm SHA256
```
