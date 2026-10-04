# s3-anysync Plan 4: Packaging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a universal macOS app (DMG + ZIP) and a per-user Windows installer from this Mac, with app identity, the Windows agent/CLI split, a first-run autostart prompt, install notes, a release checklist and automated artifact verification.

**Architecture:** `scripts/release.sh` drives everything: version from git, frontend build, `wails build` for the GUI (darwin/universal and windows/amd64), `go build` for the agent (twice per platform role), `lipo`, `hdiutil`, `go-winres`, `makensis`. `scripts/verify-dist.sh` checks the outputs (with a small Go tool for PE subsystems). Go code changes are small: a build-time `role` in `cmd/agent`, autostart target selection, an `autostartOffered` setting, and a GUI version.

**Tech Stack:** Go 1.27, Wails v2.16, bash, NSIS 3 (`makensis`, brew), `github.com/tc-hib/go-winres` (via `go run`, pinned), macOS `lipo`/`hdiutil`/`ditto`/`codesign`/`PlistBuddy`.

**Spec:** `docs/superpowers/specs/2026-10-04-s3-anysync-packaging-design.md`.

## Global Constraints

- Module `github.com/niceit/s3-anysync`; Go at `/opt/homebrew/bin/go`; work and commit on `main` (user-approved). Every commit ends with a `Co-Authored-By:` trailer line naming the implementing model (the dispatch gives it).
- After every task these must pass: `go test -count=1 -race ./...`, `go vet ./...`, `gofmt -l .` (empty), `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...`, and `cd frontend && npm run typecheck && npm test -- --run`.
- NEVER launch a built app, GUI or agent from `dist/` or `cmd/gui/build/bin/` (it would use the real data folder, Keychain and login items). NEVER run the Windows installer. NEVER create git tags. Tests are hermetic (temp dirs, memory secrets, fake autostart; never call Enable/Disable on a real autostart manager).
- Identity: bundle id `com.niceit.s3-anysync`; product name `s3-anysync`; company `niceit`; minimum macOS `13.0`.
- Version: `git describe --tags --exact-match` → `vX.Y.Z` gives `X.Y.Z`; otherwise `0.0.0-dev+<git rev-parse --short HEAD>`. Numeric form (plist, Windows resources, NSIS) is the part before `-`, with `.0` appended for 4-part Windows versions.
- Windows executables: `s3-anysync.exe` (GUI), `s3-anysync-agent.exe` (`-H=windowsgui`, role `agent`), `s3-anysync-cli.exe` (console, role `cli`). Windows builds are `CGO_ENABLED=0`.
- Outputs in `dist/` (gitignored): `s3-anysync-<ver>-macos-universal.dmg`, `s3-anysync-<ver>-macos-universal.zip`, `s3-anysync-<ver>-windows-amd64-installer.exe`, `SHA256SUMS`.
- Installer: per user (`RequestExecutionLevel user`), install dir `$LOCALAPPDATA\Programs\s3-anysync`, HKCU uninstall key `Software\Microsoft\Windows\CurrentVersion\Uninstall\s3-anysync`, HKCU Run value `s3-anysync` removed on uninstall, data folder `%APPDATA%\s3-anysync` kept.
- Tool installs approved: `brew install makensis`. `~/go/bin/wails` exists. No other global installs.

---

### Task 1: Identity and versions

**Files:**
- Create: `tools/genicon/main.go`, `tools/genicon/main_test.go`, `scripts/version.sh`
- Modify: `cmd/gui/build/appicon.png` (regenerated), `cmd/gui/build/darwin/Info.plist`, `cmd/gui/wails.json`, `cmd/gui/main.go`, `internal/gui/app.go`, `internal/gui/gui_test.go`, `frontend/src/lib/api.ts`, `frontend/src/screens/Settings.tsx`, `frontend/src/screens/Settings.test.tsx`, `frontend/wailsjs/**` (regenerated)

**Interfaces:**
- Produces: `scripts/version.sh` printing two lines: line 1 full version (`0.1.0` or `0.0.0-dev+abc1234`), line 2 numeric version (`0.1.0` or `0.0.0`); `cmd/gui` `var version = "dev"` (set by `-ldflags -X main.version=`); `gui.NewApp(conn *Conn, dlg Dialogs, dataDir, version string) *App` and bound `(*App).Version() string`; frontend `api.version(): Promise<string>`; Settings shows "App version".

- [ ] **Step 1: Icon generator test** (`tools/genicon/main_test.go`):
```go
package main

import (
	"image/color"
	"testing"
)

func TestIconShape(t *testing.T) {
	img := render(256)
	if b := img.Bounds(); b.Dx() != 256 || b.Dy() != 256 {
		t.Fatalf("size %v", b)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatal("corner must be transparent (rounded square)")
	}
	c := color.NRGBAModel.Convert(img.At(128, 40)).(color.NRGBA)
	if c.A != 255 || c.B < 150 {
		t.Fatalf("top centre should be opaque blue background, got %v", c)
	}
	w := color.NRGBAModel.Convert(img.At(int(256*0.36), int(256*0.62))).(color.NRGBA)
	if w.R < 230 || w.G < 230 || w.B < 230 {
		t.Fatalf("cloud area should be white, got %v", w)
	}
}
```
Run `go test ./tools/genicon/` → FAIL (undefined: render).

