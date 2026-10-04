package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.json")
	s := NewFile(p)
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
	if err := s.Set("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("k2", "v2"); err != nil {
		t.Fatal(err)
	}
	// A second store on the same file sees the values (persisted, not cached).
	if v, err := NewFile(p).Get("k"); err != nil || v != "v1" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("never-set"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if v, _ := s.Get("k2"); v != "v2" {
		t.Fatalf("k2 = %q", v)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v", fi.Mode(), err)
		}
	}
	if _, err := os.Stat(p + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temp file left behind")
	}
}

func TestFileStoreRejectsCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.json")
	os.WriteFile(p, []byte("{not json"), 0o600)
	if _, err := NewFile(p).Get("k"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("corrupt file: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvVar, "")
	if s, err := FromEnv(dir); err != nil {
		t.Fatal(err)
	} else if _, ok := s.(Keyring); !ok {
		t.Fatalf("default store = %T", s)
	}
	t.Setenv(EnvVar, "file")
	s, err := FromEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := s.(*File)
	if !ok || f.path != filepath.Join(dir, FileName) {
		t.Fatalf("file store = %#v", s)
	}
	t.Setenv(EnvVar, "plaintext")
	if _, err := FromEnv(dir); err == nil {
		t.Fatal("unknown backend accepted")
	}
}
