package schedule

import (
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

func TestCompile(t *testing.T) {
	from := time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local) // a Thursday
	cases := []struct {
		name string
		s    model.Schedule
		want time.Time
	}{
		{"interval", model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: 15}, from.Add(15 * time.Minute)},
		{"daily later today", model.Schedule{Kind: model.ScheduleDaily, At: "09:30"}, time.Date(2026, 10, 1, 9, 30, 0, 0, time.Local)},
		{"daily tomorrow", model.Schedule{Kind: model.ScheduleDaily, At: "07:00"}, time.Date(2026, 10, 2, 7, 0, 0, 0, time.Local)},
		{"weekly monday", model.Schedule{Kind: model.ScheduleWeekly, Weekday: 1, At: "10:00"}, time.Date(2026, 10, 5, 10, 0, 0, 0, time.Local)},
		{"cron", model.Schedule{Kind: model.ScheduleCron, Cron: "*/10 * * * *"}, from.Add(10 * time.Minute)},
		{"cron descriptor", model.Schedule{Kind: model.ScheduleCron, Cron: "@every 1s"}, from.Add(time.Second)},
	}
	for _, tc := range cases {
		s, err := Compile(tc.s)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := s.Next(from); !got.Equal(tc.want) {
			t.Errorf("%s: next = %v, want %v", tc.name, got, tc.want)
		}
	}
	for _, none := range []model.Schedule{{}, {Kind: model.ScheduleNone}} {
		if s, err := Compile(none); s != nil || err != nil {
			t.Errorf("none: %v %v", s, err)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	bad := []model.Schedule{
		{Kind: model.ScheduleInterval},
		{Kind: model.ScheduleDaily, At: "25:00"},
		{Kind: model.ScheduleDaily, At: "9:30"},
		{Kind: model.ScheduleDaily},
		{Kind: model.ScheduleWeekly, Weekday: 7, At: "10:00"},
		{Kind: model.ScheduleCron, Cron: "not a cron"},
		{Kind: "hourly"},
	}
	for _, s := range bad {
		if err := Validate(s); err == nil {
			t.Errorf("Validate(%+v) = nil", s)
		}
	}
}

func TestNextRuns(t *testing.T) {
	from := time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local)
	runs, err := NextRuns(model.Schedule{Kind: model.ScheduleDaily, At: "09:30"}, from, 3)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	for i, r := range runs {
		want := time.Date(2026, 10, 1+i, 9, 30, 0, 0, time.Local)
		if !r.Equal(want) {
			t.Errorf("run %d = %v, want %v", i, r, want)
		}
	}
	if runs, err := NextRuns(model.Schedule{Kind: model.ScheduleNone}, from, 3); err != nil || len(runs) != 0 {
		t.Fatalf("none: %v %v", runs, err)
	}
}