- [ ] **Step 2: Implement** `tools/genicon/main.go` (pure Go, 4×4 supersampling; writes a PNG):
```go
// Command genicon draws the placeholder s3-anysync icon (a white cloud with
// two sync arrows on a blue rounded square) as a PNG.
//
//	go run ./tools/genicon -size 1024 -o cmd/gui/build/appicon.png
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

func main() {
	size := flag.Int("size", 1024, "edge length in pixels")
	out := flag.String("o", "appicon.png", "output PNG")
	flag.Parse()
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(f, render(*size)); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}

type rgba struct{ r, g, b, a float64 }

// shade returns the colour at a point given in unit coordinates (0..1).
func shade(x, y float64) rgba {
	// Rounded square background, vertical blue gradient.
	const r = 0.18
	dx := math.Max(math.Max(r-x, x-(1-r)), 0)
	dy := math.Max(math.Max(r-y, y-(1-r)), 0)
	if dx*dx+dy*dy > r*r {
		return rgba{}
	}
	top, bottom := rgba{0.15, 0.39, 0.92, 1}, rgba{0.12, 0.25, 0.69, 1}
	c := rgba{top.r + (bottom.r-top.r)*y, top.g + (bottom.g-top.g)*y, top.b + (bottom.b-top.b)*y, 1}

	inCircle := func(cx, cy, rad float64) bool { return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad }
	cloud := inCircle(0.35, 0.59, 0.11) || inCircle(0.47, 0.49, 0.15) || inCircle(0.64, 0.46, 0.17) ||
		inCircle(0.77, 0.59, 0.11) || (x >= 0.35 && x <= 0.77 && y >= 0.48 && y <= 0.70)
	if !cloud {
		return c
	}
	white := rgba{1, 1, 1, 1}
	// Two arcs around the cloud's centre with arrow heads: the sync symbol.
	const cx, cy, rad, thick = 0.56, 0.585, 0.085, 0.026
	d := math.Hypot(x-cx, y-cy)
	ang := math.Atan2(y-cy, x-cx) * 180 / math.Pi // -180..180, 0 = right, 90 = down
	onRing := math.Abs(d-rad) <= thick/2
	arcA := ang >= -150 && ang <= -20 // upper arc
	arcB := ang >= 30 && ang <= 160   // lower arc
	if onRing && (arcA || arcB) {
		return c
	}
	if inTriangle(x, y, headAt(cx, cy, rad, -20, 1)) || inTriangle(x, y, headAt(cx, cy, rad, 160, 1)) {
		return c
	}
	return white
}

type tri [3][2]float64

// headAt returns an arrow head at angle deg on the ring, pointing along the
// direction of increasing angle.
func headAt(cx, cy, rad, deg, dir float64) tri {
	a := deg * math.Pi / 180
	px, py := cx+rad*math.Cos(a), cy+rad*math.Sin(a)
	tx, ty := -math.Sin(a)*dir, math.Cos(a)*dir // tangent
	nx, ny := math.Cos(a), math.Sin(a)          // normal
	const l, w = 0.05, 0.035
	return tri{{px + tx*l, py + ty*l}, {px + nx*w, py + ny*w}, {px - nx*w, py - ny*w}}
}

func inTriangle(x, y float64, t tri) bool {
	sign := func(a, b [2]float64) float64 { return (x-b[0])*(a[1]-b[1]) - (a[0]-b[0])*(y-b[1]) }
	d1, d2, d3 := sign(t[0], t[1]), sign(t[1], t[2]), sign(t[2], t[0])
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

func render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var acc rgba
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := shade((float64(px)+(float64(sx)+0.5)/ss)/float64(size), (float64(py)+(float64(sy)+0.5)/ss)/float64(size))
					acc.r += c.r * c.a
					acc.g += c.g * c.a
					acc.b += c.b * c.a
					acc.a += c.a
				}
			}
			n := float64(ss * ss)
			if acc.a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(acc.r / acc.a * 255)), G: uint8(math.Round(acc.g / acc.a * 255)),
				B: uint8(math.Round(acc.b / acc.a * 255)), A: uint8(math.Round(acc.a / n * 255)),
			})
		}
	}
	return img
}
```
Run the test → PASS. Generate: `go run ./tools/genicon -size 1024 -o cmd/gui/build/appicon.png`. Look at it once with the Read tool (image) and say in the report what it shows; the test pins only coarse properties, the look is a placeholder.

- [ ] **Step 3: Version script** `scripts/version.sh` (executable):
```bash
#!/usr/bin/env bash
# Prints the release version (line 1) and its numeric form (line 2).
# A commit tagged vX.Y.Z gives X.Y.Z; anything else 0.0.0-dev+<sha>.
set -euo pipefail
tag=$(git describe --tags --exact-match 2>/dev/null || true)
if [[ $tag =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  full=${BASH_REMATCH[1]}
else
  full="0.0.0-dev+$(git rev-parse --short HEAD)"
fi
echo "$full"
echo "${full%%-*}"
```
Check: `scripts/version.sh` prints `0.0.0-dev+<sha>` and `0.0.0` (no tag exists; do not create one).

- [ ] **Step 4: Identity in Wails config.** `cmd/gui/wails.json` gains:
```json
  "info": {
    "companyName": "niceit",
    "productName": "s3-anysync",
    "productVersion": "0.0.0",
    "copyright": "niceit",
    "comments": "Sync application settings through S3"
  }
```
`cmd/gui/build/darwin/Info.plist`: `CFBundleIdentifier` → `com.niceit.s3-anysync` (replace `com.wails.{{safeBundleID .Name}}`), `LSMinimumSystemVersion` → `13.0`. Leave the rest (the release script sets the version keys with PlistBuddy).

- [ ] **Step 5: GUI version.** Failing test first in `internal/gui/gui_test.go`: `NewApp(NewConn(ConnOptions{}), nil, "/data", "1.2.3").Version() == "1.2.3"`. Then: `internal/gui/app.go` adds field `version string`, `NewApp(conn *Conn, dlg Dialogs, dataDir, version string) *App`, and
```go
// Version is the GUI's build version.
func (a *App) Version() string { return a.version }
```
Update every `NewApp(` call (tests, `cmd/gui/main.go`). `cmd/gui/main.go`: add `// version is set at build time with -ldflags "-X main.version=...".` and `var version = "dev"`, pass it to `NewApp`. Regenerate bindings: `cd cmd/gui && ~/go/bin/wails generate module`.

- [ ] **Step 6: Frontend.** `api.ts`: `version: () => call<string>('Version'),`. `Settings.tsx`: in the info `<dl>` add `<dt>App version</dt><dd>{appVersion || '—'}</dd>` loaded once with `api.version()` (failure → '—', no toast). Test in `Settings.test.tsx`: `bindings.Version.mockResolvedValue('1.2.3')` → "1.2.3" visible.

