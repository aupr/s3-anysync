package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

// cliSetup creates a profile, an action and a dir item with n files on m.
func cliSetup(t *testing.T, m connector, n int) (actionID, itemID, dir string) {
	t.Helper()
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	p := valueAfter(t, runOK(t, m, "profile", "add", "--name", "p", "--bucket", "b", "--access-key", "k"), "profile created")
	actionID = valueAfter(t, runOK(t, m, "action", "create", "--profile", p, "--name", "Editor"), "action created")
	dir = filepath.Join(t.TempDir(), "snippets")
	os.MkdirAll(dir, 0o755)
	for i := 0; i < n; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("s%02d.json", i)), []byte(fmt.Sprintf("snippet %d", i)), 0o644)
	}
	itemID = valueAfter(t, runOK(t, m, "item", "add", "--remote-name", "snippets", "--type", "dir", "--path", dir, actionID), "item added")
	return
}

func TestCLIStatusPauseResumeHistory(t *testing.T) {
	m := opener(t, storage.NewMemory())
	act, _, _ := cliSetup(t, m, 1)
	runOK(t, m, "sync", act)
	out := runOK(t, m, "status")
	if !strings.Contains(out, "in this process") || !strings.Contains(out, "Editor") || !strings.Contains(out, "idle") {
		t.Fatalf("status:\n%s", out)
	}
	if out := runOK(t, m, "pause", act); !strings.Contains(out, "paused") {
		t.Fatalf("pause:\n%s", out)
	}
	if out := runOK(t, m, "status"); !strings.Contains(out, "paused") {
		t.Fatalf("status after pause:\n%s", out)
	}
	runOK(t, m, "resume", act)
	if out := runOK(t, m, "history", "--limit", "5", act); !strings.Contains(out, "sync") || !strings.Contains(out, "ok") {
		t.Fatalf("history:\n%s", out)
	}
}

func TestCLIRules(t *testing.T) {
	m := opener(t, storage.NewMemory())
	act, _, _ := cliSetup(t, m, 1)
	out := runOK(t, m, "action", "rules", "--on-change", "--schedule", "daily:09:30", "--exclude", "*.log", "--delete-guard-files", "10", "--receiver-restores-cloud", act)
	for _, want := range []string{"rules updated", "on change: yes", "daily at 09:30", "next runs:", "excludes: *.log", "delete guard: 10 file(s) and 50%", "receiver restores cloud: yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("rules output lacks %q:\n%s", want, out)
		}
	}
	if out := runOK(t, m, "action", "rules", "--on-change=false", "--no-excludes", act); !strings.Contains(out, "on change: no") || !strings.Contains(out, "excludes: none") {
		t.Fatalf("second update:\n%s", out)
	}
	if out := runFail(t, m, "action", "rules", "--schedule", "daily:9:30", act); !strings.Contains(out, "HH:MM") {
		t.Fatalf("invalid schedule:\n%s", out)
	}
	if out := runOK(t, m, "action", "rules", act); !strings.Contains(out, "name: Editor") {
		t.Fatalf("show rules:\n%s", out)
	}
}

func TestCLIApproveDeletes(t *testing.T) {
	m := opener(t, storage.NewMemory())
	act, _, dir := cliSetup(t, m, 8)
	runOK(t, m, "sync", act)
	for i := 0; i < 5; i++ {
		os.Remove(filepath.Join(dir, fmt.Sprintf("s%02d.json", i)))
	}
	out := runOK(t, m, "sync", act)
	if !strings.Contains(out, "deletes-pending") || !strings.Contains(out, "pending deletion: snippets/s00.json") || !strings.Contains(out, "approve-deletes") {
		t.Fatalf("sync:\n%s", out)
	}
	if out := runOK(t, m, "approve-deletes", act); strings.Count(out, "pending deletion:") != 5 || !strings.Contains(out, "approve-deletes --yes") {
		t.Fatalf("list without --yes must not approve:\n%s", out)
	}
	if out := runOK(t, m, "approve-deletes", "--yes", act); !strings.Contains(out, "deleted in cloud 5") {
		t.Fatalf("approve:\n%s", out)
	}
	if out := runOK(t, m, "approve-deletes", "--yes", act); !strings.Contains(out, "no deletions are waiting") {
		t.Fatalf("nothing pending:\n%s", out)
	}
}

