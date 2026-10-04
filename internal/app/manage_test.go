package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	cur, _ := a.Action(act.ID)
	updated, err := a.UpdateRules(ctx, act.ID, rules, cur.UpdatedAt)
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
	if _, err := a.UpdateRules(ctx, act.ID, rules, updated.UpdatedAt); err == nil {
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
	found := false
	for _, it := range local.Items {
		if it.ID == folder.ID {
			found = true
			if it.LocalPaths[a.MachineID] != "" {
				t.Fatal("path still mapped")
			}
		}
	}
	if !found {
		t.Fatal("unmapped item missing from the local copy")
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

// bucketAndLocal returns the bucket copy and the local copy of an action.
func bucketAndLocal(t *testing.T, a *App, store *storage.Memory, id string) (model.Action, model.Action) {
	t.Helper()
	shared, err := engine.FetchAction(ctx, store, "", id)
	if err != nil {
		t.Fatal(err)
	}
	local, err := a.Action(id)
	if err != nil {
		t.Fatal(err)
	}
	return shared, local
}

func TestUpdateRulesRejectsInvalid(t *testing.T) {
	a, store, act, _, _, _ := manageFixture(t, false)
	good := act.Rules()
	cur, _ := a.Action(act.ID)
	sharedBefore, localBefore := bucketAndLocal(t, a, store, act.ID)
	bad := map[string]func(*model.Rules){
		"exclude":   func(r *model.Rules) { r.Excludes = []string{"["} },
		"name":      func(r *model.Rules) { r.Name = "" },
		"direction": func(r *model.Rules) { r.Direction = model.Direction("sideways") },
	}
	for name, mut := range bad {
		r := good
		mut(&r)
		if _, err := a.UpdateRules(ctx, act.ID, r, cur.UpdatedAt); err == nil {
			t.Errorf("%s: invalid rules accepted", name)
		}
		shared, local := bucketAndLocal(t, a, store, act.ID)
		if !reflect.DeepEqual(shared, sharedBefore) || !reflect.DeepEqual(local, localBefore) {
			t.Errorf("%s: a failed update changed a copy", name)
		}
	}
}

func TestAddItemAndCreateActionRejectBadExcludes(t *testing.T) {
	a, store, act, _, _, dir := manageFixture(t, false)
	sharedBefore, localBefore := bucketAndLocal(t, a, store, act.ID)
	_, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "x", Type: model.ItemDir, Excludes: []string{"a[b"}}, dir)
	if err == nil {
		t.Fatal("bad item exclude accepted")
	}
	shared, local := bucketAndLocal(t, a, store, act.ID)
	if !reflect.DeepEqual(shared, sharedBefore) || !reflect.DeepEqual(local, localBefore) {
		t.Fatal("failed AddItem changed a copy")
	}
	keys := func() int {
		l, _ := store.List(ctx, "")
		return len(l)
	}
	n := keys()
	profiles, _ := a.Profiles()
	if _, err := a.CreateAction(ctx, profiles[0].ID, model.Action{Name: "Bad", Excludes: []string{"["}}, ""); err == nil {
		t.Fatal("bad exclude accepted by CreateAction")
	}
	if keys() != n {
		t.Fatal("CreateAction published something")
	}
}

func TestAddItemReusingRemovedNameDeletesNothing(t *testing.T) {
	a, store, act, _, folder, _ := manageFixture(t, false)
	if err := a.RemoveItem(ctx, act.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate a stale baseline left behind by an earlier removal.
	base, _ := a.DB.LoadBaseline(act.ID)
	base["snippets/a.json"] = model.FileEntry{Size: 1}
	if err := a.DB.SaveBaseline(act.ID, base); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	if _, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "snippets", Type: model.ItemDir}, empty); err != nil {
		t.Fatal(err)
	}
	r, _ := a.Runner(act.ID)
	res, err := r.Sync(ctx)
	if err != nil || res.DeletedRemote != 0 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	if _, err := store.Head(ctx, engine.NewLayout("", act.ID).Current("snippets/a.json")); err != nil {
		t.Fatal("remote file deleted")
	}
	if _, err := os.Stat(filepath.Join(empty, "a.json")); err != nil {
		t.Fatalf("file did not come down: %v", err)
	}
}

// flakySecrets fails Delete while fail is set.
type flakySecrets struct {
	secrets.Store
	fail bool
}

func (f *flakySecrets) Delete(key string) error {
	if f.fail {
		return errors.New("keychain locked")
	}
	return f.Store.Delete(key)
}

func TestLeaveActionPassphraseFailure(t *testing.T) {
	a, _, act, _, _, _ := manageFixture(t, true)
	fs := &flakySecrets{Store: a.Secrets}
	a.Secrets = fs
	fs.fail = true
	err := a.LeaveAction(ctx, act.ID, false)
	if err == nil || !strings.Contains(err.Error(), "removed locally") || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("err = %v", err)
	}
	if _, err := a.Action(act.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("action kept")
	}
	// Retry: action is gone but the orphan secret is still cleaned up.
	fs.fail = false
	if err := a.LeaveAction(ctx, act.ID, false); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("retry err = %v", err)
	}
	if _, err := fs.Get(secrets.ActionPassphraseKey(act.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("orphan passphrase kept")
	}
}

