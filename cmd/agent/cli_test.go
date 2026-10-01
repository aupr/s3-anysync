package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

// opener simulates one machine: its own data dir and secrets, a shared bucket.
func opener(t *testing.T, store *storage.Memory) func() (*app.App, error) {
	dir, sec := t.TempDir(), secrets.NewMemory()
	return func() (*app.App, error) {
		a, err := app.Open(dir, sec, slog.New(slog.DiscardHandler))
		if err != nil {
			return nil, err
		}
		a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
		a.LockSettle = 0
		return a, nil
	}
}

func runOK(t *testing.T, open func() (*app.App, error), args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb, open); code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, errb.String())
	}
	return out.String()
}

func valueAfter(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, label+": "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no %q line in:\n%s", label, out)
	return ""
}

func TestCLIEndToEnd(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	store := storage.NewMemory()
	a, b := opener(t, store), opener(t, store)

	out := runOK(t, a, "profile", "add", "--name", "test", "--bucket", "bkt", "--access-key", "k", "--prefix", "cfg")
	pa := valueAfter(t, out, "profile created")
	if !strings.Contains(out, "conditional writes") {
		t.Fatalf("probe result not printed:\n%s", out)
	}
	act := valueAfter(t, runOK(t, a, "action", "create", "--profile", pa, "--name", "Editor"), "action created")

	dirA := t.TempDir()
	os.WriteFile(filepath.Join(dirA, "settings.json"), []byte(`{"a":1}`), 0o644)
	item := valueAfter(t, runOK(t, a, "item", "add", "--remote-name", "settings.json", "--type", "file", "--path", filepath.Join(dirA, "settings.json"), act), "item added")
	if out := runOK(t, a, "sync", act); !strings.Contains(out, "uploaded 1,") {
		t.Fatalf("sync A:\n%s", out)
	}
	if out := runOK(t, a, "snapshot", act); !strings.Contains(out, "snapshot taken:") {
		t.Fatalf("snapshot:\n%s", out)
	}
	if out := runOK(t, a, "snapshots", act); !strings.Contains(out, "manual") {
		t.Fatalf("snapshots:\n%s", out)
	}

	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "test", "--bucket", "bkt", "--access-key", "k", "--prefix", "cfg"), "profile created")
	if out := runOK(t, b, "action", "discover", "--profile", pb); !strings.Contains(out, act) {
		t.Fatalf("discover:\n%s", out)
	}
	if out := runOK(t, b, "action", "join", "--profile", pb, act); !strings.Contains(out, "not mapped") {
		t.Fatalf("join should list unmapped items:\n%s", out)
	}
	dirB := t.TempDir()
	runOK(t, b, "item", "map", "--path", filepath.Join(dirB, "settings.json"), act, item)
	if out := runOK(t, b, "sync", act); !strings.Contains(out, "downloaded 1,") {
		t.Fatalf("sync B:\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(dirB, "settings.json")); string(data) != `{"a":1}` {
		t.Fatalf("B file = %q", data)
	}

	// Conflict, list it, resolve it.
	os.WriteFile(filepath.Join(dirA, "settings.json"), []byte(`{"a":"A"}`), 0o644)
	os.WriteFile(filepath.Join(dirB, "settings.json"), []byte(`{"a":"BB"}`), 0o644)
	runOK(t, a, "sync", act)
	if out := runOK(t, b, "sync", act); !strings.Contains(out, "conflict: settings.json") {
		t.Fatalf("conflict not reported:\n%s", out)
	}
	if out := runOK(t, b, "conflicts", act); !strings.Contains(out, "settings.json") {
		t.Fatalf("conflicts:\n%s", out)
	}
	runOK(t, b, "resolve", "--all", "cloud", act)
	if data, _ := os.ReadFile(filepath.Join(dirB, "settings.json")); string(data) != `{"a":"A"}` {
		t.Fatalf("after resolve B = %q", data)
	}
	runOK(t, b, "action", "direction", act, "cloud-to-local")
	if out := runOK(t, b, "action", "list"); !strings.Contains(out, "cloud-to-local") {
		t.Fatalf("action list:\n%s", out)
	}
}

func TestCLIUsageAndErrors(t *testing.T) {
	open := opener(t, storage.NewMemory())
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb, open); code != 2 {
		t.Fatalf("no args: exit %d", code)
	}
	if code := run([]string{"bogus"}, &out, &errb, open); code != 2 {
		t.Fatalf("unknown command: exit %d", code)
	}
	errb.Reset()
	if code := run([]string{"sync"}, &out, &errb, open); code != 1 || !strings.Contains(errb.String(), "ACTION_ID") {
		t.Fatalf("sync without id: exit %d, %s", code, errb.String())
	}
	errb.Reset()
	t.Setenv("S3_ANYSYNC_SECRET", "")
	if code := run([]string{"profile", "add", "--name", "x", "--bucket", "b"}, &out, &errb, open); code != 1 || !strings.Contains(errb.String(), "S3_ANYSYNC_SECRET") {
		t.Fatalf("missing secret: exit %d, %s", code, errb.String())
	}
}

