package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

// testClock is a controllable clock safe for concurrent use.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const lockKey = "x/.meta/lock"

func readInfo(t *testing.T, mem *Memory) LockInfo {
	t.Helper()
	data, _, err := mem.Get(context.Background(), lockKey)
	if err != nil {
		t.Fatal(err)
	}
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestLockSkewMargin(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		margin  time.Duration
		expired time.Duration
		locked  bool
	}{
		{"within default margin", 0, 10 * time.Second, true},
		{"beyond default margin", 0, 40 * time.Second, false},
		{"negative disables margin", -1, 1 * time.Second, false},
		{"explicit margin", time.Minute, 40 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := NewMemory()
			clk := newTestClock()
			a, b := lockPair(mem, LockConditional, clk.Now)
			a.SkewMargin, b.SkewMargin = tc.margin, tc.margin
			if err := a.Acquire(ctx); err != nil {
				t.Fatal(err)
			}
			clk.Advance(time.Minute + tc.expired)
			err := b.Acquire(ctx)
			if tc.locked && !errors.Is(err, ErrLocked) {
				t.Fatalf("want ErrLocked, got %v", err)
			}
			if !tc.locked && err != nil {
				t.Fatalf("want takeover, got %v", err)
			}
		})
	}
}

func TestLockDefaults(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	clk := newTestClock()
	a := &Locker{Store: mem, Key: lockKey, MachineID: "A", Now: clk.Now}
	if err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readInfo(t, mem).ExpiresAt.Sub(clk.Now()); got != DefaultLockTTL {
		t.Fatalf("TTL default = %v, want %v", got, DefaultLockTTL)
	}
	if a.TTL != 0 || a.SkewMargin != 0 {
		t.Fatal("defaults must not mutate the struct")
	}
}

func testRenew(t *testing.T, mem *Memory, mode LockMode) {
	ctx := context.Background()
	clk := newTestClock()
	a, b := lockPair(mem, mode, clk.Now)
	if err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	clk.Advance(30 * time.Second)
	if err := a.Renew(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := readInfo(t, mem).ExpiresAt, clk.Now().Add(time.Minute); !got.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got, want)
	}
	if err := b.Renew(ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("foreign Renew = %v, want ErrLockLost", err)
	}
	if err := a.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Renew(ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("Renew after delete = %v, want ErrLockLost", err)
	}
}

func TestLockRenewConditional(t *testing.T) { testRenew(t, NewMemory(), LockConditional) }

func TestLockRenewBestEffort(t *testing.T) {
	mem := NewMemory()
	mem.NoConditional = true
	testRenew(t, mem, LockBestEffort)
}

func TestLockRenewCorruptBody(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	a, _ := lockPair(mem, LockConditional, time.Now)
	mem.Put(ctx, lockKey, []byte("garbage"), nil)
	if err := a.Renew(ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("got %v", err)
	}
}

func TestLockReleaseSafety(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	clk := newTestClock()
	a, b := lockPair(mem, LockConditional, clk.Now)

	// Own expired lock is left alone.
	if err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Minute)
	if err := a.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Head(ctx, lockKey); err != nil {
		t.Fatal("Release deleted our own expired lock")
	}

	// Another machine's lock is left alone.
	clk.Advance(time.Minute)
	if err := b.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Head(ctx, lockKey); err != nil {
		t.Fatal("Release deleted another machine's lock")
	}

	// Our own live lock is deleted.
	if err := b.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Head(ctx, lockKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("live own lock not deleted: %v", err)
	}
}

func holdPair(mem *Memory, mode LockMode, clk *testClock) (*Locker, *Locker) {
	a, b := lockPair(mem, mode, clk.Now)
	a.RenewEvery = 10 * time.Millisecond
	return a, b
}

