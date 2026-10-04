package engine

import (
	"fmt"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func init() {
	scenarios["mass delete waits for approval on each machine"] = scenarioMassDelete
	scenarios["deletes below the guard go through"] = scenarioFewDeletes
	scenarios["new deletions after approval trip the guard again"] = scenarioGuardRetrips
	scenarios["recreated files clear pending deletions"] = scenarioPendingCleared
	scenarios["resolve and approve deletions in either order"] = scenarioResolveThenApprove
}

func snippetPath(i int) string { return fmt.Sprintf("app/snippets/s%02d.json", i) }

func writeSnippets(m *machine, n int) {
	for i := 0; i < n; i++ {
		m.write(snippetPath(i), fmt.Sprintf("snippet %d", i))
	}
}

func syncedSnippets(t *testing.T, f storeFactory, name string, n int) (*harness, *machine, *machine, model.Action) {
	t.Helper()
	h := newHarness(t, f)
	a, b := h.machine("A", ""), h.machine("B", "")
	writeSnippets(a, n)
	act := h.create(a, testAction(name))
	expectCounts(t, a.sync(), n, 0, 0, 0)
	h.join(b, act.ID, defaultPaths)
	expectCounts(t, b.sync(), 0, n, 0, 0)
	return h, a, b, act
}

func scenarioMassDelete(t *testing.T, f storeFactory) {
	h, a, b, act := syncedSnippets(t, f, "massdelete", 8)
	for i := 0; i < 5; i++ {
		a.remove(snippetPath(i))
	}
	res := a.sync()
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 5 {
		t.Fatalf("want 5 pending deletions, got %+v", res)
	}
	expectCounts(t, res, 0, 0, 0, 0)
	if _, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current("snippets/s00.json")); err != nil {
		t.Fatalf("cloud file deleted before approval: %v", err)
	}
	if res := a.sync(); res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("second unapproved sync applied deletions: %+v", res)
	}

	res, err := a.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 5, 0)
	if p, _ := a.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending deletions not cleared: %+v", p)
	}
	if _, err := a.runner.ApproveDeletes(ctx); err == nil {
		t.Fatal("approve with nothing pending accepted")
	}

	// B receives 5 cloud deletions out of 8 synced files: its own guard trips.
	res = b.sync()
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 5 {
		t.Fatalf("B: want 5 pending deletions, got %+v", res)
	}
	if !b.exists(snippetPath(0)) {
		t.Fatal("B deleted a local file before approval")
	}
	res, err = b.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 0, 5)
	if b.exists(snippetPath(0)) {
		t.Fatal("B kept a file after approving its deletion")
	}
}

func scenarioFewDeletes(t *testing.T, f storeFactory) {
	_, a, _, _ := syncedSnippets(t, f, "fewdeletes", 8)
	for i := 0; i < 4; i++ {
		a.remove(snippetPath(i))
	}
	expectCounts(t, a.sync(), 0, 0, 4, 0)

	_, c, _, _ := syncedSnippets(t, f, "quarterdeletes", 20)
	for i := 0; i < 5; i++ {
		c.remove(snippetPath(i))
	}
	expectCounts(t, c.sync(), 0, 0, 5, 0)
}

func scenarioGuardRetrips(t *testing.T, f storeFactory) {
	_, a, _, act := syncedSnippets(t, f, "retrip", 8)
	for i := 0; i < 5; i++ {
		a.remove(snippetPath(i))
	}
	if res := a.sync(); res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("setup: %+v", res)
	}
	a.remove(snippetPath(5))
	res, err := a.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 6 {
		t.Fatalf("want the guard to trip again with 6 paths, got %+v", res)
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p == nil || p.Approved || len(p.Paths) != 6 {
		t.Fatalf("pending after re-trip = %+v", p)
	}
	res, err = a.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 6, 0)
}

func scenarioPendingCleared(t *testing.T, f storeFactory) {
	_, a, _, act := syncedSnippets(t, f, "cleared", 8)
	for i := 0; i < 5; i++ {
		a.remove(snippetPath(i))
	}
	if res := a.sync(); res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("setup: %+v", res)
	}
	writeSnippets(a, 5) // same content as before
	res := a.sync()
	if res.Outcome != model.OutcomeOK {
		t.Fatalf("want ok after recreating the files, got %+v", res)
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending deletions not cleared: %+v", p)
	}
}

func scenarioResolveThenApprove(t *testing.T, f storeFactory) {
	_, a, b, act := syncedSnippets(t, f, "resolveapprove", 8)
	// A conflict on s07 plus five local deletions on B.
	a.write(snippetPath(7), "edit from A")
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	b.write(snippetPath(7), "edit from B!")
	for i := 0; i < 5; i++ {
		b.remove(snippetPath(i))
	}
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("conflicts are checked first, got %+v", res)
	}
	res, err := b.runner.Resolve(ctx, map[string]model.Side{"snippets/s07.json": model.SideLocal})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("resolve must still stop at the delete guard, got %+v", res)
	}
	res, err = b.runner.ApproveDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeConflict {
		t.Fatalf("approval alone cannot settle the conflict, got %+v", res)
	}
	res, err = b.runner.Resolve(ctx, map[string]model.Side{"snippets/s07.json": model.SideLocal})
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 1, 0, 5, 0)
	if cs, _ := b.runner.Conflicts(); len(cs) != 0 {
		t.Fatalf("conflicts left: %+v", cs)
	}
	if p, _ := b.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending deletions left: %+v", p)
	}
}
