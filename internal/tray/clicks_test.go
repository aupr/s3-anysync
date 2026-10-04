package tray

import (
	"sync/atomic"
	"testing"
	"time"
)

func counters() (*[3]int32, clickHandlers) {
	var n [3]int32
	return &n, clickHandlers{
		Sync:     func() { atomic.AddInt32(&n[0], 1) },
		Snapshot: func() { atomic.AddInt32(&n[1], 1) },
		Pause:    func() { atomic.AddInt32(&n[2], 1) },
	}
}

func runClicks(stop <-chan struct{}, s, p, r <-chan struct{}, h clickHandlers) <-chan struct{} {
	done := make(chan struct{})
	go func() { serveClicks(stop, s, p, r, h); close(done) }()
	return done
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveClicks did not return")
	}
}

func TestServeClicksClosedChannelsInvokeNothing(t *testing.T) {
	n, h := counters()
	stop := make(chan struct{})
	s, p, r := make(chan struct{}), make(chan struct{}), make(chan struct{})
	close(s)
	close(p)
	close(r)
	waitDone(t, runClicks(stop, s, p, r, h))
	if *n != [3]int32{} {
		t.Fatalf("handlers ran: %v", *n)
	}
}

func TestServeClicksOneClickOneCall(t *testing.T) {
	n, h := counters()
	stop := make(chan struct{})
	s, p, r := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := runClicks(stop, s, p, r, h)
	s <- struct{}{}
	p <- struct{}{}
	p <- struct{}{}
	r <- struct{}{}
	for i := 0; i < 200 && atomic.LoadInt32(&n[2]) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	waitDone(t, done)
	got := [3]int32{atomic.LoadInt32(&n[0]), atomic.LoadInt32(&n[1]), atomic.LoadInt32(&n[2])}
	if got != [3]int32{1, 2, 1} {
		t.Fatalf("calls = %v, want [1 2 1]", got)
	}
}

func TestServeClicksStopEndsLoop(t *testing.T) {
	_, h := counters()
	stop := make(chan struct{})
	done := runClicks(stop, make(chan struct{}), make(chan struct{}), make(chan struct{}), h)
	close(stop)
	waitDone(t, done)
}

func TestServeClicksStopAndClosedNoHandler(t *testing.T) {
	for i := 0; i < 200; i++ {
		n, h := counters()
		stop := make(chan struct{})
		s, p, r := make(chan struct{}), make(chan struct{}), make(chan struct{})
		close(stop)
		close(s)
		close(p)
		close(r)
		waitDone(t, runClicks(stop, s, p, r, h))
		if *n != [3]int32{} {
			t.Fatalf("iteration %d: handlers ran: %v", i, *n)
		}
	}
}
