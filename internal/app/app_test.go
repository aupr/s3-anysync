package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

var ctx = context.Background()

func newTestApp(t *testing.T, store *storage.Memory) *App {
	t.Helper()
	a, err := Open(t.TempDir(), secrets.NewMemory(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
	a.LockSettle = 0
	t.Cleanup(func() { a.Close() })
	return a
}

func TestTwoMachinesThroughApp(t *testing.T) {
	store := storage.NewMemory()
	a, b := newTestApp(t, store), newTestApp(t, store)
	if a.MachineID == b.MachineID {
		t.Fatal("two data dirs share a machine id")
	}

	p, err := a.AddProfile(model.Profile{Name: "test", Bucket: "bkt", RootPrefix: `\cfg\`, AccessKeyID: "k"}, "secret")
	if err != nil || p.RootPrefix != "cfg" {
		t.Fatalf("AddProfile = %+v, %v", p, err)
	}
	if _, err := a.AddProfile(model.Profile{Name: "x", Bucket: "b"}, ""); err == nil {
		t.Fatal("profile without secret accepted")
	}
	res, err := a.TestProfile(ctx, p.ID)
	if err != nil || !res.ConditionalWrites {
		t.Fatalf("TestProfile = %+v, %v", res, err)
	}
	if saved, _ := a.DB.GetProfile(p.ID); !saved.ConditionalWrites {
		t.Fatal("conditional-write support not stored on the profile")
	}

	if _, err := a.CreateAction(ctx, p.ID, model.Action{Name: "Editor", Encrypt: true}, ""); !errors.Is(err, engine.ErrPassphraseMissing) {
		t.Fatalf("encrypted action without passphrase: %v", err)
	}
	act, err := a.CreateAction(ctx, p.ID, model.Action{Name: "Editor", Encrypt: true, PropagateDeletes: true}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	dirA := t.TempDir()
	os.WriteFile(filepath.Join(dirA, "settings.json"), []byte(`{"a":1}`), 0o644)
	it, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "/settings.json", Type: model.ItemFile}, filepath.Join(dirA, "settings.json"))
	if err != nil || it.RemoteName != "settings.json" {
		t.Fatalf("AddItem = %+v, %v", it, err)
	}
	ra, err := a.Runner(act.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := ra.Sync(ctx); err != nil || res.Uploaded != 1 {
		t.Fatalf("A sync = %+v, %v", res, err)
	}

	pb, err := b.AddProfile(model.Profile{Name: "test", Bucket: "bkt", RootPrefix: "cfg", AccessKeyID: "k"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	found, err := b.Discover(ctx, pb.ID)
	if err != nil || len(found) != 1 || found[0].ID != act.ID {
		t.Fatalf("Discover = %+v, %v", found, err)
	}
	if _, err := b.Join(ctx, pb.ID, act.ID, "wrong"); !errors.Is(err, engine.ErrDecrypt) {
		t.Fatalf("join with wrong passphrase: %v", err)
	}
	if _, err := b.DB.GetAction(act.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed join left an action behind: %v", err)
	}
	if _, err := b.Secrets.Get(secrets.ActionPassphraseKey(act.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("failed join stored a passphrase: %v", err)
	}
	if _, err := b.Join(ctx, pb.ID, act.ID, "pw"); err != nil {
		t.Fatal(err)
	}
	dirB := t.TempDir()
	if err := b.MapItem(ctx, act.ID, it.ID, filepath.Join(dirB, "settings.json")); err != nil {
		t.Fatal(err)
	}
	rb, _ := b.Runner(act.ID)
	if res, err := rb.Sync(ctx); err != nil || res.Downloaded != 1 {
		t.Fatalf("B sync = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dirB, "settings.json")); string(data) != `{"a":1}` {
		t.Fatalf("B file = %q", data)
	}

	// Re-pointing an item drops its baseline, so an empty new folder is a
	// first run (download), never a mass deletion in the cloud.
	dirB2 := t.TempDir()
	if err := b.MapItem(ctx, act.ID, it.ID, filepath.Join(dirB2, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if base, _ := b.DB.LoadBaseline(act.ID); len(base) != 0 {
		t.Fatalf("baseline not dropped after re-mapping: %+v", base)
	}
	if res, err := rb.Sync(ctx); err != nil || res.DeletedRemote != 0 || res.Downloaded != 1 {
		t.Fatalf("B sync after re-map = %+v, %v", res, err)
	}

	if err := b.SetMachineDirection(ctx, act.ID, model.CloudToLocal); err != nil {
		t.Fatal(err)
	}
	got, _ := b.DB.GetAction(act.ID)
	if got.DirectionFor(b.MachineID) != model.CloudToLocal || got.Machines[b.MachineID] != b.MachineName {
		t.Fatalf("direction override not stored: %+v", got)
	}
	if err := b.SetMachineDirection(ctx, act.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.DB.GetAction(act.ID); got.DirectionFor(b.MachineID) != model.TwoWay {
		t.Fatal("direction override not cleared")
	}
	if err := b.SetMachineDirection(ctx, act.ID, "up"); err == nil {
		t.Fatal("invalid direction accepted")
	}
}

func TestJoinMissingKeyCheck(t *testing.T) {
	store := storage.NewMemory()
	a, b := newTestApp(t, store), newTestApp(t, store)
	p, err := a.AddProfile(model.Profile{Name: "t", Bucket: "bkt", RootPrefix: "cfg", AccessKeyID: "k"}, "s")
	if err != nil {
		t.Fatal(err)
	}
	act, err := a.CreateAction(ctx, p.ID, model.Action{Name: "E", Encrypt: true}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
	if _, err := a.AddItem(ctx, act.ID, model.Item{RemoteName: "f.txt", Type: model.ItemFile}, filepath.Join(dir, "f.txt")); err != nil {
		t.Fatal(err)
	}
	r, _ := a.Runner(act.ID)
	if _, err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, engine.NewLayout("cfg", act.ID).KeyCheck()); err != nil {
		t.Fatal(err)
	}

	pb, _ := b.AddProfile(model.Profile{Name: "t", Bucket: "bkt", RootPrefix: "cfg", AccessKeyID: "k"}, "s")
	if _, err := b.Join(ctx, pb.ID, act.ID, "pw"); !errors.Is(err, engine.ErrKeyCheckMissing) {
		t.Fatalf("join without keycheck: %v", err)
	}
	if _, err := b.Secrets.Get(secrets.ActionPassphraseKey(act.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("passphrase stored despite failed verification: %v", err)
	}
	if _, err := b.DB.GetAction(act.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("action stored despite failed verification: %v", err)
	}
}

func TestLockMode(t *testing.T) {
	if lockMode(model.Profile{ConditionalWrites: true}) != storage.LockConditional {
		t.Fatal("conditional profile should use LockConditional")
	}
	if lockMode(model.Profile{ConditionalWrites: false}) != storage.LockBestEffort {
		t.Fatal("profile without conditional writes should use LockBestEffort")
	}
	a := newTestApp(t, storage.NewMemory())
	p, _ := a.AddProfile(model.Profile{Name: "t", Bucket: "b", AccessKeyID: "k"}, "s")
	d, err := a.deps(p, "x", false)
	if err != nil || d.LockMode != storage.LockBestEffort {
		t.Fatalf("deps lock mode = %v, %v", d.LockMode, err)
	}
}
