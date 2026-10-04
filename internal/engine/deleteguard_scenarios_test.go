package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func init() {
	scenarios["mass delete waits for approval on each machine"] = scenarioMassDelete
	scenarios["deletes below the guard go through"] = scenarioFewDeletes
	scenarios["new deletions after approval trip the guard again"] = scenarioGuardRetrips
	scenarios["recreated files clear pending deletions"] = scenarioPendingCleared
	scenarios["approving a different list approves nothing"] = scenarioApproveOtherList
	scenarios["a larger pending set needs a fresh approval"] = scenarioApproveStaleList
	scenarios["approval ignores order and duplicates"] = scenarioApproveOrderDups
	scenarios["a conflict clears a saved approval"] = scenarioConflictClearsApproval
	scenarios["approval survives a failed apply"] = scenarioApprovalSurvivesFailure
	scenarios["stale approval cleared below the threshold"] = scenarioStaleApprovalCleared
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

	res, err := a.runner.ApproveDeletes(ctx, res.PendingDeletes)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 5, 0)
	if p, _ := a.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending deletions not cleared: %+v", p)
	}
	if _, err := a.runner.ApproveDeletes(ctx, nil); !errors.Is(err, ErrInvalidInput) {
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
	res, err = b.runner.ApproveDeletes(ctx, res.PendingDeletes)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 0, 5)
	if b.exists(snippetPath(0)) {
		t.Fatal("B kept a file after approving its deletion")
	}
}

func scenarioFewDeletes(t *testing.T, f storeFactory) {
	_, a, _, act := syncedSnippets(t, f, "fewdeletes", 8)
	for i := 0; i < 4; i++ {
		a.remove(snippetPath(i))
	}
	expectCounts(t, a.sync(), 0, 0, 4, 0)
	if p, _ := a.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending record saved for a small delete: %+v", p)
	}

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
	first := a.sync()
	if first.Outcome != model.OutcomeDeletesPending {
		t.Fatalf("setup: %+v", first)
	}
	a.remove(snippetPath(5))
	res, err := a.runner.ApproveDeletes(ctx, first.PendingDeletes)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 6 {
		t.Fatalf("want the guard to trip again with 6 paths, got %+v", res)
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p == nil || p.Approved || len(p.Paths) != 6 {
		t.Fatalf("pending after re-trip = %+v", p)
	}
	res, err = a.runner.ApproveDeletes(ctx, res.PendingDeletes)
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
	res, err = b.runner.ApproveDeletes(ctx, res.PendingDeletes)
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

func tripFive(t *testing.T, a *machine) model.SyncResult {
	t.Helper()
	for i := 0; i < 5; i++ {
		a.remove(snippetPath(i))
	}
	res := a.sync()
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 5 {
		t.Fatalf("setup: want 5 pending, got %+v", res)
	}
	return res
}

func cloudHas(h *harness, act model.Action, p string) bool {
	_, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current(p))
	return err == nil
}

func scenarioApproveOtherList(t *testing.T, f storeFactory) {
	h, a, _, act := syncedSnippets(t, f, "otherlist", 8)
	saved := tripFive(t, a).PendingDeletes
	for name, reviewed := range map[string][]string{
		"subset":   saved[:4],
		"superset": append(append([]string{}, saved...), snippetPath(7)),
		"empty":    nil,
	} {
		res, err := a.runner.ApproveDeletes(ctx, reviewed)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Outcome != model.OutcomeDeletesPending || !reflect.DeepEqual(res.PendingDeletes, saved) {
			t.Fatalf("%s: want the saved list back, got %+v", name, res)
		}
		if p, _ := a.db.LoadPendingDeletes(act.ID); p == nil || p.Approved || len(p.Paths) != 5 {
			t.Fatalf("%s: pending = %+v", name, p)
		}
		if !cloudHas(h, act, "snippets/s00.json") {
			t.Fatalf("%s: cloud file deleted", name)
		}
	}
}

