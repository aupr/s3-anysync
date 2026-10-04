//go:build !windows

package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPathLimitAndMode(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	if _, err := Listen(long); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("long path: %v", err)
	}
	dir, _ := os.MkdirTemp("", "sa")
	defer os.RemoveAll(dir)
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	info, err := os.Stat(filepath.Join(dir, "agent.sock"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v", info.Mode(), err)
	}
	// A stale socket file (no listener) is replaced.
	l.Close()
	l2, err := Listen(dir)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	l2.Close()
}

func TestTokenFile(t *testing.T) {
	dir := t.TempDir()
	tok, err := NewToken(dir)
	if err != nil || len(tok) != 64 {
		t.Fatalf("token = %q, %v", tok, err)
	}
	got, err := ReadToken(dir)
	if err != nil || got != tok {
		t.Fatalf("read = %q, %v", got, err)
	}
	info, _ := os.Stat(filepath.Join(dir, "ipc.token"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %v", info.Mode())
	}
	tok2, _ := NewToken(dir)
	if tok2 == tok {
		t.Fatal("token not rotated")
	}
}
