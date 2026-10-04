package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

// opener simulates one machine in the CLI's in-process mode: its own data
// dir and secrets, sharing one bucket with the other machines.
func opener(t *testing.T, store storage.Storage) connector {
	dir, sec := t.TempDir(), secrets.NewMemory()
	return func(ctx context.Context) (api.Service, func(), error) {
		return connectTo(ctx, dir, sec, nil, func(a *app.App) {
			a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
			a.LockSettle = 0
		})
	}
}

func runOK(t *testing.T, open connector, args ...string) string {
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
	if out := runOK(t, a, "sync", act); !strings.Contains(out, "uploaded 1,") {
		t.Fatalf("sync A before conflict:\n%s", out)
	}
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
	// A cloud-to-local machine never uploads its own edits.
	os.WriteFile(filepath.Join(dirB, "settings.json"), []byte(`{"a":"B-local-edit"}`), 0o644)
	if out := runOK(t, b, "sync", act); !strings.Contains(out, "uploaded 0,") {
		t.Fatalf("cloud-to-local B uploaded:\n%s", out)
	}
	if out := runOK(t, a, "sync", act); !strings.Contains(out, "downloaded 0,") {
		t.Fatalf("A should see nothing new:\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(dirA, "settings.json")); string(data) != `{"a":"A"}` {
		t.Fatalf("cloud copy changed, A = %q", data)
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
	if code := run([]string{"profile", "add", "--name", "x", "--bucket", "b", "--access-key", "k"}, &out, &errb, open); code != 1 || !strings.Contains(errb.String(), "S3_ANYSYNC_SECRET") {
		t.Fatalf("missing secret: exit %d, %s", code, errb.String())
	}
}

func TestExplain(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{engine.ErrKeyCheckMissing, "backup of the bucket"},
		{engine.ErrDecrypt, "passphrase"},
		{engine.ErrPassphraseMissing, "re-run the same command"},
		{storage.ErrLocked, "another machine is syncing this action; try again in a minute"},
		{storage.ErrLockLost, "lock"},
		{engine.ErrRootMissing, "unmap the item"},
		{engine.ErrConflictChanged, "list them again with `conflicts` and choose again"},
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
	open := func(context.Context) (api.Service, func(), error) {
		return nil, nil, fmt.Errorf("open: %w", storage.ErrLocked)
	}
	if code := run([]string{"machine"}, &out, &errb, open); code != 1 || !strings.Contains(errb.String(), "try again in a minute") {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestCLIReceiverRestoresCloudFlag(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	open := opener(t, storage.NewMemory())
	p := valueAfter(t, runOK(t, open, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, open, "action", "create", "--profile", p, "--name", "A", "--no-propagate-deletes", "--receiver-restores-cloud"), "action created")
	if out := runOK(t, open, "action", "rules", act); !strings.Contains(out, "receiver restores cloud: yes") || !strings.Contains(out, "propagate deletes: no") {
		t.Fatalf("flags not stored:\n%s", out)
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

func TestPrintResultDeferredAndConflictHint(t *testing.T) {
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

func readFile(t *testing.T, p string) string {
	t.Helper()
	d, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(d)
}

func runFail(t *testing.T, open connector, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb, open); code != 1 {
		t.Fatalf("%v: exit %d, want 1 (%s)", args, code, errb.String())
	}
	return errb.String()
}

func TestCLIRestore(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	open := opener(t, storage.NewMemory())
	p := valueAfter(t, runOK(t, open, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, open, "action", "create", "--profile", p, "--name", "A"), "action created")
	dir := t.TempDir()
	one, two := filepath.Join(dir, "one.txt"), filepath.Join(dir, "two.txt")
	os.WriteFile(one, []byte("one-v1"), 0o644)
	os.WriteFile(two, []byte("two-v1"), 0o644)
	runOK(t, open, "item", "add", "--remote-name", "d", "--type", "dir", "--path", dir, act)
	runOK(t, open, "sync", act)
	ts := strings.Fields(valueAfter(t, runOK(t, open, "snapshot", act), "snapshot taken"))[0]
	os.WriteFile(one, []byte("one-v2"), 0o644)
	os.WriteFile(two, []byte("two-v2"), 0o644)
	if out := runOK(t, open, "sync", act); !strings.Contains(out, "uploaded 2,") {
		t.Fatalf("sync v2:\n%s", out)
	}

	// Flags must come before ACTION_ID TS.
	if msg := runFail(t, open, "restore", act, ts, "--to", "cloud"); !strings.Contains(msg, "expected ACTION_ID TS") {
		t.Fatalf("flags after arguments: %s", msg)
	}
	if msg := runFail(t, open, "restore", "--to", "bogus", act, ts); !strings.Contains(msg, "--to must be local, cloud or both") {
		t.Fatalf("bad --to: %s", msg)
	}
	if msg := runFail(t, open, "restore", act, ts); !strings.Contains(msg, "--to must be") {
		t.Fatalf("missing --to: %s", msg)
	}

	// --to cloud with a repeated --path restores both files in the cloud only.
	out := runOK(t, open, "restore", "--to", "cloud", "--path", "d/one.txt", "--path", "d/two.txt", act, ts)
	if !strings.Contains(out, "restored 2 file(s)") || strings.Contains(out, "note:") {
		t.Fatalf("restore cloud:\n%s", out)
	}
	if got := readFile(t, one); got != "one-v2" {
		t.Fatalf("--to cloud touched local: %q", got)
	}
	if out := runOK(t, open, "sync", act); !strings.Contains(out, "downloaded 2,") {
		t.Fatalf("sync should pull the restored cloud:\n%s", out)
	}
	if got := readFile(t, one) + readFile(t, two); got != "one-v1two-v1" {
		t.Fatalf("after cloud restore: %q", got)
	}

	// --to local with one --path: restores that file only, prints the note.
	os.WriteFile(one, []byte("one-v3"), 0o644)
	os.WriteFile(two, []byte("two-v3"), 0o644)
	out = runOK(t, open, "restore", "--to", "local", "--path", "d/one.txt", act, ts)
	if !strings.Contains(out, "restored 1 file(s)") || !strings.Contains(out, "note: restored locally only; a two-way machine will upload") {
		t.Fatalf("restore local:\n%s", out)
	}
	if got := readFile(t, one); got != "one-v1" {
		t.Fatalf("one.txt = %q", got)
	}
	if got := readFile(t, two); got != "two-v3" {
		t.Fatalf("two.txt must be untouched: %q", got)
	}

	// --to both restores everything on both sides, no note.
	out = runOK(t, open, "restore", "--to", "both", act, ts)
	if !strings.Contains(out, "restored 2 file(s)") || strings.Contains(out, "note:") {
		t.Fatalf("restore both:\n%s", out)
	}
	if got := readFile(t, two); got != "two-v1" {
		t.Fatalf("two.txt = %q", got)
	}
}

func TestCLIResolveStaleChoiceReprintsConflicts(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	store := storage.NewMemory()
	a, b := opener(t, store), opener(t, store)
	pa := valueAfter(t, runOK(t, a, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, a, "action", "create", "--profile", pa, "--name", "A"), "action created")
	dirA, dirB := t.TempDir(), t.TempDir()
	fa, fb := filepath.Join(dirA, "s.json"), filepath.Join(dirB, "s.json")
	os.WriteFile(fa, []byte("v0"), 0o644)
	item := valueAfter(t, runOK(t, a, "item", "add", "--remote-name", "s.json", "--type", "file", "--path", fa, act), "item added")
	runOK(t, a, "sync", act)
	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	runOK(t, b, "action", "join", "--profile", pb, act)
	runOK(t, b, "item", "map", "--path", fb, act, item)
	runOK(t, b, "sync", act)
	os.WriteFile(fa, []byte("A1"), 0o644)
	os.WriteFile(fb, []byte("B1-local"), 0o644)
	runOK(t, a, "sync", act)
	if out := runOK(t, b, "sync", act); !strings.Contains(out, "conflict: s.json") {
		t.Fatalf("no conflict:\n%s", out)
	}
	runOK(t, b, "conflicts", act)
	// Another machine changes the cloud after the conflicts were reviewed.
	os.WriteFile(fa, []byte("A2-newer"), 0o644)
	runOK(t, a, "sync", act)

	out := runOK(t, b, "resolve", "--all", "local", act)
	if !strings.Contains(out, "conflict: s.json") || !strings.Contains(out, "To continue: review the conflicts (conflicts ACTION_ID)") {
		t.Fatalf("stale resolve should print conflicts and the review hint:\n%s", out)
	}
	if got := readFile(t, fb); got != "B1-local" {
		t.Fatalf("stale resolve must not write: %q", got)
	}
}

func TestCLIResolvePathSplitsAtLastEquals(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	store := storage.NewMemory()
	a, b := opener(t, store), opener(t, store)
	pa := valueAfter(t, runOK(t, a, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, a, "action", "create", "--profile", pa, "--name", "A"), "action created")
	dirA, dirB := t.TempDir(), t.TempDir()
	fa, fb := filepath.Join(dirA, "a=b.json"), filepath.Join(dirB, "a=b.json")
	os.WriteFile(fa, []byte("v0"), 0o644)
	item := valueAfter(t, runOK(t, a, "item", "add", "--remote-name", "a=b.json", "--type", "file", "--path", fa, act), "item added")
	runOK(t, a, "sync", act)
	pb := valueAfter(t, runOK(t, b, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	runOK(t, b, "action", "join", "--profile", pb, act)
	runOK(t, b, "item", "map", "--path", fb, act, item)
	runOK(t, b, "sync", act)
	os.WriteFile(fa, []byte("A1"), 0o644)
	os.WriteFile(fb, []byte("B1-local"), 0o644)
	runOK(t, a, "sync", act)
	runOK(t, b, "sync", act)
	runOK(t, b, "resolve", "--path", "a=b.json=cloud", act)
	if got := readFile(t, fb); got != "A1" {
		t.Fatalf("a=b.json = %q", got)
	}
}

func TestCLIEarlyValidation(t *testing.T) {
	t.Setenv("S3_ANYSYNC_SECRET", "secret")
	open := opener(t, storage.NewMemory())
	if msg := runFail(t, open, "profile", "add", "--name", "x", "--bucket", "b"); !strings.Contains(msg, "--access-key is required") {
		t.Fatalf("empty access key: %s", msg)
	}
	p := valueAfter(t, runOK(t, open, "profile", "add", "--name", "t", "--bucket", "b", "--access-key", "k"), "profile created")
	act := valueAfter(t, runOK(t, open, "action", "create", "--profile", p, "--name", "A"), "action created")
	for _, typ := range []string{"", "folder"} {
		msg := runFail(t, open, "item", "add", "--remote-name", "x", "--type", typ, "--path", t.TempDir(), act)
		if !strings.Contains(msg, "--type must be file or dir") {
			t.Fatalf("type %q: %s", typ, msg)
		}
	}
}

func TestCLIAutostartEnableMarksOffered(t *testing.T) {
	dir, sec, fa := t.TempDir(), secrets.NewMemory(), &fakeAutostart{}
	open := func(ctx context.Context) (api.Service, func(), error) {
		return connectTo(ctx, dir, sec, fa, nil)
	}
	runOK(t, open, "autostart", "enable")
	if fa.enables != 1 {
		t.Fatalf("enables = %d", fa.enables)
	}
	svc, closeFn, err := open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if s, err := svc.Settings(context.Background()); err != nil || !s.AutostartOffered {
		t.Fatalf("settings = %+v, %v", s, err)
	}
}

func TestCLIAutostartDisableMarksOffered(t *testing.T) {
	dir, sec, fa := t.TempDir(), secrets.NewMemory(), &fakeAutostart{}
	open := func(ctx context.Context) (api.Service, func(), error) {
		return connectTo(ctx, dir, sec, fa, nil)
	}
	runOK(t, open, "autostart", "disable") // already off: still answers the question
	svc, closeFn, err := open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if s, err := svc.Settings(context.Background()); err != nil || !s.AutostartOffered {
		t.Fatalf("settings = %+v, %v", s, err)
	}
}

func TestUsageForRole(t *testing.T) {
	cli := usageFor("cli")
	if strings.Contains(cli, "run [--no-tray]") || strings.Contains(cli, "s3-anysync-agent") || !strings.Contains(cli, "s3-anysync-cli") {
		t.Fatalf("cli usage:\n%s", cli)
	}
	if !strings.Contains(cli, "  status\n") {
		t.Fatal("cli usage lost status")
	}
	if usageFor("") != usage || !strings.Contains(usage, "run [--no-tray]") {
		t.Fatal("agent usage changed")
	}
}
