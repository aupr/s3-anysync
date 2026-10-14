package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aupr/s3-anysync/internal/crypto"
	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
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

func encAction(id string) model.Action {
	a := basicAction(id)
	a.Encrypt = true
	return a
}

func TestVerifyPassphraseSeedsFreshAction(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Passphrase = "pw"
	a := encAction("fresh")
	if err := VerifyPassphrase(ctx, d, "", a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mem.Get(ctx, NewLayout("", "fresh").KeyCheck()); err != nil {
		t.Fatalf("keycheck not seeded: %v", err)
	}
	d.Passphrase = "other"
	if err := VerifyPassphrase(ctx, d, "", a); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong passphrase after seed: %v", err)
	}
}

func TestVerifyPassphraseMissingKeyCheckWithFiles(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Passphrase = "pw"
	l := NewLayout("", "old")
	mem.Put(ctx, l.Current("f.txt"), []byte("x"), nil)
	if err := VerifyPassphrase(ctx, d, "", encAction("old")); !errors.Is(err, ErrKeyCheckMissing) {
		t.Fatalf("want ErrKeyCheckMissing, got %v", err)
	}
	if _, err := mem.Head(ctx, l.KeyCheck()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("keycheck must not be written")
	}
}

// raceStore lets another machine seed the keycheck just before our PutIfAbsent.
type raceStore struct {
	*storage.Memory
	key  string
	body []byte
}

func (r *raceStore) PutIfAbsent(ctx context.Context, key string, body []byte) (storage.Object, error) {
	if key == r.key && r.body != nil {
		r.Memory.Put(ctx, key, r.body, nil)
		r.body = nil
	}
	return r.Memory.PutIfAbsent(ctx, key, body)
}

func TestVerifyPassphraseSeedRace(t *testing.T) {
	mem := storage.NewMemory()
	l := NewLayout("", "race")
	first, err := crypto.Encrypt([]byte(keyCheckPlaintext), "first")
	if err != nil {
		t.Fatal(err)
	}
	rs := &raceStore{Memory: mem, key: l.KeyCheck(), body: first}
	d := memDeps(mem, "B")
	d.Storage = rs
	d.Passphrase = "second"
	if err := VerifyPassphrase(ctx, d, "", encAction("race")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("racing machine with wrong passphrase: %v", err)
	}
	got, _, _ := mem.Get(ctx, l.KeyCheck())
	if !bytes.Equal(got, first) {
		t.Fatal("keycheck was overwritten")
	}

	// Same race, matching passphrase: verifies fine.
	l2 := NewLayout("", "race2")
	rs2 := &raceStore{Memory: mem, key: l2.KeyCheck(), body: first}
	d.Storage = rs2
	d.Passphrase = "first"
	if err := VerifyPassphrase(ctx, d, "", encAction("race2")); err != nil {
		t.Fatal(err)
	}
}

func TestCreateSharedActionOverwritesStaleKeyCheck(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Passphrase = "new"
	l := NewLayout("", "re")
	old, _ := crypto.Encrypt([]byte(keyCheckPlaintext), "old")
	mem.Put(ctx, l.KeyCheck(), old, nil)
	if err := CreateSharedAction(ctx, d, "", encAction("re")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassphrase(ctx, d, "", encAction("re")); err != nil {
		t.Fatalf("keycheck not replaced: %v", err)
	}
}

func TestUpdateSharedActionGuards(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Passphrase = "pw"
	d.Now = fixedClock()
	if err := CreateSharedAction(ctx, d, "", encAction("g")); err != nil {
		t.Fatal(err)
	}
	l := NewLayout("", "g")
	before, _, _ := mem.Get(ctx, l.ActionJSON())

	_, err := UpdateSharedAction(ctx, d, "", "g", func(a *model.Action) error { a.ID = "other"; return nil })
	if err == nil || !strings.Contains(err.Error(), "must not change the action id") {
		t.Fatalf("id change: %v", err)
	}
	_, err = UpdateSharedAction(ctx, d, "", "g", func(a *model.Action) error { a.Encrypt = false; return nil })
	if err == nil || !strings.Contains(err.Error(), "encryption cannot be changed") {
		t.Fatalf("encrypt change: %v", err)
	}
	after, _, _ := mem.Get(ctx, l.ActionJSON())
	if !bytes.Equal(before, after) {
		t.Fatal("action.json changed by rejected update")
	}
	if _, err := mem.Head(ctx, l.Lock()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("lock not released")
	}
}

func TestUpdateSharedActionLockedLeavesForeignLock(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	d.Now = fixedClock()
	CreateSharedAction(ctx, d, "", basicAction("x"))
	lock := []byte(`{"machineId":"B","expiresAt":"` + d.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`)
	l := NewLayout("", "x")
	mem.Put(ctx, l.Lock(), lock, nil)
	if _, err := UpdateSharedAction(ctx, d, "", "x", func(*model.Action) error { return nil }); !errors.Is(err, storage.ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	got, _, err := mem.Get(ctx, l.Lock())
	if err != nil || !bytes.Equal(got, lock) {
		t.Fatalf("foreign lock touched: %q, %v", got, err)
	}
}

// Triggering a real lock loss deterministically needs the renewal timers, so
// the cause-wrapping helper is tested directly.
func TestCtxErrLockLost(t *testing.T) {
	c, cancel := context.WithCancelCause(context.Background())
	cancel(fmt.Errorf("%w: gone", storage.ErrLockLost))
	err := ctxErr(c, c.Err())
	if !errors.Is(err, storage.ErrLockLost) || !errors.Is(err, context.Canceled) {
		t.Fatalf("ctxErr = %v", err)
	}
	plain := errors.New("boom")
	if got := ctxErr(context.Background(), plain); got != plain {
		t.Fatalf("ctxErr passthrough = %v", got)
	}
}

func TestDiscoverActionsTolerant(t *testing.T) {
	mem := storage.NewMemory()
	d := memDeps(mem, "A")
	for _, id := range []string{"z1", "a2", "a1"} {
		a := basicAction(id)
		a.Name = "same"
		if id == "z1" {
			a.Name = "alpha"
		}
		if err := CreateSharedAction(ctx, d, "cfg", a); err != nil {
			t.Fatal(err)
		}
	}
	mem.Put(ctx, NewLayout("cfg", "bad").ActionJSON(), []byte("{not json"), nil)
	// Folder "mis" holds an action whose ID says otherwise.
	other := basicAction("elsewhere")
	data, _ := json.Marshal(other)
	mem.Put(ctx, NewLayout("cfg", "mis").ActionJSON(), data, nil)

	list, err := DiscoverActions(ctx, mem, "cfg")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, ",") != "z1,a1,a2" {
		t.Fatalf("ids = %v", ids)
	}
}
