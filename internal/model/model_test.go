package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func validAction() Action {
	a := Action{ID: "a1", Name: "VS Code", Items: []Item{
		{ID: "i1", RemoteName: "settings.json", Type: ItemFile},
		{ID: "i2", RemoteName: "snippets", Type: ItemDir},
	}}
	a.ApplyDefaults()
	return a
}

func TestApplyDefaults(t *testing.T) {
	var a Action
	a.ApplyDefaults()
	if a.Direction != TwoWay || a.Triggers.DebounceSeconds != 3 || a.Triggers.RemotePollMinutes != 5 ||
		a.SnapshotRetention != 20 || a.Triggers.Schedule.Kind != ScheduleNone ||
		a.Machines == nil || a.MachineDirections == nil {
		t.Fatalf("defaults not applied: %+v", a)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Action)
		wantErr string
	}{
		{"valid", func(*Action) {}, ""},
		{"empty name", func(a *Action) { a.Name = " " }, "name is empty"},
		{"bad direction", func(a *Action) { a.Direction = "sideways" }, "unknown direction"},
		{"bad machine direction", func(a *Action) { a.MachineDirections["m1"] = "up" }, "machine m1"},
		{"bad schedule", func(a *Action) { a.Triggers.Schedule.Kind = "hourly" }, "unknown schedule kind"},
		{"retention zero", func(a *Action) { a.SnapshotRetention = 0 }, "retention"},
		{"empty item id", func(a *Action) { a.Items[0].ID = "" }, "item id is empty"},
		{"bad item type", func(a *Action) { a.Items[0].Type = "link" }, "unknown type"},
		{"duplicate item id", func(a *Action) { a.Items[1].ID = "i1" }, "duplicate item id"},
		{"unnormalized name", func(a *Action) { a.Items[0].RemoteName = "/settings.json" }, "not normalized"},
		{"nested overlap", func(a *Action) { a.Items[0].RemoteName = "snippets/a.json" }, "overlap"},
		{"same name", func(a *Action) { a.Items[0].RemoteName = "snippets" }, "overlap"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validAction()
			tt.mutate(&a)
			err := a.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestNormalizeRemoteName(t *testing.T) {
	ok := map[string]string{
		" /a/b/ ": "a/b",
		`a\b`:     "a/b",
		"a/./b":   "a/b",
		"a/../b":  "b",
		"x.json":  "x.json",
	}
	for in, want := range ok {
		got, err := NormalizeRemoteName(in)
		if err != nil || got != want {
			t.Errorf("NormalizeRemoteName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "/", ".", "..", "../x", "a/../../x"} {
		if got, err := NormalizeRemoteName(in); err == nil {
			t.Errorf("NormalizeRemoteName(%q) = %q, want error", in, got)
		}
	}
}

func TestDirectionFor(t *testing.T) {
	a := validAction()
	a.MachineDirections["mac"] = LocalToCloud
	if got := a.DirectionFor("mac"); got != LocalToCloud {
		t.Fatalf("override: got %s", got)
	}
	if got := a.DirectionFor("pc"); got != TwoWay {
		t.Fatalf("default: got %s", got)
	}
}

func TestActionJSONOmitsProfileID(t *testing.T) {
	a := validAction()
	a.ProfileID = "local-profile"
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "local-profile") {
		t.Fatalf("profile id leaked into shared JSON: %s", data)
	}
}

func TestSummary(t *testing.T) {
	r := SyncResult{Uploaded: 2, Conflicts: make([]Conflict, 1)}
	want := "uploaded 2, downloaded 0, deleted in cloud 0, deleted locally 0, adopted 0, conflicts 1"
	if got := r.Summary(); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestReceiverRestoresCloudJSON(t *testing.T) {
	a := validAction()
	a.ApplyDefaults()
	if a.ReceiverRestoresCloud {
		t.Fatal("default must be false")
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"receiverRestoresCloud":false`) {
		t.Fatalf("missing field: %s", data)
	}
	a.ReceiverRestoresCloud = true
	data, _ = json.Marshal(a)
	var back Action
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !back.ReceiverRestoresCloud {
		t.Fatalf("round trip lost field: %s", data)
	}
}

func TestSummaryDeferred(t *testing.T) {
	r := SyncResult{Deferred: []string{"a", "b"}}
	want := "uploaded 0, downloaded 0, deleted in cloud 0, deleted locally 0, adopted 0, conflicts 0, deferred 2"
	if got := r.Summary(); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestDeleteGuardDefaultsAndValidation(t *testing.T) {
	var a Action
	a.ApplyDefaults()
	if a.DeleteGuardMinFiles != 5 || a.DeleteGuardPercent != 50 {
		t.Fatalf("delete guard defaults = %d/%d", a.DeleteGuardMinFiles, a.DeleteGuardPercent)
	}
	for _, tc := range []struct{ min, pct int }{{5, 0}, {5, 101}, {0, 50}} {
		v := validAction()
		v.DeleteGuardMinFiles, v.DeleteGuardPercent = tc.min, tc.pct
		if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "delete guard") {
			t.Errorf("min=%d pct=%d: want delete guard error, got %v", tc.min, tc.pct, err)
		}
	}
}

func TestSummaryExtras(t *testing.T) {
	r := SyncResult{PendingDeletes: []string{"a"}, Unsupported: []string{"b", "c"}}
	want := "uploaded 0, downloaded 0, deleted in cloud 0, deleted locally 0, adopted 0, conflicts 0, pending deletions 1, unsupported 2"
	if got := r.Summary(); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRulesRoundTrip(t *testing.T) {
	a := validAction()
	a.Encrypt = true
	r := a.Rules()
	r.Name = "Renamed"
	r.Excludes = []string{"*.log"}
	r.Triggers.OnChange = true
	r.Triggers.DebounceSeconds = 0 // defaulted again by ApplyRules
	r.DeleteGuardPercent = 80
	a.ApplyRules(r)
	if a.Name != "Renamed" || a.Excludes[0] != "*.log" || !a.Triggers.OnChange || a.Triggers.DebounceSeconds != DefaultDebounceSeconds || a.DeleteGuardPercent != 80 {
		t.Fatalf("rules not applied: %+v", a)
	}
	if a.ID != "a1" || len(a.Items) != 2 || !a.Encrypt {
		t.Fatal("ApplyRules changed identity, items or encryption")
	}
}

func TestValidateExcludePatterns(t *testing.T) {
	for _, bad := range []string{"[", "a[b"} {
		a := validAction()
		a.Excludes = []string{bad}
		err := a.Validate()
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("action exclude %q: err = %v", bad, err)
		}
		a = validAction()
		a.Items[1].Excludes = []string{"ok", bad}
		if err := a.Validate(); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("item exclude %q: err = %v", bad, err)
		}
	}
	a := validAction()
	a.Excludes = []string{"*.log", "/root.txt"}
	a.Items[1].Excludes = []string{"**/gen/**"}
	if err := a.Validate(); err != nil {
		t.Fatalf("valid excludes rejected: %v", err)
	}
}
