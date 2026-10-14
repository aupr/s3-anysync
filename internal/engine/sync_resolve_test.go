package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
)

// hookStore lets a test run code at chosen storage calls.
type hookStore struct {
	storage.Storage
	onGet  func(key string)
	onHead func(key string)
	onPut  func(key string) error
	gets   map[string]int
}

func (s *hookStore) Get(c context.Context, key string) ([]byte, storage.Object, error) {
	if s.gets == nil {
		s.gets = map[string]int{}
	}
	s.gets[key]++
	if s.onGet != nil {
		s.onGet(key)
	}
	return s.Storage.Get(c, key)
}

func (s *hookStore) Head(c context.Context, key string) (storage.Object, error) {
	if s.onHead != nil {
		s.onHead(key)
	}
	return s.Storage.Head(c, key)
}

func (s *hookStore) Put(c context.Context, key string, body []byte, meta map[string]string) (storage.Object, error) {
	if s.onPut != nil {
		if err := s.onPut(key); err != nil {
			return storage.Object{}, err
		}
	}
	return s.Storage.Put(c, key, body, meta)
}

func hookFactory(hs *hookStore) storeFactory {
	return func(*testing.T) (storage.Storage, string) { return hs, "cfg" }
}

func init() {
	scenarios["receiverRestoresCloud off: receiver leaves a cloud-deleted file alone"] = scenarioReceiverNoRestore
	scenarios["receiverRestoresCloud on: receiver uploads it back"] = scenarioReceiverRestore
	scenarios["resolve both edited: local wins"] = func(t *testing.T, f storeFactory) { resolveBothEdited(t, f, model.SideLocal) }
	scenarios["resolve both edited: cloud wins"] = func(t *testing.T, f storeFactory) { resolveBothEdited(t, f, model.SideCloud) }
	scenarios["resolve local edited, cloud deleted: local wins"] = func(t *testing.T, f storeFactory) { resolveEditDelete(t, f, model.SideLocal) }
	scenarios["resolve local edited, cloud deleted: cloud wins"] = func(t *testing.T, f storeFactory) { resolveEditDelete(t, f, model.SideCloud) }
	scenarios["resolve local deleted, cloud edited: local wins"] = func(t *testing.T, f storeFactory) { resolveDeleteEdit(t, f, model.SideLocal) }
	scenarios["resolve local deleted, cloud edited: cloud wins"] = func(t *testing.T, f storeFactory) { resolveDeleteEdit(t, f, model.SideCloud) }
	scenarios["resolve rejects partial and unknown choices"] = scenarioResolveStrict
	scenarios["resolve keeps every conflict when a new one appears"] = scenarioResolveNewConflict
	scenarios["unmapping an item drops its baseline"] = scenarioUnmapDropsBaseline
	scenarios["remap after local cleanup downloads, never deletes"] = scenarioRemapAfterLocalCleanup
	scenarios["remap after a cloud edit follows first-run rules"] = scenarioRemapAfterCloudEdit
	scenarios["resolve ignores content the user never reviewed"] = scenarioResolveIgnoresUnreviewedContent
}

func cloudBody(t *testing.T, h *harness, act model.Action, p string) (string, error) {
	t.Helper()
	b, _, err := h.store.Get(ctx, NewLayout(h.root, act.ID).Current(p))
	return string(b), err
}

func cloudDelete(t *testing.T, h *harness, act model.Action, p string) {
	t.Helper()
	l := NewLayout(h.root, act.ID)
	if err := h.store.Delete(ctx, l.Current(p)); err != nil {
		t.Fatal(err)
	}
	m, _, err := loadManifest(ctx, h.store, l)
	if err != nil {
		t.Fatal(err)
	}
	delete(m.Files, p)
	if err := saveManifest(ctx, h.store, l, m); err != nil {
		t.Fatal(err)
	}
}

func scenarioReceiverNoRestore(t *testing.T, f storeFactory) {
	act := testAction("recv-off")
	act.PropagateDeletes = false
	act.MachineDirections = map[string]model.Direction{"B": model.CloudToLocal}
	h, a, b, act := syncedPair(t, f, act)
	cloudDelete(t, h, act, "snippets/go.json")
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	if got := b.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("receiver file changed: %q", got)
	}
	if _, err := cloudBody(t, h, act, "snippets/go.json"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("receiver uploaded to the cloud (err=%v)", err)
	}
	// the two-way machine restores the cloud copy as before
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	expectCounts(t, b.sync(), 0, 0, 0, 0)
}

