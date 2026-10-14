package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
)

func snapshotRunner(t *testing.T, retention int) (*Runner, *storage.Memory, Layout) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Now = fixedClock()
	a := basicAction("snap")
	a.SnapshotRetention = retention
	l := NewLayout("root", a.ID)
	for _, p := range []string{"settings.json", "d/x.json"} {
		mem.Put(ctx, l.Current(p), []byte(p), map[string]string{metaSHA256: "h-" + p})
	}
	return NewRunner(d, "root", a), mem, l
}

func TestWriteSnapshot(t *testing.T) {
	r, mem, l := snapshotRunner(t, 20)
	files := []model.FileEntry{{Path: "settings.json", Hash: "h-settings.json"}, {Path: "d/x.json", Hash: "h-d/x.json"}}
	info, err := r.writeSnapshot(ctx, ReasonManual, files)
	if err != nil {
		t.Fatal(err)
	}
	if info.TS != "20261001T120000Z" || info.Reason != ReasonManual || info.MachineID != "A" || len(info.Files) != 2 || info.Files[0].Path != "d/x.json" {
		t.Fatalf("info = %+v", info)
	}
	body, obj, err := mem.Get(ctx, l.SnapshotFile(info.TS, "d/x.json"))
	if err != nil || string(body) != "d/x.json" || obj.Metadata[metaSHA256] != "h-d/x.json" {
		t.Fatalf("snapshot copy = %q %v %v", body, obj.Metadata, err)
	}

	// The same clock second is taken, so the next snapshot moves one second on.
	second, err := r.writeSnapshot(ctx, ReasonAutoPreSync, files[:1])
	if err != nil || second.TS != "20261001T120001Z" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	list, err := r.ListSnapshots(ctx)
	if err != nil || len(list) != 2 || list[0].TS != second.TS {
		t.Fatalf("ListSnapshots = %+v, %v", list, err)
	}
	got, err := r.snapshotInfo(ctx, info.TS)
	if err != nil || len(got.Files) != 2 {
		t.Fatalf("snapshotInfo = %+v, %v", got, err)
	}
	if _, err := r.snapshotInfo(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing snapshot: %v", err)
	}
}

func TestSnapshotRetention(t *testing.T) {
	r, mem, l := snapshotRunner(t, 2)
	files := []model.FileEntry{{Path: "settings.json"}}
	var all []string
	for i := 0; i < 3; i++ {
		info, err := r.writeSnapshot(ctx, ReasonManual, files)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, info.TS)
	}
	list, _ := r.ListSnapshots(ctx)
	if len(list) != 2 || list[0].TS != all[2] || list[1].TS != all[1] {
		t.Fatalf("kept = %+v", list)
	}
	for _, k := range mem.Keys() {
		if strings.HasPrefix(k, l.SnapshotsPrefix()+all[0]) {
			t.Fatalf("object of pruned snapshot left behind: %s", k)
		}
	}
}

// failStore injects failures into Copy/Put and can cancel a context mid-way.
type failStore struct {
	*storage.Memory
	copies, failCopyAt int // failCopyAt 1-based; 0 disables
	failPut            bool
	cancelAfterCopy    context.CancelFunc
}

func (f *failStore) Copy(c context.Context, src, dst string) (storage.Object, error) {
	f.copies++
	if f.failCopyAt > 0 && f.copies == f.failCopyAt {
		return storage.Object{}, errors.New("boom copy")
	}
	if f.cancelAfterCopy != nil && f.copies > 1 {
		if err := c.Err(); err != nil {
			return storage.Object{}, err
		}
	}
	o, err := f.Memory.Copy(c, src, dst)
	if f.cancelAfterCopy != nil {
		f.cancelAfterCopy()
	}
	return o, err
}

func (f *failStore) Put(c context.Context, key string, body []byte, meta map[string]string) (storage.Object, error) {
	if f.failPut && strings.HasSuffix(key, ".json") && strings.Contains(key, "/snapshots/") {
		return storage.Object{}, errors.New("boom put")
	}
	return f.Memory.Put(c, key, body, meta)
}

func snapKeys(mem *storage.Memory, l Layout) []string {
	var out []string
	for _, k := range mem.Keys() {
		if strings.HasPrefix(k, l.SnapshotsPrefix()) {
			out = append(out, k)
		}
	}
	return out
}

