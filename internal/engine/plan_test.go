package engine

import (
	"testing"

	"github.com/aupr/s3-anysync/internal/model"
)

func side(hash string) map[string]model.FileEntry {
	if hash == "" {
		return map[string]model.FileEntry{}
	}
	return map[string]model.FileEntry{"f": {Path: "f", Hash: hash}}
}

func TestPlanTable(t *testing.T) {
	const (
		tw  = model.TwoWay
		l2c = model.LocalToCloud
		c2l = model.CloudToLocal
	)
	// l, r, b are hashes on the local side, the cloud side and the baseline; "" means absent.
	// want "" means no op at all (the path is left alone).
	tests := []struct {
		name    string
		dir     model.Direction
		prop    bool
		restore bool // ReceiverRestoresCloud
		l, r, b string
		want    OpKind
	}{
		{"unchanged", tw, true, false, "a", "a", "a", OpKeep},
		{"local edit", tw, true, false, "b", "a", "a", OpUpload},
		{"cloud edit", tw, true, false, "a", "b", "a", OpDownload},
		{"both edited differently", tw, true, false, "b", "c", "a", OpConflict},
		{"both edited identically", tw, true, false, "b", "b", "a", OpAdopt},
		{"local delete propagates", tw, true, false, "", "a", "a", OpDeleteRemote},
		{"local delete not propagated", tw, false, false, "", "a", "a", OpDownload},
		{"cloud delete propagates", tw, true, false, "a", "", "a", OpDeleteLocal},
		{"cloud delete not propagated", tw, false, false, "a", "", "a", OpUpload},
		{"deleted on both sides", tw, true, false, "", "", "a", OpDrop},
		{"new local file", tw, true, false, "a", "", "", OpUpload},
		{"new cloud file", tw, true, false, "", "a", "", OpDownload},
		{"new on both, different", tw, true, false, "a", "b", "", OpConflict},
		{"new on both, same", tw, true, false, "a", "a", "", OpAdopt},
		{"local edit vs cloud delete", tw, true, false, "b", "", "a", OpConflict},

		{"l2c local edit", l2c, true, false, "b", "a", "a", OpUpload},
		{"l2c cloud edit overwritten", l2c, true, false, "a", "b", "a", OpUpload},
		{"l2c both edited", l2c, true, false, "b", "c", "a", OpUpload},
		{"l2c cloud delete restored", l2c, true, false, "a", "", "a", OpUpload},
		{"l2c local delete propagates", l2c, true, false, "", "a", "a", OpDeleteRemote},
		{"l2c local delete not propagated", l2c, false, false, "", "a", "a", OpDownload},
		{"l2c local delete vs cloud edit", l2c, true, false, "", "b", "a", OpDeleteRemote},
		{"l2c untracked cloud file left alone", l2c, true, false, "", "a", "", ""},
		{"l2c new on both", l2c, true, false, "a", "b", "", OpUpload},

		{"c2l local edit replaced", c2l, true, false, "b", "a", "a", OpDownload},
		{"c2l cloud edit", c2l, true, false, "a", "b", "a", OpDownload},
		{"c2l local delete restored", c2l, true, false, "", "a", "a", OpDownload},
		{"c2l cloud delete propagates", c2l, true, false, "a", "", "a", OpDeleteLocal},
		{"c2l cloud delete not propagated: receiver never uploads", c2l, false, false, "a", "", "a", ""},
		{"c2l cloud delete vs local edit, deletes off: receiver never uploads", c2l, false, false, "b", "", "a", ""},
		{"c2l untracked local file left alone", c2l, true, false, "a", "", "", ""},
		{"c2l both edited", c2l, true, false, "b", "c", "a", OpDownload},
		{"c2l cloud delete vs local edit", c2l, true, false, "b", "", "a", OpDeleteLocal},

		{"c2l cloud delete not propagated, receiver restores cloud", c2l, false, true, "a", "", "a", OpUpload},
		{"c2l cloud delete vs local edit, deletes off, receiver restores", c2l, false, true, "b", "", "a", OpUpload},
		{"c2l restore flag ignored when propagate on (delete)", c2l, true, true, "a", "", "a", OpDeleteLocal},
		{"c2l restore flag ignored when propagate on (edit vs delete)", c2l, true, true, "b", "", "a", OpDeleteLocal},
		{"c2l restore flag: untracked local left alone", c2l, false, true, "a", "", "", ""},
		{"c2l restore flag: local delete still downloads", c2l, false, true, "", "a", "a", OpDownload},
		{"tw restore flag no effect", tw, false, true, "a", "", "a", OpUpload},
		{"tw restore flag no effect (propagate)", tw, true, true, "a", "", "a", OpDeleteLocal},
		{"l2c restore flag no effect", l2c, true, true, "a", "", "a", OpUpload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := Plan(PlanInput{Direction: tt.dir, PropagateDeletes: tt.prop, ReceiverRestoresCloud: tt.restore, Local: side(tt.l), Remote: side(tt.r), Baseline: side(tt.b)})
			if tt.want == "" {
				if len(ops) != 0 {
					t.Fatalf("want no op, got %+v", ops)
				}
				return
			}
			if len(ops) != 1 || ops[0].Kind != tt.want {
				t.Fatalf("want %s, got %+v", tt.want, ops)
			}
			op := ops[0]
			if (tt.l != "") != (op.Local != nil) || (tt.r != "") != (op.Remote != nil) {
				t.Fatalf("op sides do not match inputs: %+v", op)
			}
		})
	}
}