- [ ] **Step 7: Verify and commit** (Global Constraints checks).
```bash
git add tools scripts/version.sh cmd/gui internal/gui frontend
git commit -m "feat(packaging): app identity, icon generator, version plumbing" -m "Co-Authored-By: <model line>"
```

---

### Task 2: Agent roles and the autostart target

**Files:**
- Create: `cmd/agent/role.go`, `cmd/agent/role_test.go`
- Modify: `cmd/agent/main.go`, `cmd/agent/connect.go` (only if `version` moves; it stays), `.gitignore`

**Interfaces:**
- Produces: `var role = ""` in `cmd/agent` (set with `-X main.role=agent|cli`); `checkRunAllowed(role string) error`; `autostartTarget(exe, role string) (string, error)`; `agentExeName = "s3-anysync-agent.exe"`.

- [ ] **Step 1: Failing tests** `cmd/agent/role_test.go`:
```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRunAllowed(t *testing.T) {
	if err := checkRunAllowed(""); err != nil {
		t.Fatal(err)
	}
	if err := checkRunAllowed("agent"); err != nil {
		t.Fatal(err)
	}
	if err := checkRunAllowed("cli"); err == nil || !strings.Contains(err.Error(), agentExeName+" run") {
		t.Fatalf("cli role: %v", err)
	}
}

func TestAutostartTarget(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "s3-anysync-cli.exe")
	agentExe := filepath.Join(dir, agentExeName)
	for _, role := range []string{"", "agent"} {
		if got, err := autostartTarget(cli, role); err != nil || got != cli {
			t.Fatalf("role %q: %q, %v", role, got, err)
		}
	}
	if _, err := autostartTarget(cli, "cli"); err == nil {
		t.Fatal("missing sibling agent accepted")
	}
	if err := os.WriteFile(agentExe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := autostartTarget(cli, "cli"); err != nil || got != agentExe {
		t.Fatalf("cli role: %q, %v", got, err)
	}
}
```
Run `go test ./cmd/agent/ -run 'RunAllowed|AutostartTarget'` → FAIL.

- [ ] **Step 2: Implement** `cmd/agent/role.go`:
```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// role is set at build time (-ldflags "-X main.role=..."): "" for the single
// macOS/dev binary, "agent" for the windowless Windows agent, "cli" for the
// Windows console CLI.
var role = ""

// agentExeName is the Windows agent executable installed next to the CLI.
const agentExeName = "s3-anysync-agent.exe"

// checkRunAllowed refuses "run" in the CLI build: the agent must run from the
// windowless executable so login never opens a console window.
func checkRunAllowed(role string) error {
	if role == "cli" {
		return fmt.Errorf("this is the command-line tool; start the agent with %s run", agentExeName)
	}
	return nil
}

// autostartTarget is the executable the login item starts: the running one,
// except for the CLI build, which registers the agent next to it.
func autostartTarget(exe, role string) (string, error) {
	if role != "cli" {
		return exe, nil
	}
	p := filepath.Join(filepath.Dir(exe), agentExeName)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("the agent is not installed next to the command-line tool (%s): %w", p, err)
	}
	return p, nil
}
```
`main.go`: in `main`, before `runAgentCommand`:
```go
	if len(args) > 0 && args[0] == "run" {
		if err := checkRunAllowed(role); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		os.Exit(runAgentCommand(args[1:], os.Stderr))
	}
```
`defaultAutostart`: after resolving symlinks, `target, err := autostartTarget(exe, role); if err != nil { return nil }` and use `target` in `autostart.New(target, []string{"run"})`. `.gitignore`: add `/cmd/agent/*.syso`.

- [ ] **Step 3: Verify and commit**
```bash
git add cmd/agent .gitignore
git commit -m "feat(agent): Windows agent/CLI roles; autostart always targets the agent" -m "Co-Authored-By: <model line>"
```

---

### Task 3: First-run autostart prompt

**Files:**
- Modify: `internal/app/manage.go`, `internal/app/manage_test.go`, `internal/api/api.go`, `internal/agent/service.go`, `internal/agent/manager_test.go`, `frontend/src/lib/types.ts`, `frontend/src/components/Shell.tsx`
- Create: `frontend/src/components/AutostartPrompt.tsx`, `frontend/src/components/AutostartPrompt.test.tsx`

**Interfaces:**
- Produces: `api.Settings.AutostartOffered bool \`json:"autostartOffered"\``; `(*app.App).AutostartOffered() (bool, error)`, `(*app.App).SetAutostartOffered(bool) error` (setting key `autostart_offered`, values `"1"`/absent); `UpdateSettings` stores `AutostartOffered` when it differs and sets it to true whenever the autostart value changes; `Settings()` reports it. Frontend `Settings.autostartOffered: boolean`; `<AutostartPrompt />` rendered once inside `Shell`.

- [ ] **Step 1: Failing Go tests.** `manage_test.go`:
```go
func TestAutostartOffered(t *testing.T) {
	a := newTestApp(t, storage.NewMemory())
	if v, err := a.AutostartOffered(); err != nil || v {
		t.Fatalf("default = %v, %v", v, err)
	}
	if err := a.SetAutostartOffered(true); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.AutostartOffered(); !v {
		t.Fatal("not stored")
	}
	if err := a.SetAutostartOffered(false); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.AutostartOffered(); v {
		t.Fatal("not cleared")
	}
}
```
`manager_test.go` (uses the existing `fakeAutostart` and `newEnv` option pattern from `TestSettings`):
```go
func TestAutostartOfferedThroughSettings(t *testing.T) {
	fa := &fakeAutostart{}
	e := newEnv(t, storage.NewMemory(), func(o *Options) { o.Autostart = fa })
	s, err := e.m.Settings(ctx)
	if err != nil || s.AutostartOffered {
		t.Fatalf("initial = %+v, %v", s, err)
	}
	s.AutostartOffered = true // "Not now"
	if s, err = e.m.UpdateSettings(ctx, s); err != nil || !s.AutostartOffered || s.Autostart {
		t.Fatalf("not now = %+v, %v", s, err)
	}
	e2 := newEnv(t, storage.NewMemory(), func(o *Options) { o.Autostart = &fakeAutostart{} })
	s2, _ := e2.m.Settings(ctx)
	s2.Autostart = true // changing autostart (CLI or Settings) also counts as offered
	if s2, err = e2.m.UpdateSettings(ctx, s2); err != nil || !s2.AutostartOffered || !s2.Autostart {
		t.Fatalf("enable = %+v, %v", s2, err)
	}
}
```
Run → FAIL.

