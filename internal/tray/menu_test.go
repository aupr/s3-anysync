package tray

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"strings"
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
	seen := map[color.RGBA]iconKind{}
	for _, k := range []iconKind{iconIdle, iconSyncing, iconAttention, iconError} {
		img, err := png.Decode(bytes.NewReader(iconPNG(k)))
		if err != nil {
			t.Fatalf("icon %v: %v", k, err)
		}
		if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
			t.Fatalf("icon %v: size %v", k, b)
		}
		r, g, bl, a := img.At(16, 16).RGBA()
		c := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)}
		if c.A == 0 {
			t.Errorf("icon %v: transparent centre", k)
		}
		if prev, dup := seen[c]; dup {
			t.Errorf("icons %v and %v share centre colour %v", prev, k, c)
		}
		seen[c] = k
	}
}

func TestIconICO(t *testing.T) {
	p := iconPNG(iconIdle)
	ico := iconICO(p, 32)
	want := []byte{0, 0, 1, 0, 1, 0, 32, 32, 0, 0, 1, 0, 32, 0}
	if !bytes.Equal(ico[:14], want) {
		t.Fatalf("header % x, want % x", ico[:14], want)
	}
	if n := binary.LittleEndian.Uint32(ico[14:18]); int(n) != len(p) {
		t.Errorf("bytesInRes %d, want %d", n, len(p))
	}
	if off := binary.LittleEndian.Uint32(ico[18:22]); off != 22 {
		t.Errorf("offset %d, want 22", off)
	}
	if !bytes.Equal(ico[22:], p) {
		t.Error("payload differs from PNG")
	}
	if _, err := png.Decode(bytes.NewReader(ico[22:])); err != nil {
		t.Errorf("decode from offset: %v", err)
	}
}

func TestIconBytesFor(t *testing.T) {
	for _, k := range []iconKind{iconIdle, iconSyncing, iconAttention, iconError} {
		if b := iconBytesFor("windows", k); !bytes.HasPrefix(b, []byte{0, 0, 1, 0, 1, 0}) {
			t.Errorf("windows icon %v lacks ICO header", k)
		}
		if b := iconBytesFor("darwin", k); !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) {
			t.Errorf("darwin icon %v lacks PNG signature", k)
		}
	}
}

func TestActionTitleStripsControlChars(t *testing.T) {
	s := as(api.StateError, func(s *api.ActionStatus) { s.LastError = "line1\nline2\r\x00\ttab" })
	got := actionTitle(s)
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("control char %q in %q", r, got)
		}
	}
	if !strings.Contains(got, "line1 line2") {
		t.Errorf("got %q", got)
	}
}
