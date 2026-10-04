package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
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

const (
	// DefaultLockTTL is the lock lifetime when Locker.TTL is zero.
	DefaultLockTTL = 2 * time.Minute
	// DefaultSkewMargin is the clock-skew tolerance when Locker.SkewMargin is zero.
	DefaultSkewMargin = 30 * time.Second
)

// ErrLocked means another machine holds an unexpired lock.
var ErrLocked = errors.New("storage: locked by another machine")

// ErrLockLost means this machine no longer owns the lock it was holding.
var ErrLockLost = errors.New("storage: lock lost")

// LockInfo is the lock object's JSON body.
type LockInfo struct {
	MachineID string    `json:"machineId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Locker takes and releases the per-action lock object.
//
// Conditional mode is atomic. Best-effort mode is read-then-write
// (last-writer-wins) followed by a read-back after Settle; with Settle == 0
// the read-back happens immediately and so proves nothing about racing
// writers. Use it only for providers lacking conditional writes.
type Locker struct {
	Store     Storage
	Key       string
	MachineID string
	// TTL is the lock lifetime; zero or negative means DefaultLockTTL.
	TTL  time.Duration
	Mode LockMode
	// Settle is the best-effort wait between write and read-back.
	Settle time.Duration
	// SkewMargin is how long past ExpiresAt (by the local clock) another
	// machine's lock still counts as held, to tolerate clock skew. 0 means
	// DefaultSkewMargin; negative means no margin.
	SkewMargin time.Duration
	// RenewEvery is the Hold renewal period; 0 means TTL/3. It is clamped to
	// [1ms, TTL/2] (the 1ms floor wins for tiny TTLs), so any value is safe.
	RenewEvery time.Duration
	Now        func() time.Time
	// Logger receives warnings (e.g. a failed Release inside Hold's release
	// func). nil discards them.
	Logger *slog.Logger
}

func (l *Locker) logger() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (l *Locker) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Locker) ttl() time.Duration {
	if l.TTL <= 0 {
		return DefaultLockTTL
	}
	return l.TTL
}

func (l *Locker) skewMargin() time.Duration {
	switch {
	case l.SkewMargin == 0:
		return DefaultSkewMargin
	case l.SkewMargin < 0:
		return 0
	}
	return l.SkewMargin
}

const minRenew = time.Millisecond

// renewEvery returns the renewal period clamped to [minRenew, TTL/2]; it is
// never <= 0, so time.NewTicker cannot panic.
func (l *Locker) renewEvery() time.Duration {
	ttl := l.ttl()
	r := l.RenewEvery
	if r <= 0 {
		r = ttl / 3
	}
	if r > ttl/2 {
		r = ttl / 2
	}
	if r < minRenew {
		r = minRenew
	}
	return r
}

// renewTimeout bounds a single Renew call: min(renewEvery, TTL/3), at least 1ms.
func (l *Locker) renewTimeout() time.Duration {
	t := l.renewEvery()
	if third := l.ttl() / 3; third < t {
		t = third
	}
	if t < minRenew {
		t = minRenew
	}
	return t
}

// cutoff is how long after the last request was sent the held context must be
// cancelled: one renew interval before the expiry that request wrote.
func (l *Locker) cutoff() time.Duration {
	ttl := l.ttl()
	safety := l.renewEvery()
	if safety > ttl/3 { // keep cutoff after the next renewal when RenewEvery is clamped to TTL/2
		safety = ttl / 3
	}
	return ttl - safety
}

func (l *Locker) body() []byte {
	b, _ := json.Marshal(LockInfo{MachineID: l.MachineID, ExpiresAt: l.now().Add(l.ttl()).UTC()})
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
	return ok && info.MachineID != l.MachineID && l.now().Before(info.ExpiresAt.Add(l.skewMargin()))
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
	return fmt.Errorf("%w: contention persisted after 3 attempts", ErrLocked)
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
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: lock object vanished after write", ErrLocked)
	}
	if err != nil {
		return err
	}
	info, ok := readLock(data)
	if !ok {
		return fmt.Errorf("%w: lock object unreadable after write", ErrLocked)
	}
	if info.MachineID != l.MachineID {
		return lockedErr(info)
	}
	return nil
}

// Renew extends the lock's expiry. It returns an error wrapping ErrLockLost
// when the lock is missing, corrupt or owned by another machine.
func (l *Locker) Renew(ctx context.Context) error {
	data, obj, err := l.Store.Get(ctx, l.Key)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: lock object is gone", ErrLockLost)
	}
	if err != nil {
		return err
	}
	info, ok := readLock(data)
	if !ok {
		return fmt.Errorf("%w: lock object unreadable", ErrLockLost)
	}
	if info.MachineID != l.MachineID {
		return fmt.Errorf("%w: now held by %s", ErrLockLost, info.MachineID)
	}
	if l.Mode == LockBestEffort {
		_, err = l.Store.Put(ctx, l.Key, l.body(), nil)
		return err
	}
	_, err = l.Store.PutIfMatch(ctx, l.Key, l.body(), obj.ETag)
	if errors.Is(err, ErrPreconditionFailed) || errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: lock changed during renewal", ErrLockLost)
	}
	return err
}

// Release deletes the lock if this machine owns it and it has not expired.
// An expired own lock is left to lapse on its own, since another machine may
// already have taken it over between our read and a delete.
func (l *Locker) Release(ctx context.Context) error {
	data, _, err := l.Store.Get(ctx, l.Key)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if info, ok := readLock(data); ok {
		if info.MachineID != l.MachineID || !l.now().Before(info.ExpiresAt) {
			return nil
		}
	}
	return l.Store.Delete(ctx, l.Key)
}

// Hold acquires the lock and keeps renewing it in the background. Use the
// returned context for all remote work: it is cancelled with cause
// ErrLockLost if the lock is lost, or if renewals keep failing until one
// renew interval before the last written expiry (always before another
// machine may take over). The returned release func is idempotent, stops
// renewal and releases the lock; it is a no-op func when err != nil.
func (l *Locker) Hold(ctx context.Context) (context.Context, func(), error) {
	sent := l.now() // the expiry written is based on a time no earlier than this
	if err := l.Acquire(ctx); err != nil {
		return ctx, func() {}, err
	}
	held, cancel := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	lastOK := sent
	getLastOK := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return lastOK
	}
	lost := func(err error) {
		cancel(fmt.Errorf("%w: renewal failing since %s: %v", ErrLockLost, getLastOK().Format(time.RFC3339), err))
	}
	cutoff := l.cutoff()

	// Watchdog: cancels at the local cutoff even if a tick or a Renew is stuck.
	wg.Add(2)
	go func() {
		defer wg.Done()
		remaining := func() time.Duration {
			d := cutoff - l.now().Sub(getLastOK())
			if d < minRenew {
				d = minRenew
			}
			return d
		}
		t := time.NewTimer(remaining())
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-held.Done():
				return
			case <-t.C:
			}
			if l.now().Sub(getLastOK()) >= cutoff {
				lost(errors.New("no successful renewal before cutoff"))
				return
			}
			t.Reset(remaining())
		}
	}()
	go func() {
		defer wg.Done()
		tick := time.NewTicker(l.renewEvery())
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-held.Done():
				return
			case <-tick.C:
			}
			before := l.now()
			rctx, rcancel := context.WithTimeout(held, l.renewTimeout())
			err := l.Renew(rctx)
			rcancel()
			switch {
			case err == nil:
				mu.Lock()
				lastOK = before
				mu.Unlock()
			case errors.Is(err, ErrLockLost):
				cancel(err)
				return
			case held.Err() != nil:
				return
			case l.now().Sub(getLastOK()) >= cutoff:
				lost(err)
				return
			}
		}
	}()
	var once sync.Once
	release := func() {
		once.Do(func() {
			close(stop)
			cancel(nil) // abort any in-flight Renew; cause stays context.Canceled
			wg.Wait()
			if errors.Is(context.Cause(held), ErrLockLost) {
				// The lock is no longer ours: releasing could only block on a
				// broken store or race the machine that took it over.
				return
			}
			rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer rcancel()
			if err := l.Release(rctx); err != nil {
				l.logger().Warn("storage: lock release failed", "key", l.Key, "err", err)
			}
		})
	}
	return held, release, nil
}
