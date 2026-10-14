package engine

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
)

func init() {
	scenarios["two-way propagation"] = scenarioTwoWay
	scenarios["conflict pauses the whole action"] = scenarioConflict
	scenarios["delete propagates after a snapshot"] = scenarioDelete
	scenarios["delete not propagated when disabled"] = scenarioDeleteOff
	scenarios["first run adopts equal files and flags different ones"] = scenarioFirstRun
	scenarios["unmapped items are left alone"] = scenarioUnmapped
	scenarios["per-machine one-way directions"] = scenarioOneWay
	scenarios["encrypted action"] = scenarioEncrypted
	scenarios["lock held by another machine"] = scenarioLockHeld
}

func scenarioTwoWay(t *testing.T, f storeFactory) {
	_, a, b, _ := syncedPair(t, f, testAction("twoway"))
	if got := b.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("B settings = %q", got)
	}
	b.write("app/settings.json", `{"v":22}`)
	expectCounts(t, b.sync(), 1, 0, 0, 0)
	expectCounts(t, a.sync(), 0, 1, 0, 0)
	if got := a.read("app/settings.json"); got != `{"v":22}` {
		t.Fatalf("A settings = %q", got)
	}
	expectCounts(t, a.sync(), 0, 0, 0, 0)
	expectCounts(t, b.sync(), 0, 0, 0, 0)
}

func scenarioConflict(t *testing.T, f storeFactory) {
	h, a, b, act := syncedPair(t, f, testAction("conflict"))
	a.write("app/settings.json", `{"v":2}`)
	b.write("app/settings.json", `{"v":333}`)
	b.write("app/snippets/new.json", "new")
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	res := b.sync()
	if res.Outcome != model.OutcomeConflict || len(res.Conflicts) != 1 || res.Conflicts[0].Path != "settings.json" {
		t.Fatalf("want one conflict on settings.json, got %+v", res)
	}
	if _, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current("snippets/new.json")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("new snippet uploaded while the action is paused (err=%v)", err)
	}
	if saved, err := b.runner.Conflicts(); err != nil || len(saved) != 1 {
		t.Fatalf("saved conflicts = %+v, %v", saved, err)
	}
	if got := b.read("app/settings.json"); got != `{"v":333}` {
		t.Fatalf("local file changed during a conflict: %q", got)
	}
}

func scenarioDelete(t *testing.T, f storeFactory) {
	_, a, b, act := syncedPair(t, f, testAction("delete"))
	a.remove("app/snippets/go.json")
	expectCounts(t, a.sync(), 0, 0, 1, 0)

	snaps, err := a.runner.ListSnapshots(ctx)
	if err != nil || len(snaps) != 1 || snaps[0].Reason != ReasonAutoPreSync || len(snaps[0].Files) != 1 || snaps[0].Files[0].Path != "snippets/go.json" {
		t.Fatalf("safety snapshot = %+v, %v", snaps, err)
	}

	expectCounts(t, b.sync(), 0, 0, 0, 1)
	if b.exists("app/snippets/go.json") {
		t.Fatal("B still has the deleted file")
	}
	backups, _ := filepath.Glob(filepath.Join(b.deps.BackupRoot, act.ID, "*", "snippets", "go.json"))
	if len(backups) != 1 {
		t.Fatalf("local backup missing: %v", backups)
	}
}

func scenarioDeleteOff(t *testing.T, f storeFactory) {
	act := testAction("nodelete")
	act.PropagateDeletes = false
	_, a, _, _ := syncedPair(t, f, act)
	a.remove("app/snippets/go.json")
	expectCounts(t, a.sync(), 0, 1, 0, 0)
	if got := a.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("file not restored: %q", got)
	}
}

