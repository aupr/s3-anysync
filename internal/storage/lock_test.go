package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func lockPair(mem *Memory, mode LockMode, clock func() time.Time) (*Locker, *Locker) {
	mk := func(id string) *Locker {
		return &Locker{Store: mem, Key: "x/.meta/lock", MachineID: id, TTL: time.Minute, Mode: mode, Now: clock}
	}
	return mk("A"), mk("B")
}

func testLockLifecycle(t *testing.T, mem *Memory, mode LockMode) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	a, b := lockPair(mem, mode, func() time.Time { return now })

	if err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquire(ctx); !errors.Is(err, ErrLocked) {
		t.Fatalf("B acquired a held lock: %v", err)
	}
	if err := a.Acquire(ctx); err != nil {
		t.Fatalf("A could not re-acquire its own lock: %v", err)
	}
	if err := b.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Head(ctx, "x/.meta/lock"); err != nil {
		t.Fatal("B's release removed A's lock")
	}
	if err := a.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Acquire(ctx); err != nil {
		t.Fatalf("B after A released: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := a.Acquire(ctx); err != nil {
		t.Fatalf("A could not take over an expired lock: %v", err)
	}
}

func TestLockConditional(t *testing.T) {
	testLockLifecycle(t, NewMemory(), LockConditional)
}

func TestLockBestEffort(t *testing.T) {
	mem := NewMemory()
	mem.NoConditional = true
	testLockLifecycle(t, mem, LockBestEffort)
}

func TestLockCorruptBodyIsTreatedAsExpired(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	mem.Put(ctx, "x/.meta/lock", []byte("garbage"), nil)
	a, _ := lockPair(mem, LockConditional, time.Now)
	if err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
}
