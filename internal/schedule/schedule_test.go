package schedule

import (
	"math"
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

func TestNextRunsEdgeCases(t *testing.T) {
	from := time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local)
	// n <= 0 should return empty slice
	runs, err := NextRuns(model.Schedule{Kind: model.ScheduleDaily, At: "09:30"}, from, 0)
	if err != nil || len(runs) != 0 {
		t.Errorf("n=0: runs = %v, err = %v, want empty slice", runs, err)
	}
	runs, err = NextRuns(model.Schedule{Kind: model.ScheduleDaily, At: "09:30"}, from, -1)
	if err != nil || len(runs) != 0 {
		t.Errorf("n=-1: runs = %v, err = %v, want empty slice", runs, err)
	}
}

func TestCompileOverflow(t *testing.T) {
	// Test MaxEveryMinutes is accepted
	s, err := Compile(model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: MaxEveryMinutes})
	if err != nil || s == nil {
		t.Errorf("MaxEveryMinutes accepted: err = %v, s = %v", err, s)
	}

	// Test MaxEveryMinutes+1 is rejected
	_, err = Compile(model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: MaxEveryMinutes + 1})
	if err == nil {
		t.Error("MaxEveryMinutes+1 should be rejected")
	}

	// Test huge value (math.MaxInt) is rejected
	_, err = Compile(model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: math.MaxInt})
	if err == nil {
		t.Error("math.MaxInt should be rejected")
	}
}

func TestParseAtErrors(t *testing.T) {
	bad := []string{
		"12:60",    // invalid minute
		"09:30:00", // wrong format (has seconds)
		" 09:30",   // leading space
		"ab:cd",    // non-numeric
	}
	for _, at := range bad {
		_, _, err := parseAt(at)
		if err == nil {
			t.Errorf("parseAt(%q) should fail", at)
		}
	}
}

func TestWeekdayNegative(t *testing.T) {
	_, err := Compile(model.Schedule{Kind: model.ScheduleWeekly, Weekday: -1, At: "10:00"})
	if err == nil {
		t.Error("weekday -1 should be rejected")
	}
}

func TestWeeklySunday(t *testing.T) {
	from := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	s, err := Compile(model.Schedule{Kind: model.ScheduleWeekly, Weekday: 0, At: "10:00"})
	if err != nil {
		t.Fatalf("weekday 0 (Sunday) should be accepted: %v", err)
	}
	next := s.Next(from)
	// Verify next is a Sunday at 10:00
	if next.Weekday() != time.Sunday {
		t.Errorf("next should be Sunday, got %v", next.Weekday())
	}
	if next.Hour() != 10 || next.Minute() != 0 {
		t.Errorf("next time should be 10:00, got %02d:%02d", next.Hour(), next.Minute())
	}
}

func TestValidateSubMinute(t *testing.T) {
	// @every 1s and @every 30s should be rejected
	if err := Validate(model.Schedule{Kind: model.ScheduleCron, Cron: "@every 1s"}); err == nil {
		t.Error("@every 1s should be rejected by Validate")
	}
	if err := Validate(model.Schedule{Kind: model.ScheduleCron, Cron: "@every 30s"}); err == nil {
		t.Error("@every 30s should be rejected by Validate")
	}

	// * * * * * is every minute (exactly MinInterval), should be ACCEPTED
	if err := Validate(model.Schedule{Kind: model.ScheduleCron, Cron: "* * * * *"}); err != nil {
		t.Errorf("* * * * * should be accepted: %v", err)
	}

	// Setting MinInterval = 0 should make @every 1s valid
	oldMinInterval := MinInterval
	t.Cleanup(func() { MinInterval = oldMinInterval })
	MinInterval = 0
	if err := Validate(model.Schedule{Kind: model.ScheduleCron, Cron: "@every 1s"}); err != nil {
		t.Errorf("@every 1s should be accepted with MinInterval=0: %v", err)
	}
}

func TestValidateSubMinuteAcceptable(t *testing.T) {
	// These should be accepted (>= MinInterval)
	acceptable := []model.Schedule{
		{Kind: model.ScheduleInterval, EveryMinutes: 1},
		{Kind: model.ScheduleDaily, At: "10:00"},
		{Kind: model.ScheduleWeekly, Weekday: 1, At: "10:00"},
		{Kind: model.ScheduleCron, Cron: "*/5 * * * *"},
	}
	for i, s := range acceptable {
		if err := Validate(s); err != nil {
			t.Errorf("acceptable[%d] should pass Validate: %v", i, err)
		}
	}
}
