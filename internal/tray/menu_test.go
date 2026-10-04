package tray

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/model"
)

func as(state string, mod func(*api.ActionStatus)) api.ActionStatus {
	s := api.ActionStatus{Action: model.Action{ID: "a", Name: "Editor"}, State: state}
	if mod != nil {
		mod(&s)
	}
	return s
}

func TestIconFor(t *testing.T) {
	cases := []struct {
		acts []api.ActionStatus
		want iconKind
	}{
		{nil, iconIdle},
		{[]api.ActionStatus{as(api.StateIdle, nil)}, iconIdle},
		{[]api.ActionStatus{as(api.StateIdle, func(s *api.ActionStatus) { s.Running = true })}, iconSyncing},
		{[]api.ActionStatus{as(api.StatePausedUser, nil)}, iconIdle},
		{[]api.ActionStatus{as(api.StatePausedConflict, nil), as(api.StateIdle, func(s *api.ActionStatus) { s.Running = true })}, iconAttention},
		{[]api.ActionStatus{as(api.StatePausedDeletes, nil)}, iconAttention},
		{[]api.ActionStatus{as(api.StateError, nil), as(api.StatePausedConflict, nil)}, iconError},
	}
	for i, c := range cases {
		if got := iconFor(api.Status{Actions: c.acts}); got != c.want {
			t.Errorf("case %d: icon %v, want %v", i, got, c.want)
		}
	}
}

func TestActionTitle(t *testing.T) {
	cases := map[string]api.ActionStatus{
		"Editor: idle":                     as(api.StateIdle, nil),
		"Editor: syncing…":                 as(api.StateIdle, func(s *api.ActionStatus) { s.Running = true }),
		"Editor: 2 conflict(s)":            as(api.StatePausedConflict, func(s *api.ActionStatus) { s.Conflicts = 2 }),
		"Editor: 5 deletion(s) to approve": as(api.StatePausedDeletes, func(s *api.ActionStatus) { s.PendingDeletes = 5 }),
		"Editor: paused":                   as(api.StatePausedUser, nil),
		"Editor: error (network is down)":  as(api.StateError, func(s *api.ActionStatus) { s.LastError = "network is down" }),
	}
	for want, s := range cases {
		if got := actionTitle(s); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	long := as(api.StateError, func(s *api.ActionStatus) { s.LastError = string(make([]byte, 200)) })
	if got := actionTitle(long); len([]rune(got)) > 80 {
		t.Errorf("title not shortened: %d runes", len([]rune(got)))
	}
}

func TestTooltip(t *testing.T) {
	st := api.Status{Actions: []api.ActionStatus{as(api.StateIdle, nil), as(api.StatePausedConflict, nil), as(api.StateError, nil)}}
	if got := tooltip(st); got != "s3-anysync: 3 action(s), 2 need attention" {
		t.Fatalf("tooltip = %q", got)
	}
	if got := tooltip(api.Status{}); got != "s3-anysync: no actions" {
		t.Fatalf("tooltip = %q", got)
	}
}

func TestIconsArePNG(t *testing.T) {
	for _, k := range []iconKind{iconIdle, iconSyncing, iconAttention, iconError} {
		img, err := png.Decode(bytes.NewReader(iconPNG(k)))
		if err != nil || img.Bounds().Dx() != 32 {
			t.Fatalf("icon %v: %v", k, err)
		}
	}
}