func TestHoldRenewsAndKeepsLock(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	clk := newTestClock()
	a, b := holdPair(mem, LockConditional, clk)
	start := clk.Now()

	held, release, err := a.Hold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var stolen error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := b.Acquire(ctx); !errors.Is(err, ErrLocked) {
				stolen = fmt.Errorf("B Acquire = %v", err)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	for i := 0; i < 6; i++ {
		time.Sleep(30 * time.Millisecond)
		clk.Advance(20 * time.Second)
	}
	time.Sleep(30 * time.Millisecond)
	close(stop)
	wg.Wait()
	if stolen != nil {
		t.Fatal(stolen)
	}
	if held.Err() != nil {
		t.Fatalf("held ctx cancelled: %v", context.Cause(held))
	}
	if !readInfo(t, mem).ExpiresAt.After(start.Add(time.Minute)) {
		t.Fatal("renewal did not push ExpiresAt forward")
	}
}

func TestHoldLockLostCancelsContext(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	clk := newTestClock()
	a, _ := holdPair(mem, LockConditional, clk)
	held, release, err := a.Hold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	body, _ := json.Marshal(LockInfo{MachineID: "B", ExpiresAt: clk.Now().Add(time.Hour)})
	mem.Put(ctx, lockKey, body, nil)
	select {
	case <-held.Done():
	case <-time.After(time.Second):
		t.Fatal("held ctx not cancelled")
	}
	if cause := context.Cause(held); !errors.Is(cause, ErrLockLost) {
		t.Fatalf("cause = %v", cause)
	}
	release()
	if readInfo(t, mem).MachineID != "B" {
		t.Fatal("release touched B's lock")
	}
}

func TestHoldReleaseIdempotent(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	clk := newTestClock()
	a, _ := holdPair(mem, LockConditional, clk)
	held, release, err := a.Hold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if _, err := mem.Head(ctx, lockKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lock object remains: %v", err)
	}
	if held.Err() == nil {
		t.Fatal("held ctx not cleaned up")
	}
}

func TestHoldFailureReturnsNoopRelease(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	clk := newTestClock()
	a, b := holdPair(mem, LockConditional, clk)
	if err := b.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	held, release, err := a.Hold(ctx)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v", err)
	}
	if release == nil {
		t.Fatal("release must be a no-op func, not nil")
	}
	release()
	if held == nil {
		t.Fatal("held must be non-nil")
	}
	if readInfo(t, mem).MachineID != "B" {
		t.Fatal("no-op release touched B's lock")
	}
}

func TestHoldBestEffort(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	mem.NoConditional = true
	clk := newTestClock()
	a, _ := holdPair(mem, LockBestEffort, clk)
	held, release, err := a.Hold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if held.Err() != nil {
		t.Fatal("ctx cancelled")
	}
	release()
	if _, err := mem.Head(ctx, lockKey); !errors.Is(err, ErrNotFound) {
		t.Fatal("lock remains")
	}
}

// flakyStore wraps Memory to inject failures.
type flakyStore struct {
	*Memory
	getOverride func(real func() ([]byte, Object, error)) ([]byte, Object, error)
	putAbsent   error
	failGets    atomic.Bool
}

func (f *flakyStore) Get(ctx context.Context, key string) ([]byte, Object, error) {
	if f.failGets.Load() {
		return nil, Object{}, errors.New("network down")
	}
	real := func() ([]byte, Object, error) { return f.Memory.Get(ctx, key) }
	if f.getOverride != nil {
		return f.getOverride(real)
	}
	return real()
}

func (f *flakyStore) PutIfAbsent(ctx context.Context, key string, body []byte) (Object, error) {
	if f.putAbsent != nil {
		return Object{}, f.putAbsent
	}
	return f.Memory.PutIfAbsent(ctx, key, body)
}

