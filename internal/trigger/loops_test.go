package trigger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type every time.Duration

func (e every) Next(t time.Time) time.Time { return t.Add(time.Duration(e)) }

func TestRunScheduleFiresUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	done := make(chan struct{})
	go func() { RunSchedule(ctx, every(30*time.Millisecond), nil, func() { n.Add(1) }); close(done) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	if got := n.Load(); got < 3 {
		t.Fatalf("fired %d times", got)
	}
}

func TestRunPollerAndETagChecker(t *testing.T) {
	var etag atomic.Value
	etag.Store("a")
	var calls atomic.Int32
	check := ETagChecker(func(context.Context) (string, error) {
		if calls.Add(1) == 3 {
			return "", errors.New("transient")
		}
		return etag.Load().(string), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fired := make(chan struct{}, 4)
	var errs atomic.Int32
	go RunPoller(ctx, 20*time.Millisecond, check, func() { fired <- struct{}{} }, func(error) { errs.Add(1) })
	time.Sleep(120 * time.Millisecond)
	select {
	case <-fired:
		t.Fatal("fired without a change")
	default:
	}
	etag.Store("b")
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("no fire after the ETag changed")
	}
	if errs.Load() != 1 {
		t.Fatalf("errors reported = %d", errs.Load())
	}
}

type fixedNext func(time.Time) time.Time

func (f fixedNext) Next(t time.Time) time.Time { return f(t) }

func TestRunScheduleStopsWithoutAdvancingNext(t *testing.T) {
	for name, next := range map[string]func(time.Time) time.Time{
		"zero": func(time.Time) time.Time { return time.Time{} },
		"past": func(t time.Time) time.Time { return t.Add(-time.Hour) },
		"same": func(t time.Time) time.Time { return t },
	} {
		t.Run(name, func(t *testing.T) {
			var n atomic.Int32
			done := make(chan struct{})
			go func() {
				RunSchedule(context.Background(), fixedNext(next), nil, func() { n.Add(1) })
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("RunSchedule did not return")
			}
			if n.Load() != 0 {
				t.Fatalf("fired %d times", n.Load())
			}
		})
	}
}
