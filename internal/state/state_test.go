package state

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	// A space in the path mirrors "~/Library/Application Support".
	db, err := Open(filepath.Join(t.TempDir(), "with space", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesParentAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "with space", "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("k", "v"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v, ok, err := db.GetSetting("k"); err != nil || !ok || v != "v" {
		t.Fatalf("after reopen: %q %v %v", v, ok, err)
	}
}

func TestMachineIdentityIsStable(t *testing.T) {
	db := openTest(t)
	id1, name1, err := db.MachineIdentity()
	if err != nil || id1 == "" || name1 == "" {
		t.Fatalf("identity: %q %q %v", id1, name1, err)
	}
	id2, _, _ := db.MachineIdentity()
	if id1 != id2 {
		t.Fatal("machine id changed between calls")
	}
	if err := db.SetMachineName("Work Mac"); err != nil {
		t.Fatal(err)
	}
	if _, name, _ := db.MachineIdentity(); name != "Work Mac" {
		t.Fatalf("name = %q", name)
	}
	if err := db.SetMachineName("  "); err == nil {
		t.Fatal("empty name accepted")
	}
}

func TestProfilesAndActions(t *testing.T) {
	db := openTest(t)
	p := model.Profile{ID: "p1", Name: "Wasabi", Bucket: "b"}
	if err := db.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if got, err := db.GetProfile("p1"); err != nil || got.Name != "Wasabi" {
		t.Fatalf("GetProfile = %+v, %v", got, err)
	}
	if _, err := db.GetProfile("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing profile: %v", err)
	}

	a := model.Action{ID: "a1", Name: "Editor", ProfileID: "p1"}
	a.ApplyDefaults()
	if err := db.PutAction(a); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAction("a1")
	if err != nil || got.ProfileID != "p1" || got.Name != "Editor" {
		t.Fatalf("GetAction = %+v, %v", got, err)
	}
	if list, _ := db.ListActions(); len(list) != 1 {
		t.Fatalf("ListActions len %d", len(list))
	}
	if err := db.DeleteProfile("p1"); err == nil {
		t.Fatal("deleted a profile that is in use")
	}
	if err := db.PutAction(model.Action{ID: "a2", Name: "x"}); err == nil {
		t.Fatal("action without profile accepted")
	}
	if err := db.DeleteAction("a1"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProfile("p1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.ListProfiles(); len(list) != 0 {
		t.Fatalf("ListProfiles len %d", len(list))
	}
}

func TestBaselines(t *testing.T) {
	db := openTest(t)
	b := map[string]model.FileEntry{
		"a.json": {Path: "a.json", Hash: "h1", Size: 3, MTime: 10},
		"d/b":    {Path: "d/b", Hash: "h2", Size: 4, MTime: 20},
	}
	if err := db.SaveBaseline("a1", b); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadBaseline("a1")
	if err != nil || len(got) != 2 || got["d/b"].Hash != "h2" || got["a.json"].MTime != 10 {
		t.Fatalf("LoadBaseline = %+v, %v", got, err)
	}
	delete(b, "d/b")
	db.SaveBaseline("a1", b)
	if got, _ := db.LoadBaseline("a1"); len(got) != 1 {
		t.Fatalf("baseline not replaced: %+v", got)
	}
	if got, _ := db.LoadBaseline("other"); got == nil || len(got) != 0 {
		t.Fatal("missing baseline should be an empty, non-nil map")
	}
}

func TestConflictsAndHistory(t *testing.T) {
	db := openTest(t)
	cs := []model.Conflict{{Path: "z"}, {Path: "a", Local: &model.FileEntry{Hash: "l"}}}
	if err := db.SaveConflicts("a1", cs); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadConflicts("a1")
	if err != nil || len(got) != 2 || got[0].Path != "a" || got[0].Local.Hash != "l" {
		t.Fatalf("LoadConflicts = %+v, %v", got, err)
	}
	db.SaveConflicts("a1", nil)
	if got, _ := db.LoadConflicts("a1"); len(got) != 0 {
		t.Fatal("conflicts not cleared")
	}

	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, outcome := range []string{"ok", "error"} {
		db.AddHistory(model.HistoryEntry{ActionID: "a1", Kind: "sync", StartedAt: start, FinishedAt: start.Add(time.Duration(i) * time.Second), Outcome: outcome, Message: "m"})
	}
	h, err := db.ListHistory("a1", 10)
	if err != nil || len(h) != 2 || h[0].Outcome != "error" || !h[0].StartedAt.Equal(start) {
		t.Fatalf("ListHistory = %+v, %v", h, err)
	}
}

func TestPendingDeletesAndPause(t *testing.T) {
	db := openTest(t)
	if p, err := db.LoadPendingDeletes("a1"); err != nil || p != nil {
		t.Fatalf("empty: %+v %v", p, err)
	}
	want := &model.PendingDeletes{Paths: []string{"x", "y"}, Approved: true, DetectedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	if err := db.SavePendingDeletes("a1", want); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadPendingDeletes("a1")
	if err != nil || got == nil || len(got.Paths) != 2 || !got.Approved || !got.DetectedAt.Equal(want.DetectedAt) {
		t.Fatalf("loaded %+v %v", got, err)
	}
	if err := db.SavePendingDeletes("a1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.LoadPendingDeletes("a1"); got != nil {
		t.Fatal("nil did not clear pending deletes")
	}
	if p, _ := db.Paused("a1"); p {
		t.Fatal("paused by default")
	}
	db.SetPaused("a1", true)
	if p, _ := db.Paused("a1"); !p {
		t.Fatal("pause not stored")
	}
	db.SetPaused("a1", false)
	if p, _ := db.Paused("a1"); p {
		t.Fatal("pause not cleared")
	}
}

func TestDeleteActionClearsActionState(t *testing.T) {
	db := openTest(t)
	db.PutProfile(model.Profile{ID: "p1", Name: "p", Bucket: "b"})
	a := model.Action{ID: "a1", Name: "x", ProfileID: "p1"}
	a.ApplyDefaults()
	if err := db.PutAction(a); err != nil {
		t.Fatal(err)
	}
	db.SetPaused("a1", true)
	db.SavePendingDeletes("a1", &model.PendingDeletes{Paths: []string{"x"}})
	if err := db.DeleteAction("a1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := db.Paused("a1"); p {
		t.Fatal("pause flag survived DeleteAction")
	}
	if got, _ := db.LoadPendingDeletes("a1"); got != nil {
		t.Fatal("pending deletes survived DeleteAction")
	}
}