func TestLockBestEffortReadBackErrors(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		get  func(func() ([]byte, Object, error)) ([]byte, Object, error)
	}{
		{"corrupt", func(func() ([]byte, Object, error)) ([]byte, Object, error) {
			return []byte("garbage"), Object{}, nil
		}},
		{"vanished", func(func() ([]byte, Object, error)) ([]byte, Object, error) {
			return nil, Object{}, ErrNotFound
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := NewMemory()
			mem.NoConditional = true
			calls := 0
			st := &flakyStore{Memory: mem}
			st.getOverride = func(real func() ([]byte, Object, error)) ([]byte, Object, error) {
				calls++
				if calls == 1 {
					return nil, Object{}, ErrNotFound // initial read: no lock
				}
				return tc.get(real)
			}
			a := &Locker{Store: st, Key: lockKey, MachineID: "A", TTL: time.Minute, Mode: LockBestEffort}
			err := a.Acquire(ctx)
			if !errors.Is(err, ErrLocked) {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "0001-01-01") {
				t.Fatalf("zero timestamp in message: %v", err)
			}
		})
	}
}

func TestLockConditionalRetryExhaustion(t *testing.T) {
	st := &flakyStore{Memory: NewMemory(), putAbsent: fmt.Errorf("%w: raced", ErrPreconditionFailed)}
	a := &Locker{Store: st, Key: lockKey, MachineID: "A", TTL: time.Minute}
	err := a.Acquire(context.Background())
	if !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "contention persisted") {
		t.Fatalf("err = %v", err)
	}
}

func TestHoldTransientRenewErrorsEventuallyLoseLock(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	clk := newTestClock()
	st := &flakyStore{Memory: mem}
	a := &Locker{Store: st, Key: lockKey, MachineID: "A", TTL: time.Minute, Now: clk.Now, RenewEvery: 10 * time.Millisecond}
	held, release, err := a.Hold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	st.failGets.Store(true)
	time.Sleep(50 * time.Millisecond)
	if held.Err() != nil {
		t.Fatal("cancelled on a transient error before TTL elapsed")
	}
	clk.Advance(2 * time.Minute)
	select {
	case <-held.Done():
	case <-time.After(time.Second):
		t.Fatal("not cancelled after TTL of failed renewals")
	}
	if !errors.Is(context.Cause(held), ErrLockLost) {
		t.Fatalf("cause = %v", context.Cause(held))
	}
}

// gateStore wraps Memory so Get can fail, or hang until its ctx is done.
type gateStore struct {
	*Memory
	mu       sync.Mutex
	failGets bool
	hangGets bool
	inflight chan struct{} // signalled (non-blocking) when a Get starts hanging
}

func (g *gateStore) set(fail, hang bool) {
	g.mu.Lock()
	g.failGets, g.hangGets = fail, hang
	g.mu.Unlock()
}

func (g *gateStore) Get(ctx context.Context, key string) ([]byte, Object, error) {
	g.mu.Lock()
	fail, hang := g.failGets, g.hangGets
	g.mu.Unlock()
	if fail {
		return nil, Object{}, errors.New("network down")
	}
	if hang {
		select {
		case g.inflight <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, Object{}, ctx.Err()
	}
	return g.Memory.Get(ctx, key)
}

// driftClock is the real clock plus a controllable offset.
type driftClock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *driftClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *driftClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}

