package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func TestCreateFetchDiscover(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	for _, id := range []string{"b-action", "a-action"} {
		a := basicAction(id)
		if err := CreateSharedAction(ctx, d, "cfg", a); err != nil {
			t.Fatal(err)
		}
	}
	mem.Put(ctx, "cfg/stray/readme.txt", []byte("x"), nil)

	got, err := FetchAction(ctx, mem, "cfg", "a-action")
	if err != nil || got.Name != "a-action" || got.Machines == nil {
		t.Fatalf("FetchAction = %+v, %v", got, err)
	}
	if _, err := FetchAction(ctx, mem, "cfg", "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing action: %v", err)
	}
	list, err := DiscoverActions(ctx, mem, "cfg")
	if err != nil || len(list) != 2 || list[0].ID != "a-action" {
		t.Fatalf("DiscoverActions = %+v, %v", list, err)
	}
}

func TestUpdateSharedAction(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Now = fixedClock()
	a := basicAction("x")
	CreateSharedAction(ctx, d, "", a)

	got, err := UpdateSharedAction(ctx, d, "", "x", func(a *model.Action) error {
		a.Items[0].LocalPaths["A"] = "~/s.json"
		return nil
	})
	if err != nil || got.Items[0].LocalPaths["A"] != "~/s.json" || !got.UpdatedAt.Equal(d.Now()) {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if _, err := mem.Head(ctx, NewLayout("", "x").Lock()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("lock not released after update")
	}

	// Validation errors are not published.
	_, err = UpdateSharedAction(ctx, d, "", "x", func(a *model.Action) error { a.Name = ""; return nil })
	if err == nil {
		t.Fatal("invalid update accepted")
	}

	// A lock held by another machine blocks updates.
	mem.Put(ctx, NewLayout("", "x").Lock(), []byte(`{"machineId":"B","expiresAt":"`+d.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}`), nil)
	if _, err := UpdateSharedAction(ctx, d, "", "x", func(*model.Action) error { return nil }); !errors.Is(err, storage.ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
}

func TestVerifyPassphrase(t *testing.T) {
	mem := storage.NewMemory()
	a := basicAction("enc")
	a.Encrypt = true
	d := memDeps(mem, "A")
	if err := CreateSharedAction(ctx, d, "", a); !errors.Is(err, ErrPassphraseMissing) {
		t.Fatalf("create without passphrase: %v", err)
	}
	d.Passphrase = "right"
	if err := CreateSharedAction(ctx, d, "", a); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassphrase(ctx, d, "", a); err != nil {
		t.Fatal(err)
	}
	d.Passphrase = "wrong"
	if err := VerifyPassphrase(ctx, d, "", a); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	d.Passphrase = ""
	if err := VerifyPassphrase(ctx, d, "", a); !errors.Is(err, ErrPassphraseMissing) {
		t.Fatalf("no passphrase: %v", err)
	}
	if err := VerifyPassphrase(ctx, d, "", basicAction("plain")); err != nil {
		t.Fatalf("unencrypted action: %v", err)
	}
}
