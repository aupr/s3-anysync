# s3-anysync Plan 4: packaging design

Status: approved 2026-10-04. Builds on `2026-10-01-s3-anysync-design.md` (section 12, phase 4) and `2026-10-04-s3-anysync-gui-design.md`.

## 1. Scope

In scope: a release build script run on macOS producing a universal macOS app (DMG + ZIP) and a Windows per-user NSIS installer; app identity (bundle id, icon, version); the Windows agent/CLI split; first-run autostart prompt; install notes; release checklist; automated verification of the built artifacts.

Out of scope: code signing and notarization, auto-update, Linux, CI.

## 2. Decisions (user-approved)

- Builds run on this Mac; Windows artifacts are cross-built (CGO off) and the installer is compiled with `makensis` (`brew install makensis`, approved).
- Windows uses two executables built from `cmd/agent`: `s3-anysync-agent.exe` (`-H=windowsgui`, no console; used for `run`, login and by the GUI) and `s3-anysync-cli.exe` (console; every other command).
- macOS: one universal `.app` (arm64 + amd64), minimum macOS 13.0, distributed as DMG (drag to Applications) and ZIP.
- Identity: bundle id `com.niceit.s3-anysync`, product name `s3-anysync`, company `niceit`; a generated placeholder icon (cloud with sync arrows) the user can replace.
- macOS CLI: documented symlink, no installer step touches system paths.
- Version: `git describe --tags --exact-match` (`v0.1.0` → `0.1.0`), else `0.0.0-dev+<short sha>`; no tag is created without the user's go-ahead.

## 3. Build (`scripts/release.sh`)

- Bash, `set -euo pipefail`, run from the repo root on macOS; checks tools first (go, node/npm, `~/go/bin/wails`, makensis, lipo, hdiutil, shasum) and fails with a clear message.
- Version injected with `-ldflags "-X main.version=<ver>"` into the agent/CLI (the existing `version` variable in `cmd/agent`) and the GUI (new `version` variable in `cmd/gui`, shown via `Status`/About text as available), into Info.plist (`CFBundleShortVersionString`, `CFBundleVersion`) through `wails.json` `info.productVersion`, and into the NSIS installer.
- Frontend built once (`npm ci && npm run build`).
- macOS:
  - GUI: `wails build -platform darwin/universal -ldflags ...` → `cmd/gui/build/bin/s3-anysync.app`.
  - Agent: `CGO_ENABLED=1 GOOS=darwin GOARCH=arm64` and `GOARCH=amd64` (clang `-arch x86_64`, `CGO_CFLAGS/LDFLAGS -mmacosx-version-min=13.0`), `MACOSX_DEPLOYMENT_TARGET=13.0`, joined with `lipo -create` into `Contents/MacOS/s3-anysync-agent`.
  - `Info.plist`: bundle id, name, versions, `LSMinimumSystemVersion` 13.0, icon.
  - DMG via `hdiutil create` (contents: the .app and an `Applications` symlink); ZIP via `ditto -c -k --keepParent`.
