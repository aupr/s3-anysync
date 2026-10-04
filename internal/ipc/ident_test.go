package ipc

import (
	"strings"
	"testing"
)

func TestPipeNameFor(t *testing.T) {
	a := pipeNameFor("S-1-5-21-111-222-333-1001", `C:\Users\x\data`)
	if !strings.HasPrefix(a, `\\.\pipe\s3-anysync-S-1-5-21-111-222-333-1001-`) || len(a) != len(`\\.\pipe\s3-anysync-S-1-5-21-111-222-333-1001-`)+16 {
		t.Fatalf("name = %s", a)
	}
	if b := pipeNameFor("S-1-5-21-111-222-333-1002", `C:\Users\x\data`); a == b {
		t.Fatal("different users share a pipe name")
	}
	if b := pipeNameFor("S-1-5-21-111-222-333-1001", `C:\Users\x\other`); a == b {
		t.Fatal("different data dirs share a pipe name")
	}
	if got := pipeNameFor(`S-1\5/x y`, "d"); strings.ContainsAny(strings.TrimPrefix(got, `\\.\pipe\`), `\/ `) {
		t.Fatalf("unsanitized: %s", got)
	}
}

func TestSameSID(t *testing.T) {
	if !sameSID("S-1-5-21-1", "s-1-5-21-1") || sameSID("S-1-5-21-1", "S-1-5-21-2") || sameSID("", "") {
		t.Fatal("sameSID wrong")
	}
}