func TestHoldCancelsBeforeExpiryWhenRenewalsFail(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	st := &gateStore{Memory: mem}
	clk := &driftClock{}
	a := &Locker{Store: st, Key: lockKey, MachineID: "A", TTL: time.Second,
		RenewEvery: 100 * time.Millisecond, Now: clk.Now}
	held, release, err := a.Hold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	first := readInfo(t, mem).ExpiresAt
	deadline := time.Now().Add(5 * time.Second)
	for readInfo(t, mem).ExpiresAt.Sub(first) < 250*time.Millisecond { // a few renewals
		if time.Now().After(deadline) {
			t.Fatal("renewals did not happen")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var cancelledAt atomic.Value
	context.AfterFunc(held, func() { cancelledAt.Store(clk.Now()) })
	st.set(true, false)
	select {
	case <-held.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("held ctx never cancelled")
	}
	if !errors.Is(context.Cause(held), ErrLockLost) {
		t.Fatalf("cause = %v", context.Cause(held))
	}
	time.Sleep(20 * time.Millisecond)
	at, _ := cancelledAt.Load().(time.Time)
	expiry := readInfo(t, mem).ExpiresAt
	if at.IsZero() || !at.Before(expiry) {
		t.Fatalf("cancelled at %v, last written expiry %v: must be before expiry", at, expiry)
	}
	if expiry.Sub(at) < 30*time.Millisecond { // ~RenewEvery (100ms) of headroom
		t.Fatalf("cancelled only %v before expiry", expiry.Sub(at))
	}
}

func TestHoldHungStoreIsAbortedAndReleaseReturns(t *testing.T) {
	ctx := context.Background()
	st := &gateStore{Memory: NewMemory(), inflight: make(chan struct{}, 1)}
	a := &Locker{Store: st, Key: lockKey, MachineID: "A", TTL: 500 * time.Millisecond,
		RenewEvery: 50 * time.Millisecond}
	held, release, err := a.Hold(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.set(false, true)
	select {
	case <-st.inflight:
	case <-time.After(2 * time.Second):
		t.Fatal("no renewal in flight")
	}
	select {
	case <-held.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("held ctx not cancelled by hung store")
	}
	if !errors.Is(context.Cause(held), ErrLockLost) {
		t.Fatalf("cause = %v", context.Cause(held))
	}
	st.set(false, false)
	done := make(chan struct{})
	go func() { release(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("release blocked")
	}
}

func TestHoldReleaseAbortsInflightRenewKeepsCanceled(t *testing.T) {
	st := &gateStore{Memory: NewMemory(), inflight: make(chan struct{}, 1)}
	a := &Locker{Store: st, Key: lockKey, MachineID: "A", TTL: 30 * time.Minute,
		RenewEvery: 10 * time.Millisecond}
	held, release, err := a.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st.set(false, true)
	<-st.inflight
	st.set(false, false)
	done := make(chan struct{})
	go func() { release(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("release blocked")
	}
	if cause := context.Cause(held); !errors.Is(cause, context.Canceled) || errors.Is(cause, ErrLockLost) {
		t.Fatalf("cause = %v, want plain context.Canceled", cause)
	}
}

func TestLockTimingIsClamped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ttl   time.Duration
		every time.Duration
	}{
		{"every >= ttl", time.Second, 2 * time.Second},
		{"every huge", time.Second, 1000 * time.Hour},
		{"negative every", time.Second, -time.Second},
		{"negative ttl", -time.Minute, 0},
		{"tiny ttl", time.Nanosecond, 0},
		{"tiny ttl, huge every", time.Nanosecond, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Locker{Store: NewMemory(), Key: lockKey, MachineID: "A", TTL: tc.ttl, RenewEvery: tc.every}
			if r := a.renewEvery(); r < time.Millisecond || (a.ttl() >= 2*time.Millisecond && r > a.ttl()/2) {
				t.Fatalf("renewEvery = %v (ttl %v)", r, a.ttl())
			}
			if tc.ttl < 0 && a.ttl() != DefaultLockTTL {
				t.Fatalf("ttl = %v", a.ttl())
			}
			if a.renewTimeout() < time.Millisecond {
				t.Fatalf("renewTimeout = %v", a.renewTimeout())
			}
			_, release, err := a.Hold(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			release()
		})
	}

	// Clamped renewal still renews: RenewEvery >= TTL must renew within TTL/2.
	mem := NewMemory()
	clk := &driftClock{}
	a := &Locker{Store: mem, Key: lockKey, MachineID: "A", TTL: 400 * time.Millisecond,
		RenewEvery: time.Hour, Now: clk.Now}
	held, release, err := a.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	first := readInfo(t, mem).ExpiresAt
	time.Sleep(350 * time.Millisecond)
	if held.Err() != nil {
		t.Fatalf("cancelled: %v", context.Cause(held))
	}
	if !readInfo(t, mem).ExpiresAt.After(first) {
		t.Fatal("no renewal happened with RenewEvery >= TTL")
	}
}