- [ ] **Step 2: Implement.** `manage.go`:
```go
const settingAutostartOffered = "autostart_offered"

// AutostartOffered reports whether the user was already asked about starting at login.
func (a *App) AutostartOffered() (bool, error) {
	v, ok, err := a.DB.GetSetting(settingAutostartOffered)
	return ok && v == "1", err
}

// SetAutostartOffered records whether the user was asked about starting at login.
func (a *App) SetAutostartOffered(v bool) error {
	if !v {
		return a.DB.DeleteSetting(settingAutostartOffered)
	}
	return a.DB.SetSetting(settingAutostartOffered, "1")
}
```
`api.go` `Settings`: add `AutostartOffered bool \`json:"autostartOffered"\` // the first-run "start at login?" question was answered`. `service.go`: `Settings` fills `s.AutostartOffered, err = m.app.AutostartOffered()` (return the error); `UpdateSettings`: compute `offered := s.AutostartOffered || s.Autostart != cur.Autostart` after the autostart step succeeded, and if `offered != cur.AutostartOffered` call `m.app.SetAutostartOffered(offered)` (return error). Update the doc comment of `UpdateSettings`. Check `internal/ipc` routes tests still pass (Settings is passed through as JSON; add `AutostartOffered: true` to the existing UpdateSettings row so the field is pinned).

- [ ] **Step 3: Frontend failing test** `src/components/AutostartPrompt.test.tsx`:
```tsx
import { vi } from 'vitest'
vi.mock('../lib/bridge', () => import('../test/mockBridge'))
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { bindings } from '../test/mockBridge'
import App from '../App'

const base = { machineId: 'm1', machineName: 'mac', autostart: false, autostartSupported: true, autostartOffered: false, logLevel: 'info' }

function boot(settings: object) {
  bindings.ConnectionState.mockResolvedValue({ connected: true })
  bindings.Status.mockResolvedValue({ machineId: 'm1', machineName: 'mac', version: 'v', agent: true, actions: [] })
  bindings.Profiles.mockResolvedValue([])
  bindings.Settings.mockResolvedValue(settings)
  render(<App />)
}

test('asks once and enables on Yes', async () => {
  boot(base)
  bindings.UpdateSettings.mockResolvedValue({ ...base, autostart: true, autostartOffered: true })
  fireEvent.click(await screen.findByRole('button', { name: 'Yes, start at login' }))
  await waitFor(() => expect(bindings.UpdateSettings).toHaveBeenCalledWith({ ...base, autostart: true, autostartOffered: true }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Start s3-anysync at login?' })).toBeNull())
})

test('Not now records the answer without enabling', async () => {
  boot(base)
  bindings.UpdateSettings.mockResolvedValue({ ...base, autostartOffered: true })
  fireEvent.click(await screen.findByRole('button', { name: 'Not now' }))
  await waitFor(() => expect(bindings.UpdateSettings).toHaveBeenCalledWith({ ...base, autostartOffered: true }))
})

test.each([
  ['unsupported', { ...base, autostartSupported: false }],
  ['already answered', { ...base, autostartOffered: true }],
  ['already on', { ...base, autostart: true }],
])('not shown when %s', async (_, s) => {
  boot(s)
  await waitFor(() => expect(bindings.Settings).toHaveBeenCalled())
  expect(screen.queryByRole('dialog', { name: 'Start s3-anysync at login?' })).toBeNull()
})
```

- [ ] **Step 4: Implement** `src/components/AutostartPrompt.tsx` (rendered inside `Shell`, which only renders after the first connection):
```tsx
import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import type { Settings } from '../lib/types'
import { useToast } from './toast'
import { Button, Modal } from './ui'

// AutostartPrompt asks once, after the first connection, whether the agent
// should start at login. The answer is stored by the agent (autostartOffered).
export function AutostartPrompt() {
  const toast = useToast()
  const [settings, setSettings] = useState<Settings | null>(null)
  const [busy, setBusy] = useState(false)
  const asked = useRef(false)
  useEffect(() => {
    if (asked.current) return
    asked.current = true
    let live = true
    api.settings().then((s) => {
      if (live && s.autostartSupported && !s.autostartOffered && !s.autostart) setSettings(s)
    }).catch(() => {}) // the Settings screen reports load errors
    return () => {
      live = false
    }
  }, [])
  if (!settings) return null
  const answer = async (enable: boolean) => {
    setBusy(true)
    try {
      await api.updateSettings({ ...settings, autostart: enable || settings.autostart, autostartOffered: true })
    } catch (e) {
      toast.error(e)
    } finally {
      setBusy(false)
      setSettings(null)
    }
  }
  return (
    <Modal title="Start s3-anysync at login?" onClose={() => (busy ? undefined : setSettings(null))} footer={
      <>
        <Button disabled={busy} onClick={() => answer(false)}>Not now</Button>
        <Button variant="primary" disabled={busy} onClick={() => answer(true)}>Yes, start at login</Button>
      </>
    }>
      <p className="text-sm">The background agent keeps your settings in sync even when this window is closed. You can change this later in Settings.</p>
    </Modal>
  )
}
```
`types.ts` `Settings`: add `autostartOffered: boolean`. `Shell.tsx`: render `<AutostartPrompt />` once (e.g. after `<main>`). Check `api.settings` is called once per app session even under React StrictMode (the `asked` ref); fix `Settings.test.tsx` mocks if they now see an extra `Settings` call (assertions on call counts). Regenerate bindings (`cd cmd/gui && ~/go/bin/wails generate module`) so `models.ts` has the new field.

