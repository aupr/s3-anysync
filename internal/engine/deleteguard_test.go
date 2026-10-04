package engine

import (
	"reflect"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func TestMassDeletes(t *testing.T) {
	a := model.Action{}
	a.ApplyDefaults() // 5 files and 50%
	ops := func(n int, kind OpKind) []Op {
		var out []Op
		for i := 0; i < n; i++ {
			out = append(out, Op{Path: string(rune('a' + i)), Kind: kind})
		}
		return out
	}
	cases := []struct {
		name    string
		ops     []Op
		tracked int
		want    int
	}{
		{"below min files", ops(4, OpDeleteRemote), 4, 0},
		{"min files and half", ops(5, OpDeleteRemote), 10, 5},
		{"min files below half", ops(5, OpDeleteLocal), 11, 0},
		{"local deletes count", ops(6, OpDeleteLocal), 8, 6},
		{"uploads do not count", ops(9, OpUpload), 9, 0},
	}
	for _, tc := range cases {
		got := massDeletes(tc.ops, nil, tc.tracked, a)
		if len(got) != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	resolved := map[string]bool{"a": true, "b": true}
	if got := massDeletes(ops(6, OpDeleteRemote), resolved, 6, a); got != nil {
		t.Errorf("resolved conflict deletions counted: %v", got)
	}
	got := massDeletes([]Op{{Path: "z", Kind: OpDeleteRemote}, {Path: "a", Kind: OpDeleteRemote}, {Path: "m", Kind: OpDeleteLocal}, {Path: "b", Kind: OpDeleteLocal}, {Path: "c", Kind: OpDeleteLocal}}, nil, 5, a)
	if !reflect.DeepEqual(got, []string{"a", "b", "c", "m", "z"}) {
		t.Errorf("not sorted: %v", got)
	}
	a.DeleteGuardMinFiles = 10
	if got := massDeletes(ops(6, OpDeleteRemote), nil, 6, a); got != nil {
		t.Errorf("per-action min files ignored: %v", got)
	}
}
