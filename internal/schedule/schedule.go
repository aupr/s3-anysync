// Package schedule turns an action's schedule settings into cron schedules.
// Times are interpreted in the location of the time passed to Next (local
// time in the agent).
package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/niceit/s3-anysync/internal/model"
)

// Compile returns the schedule for s, or nil for no schedule.
func Compile(s model.Schedule) (cron.Schedule, error) {
	switch s.Kind {
	case "", model.ScheduleNone:
		return nil, nil
	case model.ScheduleInterval:
		if s.EveryMinutes < 1 {
			return nil, errors.New("schedule: every-minutes must be at least 1")
		}
		return cron.Every(time.Duration(s.EveryMinutes) * time.Minute), nil
	case model.ScheduleDaily:
		h, m, err := parseAt(s.At)
		if err != nil {
			return nil, err
		}
		return cron.ParseStandard(fmt.Sprintf("%d %d * * *", m, h))
	case model.ScheduleWeekly:
		if s.Weekday < 0 || s.Weekday > 6 {
			return nil, fmt.Errorf("schedule: weekday must be 0 (Sunday) to 6, got %d", s.Weekday)
		}
		h, m, err := parseAt(s.At)
		if err != nil {
			return nil, err
		}
		return cron.ParseStandard(fmt.Sprintf("%d %d * * %d", m, h, s.Weekday))
	case model.ScheduleCron:
		sched, err := cron.ParseStandard(strings.TrimSpace(s.Cron))
		if err != nil {
			return nil, fmt.Errorf("schedule: %w", err)
		}
		return sched, nil
	}
	return nil, fmt.Errorf("schedule: unknown kind %q", s.Kind)
}

func parseAt(at string) (int, int, error) {
	// Strictly validate format: must be exactly "HH:MM"
	if len(at) != 5 || at[2] != ':' {
		return 0, 0, fmt.Errorf("schedule: time must be HH:MM (24h), got %q", at)
	}
	t, err := time.Parse("15:04", at)
	if err != nil {
		return 0, 0, fmt.Errorf("schedule: time must be HH:MM (24h), got %q", at)
	}
	return t.Hour(), t.Minute(), nil
}

// Validate reports whether s can be compiled.
func Validate(s model.Schedule) error {
	_, err := Compile(s)
	return err
}

// NextRuns returns the next n run times after from (none for no schedule).
func NextRuns(s model.Schedule, from time.Time, n int) ([]time.Time, error) {
	sched, err := Compile(s)
	if err != nil || sched == nil {
		return nil, err
	}
	out := make([]time.Time, 0, n)
	t := from
	for i := 0; i < n; i++ {
		t = sched.Next(t)
		out = append(out, t)
	}
	return out, nil
}