func scenarioReceiverRestore(t *testing.T, f storeFactory) {
	act := testAction("recv-on")
	act.PropagateDeletes = false
	act.ReceiverRestoresCloud = true
	act.MachineDirections = map[string]model.Direction{"B": model.CloudToLocal}
	h, a, b, act := syncedPair(t, f, act)
	cloudDelete(t, h, act, "snippets/go.json")
	expectCounts(t, b.sync(), 1, 0, 0, 0)
	if got, err := cloudBody(t, h, act, "snippets/go.json"); err != nil || got != "go" {
		t.Fatalf("cloud copy = %q, %v", got, err)
	}
	expectCounts(t, a.sync(), 0, 0, 0, 0)
}

// conflictOnSettings leaves B paused on a both-edited settings.json
// (cloud {"v":2} from A, local {"v":333} on B).
func conflictOnSettings(t *testing.T, f storeFactory) (*harness, *machine, *machine, model.Action) {
	h, a, b, act := syncedPair(t, f, testAction("resolve"))
	a.write("app/settings.json", `{"v":2}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	b.write("app/settings.json", `{"v":333}`)
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("want conflict, got %+v", res)
	}
	return h, a, b, act
}

func resolveSettings(t *testing.T, b *machine, side model.Side) model.SyncResult {
	t.Helper()
	res, err := resolveCur(b.runner, map[string]model.Side{"settings.json": side})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeOK {
		t.Fatalf("outcome %q", res.Outcome)
	}
	if saved, err := b.runner.Conflicts(); err != nil || len(saved) != 0 {
		t.Fatalf("conflicts not cleared: %+v, %v", saved, err)
	}
	return res
}

func preResolveSnapshot(t *testing.T, h *harness, a *machine, act model.Action, p string) string {
	t.Helper()
	snaps, err := a.runner.ListSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps {
		if s.Reason != ReasonPreResolve {
			continue
		}
		for _, fe := range s.Files {
			if fe.Path == p {
				body, _, err := h.store.Get(ctx, NewLayout(h.root, act.ID).SnapshotFile(s.TS, p))
				if err != nil {
					t.Fatal(err)
				}
				return string(body)
			}
		}
	}
	t.Fatalf("no pre-resolve snapshot holding %s: %+v", p, snaps)
	return ""
}

func backupHolding(t *testing.T, b *machine, act model.Action, rel string) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(b.deps.BackupRoot, act.ID, "*", filepath.FromSlash(rel)))
	if len(m) != 1 {
		t.Fatalf("want one backup of %s, got %v", rel, m)
	}
	data, err := os.ReadFile(m[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func resolveBothEdited(t *testing.T, f storeFactory, side model.Side) {
	h, a, b, act := conflictOnSettings(t, f)
	res := resolveSettings(t, b, side)
	if side == model.SideLocal {
		expectCounts(t, res, 1, 0, 0, 0)
		if got, _ := cloudBody(t, h, act, "settings.json"); got != `{"v":333}` {
			t.Fatalf("cloud = %q", got)
		}
		if got := preResolveSnapshot(t, h, b, act, "settings.json"); got != `{"v":2}` {
			t.Fatalf("snapshot holds %q", got)
		}
		expectCounts(t, a.sync(), 0, 1, 0, 0)
		return
	}
	expectCounts(t, res, 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":2}` {
		t.Fatalf("local = %q", got)
	}
	if got := backupHolding(t, b, act, "settings.json"); got != `{"v":333}` {
		t.Fatalf("backup holds %q", got)
	}
	expectCounts(t, b.sync(), 0, 0, 0, 0)
}

func resolveEditDelete(t *testing.T, f storeFactory, side model.Side) {
	h, a, b, act := syncedPair(t, f, testAction("editdel"))
	a.remove("app/settings.json")
	expectCounts(t, a.sync(), 0, 0, 1, 0)
	b.write("app/settings.json", `{"v":333}`)
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("want conflict, got %+v", res)
	}
	res := resolveSettings(t, b, side)
	if side == model.SideLocal {
		expectCounts(t, res, 1, 0, 0, 0)
		if got, _ := cloudBody(t, h, act, "settings.json"); got != `{"v":333}` {
			t.Fatalf("cloud = %q", got)
		}
		return
	}
	expectCounts(t, res, 0, 0, 0, 1)
	if b.exists("app/settings.json") {
		t.Fatal("local file not deleted")
	}
	if got := backupHolding(t, b, act, "settings.json"); got != `{"v":333}` {
		t.Fatalf("backup holds %q", got)
	}
}

