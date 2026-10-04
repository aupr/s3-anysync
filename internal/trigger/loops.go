package trigger

import (
	"context"
	"time"
)

// Scheduler is the part of a cron schedule RunSchedule needs.
type Scheduler interface {
	Next(time.Time) time.Time
}

// RunSchedule calls fire at every time sched gives, until ctx ends. It stops
// (firing nothing more) when the schedule has no next time or one that is not
// after now, which would otherwise make it spin.
func RunSchedule(ctx context.Context, sched Scheduler, now func() time.Time, fire func()) {
	if now == nil {
		now = time.Now
	}
	for {
		t := now()
		next := sched.Next(t)
		if next.IsZero() || !next.After(t) {
			return
		}
		timer := time.NewTimer(next.Sub(t))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			fire()
		}
	}
}

// RunPoller calls check every interval and fire when it reports a change;
// errors go to onErr (may be nil) and polling continues.
func RunPoller(ctx context.Context, interval time.Duration, check func(context.Context) (bool, error), fire func(), onErr func(error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		changed, err := check(ctx)
		if err != nil {
			if onErr != nil {
				onErr(err)
			}
			continue
		}
		if changed {
			fire()
		}
	}
}

// ETagChecker reports a change whenever the ETag differs from the last one
// seen; the first successful call only records it.
func ETagChecker(get func(context.Context) (string, error)) func(context.Context) (bool, error) {
	var last string
	seen := false
	return func(ctx context.Context) (bool, error) {
		etag, err := get(ctx)
		if err != nil {
			return false, err
		}
		changed := seen && etag != last
		last, seen = etag, true
		return changed, nil
	}
}
