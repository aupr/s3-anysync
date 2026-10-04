package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/notify"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func memoryStorage(store storage.Storage) func(*app.App) {
	return func(a *app.App) {
		a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
		a.LockSettle = 0
	}
}

// startAgent runs a real agent (no tray) on a fresh short data dir.
func startAgent(t *testing.T, store storage.Storage) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runAgent(ctx, agentEnv{dataDir: dir, secrets: secrets.NewMemory(), notifier: notify.Nop{}, configure: memoryStorage(store), pollInterval: 200 * time.Millisecond, stderr: io.Discard})
	}()
	waitUntil(t, "the agent to answer", func() bool {
		c, err := ipc.Connect(ctx, dir)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})
	stopped := false
	return dir, func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Errorf("agent stopped with %v", err)
		}
		os.RemoveAll(dir)
	}
}

// cliFor is the real CLI connector for dir: it uses the agent when it runs.
func cliFor(dir string, store storage.Storage) connector {
	return func(ctx context.Context) (api.Service, func(), error) {
		return connectTo(ctx, dir, secrets.NewMemory(), nil, memoryStorage(store))
	}
}

func TestAgentEndToEnd(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	store := storage.NewMemory()
	dirA, stopA := startAgent(t, store)
	defer stopA()
	dirB, stopB := startAgent(t, store)
	defer stopB()
	a, b := cliFor(dirA, store), cliFor(dirB, store)

	if out := runOK(t, a, "status"); !strings.Contains(out, "background agent") {
		t.Fatalf("status:\n%s", out)
	}
	pa := valueAfter(t, runOK(t, a, "profile", "add", "--name", "p", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, a, "action", "create", "--profile", pa, "--name", "Editor"), "action created")
	filesA := filepath.Join(t.TempDir(), "snippets")
	os.MkdirAll(filesA, 0o755)
	os.WriteFile(filepath.Join(filesA, "a.json"), []byte("v1"), 0o644)
	item := valueAfter(t, runOK(t, a, "item", "add", "--remote-name", "snippets", "--type", "dir", "--path", filesA, act), "item added")
	runOK(t, a, "action", "rules", "--on-change", "--debounce", "1", act)
	waitUntil(t, "A's automatic upload", func() bool { return strings.Contains(runOK(t, a, "status"), "uploaded 1") })

	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "p", "--bucket", "b", "--access-key", "k"), "profile created")
	runOK(t, b, "action", "join", "--profile", pb, act)
	filesB := filepath.Join(t.TempDir(), "snippets")
	runOK(t, b, "item", "map", "--path", filesB, act, item)
	waitUntil(t, "B's automatic download", func() bool {
		data, err := os.ReadFile(filepath.Join(filesB, "a.json"))
		return err == nil && string(data) == "v1"
	})

	os.WriteFile(filepath.Join(filesA, "a.json"), []byte("v2 from A"), 0o644)
	waitUntil(t, "the edit to travel A -> cloud -> B", func() bool {
		data, _ := os.ReadFile(filepath.Join(filesB, "a.json"))
		return string(data) == "v2 from A"
	})

	// A second agent on the same data folder is refused.
	err := runAgent(context.Background(), agentEnv{dataDir: dirA, secrets: secrets.NewMemory(), stderr: io.Discard})
	if !errors.Is(err, errAgentRunning) {
		t.Fatalf("second agent: %v", err)
	}

	// With no agent running on a folder, the CLI works in-process there.
	stopA()
	reopened, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(reopened)
	if out := runOK(t, cliFor(reopened, store), "status"); !strings.Contains(out, "this process") {
		t.Fatalf("status without agent:\n%s", out)
	}
}

func TestRunCommandFlags(t *testing.T) {
	var errb bytes.Buffer
	if code := runAgentCommand([]string{"--bogus"}, &errb); code != 2 {
		t.Fatalf("bad flag: exit %d (%s)", code, errb.String())
	}
}

type fakeAutostart struct {
	enabled bool
	enables int
	err     error
}

func (f *fakeAutostart) Enable() error          { f.enables++; return f.err }
func (f *fakeAutostart) Disable() error         { return nil }
func (f *fakeAutostart) Enabled() (bool, error) { return f.enabled, nil }

func runBriefly(t *testing.T, env agentEnv) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runAgent(ctx, env) }()
	waitUntil(t, "the agent to answer", func() bool {
		c, err := ipc.Connect(ctx, env.dataDir)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent: %v", err)
	}
}

func TestAgentRefreshesEnabledAutostart(t *testing.T) {
	short, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(short)
	on := &fakeAutostart{enabled: true}
	runBriefly(t, agentEnv{dataDir: short, secrets: secrets.NewMemory(), notifier: notify.Nop{}, autostart: on, stderr: io.Discard})
	if on.enables != 1 {
		t.Fatalf("enabled autostart refreshed %d times, want 1", on.enables)
	}
	off := &fakeAutostart{}
	runBriefly(t, agentEnv{dataDir: short, secrets: secrets.NewMemory(), notifier: notify.Nop{}, autostart: off, stderr: io.Discard})
	if off.enables != 0 {
		t.Fatalf("disabled autostart was enabled")
	}
	// A failing refresh is logged, not fatal.
	bad := &fakeAutostart{enabled: true, err: errors.New("boom")}
	runBriefly(t, agentEnv{dataDir: short, secrets: secrets.NewMemory(), notifier: notify.Nop{}, autostart: bad, stderr: io.Discard})
}

func TestAgentStopLeavesNoSocketAndReleasesLock(t *testing.T) {
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	runBriefly(t, agentEnv{dataDir: dir, secrets: secrets.NewMemory(), notifier: notify.Nop{}, stderr: io.Discard})
	if _, err := os.Stat(filepath.Join(dir, "agent.sock")); runtime.GOOS != "windows" && !os.IsNotExist(err) {
		t.Fatalf("agent.sock left behind: %v", err)
	}
	l, err := datalock.Acquire(dir)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	l.Release()
}

func TestSettingsLogLevelReachesAgentLogger(t *testing.T) {
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	level := new(slog.LevelVar)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runAgent(ctx, agentEnv{dataDir: dir, secrets: secrets.NewMemory(), notifier: notify.Nop{}, level: level, stderr: io.Discard})
	}()
	var c *ipc.Client
	waitUntil(t, "the agent to answer", func() bool {
		var err error
		c, err = ipc.Connect(ctx, dir)
		return err == nil
	})
	defer c.Close()
	s, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.LogLevel = "debug"
	if _, err := c.UpdateSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	if level.Level() != slog.LevelDebug {
		t.Fatalf("agent log level = %v, want debug", level.Level())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBusyFolderMessage(t *testing.T) {
	if !strings.Contains(explain(errAgentRunning), "retry") {
		t.Fatalf("no hint: %q", explain(errAgentRunning))
	}
}