func TestWriteSnapshotCleansUpOnFailure(t *testing.T) {
	files := []model.FileEntry{{Path: "settings.json"}, {Path: "d/x.json"}}
	t.Run("copy fails", func(t *testing.T) {
		r, mem, l := snapshotRunner(t, 20)
		r.d.Storage = &failStore{Memory: mem, failCopyAt: 2}
		if _, err := r.writeSnapshot(ctx, ReasonManual, files); err == nil || !strings.Contains(err.Error(), "boom copy") {
			t.Fatalf("err = %v", err)
		}
		if k := snapKeys(mem, l); len(k) != 0 {
			t.Fatalf("orphans: %v", k)
		}
	})
	t.Run("index put fails", func(t *testing.T) {
		r, mem, l := snapshotRunner(t, 20)
		r.d.Storage = &failStore{Memory: mem, failPut: true}
		if _, err := r.writeSnapshot(ctx, ReasonManual, files); err == nil || !strings.Contains(err.Error(), "boom put") {
			t.Fatalf("err = %v", err)
		}
		if k := snapKeys(mem, l); len(k) != 0 {
			t.Fatalf("orphans: %v", k)
		}
	})
	t.Run("ctx cancelled", func(t *testing.T) {
		r, mem, l := snapshotRunner(t, 20)
		c, cancel := context.WithCancel(ctx)
		defer cancel()
		r.d.Storage = &failStore{Memory: mem, cancelAfterCopy: cancel}
		if _, err := r.writeSnapshot(c, ReasonManual, files); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if k := snapKeys(mem, l); len(k) != 0 {
			t.Fatalf("orphans: %v", k)
		}
	})
}

// putManualIndexes writes minimal manual snapshot indexes.
func putManualIndexes(mem *storage.Memory, l Layout, tss ...string) {
	for _, ts := range tss {
		mem.Put(ctx, l.SnapshotIndex(ts), []byte(`{"ts":"`+ts+`","reason":"manual"}`), nil)
	}
}

// putNewerAutomatic writes n automatic snapshot indexes newer than any test ts
// below 2026-02, so that older automatic ones fall outside the newest
// AutoSnapshotKeep.
func putNewerAutomatic(mem *storage.Memory, l Layout, n int) {
	for i := 0; i < n; i++ {
		ts := time.Date(2026, 2, 1, 0, 0, i, 0, time.UTC).Format(snapshotTSLayout)
		mem.Put(ctx, l.SnapshotIndex(ts), []byte(`{"reason":"auto-pre-sync"}`), nil)
	}
}

func manualTSs(t *testing.T, r *Runner) []string {
	t.Helper()
	list, err := r.ListSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range list {
		if s.Reason == ReasonManual {
			out = append(out, s.TS)
		}
	}
	return out
}

func TestRetentionCorruptIndexAndOrphans(t *testing.T) {
	r, mem, l := snapshotRunner(t, 2)
	files := []model.FileEntry{{Path: "settings.json"}}
	var all []string
	for i := 0; i < 3; i++ {
		info, err := r.writeSnapshot(ctx, ReasonManual, files)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, info.TS)
	}
	// Retention already pruned all[0]; re-create it as a corrupt, oldest snapshot
	// and add an orphan without any index.
	old := "20260101T000000Z"
	mem.Put(ctx, l.SnapshotIndex(old), []byte("{not json"), nil)
	mem.Put(ctx, l.SnapshotFile(old, "settings.json"), []byte("x"), nil)
	orphan := "20260102T000000Z"
	mem.Put(ctx, l.SnapshotFile(orphan, "x"), []byte("x"), nil)
	mem.Put(ctx, l.SnapshotsPrefix()+"junk.json", []byte("x"), nil)
	// A corrupt index counts as automatic: it is pruned only when older than 7
	// days and beyond the newest AutoSnapshotKeep automatic snapshots.
	putNewerAutomatic(mem, l, AutoSnapshotKeep)
	if err := r.applyRetention(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range snapKeys(mem, l) {
		if strings.Contains(k, old) || strings.Contains(k, orphan) {
			t.Fatalf("not swept: %s", k)
		}
	}
	if got := manualTSs(t, r); len(got) != 2 || got[0] != all[2] || got[1] != all[1] {
		t.Fatalf("manual = %v", got)
	}
	if _, err := mem.Head(ctx, l.SnapshotsPrefix()+"junk.json"); err != nil {
		t.Fatalf("non-snapshot name must be left alone: %v", err)
	}
}

func TestRetentionCorruptIndexDoesNotShrinkWindow(t *testing.T) {
	r, mem, l := snapshotRunner(t, 2)
	putManualIndexes(mem, l, "20260102T000000Z", "20260103T000000Z")
	mem.Put(ctx, l.SnapshotIndex("20260101T000000Z"), []byte("garbage"), nil)
	putNewerAutomatic(mem, l, AutoSnapshotKeep) // pushes the corrupt (automatic) index out of the newest 50
	if err := r.applyRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if got := manualTSs(t, r); len(got) != 2 || got[0] != "20260103T000000Z" || got[1] != "20260102T000000Z" {
		t.Fatalf("manual = %v", got)
	}
	if _, err := mem.Head(ctx, l.SnapshotIndex("20260101T000000Z")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("corrupt index not pruned: %v", err)
	}
}

