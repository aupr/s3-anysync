package engine

import (
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
