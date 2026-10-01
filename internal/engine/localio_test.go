package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "a", "b", "file.json")
	mtime := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	info, err := writeFileAtomic(dst, []byte("one"), mtime)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(mtime) {
		t.Fatalf("mtime = %v", info.ModTime())
	}
	if runtime.GOOS != "windows" {
		os.Chmod(dst, 0o600)
	}
	if _, err := writeFileAtomic(dst, []byte("two"), mtime); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "two" {
		t.Fatalf("content = %q", data)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(dst); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode not preserved: %v", st.Mode())
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".s3-anysync-tmp-") {
			t.Fatal("temp file left behind")
		}
	}
	if _, err := writeFileAtomic(dir, []byte("x"), mtime); err == nil {
		t.Fatal("overwrote a directory")
	}
}

func TestBackupLocalAndPrune(t *testing.T) {
	src := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(src, "s.json"), "keep me")
	files := map[string]string{"snippets/s.json": filepath.Join(src, "s.json"), "gone.json": filepath.Join(src, "missing")}
	if err := backupLocal(root, "act", "20261001T120000Z", files); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "act", "20261001T120000Z", "snippets", "s.json"))
	if err != nil || string(data) != "keep me" {
		t.Fatalf("backup = %q, %v", data, err)
	}

	for i := 0; i < LocalBackupKeep+2; i++ {
		os.MkdirAll(filepath.Join(root, "act", fmt.Sprintf("20270101T0000%02dZ", i)), 0o755)
	}
	if err := pruneBackups(filepath.Join(root, "act"), LocalBackupKeep); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "act"))
	if len(entries) != LocalBackupKeep {
		t.Fatalf("kept %d backups", len(entries))
	}
	if entries[0].Name() == "20261001T120000Z" {
		t.Fatal("oldest backup was not pruned")
	}
}

func TestRemoveLocal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	writeFile(t, p, "x")
	if err := removeLocal(p); err != nil {
		t.Fatal(err)
	}
	if err := removeLocal(p); err != nil {
		t.Fatalf("removing a missing file: %v", err)
	}
}
