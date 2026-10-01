package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func memFactory(s storage.Storage) storeFactory {
	return func(*testing.T) (storage.Storage, string) { return s, "cfg" }
}

func cloudGet(t *testing.T, h *harness, act model.Action, p string) string {
	t.Helper()
	body, _, err := h.store.Get(ctx, NewLayout(h.root, act.ID).Current(p))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func manifestOf(t *testing.T, h *harness, act model.Action) model.Manifest {
	t.Helper()
	m, _, err := loadManifest(ctx, h.store, NewLayout(h.root, act.ID))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// assertManifestMatchesCurrent checks every object under current/ has a
// manifest entry with the object's recorded hash.
func assertManifestMatchesCurrent(t *testing.T, h *harness, act model.Action) {
	t.Helper()
	l := NewLayout(h.root, act.ID)
	m := manifestOf(t, h, act)
	objs, err := h.store.List(ctx, l.CurrentPrefix())
	if err != nil || len(objs) == 0 {
		t.Fatalf("list current: %v %v", objs, err)
	}
	for _, o := range objs {
		p := strings.TrimPrefix(o.Key, l.CurrentPrefix())
		_, obj, err := h.store.Get(ctx, o.Key)
		if err != nil {
			t.Fatal(err)
		}
		e, ok := m.Files[p]
		if !ok || e.Hash != obj.Metadata[metaSHA256] {
			t.Fatalf("manifest entry for %s = %+v (ok=%v), object hash %q", p, e, ok, obj.Metadata[metaSHA256])
		}
	}
}

// restorePair returns a synced A/B pair where snapshot S1 holds the v1 state
// and the cloud has since moved to v2 for both files (B has synced them).
func restorePair(t *testing.T, f storeFactory, act model.Action) (*harness, *machine, *machine, model.Action, model.SnapshotInfo) {
	t.Helper()
	h, a, b, act := syncedPair(t, f, act)
	s1, err := a.runner.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.write("app/settings.json", `{"v":2}`)
	a.write("app/snippets/go.json", "go v2")
	expectCounts(t, a.sync(), 2, 0, 0, 0)
	expectCounts(t, b.sync(), 0, 2, 0, 0)
	return h, a, b, act, s1
}

func preRestoreSnapshot(t *testing.T, r *Runner) model.SnapshotInfo {
	t.Helper()
	list, err := r.ListSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Reason == ReasonPreRestore {
			return s
		}
	}
	t.Fatalf("no pre-restore snapshot in %+v", list)
	return model.SnapshotInfo{}
}

func TestRestoreFromOldestSnapshotAtFullRetention(t *testing.T) {
	for _, target := range []RestoreTarget{RestoreCloud, RestoreBoth} {
		t.Run(string(target), func(t *testing.T) {
			h, a, _, act, s1 := restorePair(t, memoryFactoryNew(), testAction("oldest"))
			l := NewLayout(h.root, act.ID)
			s2, err := a.runner.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Make S1 the oldest of exactly `retention` snapshots after the restore.
			a.runner.action.SnapshotRetention = 2
			list, _ := a.runner.ListSnapshots(ctx)
			for _, s := range list {
				if s.TS != s1.TS && s.TS != s2.TS {
					_ = a.runner.deleteSnapshot(ctx, s.TS)
				}
			}
			if err := a.runner.applyRetention(ctx); err != nil {
				t.Fatal(err)
			}
			oldest := s1.TS
			if s2.TS < oldest {
				oldest = s2.TS
			}
			if oldest != s1.TS {
				t.Fatalf("unexpected order %s %s", s1.TS, s2.TS)
			}
			rr, err := a.runner.Restore(ctx, oldest, nil, target)
			if err != nil || len(rr.Restored) != 2 {
				t.Fatalf("restore = %+v, %v", rr, err)
			}
			if _, err := h.store.Head(ctx, l.SnapshotIndex(oldest)); err != nil {
				t.Fatalf("source snapshot was pruned: %v", err)
			}
			if got := cloudGet(t, h, act, "settings.json"); got != `{"v":1}` {
				t.Fatalf("cloud settings = %q", got)
			}
			if got := cloudGet(t, h, act, "snippets/go.json"); got != "go" {
				t.Fatalf("cloud go = %q", got)
			}
			assertManifestMatchesCurrent(t, h, act)
			m := manifestOf(t, h, act)
			e := m.Files["settings.json"]
			if e.Hash != hashBytes([]byte(`{"v":1}`)) || e.Size != int64(len(`{"v":1}`)) || e.Writer != "A" || e.ETag == "" {
				t.Fatalf("manifest entry = %+v", e)
			}
			pre := preRestoreSnapshot(t, a.runner)
			body, _, err := h.store.Get(ctx, l.SnapshotFile(pre.TS, "settings.json"))
			if err != nil || string(body) != `{"v":2}` {
				t.Fatalf("pre-restore copy = %q, %v", body, err)
			}
			body, _, err = h.store.Get(ctx, l.SnapshotFile(pre.TS, "snippets/go.json"))
			if err != nil || string(body) != "go v2" {
				t.Fatalf("pre-restore copy = %q, %v", body, err)
			}
			if target == RestoreBoth {
				if got := a.read("app/settings.json"); got != `{"v":1}` {
					t.Fatalf("local = %q", got)
				}
			}
		})
	}
}

func memoryFactoryNew() storeFactory {
	s := storage.NewMemory()
	return memFactory(s)
}

func TestRestoreFailsEarlyWhenSourceObjectMissing(t *testing.T) {
	h, a, _, act, s1 := restorePair(t, memoryFactoryNew(), testAction("missing"))
	l := NewLayout(h.root, act.ID)
	if err := h.store.Delete(ctx, l.SnapshotFile(s1.TS, "snippets/go.json")); err != nil {
		t.Fatal(err)
	}
	before, _ := a.runner.ListSnapshots(ctx)
	_, err := a.runner.Restore(ctx, s1.TS, nil, RestoreCloud)
	if err == nil || !strings.Contains(err.Error(), "snippets/go.json") {
		t.Fatalf("err = %v", err)
	}
	after, _ := a.runner.ListSnapshots(ctx)
	if len(after) != len(before) {
		t.Fatalf("a snapshot was taken despite the early failure: %d -> %d", len(before), len(after))
	}
	if got := cloudGet(t, h, act, "settings.json"); got != `{"v":2}` {
		t.Fatalf("cloud changed: %q", got)
	}
	assertManifestMatchesCurrent(t, h, act)
}

// nthCurrentCopyFails fails the Nth Copy whose destination is under current/.
type nthCurrentCopyFails struct {
	storage.Storage
	prefix  string
	n, seen int
}

func (s *nthCurrentCopyFails) Copy(c context.Context, src, dst string) (storage.Object, error) {
	if strings.HasPrefix(dst, s.prefix) {
		s.seen++
		if s.seen == s.n {
			return storage.Object{}, errors.New("boom restore copy")
		}
	}
	return s.Storage.Copy(c, src, dst)
}

func TestRestoreCloudPartialFailureKeepsManifestConsistent(t *testing.T) {
	h, a, b, act, s1 := restorePair(t, memoryFactoryNew(), testAction("partial"))
	l := NewLayout(h.root, act.ID)
	a.runner.d.Storage = &nthCurrentCopyFails{Storage: h.store, prefix: l.CurrentPrefix(), n: 2}
	_, err := a.runner.Restore(ctx, s1.TS, nil, RestoreCloud)
	if err == nil || !strings.Contains(err.Error(), "boom restore copy") {
		t.Fatalf("err = %v", err)
	}
	if got := cloudGet(t, h, act, "settings.json"); got != `{"v":1}` {
		t.Fatalf("first file not restored: %q", got)
	}
	assertManifestMatchesCurrent(t, h, act)
	res, err := b.runner.Sync(ctx)
	if err != nil {
		t.Fatalf("second machine sync: %v", err)
	}
	expectCounts(t, res, 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("B settings = %q", got)
	}
}

func restoreActionCloudToLocal(name string) model.Action {
	act := testAction(name)
	act.MachineDirections["B"] = model.CloudToLocal
	return act
}

func TestRestoreLocalOnCloudToLocalMachineIsUndoneByNextSync(t *testing.T) {
	_, _, b, _, s1 := restorePair(t, memoryFactoryNew(), restoreActionCloudToLocal("c2l"))
	if _, err := b.runner.Restore(ctx, s1.TS, []string{"settings.json"}, RestoreLocal); err != nil {
		t.Fatal(err)
	}
	if got := b.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("after restore = %q", got)
	}
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"v":2}` {
		t.Fatalf("next sync did not re-download the cloud version: %q", got)
	}
}

func TestRestoreLocalThenCloudChangeConflictsOnTwoWay(t *testing.T) {
	_, a, b, _, s1 := restorePair(t, memoryFactoryNew(), testAction("twoway"))
	if _, err := b.runner.Restore(ctx, s1.TS, []string{"settings.json"}, RestoreLocal); err != nil {
		t.Fatal(err)
	}
	a.write("app/settings.json", `{"v":3}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	res := b.sync()
	if res.Outcome != model.OutcomeConflict || len(res.Conflicts) != 1 {
		t.Fatalf("want conflict, got %+v", res)
	}
	if got := b.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("restored content overwritten: %q", got)
	}
}