- [ ] **Step 5: Verify and commit**
```bash
git add internal frontend cmd/gui
git commit -m "feat: ask once whether to start the agent at login" -m "Co-Authored-By: <model line>"
```

---

### Task 4: Release script — frontend and universal macOS app

**Files:**
- Create: `scripts/release.sh`, `scripts/lib.sh`
- Modify: `.gitignore` (add `/dist/`)

**Interfaces:**
- Consumes: `scripts/version.sh`; `cmd/gui` version var; `cmd/agent` `version` (connect.go) and `role`.
- Produces: `scripts/release.sh [--mac-only|--windows-only]` creating `dist/` artifacts; `scripts/lib.sh` functions `need TOOL...`, `log MSG`, `numeric4 VERSION`.

- [ ] **Step 1:** `scripts/lib.sh`:
```bash
#!/usr/bin/env bash
# Helpers shared by the release scripts.
log() { printf '\033[1m==> %s\033[0m\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
need() {
  local t
  for t in "$@"; do command -v "$t" >/dev/null 2>&1 || die "missing tool: $t"; done
}
# numeric4 1.2.3 -> 1.2.3.0 (Windows resources and NSIS need four parts)
numeric4() { echo "$1.0"; }
```

- [ ] **Step 2:** `scripts/release.sh` (executable). The macOS part:
```bash
#!/usr/bin/env bash
# Builds the release artifacts into dist/. Run from the repository root on macOS.
#   scripts/release.sh                 macOS and Windows
#   scripts/release.sh --mac-only
#   scripts/release.sh --windows-only
# Never launches the built apps.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/lib.sh

mode=${1:-all}
[[ $mode == all || $mode == --mac-only || $mode == --windows-only ]] || die "unknown option $mode"
[[ $(uname -s) == Darwin ]] || die "run this on macOS"

WAILS=${WAILS:-$HOME/go/bin/wails}
need go npm git shasum
[[ -x $WAILS ]] || die "wails CLI not found at $WAILS (go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0)"

{ read -r VERSION; read -r NUMERIC; } < <(scripts/version.sh)
log "version $VERSION ($NUMERIC)"
DIST=$PWD/dist
STAGE=$DIST/stage
rm -rf "$DIST"
mkdir -p "$STAGE"
LDVER="-X main.version=$VERSION"

log "frontend"
(cd frontend && npm ci && npm run build)

build_mac() {
  need lipo hdiutil ditto codesign /usr/libexec/PlistBuddy
  export MACOSX_DEPLOYMENT_TARGET=13.0
  log "macOS GUI (universal)"
  (cd cmd/gui && "$WAILS" build -clean -platform darwin/universal -ldflags "$LDVER" -s)
  local app=$STAGE/mac/s3-anysync.app
  mkdir -p "$STAGE/mac"
  ditto cmd/gui/build/bin/s3-anysync.app "$app"

  log "macOS agent (arm64 + amd64)"
  local flags="-mmacosx-version-min=13.0"
  CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CGO_CFLAGS="$flags" CGO_LDFLAGS="$flags" \
    go build -trimpath -ldflags "$LDVER" -o "$STAGE/agent-arm64" ./cmd/agent
  CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="clang -arch x86_64" CGO_CFLAGS="$flags" CGO_LDFLAGS="$flags" \
    go build -trimpath -ldflags "$LDVER" -o "$STAGE/agent-amd64" ./cmd/agent
  lipo -create -output "$app/Contents/MacOS/s3-anysync-agent" "$STAGE/agent-arm64" "$STAGE/agent-amd64"

  local plist=$app/Contents/Info.plist
  /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $NUMERIC" -c "Set :CFBundleVersion $NUMERIC" "$plist"
  /usr/libexec/PlistBuddy -c "Set :CFBundleGetInfoString s3-anysync $VERSION" "$plist"
  # Ad-hoc signature over the whole bundle (Apple Silicon requires a signature;
  # this is not a Developer ID signature, Gatekeeper still asks on first launch).
  codesign --force --deep --sign - "$app"

  log "DMG and ZIP"
  mkdir -p "$STAGE/dmg"
  ditto "$app" "$STAGE/dmg/s3-anysync.app"
  ln -s /Applications "$STAGE/dmg/Applications"
  hdiutil create -volname "s3-anysync $VERSION" -srcfolder "$STAGE/dmg" -ov -format UDZO \
    "$DIST/s3-anysync-$VERSION-macos-universal.dmg"
  (cd "$STAGE/mac" && ditto -c -k --keepParent s3-anysync.app "$DIST/s3-anysync-$VERSION-macos-universal.zip")
}

build_windows() {
  die "Windows build is added in Task 5"
}

case $mode in
  all) build_mac; build_windows ;;
  --mac-only) build_mac ;;
  --windows-only) build_windows ;;
esac

log "checksums"
(cd "$DIST" && shasum -a 256 s3-anysync-* > SHA256SUMS)
rm -rf "$STAGE"
[[ -x scripts/verify-dist.sh ]] && scripts/verify-dist.sh "$DIST" "$VERSION"
log "done: $DIST"
```
(The `version` variable of the agent lives in `cmd/agent/connect.go` as `var version = "dev"`; `-X main.version` sets it. The final `verify-dist.sh` call is a no-op until Task 6 adds the script.)

- [ ] **Step 3: Run** `scripts/release.sh --mac-only`. Expected: `dist/s3-anysync-0.0.0-dev+<sha>-macos-universal.dmg` and `.zip`, `dist/SHA256SUMS`. Check by hand (no launching): `lipo -archs` on both `Contents/MacOS` binaries prints `x86_64 arm64`; `plutil -p .../Info.plist` shows `com.niceit.s3-anysync`, `13.0`, version `0.0.0`; `codesign --verify --deep --strict` on the staged app before it is removed (or on a copy extracted from the zip in a temp dir) succeeds. If the amd64 CGO build fails (e.g. clang cannot target x86_64), report BLOCKED with the exact error. The wails universal build must also honour 13.0 (check `vtool -show-build` or `otool -l | grep -A3 LC_BUILD_VERSION` minos on both slices of `Contents/MacOS/s3-anysync`); if Wails ignores `MACOSX_DEPLOYMENT_TARGET`, pass `CGO_CFLAGS`/`CGO_LDFLAGS` `-mmacosx-version-min=13.0` in the environment of the wails call too, and report.
- [ ] **Step 4:** `.gitignore`: `/dist/`. Commit (`git add scripts .gitignore`; message `build: release script for the universal macOS app`).