func resolveDeleteEdit(t *testing.T, f storeFactory, side model.Side) {
	h, a, b, act := syncedPair(t, f, testAction("deledit"))
	b.remove("app/settings.json")
	a.write("app/settings.json", `{"v":2}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("want conflict, got %+v", res)
	}
	res := resolveSettings(t, b, side)
	if side == model.SideLocal {
		expectCounts(t, res, 0, 0, 1, 0)
		if _, err := cloudBody(t, h, act, "settings.json"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("cloud file not deleted (err=%v)", err)
		}
		if got := preResolveSnapshot(t, h, b, act, "settings.json"); got != `{"v":2}` {
			t.Fatalf("snapshot holds %q", got)
		}
		return
	}
	expectCounts(t, res, 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":2}` {
		t.Fatalf("local = %q", got)
	}
}

func twoConflicts(t *testing.T, f storeFactory) (*harness, *machine, *machine, model.Action) {
	h, a, b, act := syncedPair(t, f, testAction("strict"))
	a.write("app/settings.json", `{"v":2}`)
	a.write("app/snippets/go.json", "go-a")
	expectCounts(t, a.sync(), 2, 0, 0, 0)
	b.write("app/settings.json", `{"v":333}`)
	b.write("app/snippets/go.json", "go-b")
	if res := b.sync(); res.Outcome != model.OutcomeConflict || len(res.Conflicts) != 2 {
		t.Fatalf("want two conflicts, got %+v", res)
	}
	return h, a, b, act
}

func scenarioResolveStrict(t *testing.T, f storeFactory) {
	h, _, b, act := twoConflicts(t, f)
	fresh := func(what string) {
		t.Helper()
		if got := b.read("app/settings.json"); got != `{"v":333}` {
			t.Fatalf("%s: local changed: %q", what, got)
		}
		if got, _ := cloudBody(t, h, act, "settings.json"); got != `{"v":2}` {
			t.Fatalf("%s: cloud changed: %q", what, got)
		}
		if saved, _ := b.runner.Conflicts(); len(saved) != 2 {
			t.Fatalf("%s: saved conflicts = %+v", what, saved)
		}
	}
	_, err := resolveCur(b.runner, map[string]model.Side{"settings.json": model.SideLocal})
	if err == nil || !strings.Contains(err.Error(), "snippets/go.json") || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("partial choice: want error naming snippets/go.json, got %v", err)
	}
	fresh("partial")
	_, err = resolveCur(b.runner, map[string]model.Side{
		"settings.json": model.SideLocal, "snippets/go.json": model.SideLocal, "nope.json": model.SideCloud})
	if err == nil || !strings.Contains(err.Error(), "nope.json") || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown path: want error naming nope.json, got %v", err)
	}
	fresh("unknown")
	if _, err := resolveCur(b.runner, map[string]model.Side{"settings.json": "both", "snippets/go.json": model.SideLocal}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown side: want ErrInvalidInput, got %v", err)
	}
	fresh("bad side")

	// all conflicts chosen: resolves, then nothing is left to resolve.
	res, err := resolveCur(b.runner, map[string]model.Side{"settings.json": model.SideLocal, "snippets/go.json": model.SideCloud})
	if err != nil || res.Outcome != model.OutcomeOK {
		t.Fatalf("resolve: %+v, %v", res, err)
	}
	_, err = resolveCur(b.runner, map[string]model.Side{"settings.json": model.SideLocal})
	if err == nil || !strings.Contains(err.Error(), "no unresolved conflicts") || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want 'no unresolved conflicts', got %v", err)
	}
}

