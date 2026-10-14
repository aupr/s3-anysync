package main

import (
	"testing"

	"github.com/aupr/s3-anysync/internal/model"
)

func TestParseSchedule(t *testing.T) {
	ok := map[string]model.Schedule{
		"none":              {Kind: model.ScheduleNone},
		"every:15":          {Kind: model.ScheduleInterval, EveryMinutes: 15},
		"daily:09:30":       {Kind: model.ScheduleDaily, At: "09:30"},
		"weekly:1@10:00":    {Kind: model.ScheduleWeekly, Weekday: 1, At: "10:00"},
		"cron:*/10 * * * *": {Kind: model.ScheduleCron, Cron: "*/10 * * * *"},
	}
	for in, want := range ok {
		got, err := parseSchedule(in)
		if err != nil || got != want {
			t.Errorf("parseSchedule(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "hourly", "every:0", "every:x", "daily:9:30", "weekly:8@10:00", "weekly:1", "cron:nope", "none:xyz"} {
		if _, err := parseSchedule(in); err == nil {
			t.Errorf("parseSchedule(%q) accepted", in)
		}
	}
	if got := describeSchedule(model.Schedule{Kind: model.ScheduleWeekly, Weekday: 1, At: "10:00"}); got != "weekly on Monday at 10:00" {
		t.Errorf("describe = %q", got)
	}
}