func TestRestoreLocalBacksUpReplacedFile(t *testing.T) {
	_, a, _, act, s1 := restorePair(t, memoryFactoryNew(), testAction("backup"))
	if _, err := a.runner.Restore(ctx, s1.TS, []string{"settings.json"}, RestoreLocal); err != nil {
		t.Fatal(err)
	}
	m, _ := filepath.Glob(filepath.Join(a.deps.BackupRoot, act.ID, "*", "settings.json"))
	if len(m) != 1 {
		t.Fatalf("backups = %v", m)
	}
	data, err := os.ReadFile(m[0])
	if err != nil || string(data) != `{"v":2}` {
		t.Fatalf("backup = %q, %v", data, err)
	}
}

func TestRestoreSkipsUnmappedItem(t *testing.T) {
	h, a, _, act, s1 := restorePair(t, memoryFactoryNew(), testAction("unmapped"))
	c := h.machine("C", "")
	h.join(c, act.ID, map[string]string{itemSettings: defaultPaths[itemSettings]})
	_ = a
	rr, err := c.runner.Restore(ctx, s1.TS, nil, RestoreLocal)
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Restored) != 1 || rr.Restored[0] != "settings.json" || len(rr.SkippedUnmapped) != 1 || rr.SkippedUnmapped[0] != "snippets/go.json" {
		t.Fatalf("result = %+v", rr)
	}
	if got := c.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("C settings = %q", got)
	}
	if c.exists("app/snippets/go.json") {
		t.Fatal("unmapped file written")
	}
}

