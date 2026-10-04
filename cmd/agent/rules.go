package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/schedule"
)

// parseSchedule parses none, every:MINUTES, daily:HH:MM,
// weekly:DAY@HH:MM (DAY 0=Sunday … 6=Saturday) or cron:SPEC.
func parseSchedule(spec string) (model.Schedule, error) {
	kind, rest, _ := strings.Cut(spec, ":")
	var s model.Schedule
	switch kind {
	case "none":
		if rest != "" {
			return s, fmt.Errorf("--schedule none takes no value, got %q", spec)
		}
		s = model.Schedule{Kind: model.ScheduleNone}
	case "every":
		n, err := strconv.Atoi(rest)
		if err != nil {
			return s, fmt.Errorf("--schedule every:MINUTES, got %q", spec)
		}
		s = model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: n}
	case "daily":
		s = model.Schedule{Kind: model.ScheduleDaily, At: rest}
	case "weekly":
		day, at, ok := strings.Cut(rest, "@")
		d, err := strconv.Atoi(day)
		if !ok || err != nil {
			return s, fmt.Errorf("--schedule weekly:DAY@HH:MM, got %q", spec)
		}
		s = model.Schedule{Kind: model.ScheduleWeekly, Weekday: d, At: at}
	case "cron":
		s = model.Schedule{Kind: model.ScheduleCron, Cron: rest}
	default:
		return s, errors.New("--schedule must be none, every:MINUTES, daily:HH:MM, weekly:DAY@HH:MM or cron:SPEC")
	}
	return s, schedule.Validate(s)
}

// describeSchedule is a human description of s.
func describeSchedule(s model.Schedule) string {
	switch s.Kind {
	case model.ScheduleInterval:
		return fmt.Sprintf("every %d minute(s)", s.EveryMinutes)
	case model.ScheduleDaily:
		return "daily at " + s.At
	case model.ScheduleWeekly:
		return fmt.Sprintf("weekly on %s at %s", time.Weekday(s.Weekday), s.At)
	case model.ScheduleCron:
		return "cron " + s.Cron
	}
	return "none"
}