func TestExplain(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{engine.ErrKeyCheckMissing, "keycheck"},
		{engine.ErrDecrypt, "passphrase"},
		{engine.ErrPassphraseMissing, "S3_ANYSYNC_PASSPHRASE"},
		{storage.ErrLocked, "another machine is syncing this action; try again in a minute"},
		{storage.ErrLockLost, "lock"},
	}
	for _, c := range cases {
		wrapped := fmt.Errorf("sync: %w", c.err)
		if got := explain(wrapped); !strings.Contains(got, c.want) {
			t.Errorf("explain(%v) = %q, want it to mention %q", c.err, got, c.want)
		}
	}
	if got := explain(errors.New("plain")); got != "" {
		t.Errorf("explain(plain) = %q, want empty", got)
	}
}

func TestRunPrintsExplanation(t *testing.T) {
	var out, errb bytes.Buffer
	open := func() (*app.App, error) { return nil, fmt.Errorf("open: %w", storage.ErrLocked) }
	if code := run([]string{"machine"}, &out, &errb, open); code != 1 || !strings.Contains(errb.String(), "try again in a minute") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestCLIReceiverRestoresCloudFlag(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	open := opener(t, storage.NewMemory())
	p := valueAfter(t, runOK(t, open, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, open, "action", "create", "--profile", p, "--name", "A", "--no-propagate-deletes", "--receiver-restores-cloud"), "action created")
	a, err := open()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got, err := a.DB.GetAction(act)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReceiverRestoresCloud || got.PropagateDeletes {
		t.Fatalf("flags not stored: %+v", got)
	}
}

func TestCLIConflictResolveStrictAndDeferred(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	store := storage.NewMemory()
	a, b := opener(t, store), opener(t, store)
	pa := valueAfter(t, runOK(t, a, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, a, "action", "create", "--profile", pa, "--name", "A"), "action created")
	dirA, dirB := t.TempDir(), t.TempDir()
	for _, n := range []string{"one.txt", "two.txt"} {
		os.WriteFile(filepath.Join(dirA, n), []byte("v0"), 0o644)
	}
	item := valueAfter(t, runOK(t, a, "item", "add", "--remote-name", "d", "--type", "dir", "--path", dirA, act), "item added")
	runOK(t, a, "sync", act)
	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	runOK(t, b, "action", "join", "--profile", pb, act)
	runOK(t, b, "item", "map", "--path", dirB, act, item)
	runOK(t, b, "sync", act)
	for _, n := range []string{"one.txt", "two.txt"} {
		os.WriteFile(filepath.Join(dirA, n), []byte("A-"+n), 0o644)
		os.WriteFile(filepath.Join(dirB, n), []byte("B-"+n+"!"), 0o644)
	}
	runOK(t, a, "sync", act)
	runOK(t, b, "sync", act)

	// Strict: a partial --path set is an error that names what is missing.
	var out, errb bytes.Buffer
	code := run([]string{"resolve", "--path", "d/one.txt=local", act}, &out, &errb, b)
	if code != 1 || !strings.Contains(errb.String(), "d/two.txt") {
		t.Fatalf("partial resolve: exit %d, %s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"resolve", "--path", "d/one.txt=local", "--path", "d/two.txt=cloud", act}, &out, &errb, b); code != 0 {
		t.Fatalf("full resolve: exit %d, %s", code, errb.String())
	}
	if d, _ := os.ReadFile(filepath.Join(dirB, "one.txt")); string(d) != "B-one.txt!" {
		t.Fatalf("one.txt = %q", d)
	}
	if d, _ := os.ReadFile(filepath.Join(dirB, "two.txt")); string(d) != "A-two.txt" {
		t.Fatalf("two.txt = %q", d)
	}
	// No conflicts left: the engine's error passes through.
	errb.Reset()
	if code := run([]string{"resolve", "--all", "local", act}, &out, &errb, b); code != 1 || !strings.Contains(errb.String(), "no unresolved conflicts") {
		t.Fatalf("resolve with none: exit %d, %s", code, errb.String())
	}
}

func TestPrintResultDeferredAndRestoreNote(t *testing.T) {
	var out bytes.Buffer
	c := &cli{out: &out}
	c.printResult(model.SyncResult{Outcome: model.OutcomeOK, Deferred: []string{"d/x.txt"}})
	if !strings.Contains(out.String(), "  deferred (changed during sync, will be re-planned): d/x.txt") {
		t.Fatalf("deferred not listed:\n%s", out.String())
	}
	out.Reset()
	c.printConflictHint([]model.Conflict{{Path: "d/x.txt"}})
	if !strings.Contains(out.String(), "conflict: d/x.txt") || !strings.Contains(out.String(), "review") {
		t.Fatalf("hint:\n%s", out.String())
	}
}