func TestRestoreFileDeletedFromCloud(t *testing.T) {
	h, a, b, act := syncedPair(t, memoryFactoryNew(), testAction("deleted"))
	s1, err := a.runner.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.remove("app/snippets/go.json")
	a.sync()
	b.sync()
	if _, ok := manifestOf(t, h, act).Files["snippets/go.json"]; ok {
		t.Fatal("setup: file still in the manifest")
	}
	rr, err := a.runner.Restore(ctx, s1.TS, []string{"snippets/go.json"}, RestoreCloud)
	if err != nil || len(rr.Restored) != 1 {
		t.Fatalf("restore = %+v, %v", rr, err)
	}
	if got := cloudGet(t, h, act, "snippets/go.json"); got != "go" {
		t.Fatalf("cloud = %q", got)
	}
	e, ok := manifestOf(t, h, act).Files["snippets/go.json"]
	if !ok || e.Hash != hashBytes([]byte("go")) || e.Writer != "A" {
		t.Fatalf("manifest entry = %+v ok=%v", e, ok)
	}
	assertManifestMatchesCurrent(t, h, act)
}

func TestRestoreSelectedFilesOnly(t *testing.T) {
	h, a, _, act, s1 := restorePair(t, memoryFactoryNew(), testAction("selected"))
	rr, err := a.runner.Restore(ctx, s1.TS, []string{"settings.json"}, RestoreCloud)
	if err != nil || len(rr.Restored) != 1 || rr.Restored[0] != "settings.json" {
		t.Fatalf("restore = %+v, %v", rr, err)
	}
	if got := cloudGet(t, h, act, "settings.json"); got != `{"v":1}` {
		t.Fatalf("settings = %q", got)
	}
	if got := cloudGet(t, h, act, "snippets/go.json"); got != "go v2" {
		t.Fatalf("untouched file changed: %q", got)
	}
	if e := manifestOf(t, h, act).Files["snippets/go.json"]; e.Hash != hashBytes([]byte("go v2")) {
		t.Fatalf("untouched manifest entry changed: %+v", e)
	}
}

