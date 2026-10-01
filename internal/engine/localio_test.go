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

func skipNoSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestWriteFileAtomicThroughSymlink(t *testing.T) {
	mtime := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, rel := range []bool{false, true} {
		dir := t.TempDir()
		target := filepath.Join(dir, "store", "real.json")
		writeFile(t, target, "old")
		os.Chmod(target, 0o600)
		link := filepath.Join(dir, "cfg", "link.json")
		os.MkdirAll(filepath.Dir(link), 0o755)
		lt := target
		if rel {
			lt = filepath.Join("..", "store", "real.json")
		}
		skipNoSymlink(t, lt, link)
		if _, err := writeFileAtomic(link, []byte("new"), mtime); err != nil {
			t.Fatal(err)
		}
		if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("link replaced (rel=%v): %v", rel, err)
		}
		if d, _ := os.ReadFile(target); string(d) != "new" {
			t.Fatalf("target = %q (rel=%v)", d, rel)
		}
		if st, _ := os.Stat(target); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v", st.Mode())
		}
	}
}

func TestWriteFileAtomicDanglingSymlink(t *testing.T) {
	mtime := time.Now()
	dir := t.TempDir()
	link := filepath.Join(dir, "link.json")
	target := filepath.Join(dir, "newdir", "real.json")
	skipNoSymlink(t, target, link)
	if _, err := writeFileAtomic(link, []byte("hi"), mtime); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link replaced")
	}
	if d, _ := os.ReadFile(target); string(d) != "hi" {
		t.Fatalf("target = %q", d)
	}
	// relative dangling
	link2 := filepath.Join(dir, "link2.json")
	skipNoSymlink(t, filepath.Join("sub", "r.json"), link2)
	if _, err := writeFileAtomic(link2, []byte("rel"), mtime); err != nil {
		t.Fatal(err)
	}
	if d, _ := os.ReadFile(filepath.Join(dir, "sub", "r.json")); string(d) != "rel" {
		t.Fatalf("rel target = %q", d)
	}
}

func TestWriteFileAtomicSymlinkedParent(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	os.MkdirAll(real, 0o755)
	skipNoSymlink(t, real, filepath.Join(dir, "linkdir"))
	if _, err := writeFileAtomic(filepath.Join(dir, "linkdir", "f.json"), []byte("x"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if d, _ := os.ReadFile(filepath.Join(real, "f.json")); string(d) != "x" {
		t.Fatalf("got %q", d)
	}
}

func TestRemoveLocalDirAndSymlink(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "d")
	writeFile(t, filepath.Join(sub, "f"), "x")
	if err := removeLocal(sub); err == nil {
		t.Fatal("removed a directory")
	}
	if _, err := os.Stat(filepath.Join(sub, "f")); err != nil {
		t.Fatal("directory contents damaged")
	}
	target := filepath.Join(dir, "t")
	writeFile(t, target, "keep")
	link := filepath.Join(dir, "l")
	skipNoSymlink(t, target, link)
	if err := removeLocal(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("link still present")
	}
	if d, _ := os.ReadFile(target); string(d) != "keep" {
		t.Fatal("target damaged")
	}
	// symlink to a directory is unlinked only
	dl := filepath.Join(dir, "dl")
	skipNoSymlink(t, sub, dl)
	if err := removeLocal(dl); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sub, "f")); err != nil {
		t.Fatal("dir target damaged")
	}
}

func TestBackupLocalCollisionKeepsBoth(t *testing.T) {
	src := t.TempDir()
	root := t.TempDir()
	p := filepath.Join(src, "s.json")
	writeFile(t, p, "first")
	files := map[string]string{"s.json": p}
	ts := "20261001T120000Z"
	if err := backupLocal(root, "act", ts, files); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "second")
	if err := backupLocal(root, "act", ts, files); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p, "third")
	if err := backupLocal(root, "act", ts, files); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "act", ts)
	for name, want := range map[string]string{"s.json": "first", "s.json.1": "second", "s.json.2": "third"} {
		d, err := os.ReadFile(filepath.Join(base, name))
		if err != nil || string(d) != want {
			t.Fatalf("%s = %q, %v", name, d, err)
		}
	}
}

func TestBackupLocalProtectsNewest(t *testing.T) {
	src := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(src, "s.json"), "x")
	for i := 0; i < LocalBackupKeep+3; i++ {
		os.MkdirAll(filepath.Join(root, "act", fmt.Sprintf("20270101T0000%02dZ", i)), 0o755)
	}
	ts := "20200101T000000Z" // sorts oldest
	if err := backupLocal(root, "act", ts, map[string]string{"s.json": filepath.Join(src, "s.json")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "act", ts, "s.json")); err != nil {
		t.Fatalf("just-created backup was pruned: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "act"))
	if len(entries) != LocalBackupKeep {
		t.Fatalf("kept %d", len(entries))
	}
}

func TestPruneIgnoresNonTimestamp(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"notes", "zzz", "20270101T000001Z", "20270101T000002Z", "20270101T000003Z"} {
		os.MkdirAll(filepath.Join(dir, n), 0o755)
	}
	writeFile(t, filepath.Join(dir, "20260101T000000Z.txt"), "x")
	if err := pruneBackups(dir, 1); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"notes", "zzz", "20270101T000003Z", "20260101T000000Z.txt"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("%s removed", n)
		}
	}
	for _, n := range []string{"20270101T000001Z", "20270101T000002Z"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Fatalf("%s kept", n)
		}
	}
}

func TestBackupLocalRejectsBadInput(t *testing.T) {
	src := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(src, "s.json"), "x")
	abs := filepath.Join(src, "s.json")
	if err := backupLocal(root, "act", "20261001T120000Z", map[string]string{"../../x": abs}); err == nil {
		t.Fatal("traversal accepted")
	}
	if err := backupLocal(root, "act", "20261001T120000Z", map[string]string{"/abs/../../../y": abs}); err == nil {
		t.Fatal("traversal accepted (abs)")
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`} {
		if err := backupLocal(root, bad, "20261001T120000Z", map[string]string{"s": abs}); err == nil {
			t.Fatalf("actionID %q accepted", bad)
		}
		if err := backupLocal(root, "act", bad, map[string]string{"s": abs}); err == nil {
			t.Fatalf("ts %q accepted", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err == nil {
		t.Fatal("escaped root")
	}
}