func scenarioResolveNewConflict(t *testing.T, f storeFactory) {
	h, a, b, act := syncedPair(t, f, testAction("newconf"))
	a.write("app/settings.json", `{"v":2}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	b.write("app/settings.json", `{"v":333}`)
	if res := b.sync(); len(res.Conflicts) != 1 {
		t.Fatalf("want one conflict, got %+v", res)
	}
	// a second conflict appears before the user resolves the first
	a.write("app/snippets/go.json", "go-a")
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	b.write("app/snippets/go.json", "go-b")

	res, err := resolveCur(b.runner, map[string]model.Side{"settings.json": model.SideLocal})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeConflict || len(res.Conflicts) != 2 {
		t.Fatalf("want both conflicts returned, got %+v", res)
	}
	if saved, _ := b.runner.Conflicts(); len(saved) != 2 {
		t.Fatalf("saved = %+v", saved)
	}
	if got, _ := cloudBody(t, h, act, "settings.json"); got != `{"v":2}` {
		t.Fatalf("cloud changed while paused: %q", got)
	}
	if got := b.read("app/settings.json"); got != `{"v":333}` {
		t.Fatalf("local changed while paused: %q", got)
	}
	res, err = resolveCur(b.runner, map[string]model.Side{"settings.json": model.SideLocal, "snippets/go.json": model.SideLocal})
	if err != nil || res.Outcome != model.OutcomeOK {
		t.Fatalf("second resolve: %+v, %v", res, err)
	}
	expectCounts(t, res, 2, 0, 0, 0)
}

// unmapSnippets removes the snippets mapping of machine m.
func unmapSnippets(t *testing.T, h *harness, m *machine, actionID string) {
	t.Helper()
	un, err := UpdateSharedAction(ctx, m.deps, h.root, actionID, func(a *model.Action) error {
		for i := range a.Items {
			if a.Items[i].ID == itemSnippets {
				delete(a.Items[i].LocalPaths, m.id)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	m.attach(un)
}

func scenarioUnmapDropsBaseline(t *testing.T, f storeFactory) {
	h, _, b, act := syncedPair(t, f, testAction("unmapbase"))
	unmapSnippets(t, h, b, act.ID)
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	base, err := b.deps.Repo.LoadBaseline(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := base["snippets/go.json"]; ok {
		t.Fatalf("baseline still holds the unmapped item: %+v", base)
	}
	if _, ok := base["settings.json"]; !ok {
		t.Fatalf("baseline of the mapped item lost: %+v", base)
	}
}

func scenarioRemapAfterLocalCleanup(t *testing.T, f storeFactory) {
	h, a, b, act := syncedPair(t, f, testAction("remapclean"))
	unmapSnippets(t, h, b, act.ID)
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	if err := os.RemoveAll(b.path("app/snippets")); err != nil {
		t.Fatal(err)
	}
	h.join(b, act.ID, map[string]string{itemSnippets: defaultPaths[itemSnippets]})
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("local = %q", got)
	}
	if got, err := cloudBody(t, h, act, "snippets/go.json"); err != nil || got != "go" {
		t.Fatalf("cloud copy = %q, %v", got, err)
	}
	expectCounts(t, a.sync(), 0, 0, 0, 0)
	if got := a.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("A lost the file: %q", got)
	}
}

func scenarioRemapAfterCloudEdit(t *testing.T, f storeFactory) {
	h, a, b, act := syncedPair(t, f, testAction("remapedit"))
	unmapSnippets(t, h, b, act.ID)
	expectCounts(t, b.sync(), 0, 0, 0, 0)
	a.write("app/snippets/go.json", "go2")
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	h.join(b, act.ID, map[string]string{itemSnippets: defaultPaths[itemSnippets]})
	// first-run rules: the local copy differs from the cloud one, so it is a conflict
	res := b.sync()
	if res.Outcome != model.OutcomeConflict || len(res.Conflicts) != 1 || res.Conflicts[0].Path != "snippets/go.json" {
		t.Fatalf("want a conflict on snippets/go.json, got %+v", res)
	}
	if got := b.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("local changed: %q", got)
	}
	if got, err := cloudBody(t, h, act, "snippets/go.json"); err != nil || got != "go2" {
		t.Fatalf("cloud = %q, %v", got, err)
	}
}

func scenarioResolveIgnoresUnreviewedContent(t *testing.T, f storeFactory) {
	h, a, b, act := conflictOnSettings(t, f)
	// the cloud copy changes again before the user decides
	a.write("app/settings.json", `{"v":3}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	res, err := resolveCur(b.runner, map[string]model.Side{"settings.json": model.SideLocal})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeConflict || len(res.Conflicts) != 1 {
		t.Fatalf("want a conflict result, got %+v", res)
	}
	if got, _ := cloudBody(t, h, act, "settings.json"); got != `{"v":3}` {
		t.Fatalf("cloud overwritten with unreviewed content: %q", got)
	}
	if got := b.read("app/settings.json"); got != `{"v":333}` {
		t.Fatalf("local changed: %q", got)
	}
	saved, _ := b.runner.Conflicts()
	if len(saved) != 1 || saved[0].Remote == nil || saved[0].Remote.Hash != hashBytes([]byte(`{"v":3}`)) {
		t.Fatalf("saved conflict not refreshed: %+v", saved)
	}
	res = resolveSettings(t, b, model.SideLocal)
	expectCounts(t, res, 1, 0, 0, 0)
	if got, _ := cloudBody(t, h, act, "settings.json"); got != `{"v":333}` {
		t.Fatalf("cloud = %q", got)
	}
}

func TestResolveDeferredKeepsConflictSaved(t *testing.T) {
	hs := &hookStore{Storage: storage.NewMemory()}
	h, _, b, act := conflictOnSettings(t, hookFactory(hs))
	l := NewLayout(h.root, act.ID)
	hs.onGet = func(key string) {
		if key == l.Current("settings.json") {
			b.write("app/settings.json", "edited during resolve")
		}
	}
	res, err := resolveCur(b.runner, map[string]model.Side{"settings.json": model.SideCloud})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deferred) != 1 || res.Deferred[0] != "settings.json" {
		t.Fatalf("want settings.json deferred, got %+v", res)
	}
	if got := b.read("app/settings.json"); got != "edited during resolve" {
		t.Fatalf("user edit lost: %q", got)
	}
	saved, _ := b.runner.Conflicts()
	if len(saved) != 1 || saved[0].Path != "settings.json" || saved[0].Remote == nil {
		t.Fatalf("deferred path not kept as a saved conflict: %+v", saved)
	}
}

