package engine

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func retentionRunner(t *testing.T, manualKeep int) (*Runner, *storage.Memory, *clock) {
	t.Helper()
	mem := storage.NewMemory()
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	d := memDeps(mem, "A")
	d.Now = c.now
	a := basicAction("ret")
	a.SnapshotRetention = manualKeep
	l := NewLayout("root", a.ID)
	mem.Put(ctx, l.Current("settings.json"), []byte("x"), map[string]string{metaSHA256: "h"})
	return NewRunner(d, "root", a), mem, c
}

func countSnapshots(t *testing.T, r *Runner) (manual, auto int) {
	t.Helper()
	list, err := r.ListSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Reason == ReasonManual {
			manual++
		} else {
			auto++
		}
	}
	return manual, auto
}

var oneFile = []model.FileEntry{{Path: "settings.json", Hash: "h"}}

func TestRetentionManualAndAutomaticAreSeparate(t *testing.T) {
	r, _, _ := retentionRunner(t, 2)
	for i := 0; i < 3; i++ {
		if _, err := r.writeSnapshot(ctx, ReasonManual, oneFile); err != nil {
			t.Fatal(err)
		}
		if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
			t.Fatal(err)
		}
	}
	if m, a := countSnapshots(t, r); m != 2 || a != 3 {
		t.Fatalf("manual=%d auto=%d, want 2 and 3", m, a)
	}
}

func TestRetentionAutomaticNewest50OrYoungerThan7Days(t *testing.T) {
	r, _, c := retentionRunner(t, 20)
	for i := 0; i < 55; i++ {
		if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
			t.Fatal(err)
		}
	}
	if _, a := countSnapshots(t, r); a != 55 {
		t.Fatalf("young automatic snapshots pruned: %d left", a)
	}
	c.set(c.now().Add(8 * 24 * time.Hour))
	if _, err := r.writeSnapshot(ctx, ReasonPreRestore, oneFile); err != nil {
		t.Fatal(err)
	}
	if _, a := countSnapshots(t, r); a != 50 {
		t.Fatalf("want the newest 50 automatic snapshots, got %d", a)
	}
}

func TestRetentionKeepsDeletionSnapshots30Days(t *testing.T) {
	r, _, c := retentionRunner(t, 20)
	for i := 0; i < 2; i++ {
		if _, err := r.writeSnapshotSpec(ctx, snapshotSpec{reason: ReasonAutoPreSync, files: oneFile, deletes: []string{"settings.json"}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 50; i++ {
		if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
			t.Fatal(err)
		}
	}
	c.set(c.now().Add(8 * 24 * time.Hour))
	if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListSnapshots(ctx)
	withDeletes := 0
	for _, s := range list {
		if len(s.Deletes) > 0 {
			withDeletes++
		}
	}
	if withDeletes != 2 || len(list) != 52 {
		t.Fatalf("after 8 days: %d snapshots, %d with deletions; want 52 and 2", len(list), withDeletes)
	}
	c.set(c.now().Add(23 * 24 * time.Hour)) // 31 days after the deletion snapshots
	if _, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile); err != nil {
		t.Fatal(err)
	}
	list, _ = r.ListSnapshots(ctx)
	for _, s := range list {
		if len(s.Deletes) > 0 {
			t.Fatalf("deletion snapshot older than 30 days and beyond the newest 50 kept: %s", s.TS)
		}
	}
}

func TestGuardSnapshotRecordsDeletions(t *testing.T) {
	_, a, _, _ := syncedPair(t, memoryStore, testAction("recorddeletes"))
	a.remove("app/snippets/go.json")
	expectCounts(t, a.sync(), 0, 0, 1, 0)
	list, err := a.runner.ListSnapshots(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("snapshots = %+v, %v", list, err)
	}
	if len(list[0].Deletes) != 1 || list[0].Deletes[0] != "snippets/go.json" {
		t.Fatalf("deletes = %v", list[0].Deletes)
	}
	if !strings.HasPrefix(list[0].Reason, "auto") {
		t.Fatalf("reason = %s", list[0].Reason)
	}
}

func TestRetentionNeverDeletesProtectedAutomaticOrUnparseable(t *testing.T) {
	r, mem, c := retentionRunner(t, 20)
	l := r.layout
	// 60 old automatic snapshots (names far in the past), one of them protected,
	// plus one with a name that is a valid ts pattern but an impossible date.
	var all []string
	for i := 0; i < 60; i++ {
		ts := time.Date(2025, 1, 1, 0, 0, i, 0, time.UTC).Format(snapshotTSLayout)
		mem.Put(ctx, l.SnapshotIndex(ts), []byte(`{"ts":"`+ts+`","reason":"auto-pre-sync"}`), nil)
		all = append(all, ts)
	}
	bad := "20241399T000000Z"
	mem.Put(ctx, l.SnapshotIndex(bad), []byte(`{"reason":"auto-pre-sync"}`), nil)
	c.set(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err := r.applyRetentionKeeping(ctx, all[0]); err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, k := range snapKeys(mem, l) {
		have[k] = true
	}
	if !have[l.SnapshotIndex(all[0])] {
		t.Fatal("protected automatic snapshot deleted")
	}
	if !have[l.SnapshotIndex(bad)] {
		t.Fatal("snapshot with an unparseable timestamp was deleted by the age rule")
	}
}

func TestRetentionKindCacheDoesNotGoStaleWhenTSIsReused(t *testing.T) {
	r, mem, c := retentionRunner(t, 1)
	l := r.layout
	first, err := r.writeSnapshot(ctx, ReasonAutoPreSync, oneFile)
	if err != nil {
		t.Fatal(err)
	}
	// Retention classified it as automatic (cached). Delete it and write a
	// manual snapshot that reuses the same timestamp.
	if err := r.deleteSnapshot(ctx, first.TS); err != nil {
		t.Fatal(err)
	}
	second, err := r.writeSnapshot(ctx, ReasonManual, oneFile)
	if err != nil {
		t.Fatal(err)
	}
	if second.TS != first.TS {
		t.Fatalf("expected the same ts to be reused, got %s then %s", first.TS, second.TS)
	}
	// With keep=1, a newer manual snapshot must evict this one only if it is
	// classified manual (its new reason); an automatic one would be young and kept.
	c.set(c.now().Add(10 * time.Second))
	if _, err := r.writeSnapshot(ctx, ReasonManual, oneFile); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Head(ctx, l.SnapshotIndex(second.TS)); err == nil {
		t.Fatal("snapshot with reused ts was still classified by its old reason (stale cache)")
	}
}
