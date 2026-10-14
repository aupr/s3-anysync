package engine

import (
	"testing"

	"github.com/aupr/s3-anysync/internal/model"
)

func init() {
	scenarios["snapshot now and restore"] = scenarioSnapshotRestore
	scenarios["snapshot retention"] = scenarioRetention
}

func scenarioSnapshotRestore(t *testing.T, f storeFactory) {
	_, a, b, _ := syncedPair(t, f, testAction("restore"))
	info, err := a.runner.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Reason != ReasonManual || len(info.Files) != 2 {
		t.Fatalf("snapshot = %+v", info)
	}
	a.write("app/settings.json", `{"v":2222}`)
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	// Restoring only the local copy: the next two-way sync uploads it again.
	rr, err := a.runner.Restore(ctx, info.TS, []string{"settings.json"}, RestoreLocal)
	if err != nil || len(rr.Restored) != 1 {
		t.Fatalf("restore local = %+v, %v", rr, err)
	}
	if got := a.read("app/settings.json"); got != `{"v":1}` {
		t.Fatalf("A after restore = %q", got)
	}
	expectCounts(t, a.sync(), 1, 0, 0, 0)

	// Restoring the cloud copy of everything: B downloads what changed.
	a.write("app/snippets/go.json", "go v2")
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if _, err := a.runner.Restore(ctx, info.TS, nil, RestoreCloud); err != nil {
		t.Fatal(err)
	}
	expectCounts(t, b.sync(), 0, 1, 0, 0)
	if got := b.read("app/snippets/go.json"); got != "go" {
		t.Fatalf("B snippet = %q", got)
	}

	if _, err := a.runner.Restore(ctx, info.TS, []string{"nope.json"}, RestoreLocal); err == nil {
		t.Fatal("restore of a file not in the snapshot accepted")
	}
	if _, err := a.runner.Restore(ctx, info.TS, nil, "sideways"); err == nil {
		t.Fatal("unknown restore target accepted")
	}
}

func scenarioRetention(t *testing.T, f storeFactory) {
	act := testAction("retention")
	act.SnapshotRetention = 2
	_, a, _, _ := syncedPair(t, f, act)
	var last model.SnapshotInfo
	for i := 0; i < 3; i++ {
		info, err := a.runner.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		last = info
	}
	snaps, err := a.runner.ListSnapshots(ctx)
	if err != nil || len(snaps) != 2 || snaps[0].TS != last.TS {
		t.Fatalf("snapshots = %+v, %v", snaps, err)
	}
}
