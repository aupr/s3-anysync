package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
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
	if err := r.applyRetention(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range snapKeys(mem, l) {
		if strings.Contains(k, old) || strings.Contains(k, orphan) {
			t.Fatalf("not swept: %s", k)
		}
	}
	list, err := r.ListSnapshots(ctx)
	if err != nil || len(list) != 2 || list[0].TS != all[2] || list[1].TS != all[1] {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := mem.Head(ctx, l.SnapshotsPrefix()+"junk.json"); err != nil {
		t.Fatalf("non-snapshot name must be left alone: %v", err)
	}
}

func TestRetentionCorruptIndexDoesNotShrinkWindow(t *testing.T) {
	r, mem, l := snapshotRunner(t, 2)
	for _, ts := range []string{"20260101T000000Z", "20260102T000000Z", "20260103T000000Z"} {
		mem.Put(ctx, l.SnapshotIndex(ts), []byte(`{"ts":"`+ts+`"}`), nil)
	}
	mem.Put(ctx, l.SnapshotIndex("20260101T000000Z"), []byte("garbage"), nil)
	if err := r.applyRetention(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListSnapshots(ctx)
	if len(list) != 2 || list[0].TS != "20260103T000000Z" || list[1].TS != "20260102T000000Z" {
		t.Fatalf("list = %+v", list)
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