- Windows (`GOOS=windows GOARCH=amd64 CGO_ENABLED=0`):
  - `s3-anysync.exe` GUI via `wails build -platform windows/amd64` (cross-compiled, no `-nsis`); the installer is the project's own NSIS script (section 5), compiled with `makensis`, not the Wails default.
  - `s3-anysync-agent.exe` with `-ldflags "-H=windowsgui -X main.role=agent"`; `s3-anysync-cli.exe` with `-X main.role=cli`.
  - Icon/version resources embedded in all three (Wails does it for the GUI; for the two agent builds a `.syso` generated with `github.com/tc-hib/go-winres` (run through `go run` at a pinned version; the generated files are build outputs, not committed).
- Output in `dist/` (gitignored): `s3-anysync-<ver>-macos-universal.dmg`, `s3-anysync-<ver>-macos-universal.zip`, `s3-anysync-<ver>-windows-amd64-installer.exe`, `SHA256SUMS`.

## 4. Agent roles and autostart target

- `cmd/agent` gets a build-time `role` variable: `""` (default, macOS/dev: one binary does both), `"agent"`, `"cli"`.
- `role == "cli"`: `run` refuses with `use s3-anysync-agent.exe run` (exit 2).
- `role == "agent"` on Windows: CLI commands still work (no console output visible, so they are not advertised); the GUI and login only use `run`.
- Autostart target: always the agent binary. On Windows, when the running executable is the CLI (`role == "cli"`), `defaultAutostart` registers the sibling `s3-anysync-agent.exe` (error if it is missing); otherwise the running executable as today (symlinks resolved).
- The GUI already finds `s3-anysync-agent[.exe]` next to itself; on macOS that is `Contents/MacOS`.

## 5. Windows installer (NSIS, per user)

- `RequestExecutionLevel user`; install dir `$LOCALAPPDATA\Programs\s3-anysync`; Start-menu shortcut to `s3-anysync.exe`; Add/Remove Programs entry under HKCU (`DisplayName`, `DisplayVersion`, `Publisher`, `UninstallString`, `DisplayIcon`, `NoModify`, `NoRepair`).
- Optional component (off by default): add the install dir to the user `PATH` (HKCU `Environment`), with a broadcast of the environment change; removed on uninstall.
- Upgrade: running the installer over an existing install stops the agent first (`taskkill /IM s3-anysync-agent.exe /F`, ignoring "not running") and replaces the files.
- Uninstall: stops the agent, deletes HKCU Run value `s3-anysync`, removes files, shortcut, uninstall entry and PATH entry; keeps the data folder (`%APPDATA%\s3-anysync`) and says so with its path.

## 6. First-run autostart prompt

- New stored setting `autostartOffered` (bool, in the agent's settings table) exposed as `api.Settings.AutostartOffered` (read-write; ignored when autostart is unsupported).
- GUI: after the first successful connection, if `autostartSupported && !autostartOffered && !autostart`, show a one-time dialog "Start s3-anysync at login?" with **Yes** (UpdateSettings `autostart: true, autostartOffered: true`) and **Not now** (`autostartOffered: true`). Never shown again; never shown for dev data folders (autostart unsupported there).
- CLI `autostart enable/disable` also sets `autostartOffered: true`.

## 7. Documentation

- `docs/INSTALL.md` (linked from README): macOS (DMG, Gatekeeper: right-click → Open or `xattr -dr com.apple.quarantine /Applications/s3-anysync.app`, CLI symlink `ln -s /Applications/s3-anysync.app/Contents/MacOS/s3-anysync-agent /usr/local/bin/s3-anysync`, uninstall), Windows (installer, SmartScreen: More info → Run anyway, PATH option, uninstall), data and log locations, upgrading.
- `docs/RELEASE.md`: prerequisites, `scripts/release.sh`, `scripts/verify-dist.sh`, the GUI hand-check script (from Plan 3), the real-hardware checks that only a person can do (Windows: install, IPC pipe owner check, tray, autostart without a console window, CLI, uninstall; macOS: tray, notifications, autostart, Gatekeeper first launch), tagging `vX.Y.Z` only after all checks pass.
- README: replace the "console window" Windows limitation; add Install section pointing to INSTALL.md.

## 8. Testing

- Go unit tests: role handling (`run` refused for cli role), autostart target selection (cli role → sibling agent path; missing sibling → error), `autostartOffered` persistence and API round trip (incl. IPC routes).
- Vitest: first-run prompt (shown once; Yes/Not now payloads; not shown when unsupported or already offered).
- `scripts/verify-dist.sh` (run by release.sh at the end, also standalone): `lipo -archs` lists `x86_64 arm64` for both macOS binaries; Info.plist values (bundle id, versions, `LSMinimumSystemVersion`); both binaries inside the .app; DMG mounts read-only and contains the app (`hdiutil attach -nobrowse -readonly`, then detach); PE subsystem of the Windows executables (GUI for `s3-anysync.exe` and `s3-anysync-agent.exe`, console for `s3-anysync-cli.exe`) via a small Go tool `scripts/pecheck` using `debug/pe`; installer exists and is a PE file; `SHA256SUMS` verifies.
- The built app is never launched by automation (it would use the real data folder, Keychain and login items). The Windows installer is compiled but not run here.
- All existing Go and frontend checks stay green.

## 9. Task order

1. Identity: icon assets, `wails.json` info block, Info.plist template values, version variables in agent/CLI/GUI.
2. Agent roles + autostart target (Windows split) with tests.
3. First-run autostart prompt (agent setting, API, IPC, CLI, GUI) with tests.
4. Release script: frontend + macOS universal app + DMG/ZIP.
5. Windows builds + NSIS per-user installer (+ resources).
6. `verify-dist.sh` + `pecheck`; SHA256SUMS.
7. INSTALL.md, RELEASE.md, README updates; full release dry run (untagged dev version) and verification.

## 10. Amendment: minimum macOS 13 (2026-10-04)

The Go 1.27.1 toolchain builds binaries whose runtime needs macOS 13 (Ventura): the linker reports its object files target macOS 13.0. The user chose to declare macOS 13.0 as the minimum (instead of the originally stated 11.0). `LSMinimumSystemVersion`, `-mmacosx-version-min` / `MACOSX_DEPLOYMENT_TARGET`, `verify-dist.sh` and the docs all say 13.0.
