package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
)

func TestRefreshAction(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Now = fixedClock()
	db := newDB(t)
	d.Repo = db
	if err := db.PutProfile(model.Profile{ID: "p1", Name: "P", Bucket: "b"}); err != nil {
		t.Fatal(err)
	}
	local := basicAction("ra")
	local.ProfileID = "p1"
	local.UpdatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := CreateSharedAction(ctx, d, "root", local); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(d, "root", local)

	// Equal UpdatedAt keeps local.
	remote, _ := FetchAction(ctx, mem, "root", "ra")
	local.UpdatedAt = remote.UpdatedAt
	local.Name = "local-name"
	r = NewRunner(d, "root", local)
	if err := r.refreshAction(ctx); err != nil || r.Action().Name != "local-name" {
		t.Fatalf("equal: %v %+v", err, r.Action())
	}

	// The bucket is authoritative: a remote that differs is taken even when
	// its UpdatedAt is older than the local one (clock skew on an editor).
	local.UpdatedAt = remote.UpdatedAt.Add(time.Hour)
	r = NewRunner(d, "root", local)
	if err := r.refreshAction(ctx); err != nil || r.Action().Name != "ra" {
		t.Fatalf("older remote not taken: %v %+v", err, r.Action())
	}

	// Newer remote stored and returned, ProfileID kept.
	local.UpdatedAt = remote.UpdatedAt.Add(-time.Hour)
	r = NewRunner(d, "root", local)
	if err := r.refreshAction(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.Action(); got.Name != "ra" || got.ProfileID != "p1" {
		t.Fatalf("action = %+v", got)
	}
	stored, err := db.GetAction("ra")
	if err != nil || stored.Name != "ra" || stored.ProfileID != "p1" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

func TestRefreshActionNilRepo(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	a := basicAction("nr")
	a.UpdatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	CreateSharedAction(ctx, d, "root", a)
	remote, _ := FetchAction(ctx, mem, "root", "nr")
	a.UpdatedAt = remote.UpdatedAt.Add(-time.Hour)
	a.Name = "stale"
	r := NewRunner(d, "root", a)
	if err := r.refreshAction(ctx); err != nil || r.Action().Name != "nr" {
		t.Fatalf("err=%v action=%+v", err, r.Action())
	}
}

func TestRefreshActionEncrypted(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Passphrase = "pw"
	a := encAction("enc")
	if err := CreateSharedAction(ctx, d, "root", a); err != nil {
		t.Fatal(err)
	}
	if err := NewRunner(d, "root", a).refreshAction(ctx); err != nil {
		t.Fatal(err)
	}
	d.Passphrase = "wrong"
	if err := NewRunner(d, "root", a).refreshAction(ctx); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	// Missing keycheck with existing files.
	d.Passphrase = "pw"
	l := NewLayout("root", "enc")
	mem.Put(ctx, l.Current("f"), []byte("x"), nil)
	mem.Delete(ctx, l.KeyCheck())
	if err := NewRunner(d, "root", a).refreshAction(ctx); !errors.Is(err, ErrKeyCheckMissing) {
		t.Fatalf("missing keycheck: %v", err)
	}
}

func TestAddHistory(t *testing.T) {
	d := memDeps(storage.NewMemory(), "A")
	d.Now = fixedClock()
	a := basicAction("h")
	NewRunner(d, "root", a).addHistory("sync", d.Now(), "ok", "nil repo is a no-op")

	db := newDB(t)
	d.Repo = db
	NewRunner(d, "root", a).addHistory("sync", d.Now(), "ok", "msg")
	list, err := db.ListHistory("h", 10)
	if err != nil || len(list) != 1 || list[0].Kind != "sync" || list[0].Message != "msg" {
		t.Fatalf("history = %+v, %v", list, err)
	}
}