func TestRestoreEncryptedAction(t *testing.T) {
	h := newHarness(t, memoryFactoryNew())
	a := h.machine("A", "pw")
	act := testAction("enc-restore")
	act.Encrypt = true
	a.write("app/settings.json", `{"secret":"one"}`)
	act = h.create(a, act)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	s1, err := a.runner.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.write("app/settings.json", `{"secret":"two"}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	if _, err := a.runner.Restore(ctx, s1.TS, nil, RestoreCloud); err != nil {
		t.Fatal(err)
	}
	body := cloudGet(t, h, act, "settings.json")
	if strings.Contains(body, "one") || strings.Contains(body, "secret") {
		t.Fatal("cloud object is not ciphertext after restore")
	}
	if e := manifestOf(t, h, act).Files["settings.json"]; e.Hash != hashBytes([]byte(`{"secret":"one"}`)) {
		t.Fatalf("manifest entry = %+v", e)
	}
	b := h.machine("B", "pw")
	h.join(b, act.ID, defaultPaths)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/settings.json"); got != `{"secret":"one"}` {
		t.Fatalf("B = %q", got)
	}

	a.write("app/settings.json", "junk")
	if _, err := a.runner.Restore(ctx, s1.TS, nil, RestoreLocal); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(a.path("app/settings.json"))
	if !bytes.Equal(data, []byte(`{"secret":"one"}`)) {
		t.Fatalf("local after restore = %q", data)
	}
}

func TestSnapshotRefusesPendingConflict(t *testing.T) {
	_, _, b, act := conflictOnSettings(t, memoryFactoryNew())
	if _, err := b.runner.Snapshot(ctx); !errors.Is(err, ErrConflictPending) {
		t.Fatalf("err = %v", err)
	}
	list, _ := b.runner.ListSnapshots(ctx)
	for _, s := range list {
		if s.Reason == ReasonManual {
			t.Fatalf("manual snapshot taken despite conflict: %+v (%s)", s, act.ID)
		}
	}
}

func TestSnapshotProceedsWhenSyncDefersPaths(t *testing.T) {
	hs := &hookStore{Storage: storage.NewMemory()}
	h, a, b, act := syncedPair(t, hookFactory(hs), testAction("snap-defer"))
	l := NewLayout(h.root, act.ID)
	a.write("app/settings.json", `{"v":2}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	hs.onGet = func(key string) {
		if key == l.Current("settings.json") {
			b.write("app/settings.json", "edit during snapshot sync")
		}
	}
	info, err := b.runner.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Reason != ReasonManual || len(info.Files) != 2 {
		t.Fatalf("info = %+v", info)
	}
	if got := b.read("app/settings.json"); got != "edit during snapshot sync" {
		t.Fatalf("user edit lost: %q", got)
	}
}