---

### Task 5: Windows builds and the per-user installer

**Files:**
- Create: `packaging/windows/s3-anysync.nsi`
- Modify: `scripts/release.sh` (`build_windows`), `cmd/gui/build/windows/*` (Wails-generated `icon.ico`, `info.json`, `wails.exe.manifest` committed), `.gitignore` (`/cmd/agent/*.syso` already from Task 2)

**Interfaces:**
- Consumes: `role` ldflag (Task 2), icon (Task 1), `numeric4` (Task 4).
- Produces: `dist/s3-anysync-<ver>-windows-amd64-installer.exe`.

- [ ] **Step 1:** `brew install makensis` (approved); `makensis -VERSION` in the report.
- [ ] **Step 2:** `build_windows` in `scripts/release.sh`:
```bash
build_windows() {
  need makensis
  local win=$STAGE/win
  mkdir -p "$win"
  log "Windows GUI"
  (cd cmd/gui && "$WAILS" build -clean -platform windows/amd64 -ldflags "$LDVER" -s)
  cp cmd/gui/build/bin/s3-anysync.exe "$win/"

  log "Windows agent and CLI"
  local v4; v4=$(numeric4 "$NUMERIC")
  go run github.com/tc-hib/go-winres@v0.3.3 simply \
    --arch amd64 --out cmd/agent/rsrc \
    --icon cmd/gui/build/windows/icon.ico --manifest cli \
    --product-name s3-anysync --product-version "$v4" --file-version "$v4" \
    --file-description "s3-anysync agent and command-line tool" --copyright niceit
  trap 'rm -f cmd/agent/rsrc_windows_*.syso' RETURN
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "$LDVER -X main.role=agent -H=windowsgui" -o "$win/s3-anysync-agent.exe" ./cmd/agent
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "$LDVER -X main.role=cli" -o "$win/s3-anysync-cli.exe" ./cmd/agent

  log "Windows installer"
  makensis -V2 -DVERSION="$VERSION" -DVERSION4="$v4" -DSRCDIR="$win" \
    -DICON="$PWD/cmd/gui/build/windows/icon.ico" \
    -DOUTFILE="$DIST/s3-anysync-$VERSION-windows-amd64-installer.exe" \
    packaging/windows/s3-anysync.nsi
}
```
(Check `go-winres simply --help` for the exact flag names of v0.3.3 and adjust; keep the pinned version. The `.syso` must be named for `windows_amd64` so only Windows builds link it; it is removed afterwards and gitignored.)
- [ ] **Step 3:** `packaging/windows/s3-anysync.nsi`:
```nsis
; s3-anysync per-user installer. Built by scripts/release.sh:
;   makensis -DVERSION=.. -DVERSION4=.. -DSRCDIR=.. -DICON=.. -DOUTFILE=.. s3-anysync.nsi
Unicode true
!include "MUI2.nsh"
!include "Sections.nsh"

!define APP "s3-anysync"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP}"
!define RUNKEY "Software\Microsoft\Windows\CurrentVersion\Run"

Name "${APP} ${VERSION}"
OutFile "${OUTFILE}"
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\${APP}"
InstallDirRegKey HKCU "${UNINSTKEY}" "InstallLocation"
SetCompressor /SOLID lzma

VIProductVersion "${VERSION4}"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "CompanyName" "niceit"
VIAddVersionKey "FileDescription" "${APP} installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "niceit"

!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_FINISHPAGE_RUN "$INSTDIR\s3-anysync.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open s3-anysync"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

; Stops a running agent and GUI so their files can be replaced or removed.
!macro StopApp
  nsExec::Exec 'taskkill /IM s3-anysync-agent.exe /F'
  Pop $0
  nsExec::Exec 'taskkill /IM s3-anysync.exe /F'
  Pop $0
  Sleep 500
!macroend

Section "${APP} (required)" SecMain
  SectionIn RO
  !insertmacro StopApp
  SetOutPath "$INSTDIR"
  File "${SRCDIR}\s3-anysync.exe"
  File "${SRCDIR}\s3-anysync-agent.exe"
  File "${SRCDIR}\s3-anysync-cli.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\s3-anysync.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "${APP}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "Publisher" "niceit"
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\s3-anysync.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
SectionEnd

; PATH editing goes through PowerShell: NSIS string buffers are limited and
; could truncate a long PATH.
Section /o "Add the command-line tool to PATH" SecPath
  nsExec::ExecToLog `powershell -NoProfile -ExecutionPolicy Bypass -Command "$$d='$INSTDIR'; $$p=[Environment]::GetEnvironmentVariable('Path','User'); $$parts=@(); if ($$p) { $$parts=$$p.Split(';') | Where-Object { $$_ -ne '' } }; if ($$parts -notcontains $$d) { [Environment]::SetEnvironmentVariable('Path', (($$parts + $$d) -join ';'), 'User') }"`
  Pop $0
  WriteRegDWORD HKCU "${UNINSTKEY}" "AddedToPath" 1
SectionEnd

Section "Uninstall"
  !insertmacro StopApp
  DeleteRegValue HKCU "${RUNKEY}" "${APP}"
  ReadRegDWORD $1 HKCU "${UNINSTKEY}" "AddedToPath"
  ${If} $1 == 1
    nsExec::ExecToLog `powershell -NoProfile -ExecutionPolicy Bypass -Command "$$d='$INSTDIR'; $$p=[Environment]::GetEnvironmentVariable('Path','User'); if ($$p) { [Environment]::SetEnvironmentVariable('Path', (($$p.Split(';') | Where-Object { $$_ -ne '' -and $$_ -ne $$d }) -join ';'), 'User') }"`
    Pop $0
  ${EndIf}
  Delete "$INSTDIR\s3-anysync.exe"
  Delete "$INSTDIR\s3-anysync-agent.exe"
  Delete "$INSTDIR\s3-anysync-cli.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APP}.lnk"
  DeleteRegKey HKCU "${UNINSTKEY}"
  MessageBox MB_OK "s3-anysync was removed. Your settings and sync data were kept in $APPDATA\s3-anysync; delete that folder to remove them too."
SectionEnd
```
(`${If}` needs `!include "LogicLib.nsh"`; add it. `$$` escapes a literal `$` for PowerShell inside NSIS. Check that makensis compiles without warnings treated as errors; fix any warning it reports.)
- [ ] **Step 4: Run** `scripts/release.sh --windows-only` → the installer exists; `file dist/*.exe` says PE32+ executable; commit Wails-generated `cmd/gui/build/windows/*`. Do NOT run the installer. Then run `scripts/release.sh` (both) once.
- [ ] **Step 5:** Commit (`git add scripts packaging cmd/gui/build/windows`; message `build: Windows executables and per-user NSIS installer`).

