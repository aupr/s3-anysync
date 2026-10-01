package engine

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

func init() {
	scenarios["missing dir item root is refused"] = scenarioDirRootMissing
	scenarios["recreated empty dir root is consistent"] = scenarioDirRootEmpty
	scenarios["file item deleted with parent present"] = scenarioFileDeletedParentPresent
	scenarios["file item with missing parent is refused"] = scenarioFileParentMissing
}

func (m *machine) removeAll(rel string) {
	m.t.Helper()
	if err := os.RemoveAll(m.path(rel)); err != nil {
		m.t.Fatal(err)
	}
}

func (m *machine) syncErr() error {
	m.t.Helper()
	_, err := m.runner.Sync(ctx)
	return err
}

func scenarioDirRootMissing(t *testing.T, f storeFactory) {
	_, a, b, _ := syncedPair(t, f, testAction("rootmissing"))
	a.removeAll("app/snippets")
	if err := a.syncErr(); !errors.Is(err, ErrRootMissing) {
		t.Fatalf("want ErrRootMissing, got %v", err)
	}
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	if got := b.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("B lost the file: %q", got)
	}
	// The cloud still has it: a fresh run on A after the folder returns restores it.
	a.write("app/snippets/go.json", "go")
	expectCounts(t, a.sync(), 0, 0, 0, 0)
}

func scenarioDirRootEmpty(t *testing.T, f storeFactory) {
	_, a, b, _ := syncedPair(t, f, testAction("rootempty"))
	a.removeAll("app/snippets")
	if err := os.MkdirAll(a.path("app/snippets"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An existing but empty root: the files are legitimately gone.
	expectCounts(t, a.sync(), 0, 0, 1, 0)
	expectCounts(t, b.sync(), 0, 0, 0, 1)
}

func scenarioFileDeletedParentPresent(t *testing.T, f storeFactory) {
	_, a, b, _ := syncedPair(t, f, testAction("filedel"))
	a.remove("app/settings.json")
	expectCounts(t, a.sync(), 0, 0, 1, 0)
	expectCounts(t, b.sync(), 0, 0, 0, 1)
	if b.exists("app/settings.json") {
		t.Fatal("B still has the deleted file")
	}
}

func scenarioFileParentMissing(t *testing.T, f storeFactory) {
	_, a, b, _ := syncedPair(t, f, testAction("fileparent"))
	a.removeAll("app")
	if err := a.syncErr(); !errors.Is(err, ErrRootMissing) {
		t.Fatalf("want ErrRootMissing, got %v", err)
	}
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("B lost the file: %q", got)
	}
}

func init() {
	scenarios["rule edit survives editor clock skew"] = scenarioClockSkew
}

func scenarioClockSkew(t *testing.T, f storeFactory) {
	h, a, b, act := syncedPair(t, f, testAction("skew"))
	a.deps.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	h.updateName(a, act.ID, "from-A")
	// B (normal clock) edits afterwards.
	h.updateName(b, act.ID, "from-B")
	a.sync()
	if got := a.runner.Action().Name; got != "from-B" {
		t.Fatalf("A kept %q, want B's later edit", got)
	}
}

func (h *harness) updateName(m *machine, id, name string) {
	h.t.Helper()
	a, err := UpdateSharedAction(ctx, m.deps, h.root, id, func(a *model.Action) error { a.Name = name; return nil })
	if err != nil {
		h.t.Fatal(err)
	}
	m.attach(a)
}