func scenarioApproveStaleList(t *testing.T, f storeFactory) {
	h, a, _, act := syncedSnippets(t, f, "stalelist", 10)
	old := tripFive(t, a).PendingDeletes
	for i := 5; i < 8; i++ {
		a.remove(snippetPath(i))
	}
	res := a.sync()
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 8 {
		t.Fatalf("want the guard to re-trip with 8, got %+v", res)
	}
	fresh := res.PendingDeletes
	res, err := a.runner.ApproveDeletes(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeDeletesPending || !reflect.DeepEqual(res.PendingDeletes, fresh) {
		t.Fatalf("want the 8 saved paths back, got %+v", res)
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p == nil || p.Approved {
		t.Fatalf("stale approval accepted: %+v", p)
	}
	if !cloudHas(h, act, "snippets/s00.json") || !cloudHas(h, act, "snippets/s07.json") {
		t.Fatal("deletions applied on a stale approval")
	}
	res, err = a.runner.ApproveDeletes(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 8, 0)
}

func scenarioApproveOrderDups(t *testing.T, f storeFactory) {
	_, a, _, _ := syncedSnippets(t, f, "orderdups", 8)
	saved := tripFive(t, a).PendingDeletes
	var reviewed []string
	for i := len(saved) - 1; i >= 0; i-- {
		reviewed = append(reviewed, saved[i], saved[i])
	}
	res, err := a.runner.ApproveDeletes(ctx, reviewed)
	if err != nil {
		t.Fatal(err)
	}
	expectCounts(t, res, 0, 0, 5, 0)
}

func scenarioConflictClearsApproval(t *testing.T, f storeFactory) {
	_, a, b, act := syncedSnippets(t, f, "conflictclears", 8)
	saved := tripFive(t, b).PendingDeletes
	// Approved, but the apply fails: the approval stays saved.
	b.runner.d.Storage = &failDeleteOnce{Storage: b.h.store}
	if _, err := b.runner.ApproveDeletes(ctx, saved); err == nil {
		t.Fatal("want the failed delete reported")
	}
	if p, _ := b.db.LoadPendingDeletes(act.ID); p == nil || !p.Approved {
		t.Fatalf("setup: approval not kept: %+v", p)
	}
	// A new conflict arises and stops the next run.
	a.write(snippetPath(7), "edit from A")
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	b.write(snippetPath(7), "edit from B!")
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("want conflict, got %+v", res)
	}
	if p, _ := b.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending left after a conflict: %+v", p)
	}
	res, err := b.runner.Resolve(ctx, map[string]model.Side{"snippets/s07.json": model.SideLocal})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeDeletesPending || len(res.PendingDeletes) != 5 {
		t.Fatalf("want the guard to trip anew, got %+v", res)
	}
}

type failDeleteOnce struct {
	storage.Storage
	failed bool
}

func (s *failDeleteOnce) Delete(c context.Context, key string) error {
	if !s.failed {
		s.failed = true
		return errors.New("boom delete")
	}
	return s.Storage.Delete(c, key)
}

func scenarioApprovalSurvivesFailure(t *testing.T, f storeFactory) {
	_, a, _, act := syncedSnippets(t, f, "survivesfail", 8)
	saved := tripFive(t, a).PendingDeletes
	a.runner.d.Storage = &failDeleteOnce{Storage: a.h.store}
	if _, err := a.runner.ApproveDeletes(ctx, saved); err == nil {
		t.Fatal("want the failed delete reported")
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p == nil || !p.Approved {
		t.Fatalf("approval lost after a failed apply: %+v", p)
	}
	res := a.sync() // retry is covered by the saved approval
	if res.Outcome != model.OutcomeOK {
		t.Fatalf("retry stopped: %+v", res)
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("pending left after the retry: %+v", p)
	}
}

func scenarioStaleApprovalCleared(t *testing.T, f storeFactory) {
	_, a, _, act := syncedSnippets(t, f, "staleapproval", 8)
	saved := tripFive(t, a).PendingDeletes
	a.runner.d.Storage = &failDeleteOnce{Storage: a.h.store}
	if _, err := a.runner.ApproveDeletes(ctx, saved); err == nil {
		t.Fatal("want the failed delete reported")
	}
	writeSnippets(a, 5) // the files come back: the next plan has no deletions
	if res := a.sync(); res.Outcome != model.OutcomeOK {
		t.Fatalf("got %+v", res)
	}
	if p, _ := a.db.LoadPendingDeletes(act.ID); p != nil {
		t.Fatalf("stale approved record kept: %+v", p)
	}
}