func TestCLIDiffAndItemsAndLeave(t *testing.T) {
	store := storage.NewMemory()
	a, b := opener(t, store), opener(t, store)
	act, item, dirA := cliSetup(t, a, 1)
	runOK(t, a, "sync", act)
	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "p", "--bucket", "b", "--access-key", "k"), "profile created")
	runOK(t, b, "action", "join", "--profile", pb, act)
	dirB := filepath.Join(t.TempDir(), "snippets")
	runOK(t, b, "item", "map", "--path", dirB, act, item)
	runOK(t, b, "sync", act)
	os.WriteFile(filepath.Join(dirA, "s00.json"), []byte("from A"), 0o644)
	runOK(t, a, "sync", act)
	os.WriteFile(filepath.Join(dirB, "s00.json"), []byte("from B!"), 0o644)
	runOK(t, b, "sync", act)
	out := runOK(t, b, "diff", act, "snippets/s00.json")
	if !strings.Contains(out, "from B!") || !strings.Contains(out, "from A") || !strings.Contains(out, "this machine") {
		t.Fatalf("diff:\n%s", out)
	}
	runOK(t, b, "resolve", "--all", "cloud", act)

	if out := runOK(t, b, "item", "unmap", act, item); !strings.Contains(out, "unmapped") {
		t.Fatalf("unmap:\n%s", out)
	}
	if out := runOK(t, a, "item", "remove", act, item); !strings.Contains(out, "removed") {
		t.Fatalf("remove:\n%s", out)
	}
	if out := runOK(t, b, "action", "leave", act); !strings.Contains(out, "left action") {
		t.Fatalf("leave:\n%s", out)
	}
	if out := runOK(t, b, "action", "list"); strings.Contains(out, act) {
		t.Fatalf("action still listed after leave:\n%s", out)
	}
}

func TestCLISettingsAndAutostartUnsupported(t *testing.T) {
	m := opener(t, storage.NewMemory())
	if out := runOK(t, m, "settings", "--log-level", "debug"); !strings.Contains(out, "log level: debug") {
		t.Fatalf("settings:\n%s", out)
	}
	if out := runOK(t, m, "autostart", "status"); !strings.Contains(out, "not supported") {
		t.Fatalf("autostart status:\n%s", out)
	}
	if out := runFail(t, m, "autostart", "enable"); !strings.Contains(out, "not supported") {
		t.Fatalf("autostart enable:\n%s", out)
	}
}

// TestCLIThroughTheAgentOverIPC plays the agent: it holds the data-folder lock
// and serves a Manager over IPC. The CLI side goes through connectTo itself, so
// the ErrBusy -> ipc.Connect branch is what is exercised.
func TestCLIThroughTheAgentOverIPC(t *testing.T) {
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	lock, err := datalock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	a, err := app.Open(dir, secrets.NewMemory(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewMemory()
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
	a.LockSettle = 0
	m, err := agent.New(agent.Options{App: a, Triggers: true, Version: "v-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	tok, err := ipc.NewToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer(m, tok, nil)
	go srv.Serve(l)
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		ipc.Cleanup(dir)
		m.Close()
		a.Close()
	}()
	// The CLI's own secrets/configure are never used: the lock is taken, so
	// connectTo must hand back the IPC client.
	remote := func(ctx context.Context) (api.Service, func(), error) {
		return connectTo(ctx, dir, secrets.NewMemory(), nil, func(*app.App) { t.Error("configure called: the in-process branch was taken") })
	}
	if out := runOK(t, remote, "status"); !strings.Contains(out, "background agent v-test") {
		t.Fatalf("status over IPC:\n%s", out)
	}
	act, _, _ := cliSetup(t, remote, 1)
	if out := runOK(t, remote, "sync", act); !strings.Contains(out, "uploaded 1,") {
		t.Fatalf("sync over IPC:\n%s", out)
	}
}

// TestConnectToAgentNotAnswering: the lock is held but nothing listens.
func TestConnectToAgentNotAnswering(t *testing.T) {
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	lock, err := datalock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = connectTo(ctx, dir, secrets.NewMemory(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "does not answer") {
		t.Fatalf("err = %v", err)
	}
}