func TestForcedLeaveEncryptedRemovesPassphrase(t *testing.T) {
	a, _, act, _, _, _ := manageFixture(t, true)
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return nil, errors.New("offline") }
	if err := a.LeaveAction(ctx, act.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Secrets.Get(secrets.ActionPassphraseKey(act.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("passphrase kept")
	}
}

func TestHistoryLimit(t *testing.T) {
	a, _, act, _, _, _ := manageFixture(t, false)
	r, _ := a.Runner(act.ID)
	for i := 0; i < 3; i++ {
		if _, err := r.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := a.History(act.ID, 0)
	if len(all) < 2 {
		t.Fatalf("only %d entries", len(all))
	}
	if h, err := a.History(act.ID, 1); err != nil || len(h) != 1 {
		t.Fatalf("limit 1 = %d, %v", len(h), err)
	}
}

func TestRemoveItemKeepsData(t *testing.T) {
	a, store, act, _, folder, dir := manageFixture(t, false)
	file := ""
	local0, _ := a.Action(act.ID)
	for _, it := range local0.Items {
		if it.RemoteName == "settings.json" {
			file = it.ID
		}
	}
	if file == "" {
		t.Fatal("file item not found")
	}
	if err := a.RemoveItem(ctx, act.ID, file); err != nil {
		t.Fatal(err)
	}
	base, _ := a.DB.LoadBaseline(act.ID)
	if _, ok := base["settings.json"]; ok {
		t.Fatal("baseline of the removed item kept")
	}
	local, _ := a.Action(act.ID)
	if len(local.Items) != 1 || local.Items[0].ID != folder.ID {
		t.Fatalf("local items = %+v", local.Items)
	}
	if _, err := store.Head(ctx, engine.NewLayout("", act.ID).Current("settings.json")); err != nil {
		t.Fatal("cloud copy removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal("local file removed")
	}
	r, _ := a.Runner(act.ID)
	if res, err := r.Sync(ctx); err != nil || res.DeletedRemote != 0 || res.DeletedLocal != 0 {
		t.Fatalf("sync after remove = %+v, %v", res, err)
	}
}

func TestItemsCannotContainTheDataFolder(t *testing.T) {
	a, _, act, file, folder, _ := manageFixture(t, false)
	data := a.DataDir
	os.MkdirAll(filepath.Join(data, "logs"), 0o755)
	bad := map[string]string{
		"data folder": data,
		"its parent":  filepath.Dir(data),
		"subfolder":   filepath.Join(data, "logs"),
	}
	for name, p := range bad {
		if _, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "x", Type: model.ItemDir}, p); !errors.Is(err, engine.ErrProtectedPath) {
			t.Errorf("AddItem %s: %v", name, err)
		}
		if err := a.MapItem(ctx, act.ID, folder.ID, p); !errors.Is(err, engine.ErrProtectedPath) {
			t.Errorf("MapItem %s: %v", name, err)
		}
	}
	got, _ := a.Action(act.ID)
	if len(got.Items) != 2 || got.Items[1].LocalPaths[a.MachineID] == data {
		t.Fatalf("a refused item was published: %+v", got.Items)
	}
	_ = file
}

func TestUpdateRulesDetectsEditConflict(t *testing.T) {
	a, store, act, _, _, _ := manageFixture(t, false)
	cur, _ := a.Action(act.ID)
	r := cur.Rules()
	r.Name = "first"
	first, err := a.UpdateRules(ctx, act.ID, r, cur.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	// An edit based on the old version is refused and changes nothing.
	r.Name = "second"
	if _, err := a.UpdateRules(ctx, act.ID, r, cur.UpdatedAt); !errors.Is(err, engine.ErrEditConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	if shared, _ := engine.FetchAction(ctx, store, "", act.ID); shared.Name != "first" {
		t.Fatalf("bucket copy = %q", shared.Name)
	}
	if _, err := a.UpdateRules(ctx, act.ID, r, time.Time{}); !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("zero version: %v", err)
	}

	// Another machine edits the bucket copy: the conflict refreshes the local copy.
	p, _ := a.DB.GetProfile(cur.ProfileID)
	d, err := a.deps(p, act.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := engine.UpdateSharedAction(ctx, d, p.RootPrefix, act.ID, func(x *model.Action) error { x.Name = "remote"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateRules(ctx, act.ID, r, first.UpdatedAt); !errors.Is(err, engine.ErrEditConflict) {
		t.Fatalf("edit after a remote change: %v", err)
	}
	local, _ := a.Action(act.ID)
	if local.Name != "remote" || !local.UpdatedAt.Equal(remote.UpdatedAt) || local.ProfileID != cur.ProfileID {
		t.Fatalf("local copy not refreshed: %+v", local)
	}
	if _, err := a.UpdateRules(ctx, act.ID, r, local.UpdatedAt); err != nil {
		t.Fatalf("edit on the fresh version: %v", err)
	}
}