func TestPlanSortedByPath(t *testing.T) {
	local := map[string]model.FileEntry{"b": {Path: "b", Hash: "1"}, "a": {Path: "a", Hash: "1"}, "c/d": {Path: "c/d", Hash: "1"}}
	ops := Plan(PlanInput{Direction: model.TwoWay, Local: local, Remote: map[string]model.FileEntry{}, Baseline: map[string]model.FileEntry{}})
	if len(ops) != 3 || ops[0].Path != "a" || ops[1].Path != "b" || ops[2].Path != "c/d" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestPlanInvariants(t *testing.T) {
	vals := []string{"", "a", "b", "c"}
	dirs := []model.Direction{model.TwoWay, model.LocalToCloud, model.CloudToLocal}
	for _, dir := range dirs {
		for _, prop := range []bool{true, false} {
			for _, restore := range []bool{false, true} {
				for _, l := range vals {
					for _, r := range vals {
						for _, b := range vals {
							in := PlanInput{Direction: dir, PropagateDeletes: prop, ReceiverRestoresCloud: restore, Local: side(l), Remote: side(r), Baseline: side(b)}
							ops := Plan(in)
							id := func() string {
								return string(dir) + " prop=" + map[bool]string{true: "on", false: "off"}[prop] + " restore=" + map[bool]string{true: "on", false: "off"}[restore] + " l=" + l + " r=" + r + " b=" + b
							}
							if len(ops) > 1 {
								t.Errorf("%s: more than one op: %+v", id(), ops)
								continue
							}
							if len(ops) == 0 {
								continue
							}
							k := ops[0].Kind
							if dir == model.LocalToCloud {
								if k == OpDeleteLocal {
									t.Errorf("%s: l2c produced delete-local", id())
								}
								if k == OpDownload && !(l == "" && b != "") {
									t.Errorf("%s: l2c download without a local deletion", id())
								}
							}
							if dir == model.CloudToLocal {
								restoreCase := restore && !prop && l != "" && r == "" && b != ""
								if k == OpDeleteRemote || (k == OpUpload && !restoreCase) {
									t.Errorf("%s: c2l wrote to the cloud (%s)", id(), k)
								}
								if restoreCase && k != OpUpload {
									t.Errorf("%s: c2l restore case produced %s, want upload", id(), k)
								}
							}
							if (k == OpDeleteRemote && r == "") || (k == OpDeleteLocal && l == "") ||
								(k == OpDownload && r == "") || (k == OpUpload && l == "") {
								t.Errorf("%s: %s on a missing side", id(), k)
							}
							if k == OpConflict && dir != model.TwoWay {
								t.Errorf("%s: conflict outside two-way", id())
							}
							if (l == "") != (r == "") && b == "" && (k == OpDeleteLocal || k == OpDeleteRemote) {
								t.Errorf("%s: deleted a never-synced one-sided path (%s)", id(), k)
							}
						}
					}
				}
			}
		}
	}
}
