package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

// manageFixture: machine A with a synced action holding one file item and one dir item.
func manageFixture(t *testing.T, encrypt bool) (*App, *storage.Memory, model.Action, model.Item, model.Item, string) {
	t.Helper()
	store := storage.NewMemory()
	a := newTestApp(t, store)
	p, err := a.AddProfile(model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, "s")
	if err != nil {
		t.Fatal(err)
	}
	pass := ""
	if encrypt {
		pass = "pw"
	}
	act, err := a.CreateAction(ctx, p.ID, model.Action{Name: "Editor", PropagateDeletes: true, Encrypt: encrypt}, pass)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}"), 0o644)
	os.MkdirAll(filepath.Join(dir, "snippets"), 0o755)
	os.WriteFile(filepath.Join(dir, "snippets", "a.json"), []byte("a"), 0o644)
	file, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "settings.json", Type: model.ItemFile}, filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "snippets", Type: model.ItemDir}, filepath.Join(dir, "snippets"))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := a.Runner(act.ID)
	if res, err := r.Sync(ctx); err != nil || res.Uploaded != 2 {
		t.Fatalf("setup sync = %+v, %v", res, err)
	}
	return a, store, act, file, folder, dir
}

func TestUpdateRules(t *testing.T) {
	a, store, act, _, _, _ := manageFixture(t, false)
	rules := act.Rules()
	rules.Name = "Editor settings"
	rules.Triggers.OnChange = true
	rules.Triggers.Schedule = model.Schedule{Kind: model.ScheduleDaily, At: "09:30"}
	rules.Excludes = []string{"*.log"}
	updated, err := a.UpdateRules(ctx, act.ID, rules)
	if err != nil || updated.Name != "Editor settings" || !updated.Triggers.OnChange {
		t.Fatalf("UpdateRules = %+v, %v", updated, err)
	}
	shared, err := engine.FetchAction(ctx, store, "", act.ID)
	if err != nil || shared.Name != "Editor settings" || shared.Excludes[0] != "*.log" {
		t.Fatalf("bucket copy = %+v, %v", shared, err)
	}
	if local, _ := a.Action(act.ID); local.Triggers.Schedule.At != "09:30" {
		t.Fatalf("local copy = %+v", local.Triggers)
	}
	rules.Triggers.Schedule = model.Schedule{Kind: model.ScheduleDaily, At: "9:30"}
	rules.Name = "Bad"
	if _, err := a.UpdateRules(ctx, act.ID, rules); err == nil {
		t.Fatal("invalid schedule accepted")
	}
	if shared, _ := engine.FetchAction(ctx, store, "", act.ID); shared.Name != "Editor settings" {
		t.Fatal("an invalid update was published")
	}
}

func TestUnmapAndRemoveItem(t *testing.T) {
	a, store, act, file, folder, _ := manageFixture(t, false)
	if err := a.UnmapItem(ctx, act.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	base, err := a.DB.LoadBaseline(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := base["snippets/a.json"]; ok {
		t.Fatal("baseline of the unmapped item kept")
	}
	local, _ := a.Action(act.ID)
	for _, it := range local.Items {
		if it.ID == folder.ID && it.LocalPaths[a.MachineID] != "" {
			t.Fatal("path still mapped")
		}
	}
	r, _ := a.Runner(act.ID)
	if res, err := r.Sync(ctx); err != nil || res.DeletedRemote != 0 {
		t.Fatalf("sync after unmap = %+v, %v", res, err)
	}
	if _, err := store.Head(ctx, engine.NewLayout("", act.ID).Current("snippets/a.json")); err != nil {
		t.Fatal("cloud copy of the unmapped item removed")
	}

	if err := a.RemoveItem(ctx, act.ID, file.ID); err != nil {
		t.Fatal(err)
	}
	shared, _ := engine.FetchAction(ctx, store, "", act.ID)
	if len(shared.Items) != 1 || shared.Items[0].ID != folder.ID {
		t.Fatalf("items after remove = %+v", shared.Items)
	}
	if err := a.RemoveItem(ctx, act.ID, "nope"); err == nil {
		t.Fatal("removing an unknown item accepted")
	}
	if err := a.UnmapItem(ctx, act.ID, "nope"); err == nil {
		t.Fatal("unmapping an unknown item accepted")
	}
}

func TestLeaveAction(t *testing.T) {
	a, store, act, _, _, _ := manageFixture(t, true)
	if err := a.SetPaused(act.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := a.LeaveAction(ctx, act.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Action(act.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("local action kept: %v", err)
	}
	if _, err := a.Secrets.Get(secrets.ActionPassphraseKey(act.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("passphrase kept")
	}
	if p, _ := a.Paused(act.ID); p {
		t.Fatal("pause flag kept")
	}
	shared, err := engine.FetchAction(ctx, store, "", act.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := shared.Machines[a.MachineID]; ok {
		t.Fatal("machine still listed in the shared action")
	}
	for _, it := range shared.Items {
		if it.LocalPaths[a.MachineID] != "" {
			t.Fatal("machine path still in the shared action")
		}
	}
	if _, err := store.Head(ctx, engine.NewLayout("", act.ID).Current("settings.json")); err != nil {
		t.Fatal("cloud files removed by leaving")
	}
}

func TestLeaveActionOffline(t *testing.T) {
	a, _, act, _, _, _ := manageFixture(t, false)
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return nil, errors.New("offline") }
	if err := a.LeaveAction(ctx, act.ID, false); err == nil {
		t.Fatal("leave without force succeeded offline")
	}
	if _, err := a.Action(act.ID); err != nil {
		t.Fatal("failed leave removed the local action")
	}
	if err := a.LeaveAction(ctx, act.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Action(act.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("forced leave kept the local action")
	}
}

func TestHistoryLogLevelAndPause(t *testing.T) {
	a, _, act, _, _, _ := manageFixture(t, false)
	h, err := a.History(act.ID, 0)
	if err != nil || len(h) == 0 || h[0].Kind != model.HistorySync {
		t.Fatalf("history = %+v, %v", h, err)
	}
	if lvl, err := a.LogLevel(); err != nil || lvl != "info" {
		t.Fatalf("default level = %q, %v", lvl, err)
	}
	if err := a.SetLogLevel("debug"); err != nil {
		t.Fatal(err)
	}
	if lvl, _ := a.LogLevel(); lvl != "debug" {
		t.Fatalf("level = %q", lvl)
	}
	if err := a.SetLogLevel("loud"); err == nil {
		t.Fatal("unknown level accepted")
	}
	if c, err := a.Conflicts(act.ID); err != nil || len(c) != 0 {
		t.Fatalf("conflicts = %v, %v", c, err)
	}
	if p, err := a.PendingDeletes(act.ID); err != nil || p != nil {
		t.Fatalf("pending = %v, %v", p, err)
	}
}