func scenarioFirstRun(t *testing.T, f storeFactory) {
	h := newHarness(t, f)
	a, b, c := h.machine("A", ""), h.machine("B", ""), h.machine("C", "")
	a.write("app/settings.json", `{"v":1}`)
	act := h.create(a, testAction("firstrun"))
	a.sync()

	b.write("app/settings.json", `{"v":1}`)
	h.join(b, act.ID, defaultPaths)
	res := b.sync()
	expectCounts(t, res, 0, 0, 0, 0)
	if res.Adopted != 1 {
		t.Fatalf("want 1 adopted, got %s", res.Summary())
	}

	c.write("app/settings.json", `{"v":"other"}`)
	h.join(c, act.ID, defaultPaths)
	if res := c.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("want conflict on first run with different content, got %+v", res)
	}
}

func scenarioUnmapped(t *testing.T, f storeFactory) {
	h := newHarness(t, f)
	a, b := h.machine("A", ""), h.machine("B", "")
	a.write("app/settings.json", `{"v":1}`)
	a.write("app/snippets/go.json", "go")
	act := h.create(a, testAction("unmapped"))
	a.sync()

	h.join(b, act.ID, map[string]string{itemSettings: defaultPaths[itemSettings]})
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if b.exists("app/snippets/go.json") {
		t.Fatal("unmapped item was downloaded")
	}
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	expectCounts(t, a.sync(), 0, 0, 0, 0)
	if _, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current("snippets/go.json")); err != nil {
		t.Fatalf("cloud copy of the unmapped item disappeared: %v", err)
	}
}

func scenarioOneWay(t *testing.T, f storeFactory) {
	h := newHarness(t, f)
	a, b := h.machine("A", ""), h.machine("B", "")
	act := testAction("oneway")
	act.MachineDirections = map[string]model.Direction{"A": model.LocalToCloud, "B": model.CloudToLocal}
	a.write("app/settings.json", `{"v":1}`)
	act = h.create(a, act)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	h.join(b, act.ID, defaultPaths)
	expectCounts(t, b.sync(), 0, 1, 0, 0)

	// The receiver's local edit is replaced by the cloud copy.
	b.write("app/settings.json", `{"v":"local edit"}`)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("receiver kept its edit: %q", got)
	}

	// Files the receiver never synced are neither uploaded nor deleted.
	b.write("app/snippets/mine.json", "mine")
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	if !b.exists("app/snippets/mine.json") {
		t.Fatal("receiver's untracked file was deleted")
	}

	// The source keeps uploading and the receiver follows.
	a.write("app/settings.json", `{"v":2}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":2}` {
		t.Fatalf("receiver = %q", got)
	}
}

func scenarioEncrypted(t *testing.T, f storeFactory) {
	h := newHarness(t, f)
	a := h.machine("A", "correct horse")
	act := testAction("encrypted")
	act.Encrypt = true
	a.write("app/settings.json", `{"secret":"value"}`)
	act = h.create(a, act)
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	body, _, err := h.store.Get(ctx, NewLayout(h.root, act.ID).Current("settings.json"))
	if err != nil || bytes.Contains(body, []byte("value")) {
		t.Fatalf("cloud object is not encrypted (err=%v)", err)
	}

	wrong := h.machine("W", "wrong")
	h.join(wrong, act.ID, defaultPaths)
	if _, err := wrong.runner.Sync(ctx); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("want ErrDecrypt, got %v", err)
	}
	if wrong.exists("app/settings.json") {
		t.Fatal("file written despite a wrong passphrase")
	}

	b := h.machine("B", "correct horse")
	h.join(b, act.ID, defaultPaths)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"secret":"value"}` {
		t.Fatalf("decrypted = %q", got)
	}
}

func scenarioLockHeld(t *testing.T, f storeFactory) {
	h, a, _, act := syncedPair(t, f, testAction("locked"))
	lock := fmt.Sprintf(`{"machineId":"Z","expiresAt":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	if _, err := h.store.Put(ctx, NewLayout(h.root, act.ID).Lock(), []byte(lock), nil); err != nil {
		t.Fatal(err)
	}
	a.write("app/settings.json", `{"v":"changed"}`)
	if _, err := a.runner.Sync(ctx); !errors.Is(err, storage.ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
}