func TestRetentionKeepBelowOneKeepsEverything(t *testing.T) {
	r, mem, l := snapshotRunner(t, 0)
	for _, ts := range []string{"20260101T000000Z", "20260102T000000Z"} {
		mem.Put(ctx, l.SnapshotIndex(ts), []byte(`{}`), nil)
	}
	if err := r.applyRetention(ctx); err != nil || len(snapKeys(mem, l)) != 2 {
		t.Fatalf("err=%v keys=%v", err, snapKeys(mem, l))
	}
}

type getFailStore struct{ *storage.Memory }

func (g getFailStore) Get(c context.Context, key string) ([]byte, storage.Object, error) {
	if strings.Contains(key, "/snapshots/") {
		return nil, storage.Object{}, errors.New("transient")
	}
	return g.Memory.Get(c, key)
}

func TestListSnapshotsReturnsTransientErrors(t *testing.T) {
	r, mem, l := snapshotRunner(t, 5)
	mem.Put(ctx, l.SnapshotIndex("20260101T000000Z"), []byte(`{}`), nil)
	r.d.Storage = getFailStore{mem}
	if _, err := r.ListSnapshots(ctx); err == nil || !strings.Contains(err.Error(), "transient") {
		t.Fatalf("err = %v", err)
	}
	mem.Put(ctx, l.SnapshotIndex("20260101T000000Z"), []byte(`nope`), nil)
	r.d.Storage = mem
	list, err := r.ListSnapshots(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("corrupt index should be skipped: %v %v", list, err)
	}
}

func TestRetentionNeverDeletesProtectedSnapshot(t *testing.T) {
	r, mem, l := snapshotRunner(t, 2)
	// Existing snapshots are newer than the one just written (clock behind).
	putManualIndexes(mem, l, "20270101T000000Z", "20270102T000000Z", "20270103T000000Z", "20260101T000000Z")
	if err := r.applyRetentionKeeping(ctx, "20260101T000000Z"); err != nil {
		t.Fatal(err)
	}
	keys := snapKeys(mem, l)
	if len(keys) != 2 {
		t.Fatalf("keys = %v", keys)
	}
	if _, err := mem.Head(ctx, l.SnapshotIndex("20260101T000000Z")); err != nil {
		t.Fatalf("protected snapshot deleted: %v", err)
	}
	if _, err := mem.Head(ctx, l.SnapshotIndex("20270103T000000Z")); err != nil {
		t.Fatalf("newest other snapshot should fill the remaining slot: %v", err)
	}
}

type deleteFailStore struct {
	*storage.Memory
	failKey string
}

func (d deleteFailStore) Delete(c context.Context, key string) error {
	if strings.Contains(key, d.failKey) {
		return errors.New("delete refused")
	}
	return d.Memory.Delete(c, key)
}

func TestRetentionContinuesPastDeleteFailure(t *testing.T) {
	r, mem, l := snapshotRunner(t, 1)
	putManualIndexes(mem, l, "20260101T000000Z", "20260102T000000Z", "20260103T000000Z")
	r.d.Storage = deleteFailStore{mem, "20260101T000000Z"}
	err := r.applyRetention(ctx)
	if err == nil || !strings.Contains(err.Error(), "delete refused") {
		t.Fatalf("err = %v", err)
	}
	if _, err := mem.Head(ctx, l.SnapshotIndex("20260102T000000Z")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("later snapshot not deleted after earlier failure: %v", err)
	}
}

func TestRetentionProtectsSeveralSnapshots(t *testing.T) {
	r, mem, l := snapshotRunner(t, 2)
	all := []string{"20260101T000000Z", "20260102T000000Z", "20260103T000000Z", "20260104T000000Z"}
	putManualIndexes(mem, l, all...)
	if err := r.applyRetentionKeeping(ctx, all[3], all[0]); err != nil {
		t.Fatal(err)
	}
	keys := snapKeys(mem, l)
	if len(keys) != 2 {
		t.Fatalf("keys = %v", keys)
	}
	for _, ts := range []string{all[0], all[3]} {
		if _, err := mem.Head(ctx, l.SnapshotIndex(ts)); err != nil {
			t.Fatalf("protected %s deleted: %v", ts, err)
		}
	}
}
