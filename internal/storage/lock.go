package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// LockMode selects how the lock is taken.
type LockMode int

const (
	// LockConditional uses If-None-Match / If-Match writes (atomic).
	LockConditional LockMode = iota
	// LockBestEffort writes, waits Settle, then reads back the owner. Used
	// for providers that ignore conditional writes.
	LockBestEffort
)

// ErrLocked means another machine holds an unexpired lock.
var ErrLocked = errors.New("storage: locked by another machine")

// LockInfo is the lock object's JSON body.
type LockInfo struct {
	MachineID string    `json:"machineId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Locker takes and releases the per-action lock object.
type Locker struct {
	Store     Storage
	Key       string
	MachineID string
	TTL       time.Duration
	Mode      LockMode
	Settle    time.Duration
	Now       func() time.Time
}

func (l *Locker) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Locker) body() []byte {
	b, _ := json.Marshal(LockInfo{MachineID: l.MachineID, ExpiresAt: l.now().Add(l.TTL).UTC()})
	return b
}

// readLock parses a lock body. ok is false for garbage, which is treated as expired.
func readLock(data []byte) (LockInfo, bool) {
	var info LockInfo
	if json.Unmarshal(data, &info) != nil || info.MachineID == "" {
		return info, false
	}
	return info, true
}

func (l *Locker) heldByOther(info LockInfo, ok bool) bool {
	return ok && info.MachineID != l.MachineID && l.now().Before(info.ExpiresAt)
}

func lockedErr(info LockInfo) error {
	return fmt.Errorf("%w (%s until %s)", ErrLocked, info.MachineID, info.ExpiresAt.Format(time.RFC3339))
}

// Acquire takes the lock, replacing an expired or own lock.
func (l *Locker) Acquire(ctx context.Context) error {
	if l.Mode == LockBestEffort {
		return l.acquireBestEffort(ctx)
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, err := l.Store.PutIfAbsent(ctx, l.Key, l.body())
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrPreconditionFailed) {
			return err
		}
		data, obj, err := l.Store.Get(ctx, l.Key)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if info, ok := readLock(data); l.heldByOther(info, ok) {
			return lockedErr(info)
		}
		_, err = l.Store.PutIfMatch(ctx, l.Key, l.body(), obj.ETag)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrPreconditionFailed) && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return ErrLocked
}

func (l *Locker) acquireBestEffort(ctx context.Context) error {
	data, _, err := l.Store.Get(ctx, l.Key)
	switch {
	case err == nil:
		if info, ok := readLock(data); l.heldByOther(info, ok) {
			return lockedErr(info)
		}
	case !errors.Is(err, ErrNotFound):
		return err
	}
	if _, err := l.Store.Put(ctx, l.Key, l.body(), nil); err != nil {
		return err
	}
	if l.Settle > 0 {
		t := time.NewTimer(l.Settle)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	data, _, err = l.Store.Get(ctx, l.Key)
	if err != nil {
		return err
	}
	if info, ok := readLock(data); !ok || info.MachineID != l.MachineID {
		return lockedErr(info)
	}
	return nil
}

// Release deletes the lock if this machine owns it.
func (l *Locker) Release(ctx context.Context) error {
	data, _, err := l.Store.Get(ctx, l.Key)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if info, ok := readLock(data); ok && info.MachineID != l.MachineID {
		return nil
	}
	return l.Store.Delete(ctx, l.Key)
}