---

### Task 6: Artifact verification

**Files:**
- Create: `tools/pecheck/main.go`, `tools/pecheck/main_test.go`, `scripts/verify-dist.sh`

**Interfaces:**
- Produces: `go run ./tools/pecheck FILE...` printing `FILE: gui|console` per file (exit 1 on a non-PE file); `subsystem(path string) (string, error)`; `scripts/verify-dist.sh DIST VERSION` (exit non-zero on any failed check, prints `ok`/`FAIL` lines).

- [ ] **Step 1: Failing test** `tools/pecheck/main_test.go` (builds two tiny Windows programs with the local Go toolchain into a temp dir):
```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func build(t *testing.T, dir, name string, ldflags string) string {
	t.Helper()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", out, src)
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0", "GO111MODULE=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	return out
}

func TestSubsystem(t *testing.T) {
	dir := t.TempDir()
	gui := build(t, dir, "gui.exe", "-H=windowsgui")
	con := build(t, dir, "con.exe", "")
	if s, err := subsystem(gui); err != nil || s != "gui" {
		t.Fatalf("gui = %q, %v", s, err)
	}
	if s, err := subsystem(con); err != nil || s != "console" {
		t.Fatalf("console = %q, %v", s, err)
	}
	if _, err := subsystem(filepath.Join(dir, "main.go")); err == nil {
		t.Fatal("non-PE accepted")
	}
}
```
- [ ] **Step 2: Implement** `tools/pecheck/main.go`:
```go
// Command pecheck prints the Windows subsystem (gui or console) of PE files.
package main

import (
	"debug/pe"
	"fmt"
	"os"
)

func subsystem(path string) (string, error) {
	f, err := pe.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var sub uint16
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		sub = h.Subsystem
	case *pe.OptionalHeader32:
		sub = h.Subsystem
	default:
		return "", fmt.Errorf("%s: no optional header", path)
	}
	switch sub {
	case pe.IMAGE_SUBSYSTEM_WINDOWS_GUI:
		return "gui", nil
	case pe.IMAGE_SUBSYSTEM_WINDOWS_CUI:
		return "console", nil
	}
	return fmt.Sprintf("subsystem-%d", sub), nil
}

func main() {
	failed := false
	for _, p := range os.Args[1:] {
		s, err := subsystem(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = true
			continue
		}
		fmt.Printf("%s: %s\n", p, s)
	}
	if failed {
		os.Exit(1)
	}
}
```
- [ ] **Step 3:** `scripts/verify-dist.sh`:
```bash
#!/usr/bin/env bash
# Verifies the release artifacts in DIST for VERSION without launching anything.
#   scripts/verify-dist.sh dist 0.1.0
set -uo pipefail
cd "$(dirname "$0")/.."
DIST=${1:?dist dir}; VERSION=${2:?version}; NUMERIC=${VERSION%%-*}
fail=0
ok() { echo "ok   $*"; }
bad() { echo "FAIL $*"; fail=1; }
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then ok "$what"; else bad "$what"; fi; }

dmg=$DIST/s3-anysync-$VERSION-macos-universal.dmg
zip=$DIST/s3-anysync-$VERSION-macos-universal.zip
exe=$DIST/s3-anysync-$VERSION-windows-amd64-installer.exe
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT

if [[ -f $zip ]]; then
  ditto -x -k "$zip" "$tmp/zip"
  app=$tmp/zip/s3-anysync.app
  for b in s3-anysync s3-anysync-agent; do
    archs=$(lipo -archs "$app/Contents/MacOS/$b" 2>/dev/null)
    [[ $archs == *x86_64* && $archs == *arm64* ]] && ok "$b universal ($archs)" || bad "$b archs: $archs"
  done
  pl() { /usr/libexec/PlistBuddy -c "Print :$1" "$app/Contents/Info.plist" 2>/dev/null; }
  [[ $(pl CFBundleIdentifier) == com.niceit.s3-anysync ]] && ok "bundle id" || bad "bundle id $(pl CFBundleIdentifier)"
  [[ $(pl CFBundleShortVersionString) == "$NUMERIC" ]] && ok "version $NUMERIC" || bad "version $(pl CFBundleShortVersionString)"
  [[ $(pl LSMinimumSystemVersion) == 13.0 ]] && ok "minimum macOS 13.0" || bad "minimum macOS $(pl LSMinimumSystemVersion)"
  check "bundle signature (ad-hoc)" codesign --verify --deep --strict "$app"
else
  bad "missing $zip"
fi

if [[ -f $dmg ]]; then
  if hdiutil attach -nobrowse -readonly -mountpoint "$tmp/mnt" "$dmg" >/dev/null; then
    [[ -d $tmp/mnt/s3-anysync.app && -L $tmp/mnt/Applications ]] && ok "dmg contents" || bad "dmg contents"
    hdiutil detach "$tmp/mnt" >/dev/null || bad "dmg detach"
  else
    bad "dmg attach"
  fi
else
  bad "missing $dmg"
fi

if [[ -f $exe ]]; then
  [[ $(go run ./tools/pecheck "$exe" 2>/dev/null) == *": gui" ]] && ok "installer is a PE GUI program" || bad "installer PE check"
else
  bad "missing $exe"
fi
if [[ -d $DIST/stage/win ]]; then
  w=$DIST/stage/win
  [[ $(go run ./tools/pecheck "$w/s3-anysync-agent.exe") == *": gui" ]] && ok "agent.exe has no console" || bad "agent.exe subsystem"
  [[ $(go run ./tools/pecheck "$w/s3-anysync-cli.exe") == *": console" ]] && ok "cli.exe is a console program" || bad "cli.exe subsystem"
  [[ $(go run ./tools/pecheck "$w/s3-anysync.exe") == *": gui" ]] && ok "GUI .exe" || bad "GUI .exe subsystem"
fi

check "SHA256SUMS" bash -c "cd '$DIST' && shasum -a 256 -c SHA256SUMS"
exit $fail
```
In `release.sh`, call `scripts/verify-dist.sh` BEFORE `rm -rf "$STAGE"` (so the Windows executables can be checked) and make a failing verification fail the release: replace the last lines with
```bash
log "checksums"
(cd "$DIST" && shasum -a 256 s3-anysync-* > SHA256SUMS)
scripts/verify-dist.sh "$DIST" "$VERSION"
rm -rf "$STAGE"
log "done: $DIST"
```
- [ ] **Step 4: Run** `go test ./tools/pecheck/` → PASS; `scripts/release.sh` → all `ok`. Commit (`git add tools/pecheck scripts`; message `build: verify release artifacts`).