func TestRetryWhenManifestChangesBeforeLock(t *testing.T) {
	hs := &hookStore{Storage: storage.NewMemory()}
	h, a, _, act := syncedPair(t, hookFactory(hs), testAction("etag"))
	l := NewLayout(h.root, act.ID)
	fired := false
	hs.onHead = func(key string) {
		if key != l.Manifest() || fired {
			return
		}
		fired = true
		// another machine's change lands between our scan and our lock
		body := []byte("new")
		meta := map[string]string{metaSHA256: hashBytes(body), metaSize: "3", metaMTime: "1", metaMachine: "Z"}
		if _, err := hs.Storage.Put(ctx, l.Current("snippets/new.json"), body, meta); err != nil {
			t.Error(err)
		}
		m, _, err := loadManifest(ctx, hs.Storage, l)
		if err != nil {
			t.Error(err)
		}
		m.Files["snippets/new.json"] = model.FileEntry{Path: "snippets/new.json", Hash: hashBytes(body), Size: 3, MTime: 1, Writer: "Z"}
		if err := saveManifest(ctx, hs.Storage, l, m); err != nil {
			t.Error(err)
		}
	}
	a.write("app/settings.json", `{"v":2}`)
	hs.gets = nil
	res := a.sync()
	expectCounts(t, res, 1, 1, 0, 0)
	if !fired || hs.gets[l.Manifest()] < 2 {
		t.Fatalf("not re-planned: fired=%v manifest reads=%d", fired, hs.gets[l.Manifest()])
	}
	if got := a.read("app/snippets/new.json"); got != "new" {
		t.Fatalf("new.json = %q", got)
	}
	expectCounts(t, a.sync(), 0, 0, 0, 0)
}

func TestFailureMidApplyThenRetryConverges(t *testing.T) {
	hs := &hookStore{Storage: storage.NewMemory()}
	h := newHarness(t, hookFactory(hs))
	a := h.machine("A", "")
	a.write("app/settings.json", `{"v":1}`)
	a.write("app/snippets/a.json", "a")
	a.write("app/snippets/b.json", "b")
	act := h.create(a, testAction("midfail"))
	l := NewLayout(h.root, act.ID)
	failed := false
	hs.onPut = func(key string) error {
		if key == l.Current("snippets/a.json") && !failed {
			failed = true
			return errors.New("boom")
		}
		return nil
	}
	if _, err := a.runner.Sync(ctx); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want boom, got %v", err)
	}
	if _, err := hs.Storage.Head(ctx, l.Current("settings.json")); err != nil {
		t.Fatalf("first file not uploaded: %v", err)
	}
	if base, _ := a.deps.Repo.LoadBaseline(act.ID); base["settings.json"].Hash == "" {
		t.Fatalf("first file not recorded in the baseline: %+v", base)
	}
	if m, _, _ := loadManifest(ctx, hs.Storage, l); m.Files["settings.json"].Hash == "" {
		t.Fatalf("first file not in the manifest: %+v", m.Files)
	}
	expectCounts(t, a.sync(), 2, 0, 0, 0)
	expectCounts(t, a.sync(), 0, 0, 0, 0)
}

