//go:build !windows

package ipc

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestTokenTempFileModeNotInherited(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "ipc.token.tmp")
	if err := os.WriteFile(tmp, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(tmp, 0o644)
	if _, err := NewToken(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "ipc.token"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %v, %v", info, err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("tmp left behind: %v", err)
	}
}

func TestListenRefusesOpenDataDir(t *testing.T) {
	dir, _ := os.MkdirTemp("", "sa")
	defer os.RemoveAll(dir)
	for _, mode := range []os.FileMode{0o755, 0o710, 0o707} {
		os.Chmod(dir, mode)
		_, err := Listen(dir)
		if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "0"+strconv.FormatUint(uint64(mode), 8)) {
			t.Fatalf("mode %o: %v", mode, err)
		}
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o707 {
		t.Fatalf("Listen changed the dir mode to %v", fi.Mode().Perm())
	}
}

func TestListenKeepsLiveSocket(t *testing.T) {
	dir, _ := os.MkdirTemp("", "sa")
	defer os.RemoveAll(dir)
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := Listen(dir); err == nil {
		t.Fatal("second Listen on a live socket succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "agent.sock")); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
}