---

### Task 7: Install notes, release checklist, README, dry run

**Files:**
- Create: `docs/INSTALL.md`, `docs/RELEASE.md`
- Modify: `README.md`

- [ ] **Step 1:** `docs/INSTALL.md` with sections:
  - **macOS:** open the DMG, drag `s3-anysync.app` to Applications. First launch: the app is not signed with an Apple Developer ID, so macOS blocks it; right-click the app → Open → Open (once), or run `xattr -dr com.apple.quarantine /Applications/s3-anysync.app`. The GUI starts the background agent; the tray icon appears in the menu bar. Command line: `sudo ln -s /Applications/s3-anysync.app/Contents/MacOS/s3-anysync-agent /usr/local/bin/s3-anysync`. Data: `~/Library/Application Support/s3-anysync` (logs in `logs/`). Uninstall: turn off "Start the agent when I log in" in Settings, quit the agent from the tray icon, move the app to the Trash; delete the data folder to remove settings and sync state.
  - **Windows:** run `s3-anysync-<version>-windows-amd64-installer.exe`. SmartScreen warns because the installer is not signed: More info → Run anyway. Installs for the current user only into `%LOCALAPPDATA%\Programs\s3-anysync` (no admin rights). Optional: "Add the command-line tool to PATH" (then `s3-anysync-cli status` works in a new terminal). Programs: `s3-anysync.exe` (window), `s3-anysync-agent.exe` (background, no console window), `s3-anysync-cli.exe` (commands). Data: `%APPDATA%\s3-anysync`. Uninstall: Settings → Apps → s3-anysync → Uninstall (stops the agent, removes the login entry and PATH entry; keeps the data folder).
  - **Upgrading:** macOS: quit the agent from the tray, replace the app. Windows: run the new installer (it stops the running agent).
  - **Verifying downloads:** `shasum -a 256 -c SHA256SUMS` / `Get-FileHash`.
- [ ] **Step 2:** `docs/RELEASE.md`: prerequisites (macOS with Xcode command-line tools, Go 1.27, Node 20, `go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`, `brew install makensis`); build `scripts/release.sh` (untagged → dev version); checks before tagging:
  1. `scripts/verify-dist.sh` all ok (run by release.sh).
  2. GUI hand-check with a temporary data folder (the steps from `.superpowers/sdd/p3/handcheck-report.md` summarized: profiles, new action, sync, rules conflict-edit, items protected path, conflicts, delete guard, snapshots restore, cancel, settings, theme, leave) using `S3_ANYSYNC_DATA_DIR=<tmp> S3_ANYSYNC_SECRETS=file` and MinIO.
  3. macOS on real use (your own account, consent implied by doing it): install from the DMG, Gatekeeper first launch, tray menu (Open, Sync, Pause, Quit), a desktop notification (cause a conflict), Start at login prompt → log out/in → agent running; uninstall steps.
  4. Windows on a real machine: install (no admin prompt), SmartScreen, GUI opens and starts the agent with no console window, tray icon, Start at login → sign out/in → agent running without a console window, `s3-anysync-cli status` (with the PATH option), the IPC pipe-owner check (a second user account cannot connect to the agent's pipe), uninstall removes the Run entry and keeps `%APPDATA%\s3-anysync`.
  5. Tag: `git tag -a vX.Y.Z -m "s3-anysync X.Y.Z" && scripts/release.sh` (tag only after 1–4 pass).
- [ ] **Step 3:** README: add an "Install" section linking `docs/INSTALL.md` and "Releasing" linking `docs/RELEASE.md`; remove the Known limitation about the Windows console window; mention `s3-anysync-cli.exe` in the CLI section for Windows.
- [ ] **Step 4: Dry run.** `scripts/release.sh` from a clean tree → all verify lines `ok`; list `dist/` with sizes in the report. Global Constraints checks. Commit (`git add docs README.md`; message `docs: install notes and release checklist`).

---

## Self-review notes

- Spec coverage: §3 → Tasks 4–5; §4 → Task 2; §5 → Task 5; §6 → Task 3; §7 → Task 7; §8 → Tasks 1–3 (unit tests), 6 (verify), 7 (dry run); identity → Task 1.
- GUI version appears in Settings (Task 1); the agent version already appears there.