func TestStaleConflictsClearedWhenPlanHasNone(t *testing.T) {
	hs := &hookStore{Storage: storage.NewMemory()}
	_, a, b, act := conflictOnSettings(t, hookFactory(hs))
	_ = a
	// the user settles it by hand: B's file now equals the cloud copy, and a
	// new file needs uploading, but that upload fails.
	b.write("app/settings.json", `{"v":2}`)
	b.write("app/snippets/new.json", "new")
	hs.onPut = func(key string) error {
		if key == NewLayout("cfg", act.ID).Current("snippets/new.json") {
			return errors.New("boom")
		}
		return nil
	}
	if _, err := b.runner.Sync(ctx); err == nil {
		t.Fatal("want the upload error")
	}
	if saved, _ := b.runner.Conflicts(); len(saved) != 0 {
		t.Fatalf("stale conflicts kept: %+v", saved)
	}
}

func TestLocalEditDuringDownloadIsDeferred(t *testing.T) {
	hs := &hookStore{Storage: storage.NewMemory()}
	h, a, b, act := syncedPair(t, hookFactory(hs), testAction("defer-dl"))
	l := NewLayout(h.root, act.ID)
	a.write("app/settings.json", `{"v":2}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	hs.onGet = func(key string) {
		if key == l.Current("settings.json") {
			b.write("app/settings.json", "my edit made during the sync")
		}
	}
	res := b.sync()
	if res.Outcome != model.OutcomeOK || len(res.Deferred) != 1 || res.Deferred[0] != "settings.json" {
		t.Fatalf("want settings.json deferred, got %+v", res)
	}
	expectCounts(t, res, 0, 0, 0, 0)
	if got := b.read("app/settings.json"); got != "my edit made during the sync" {
		t.Fatalf("user edit lost: %q", got)
	}
	hs.onGet = nil
	// the next run sees a local edit and a cloud edit: a conflict.
	if res := b.sync(); res.Outcome != model.OutcomeConflict || len(res.Conflicts) != 1 {
		t.Fatalf("want conflict on the next run, got %+v", res)
	}
	if got := b.read("app/settings.json"); got != "my edit made during the sync" {
		t.Fatalf("user edit lost: %q", got)
	}
}

func TestLocalEditBeforeDeleteIsDeferred(t *testing.T) {
	hs := &hookStore{Storage: storage.NewMemory()}
	h, a, b, act := syncedPair(t, hookFactory(hs), testAction("defer-del"))
	l := NewLayout(h.root, act.ID)
	a.remove("app/snippets/go.json")
	expectCounts(t, a.sync(), 0, 0, 1, 0)
	fired := false
	hs.onHead = func(key string) {
		if key == l.Manifest() && !fired {
			fired = true
			b.write("app/snippets/go.json", "precious new content")
		}
	}
	res := b.sync()
	if len(res.Deferred) != 1 || res.Deferred[0] != "snippets/go.json" || res.DeletedLocal != 0 {
		t.Fatalf("want go.json deferred, got %+v", res)
	}
	if got := b.read("app/snippets/go.json"); got != "precious new content" {
		t.Fatalf("user edit lost: %q", got)
	}
	// local edit vs cloud delete: next run raises a conflict
	hs.onHead = nil
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("want conflict on the next run, got %+v", res)
	}
}

// reviewedOf is what a client that just listed the conflicts would send.
func reviewedOf(t testing.TB, r *Runner) map[string]ConflictVersion {
	t.Helper()
	saved, err := r.Conflicts()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ConflictVersion{}
	for _, c := range saved {
		out[c.Path] = versionOf(c)
	}
	return out
}

// resolveCur resolves against the conflicts as currently saved.
func resolveCur(r *Runner, choices map[string]model.Side) (model.SyncResult, error) {
	saved, err := r.Conflicts()
	if err != nil {
		return model.SyncResult{}, err
	}
	rev := map[string]ConflictVersion{}
	for _, c := range saved {
		rev[c.Path] = versionOf(c)
	}
	return r.Resolve(ctx, choices, rev)
}

func TestResolveReviewedVersions(t *testing.T) {
	both := map[string]model.Side{"settings.json": model.SideLocal, "snippets/go.json": model.SideCloud}
	// assertUntouched: nothing applied, conflicts still saved
	assertUntouched := func(t *testing.T, h *harness, b *machine, act model.Action, local string) {
		t.Helper()
		if got, _ := cloudBody(t, h, act, "settings.json"); got != `{"v":2}` {
			t.Fatalf("cloud changed: %q", got)
		}
		if local == "" {
			if b.exists("app/settings.json") {
				t.Fatal("local file reappeared")
			}
		} else if got := b.read("app/settings.json"); got != local {
			t.Fatalf("local changed: %q", got)
		}
		if saved, _ := b.runner.Conflicts(); len(saved) != 2 {
			t.Fatalf("saved conflicts consumed: %+v", saved)
		}
	}
	t.Run("matching", func(t *testing.T) {
		_, _, b, _ := twoConflicts(t, memFactory(storage.NewMemory()))
		res, err := b.runner.Resolve(ctx, both, reviewedOf(t, b.runner))
		if err != nil || res.Outcome != model.OutcomeOK {
			t.Fatalf("%+v, %v", res, err)
		}
	})
	t.Run("stale hash", func(t *testing.T) {
		h, _, b, act := twoConflicts(t, memFactory(storage.NewMemory()))
		old := reviewedOf(t, b.runner)
		b.write("app/settings.json", `{"v":444}`)
		if res := b.sync(); res.Outcome != model.OutcomeConflict {
			t.Fatalf("want conflict, got %+v", res)
		}
		_, err := b.runner.Resolve(ctx, both, old)
		if !errors.Is(err, ErrConflictChanged) || !strings.Contains(err.Error(), "settings.json") || strings.Contains(err.Error(), "snippets/go.json") {
			t.Fatalf("want ErrConflictChanged naming only settings.json, got %v", err)
		}
		assertUntouched(t, h, b, act, `{"v":444}`)
		// choosing again on the fresh list works
		if res, err := b.runner.Resolve(ctx, both, reviewedOf(t, b.runner)); err != nil || res.Outcome != model.OutcomeOK {
			t.Fatalf("fresh resolve: %+v, %v", res, err)
		}
	})
	t.Run("side became nil", func(t *testing.T) {
		h, _, b, act := twoConflicts(t, memFactory(storage.NewMemory()))
		old := reviewedOf(t, b.runner)
		b.remove("app/settings.json")
		if res := b.sync(); res.Outcome != model.OutcomeConflict {
			t.Fatalf("want conflict, got %+v", res)
		}
		_, err := b.runner.Resolve(ctx, both, old)
		if !errors.Is(err, ErrConflictChanged) {
			t.Fatalf("want ErrConflictChanged, got %v", err)
		}
		assertUntouched(t, h, b, act, "")
	})
	t.Run("missing and nil and extra entries", func(t *testing.T) {
		h, _, b, act := twoConflicts(t, memFactory(storage.NewMemory()))
		cur := reviewedOf(t, b.runner)
		partial := map[string]ConflictVersion{"settings.json": cur["settings.json"]}
		_, err := b.runner.Resolve(ctx, both, partial)
		if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "reviewed version missing for: snippets/go.json") {
			t.Fatalf("missing entry: got %v", err)
		}
		for name, rev := range map[string]map[string]ConflictVersion{"nil": nil, "empty": {}} {
			if _, err := b.runner.Resolve(ctx, both, rev); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("%s reviewed: want ErrInvalidInput, got %v", name, err)
			}
		}
		extra := map[string]ConflictVersion{"nope.json": {}}
		for k, v := range cur {
			extra[k] = v
		}
		if _, err := b.runner.Resolve(ctx, both, extra); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "nope.json") {
			t.Fatalf("extra entry: got %v", err)
		}
		assertUntouched(t, h, b, act, `{"v":333}`)
	})
}
