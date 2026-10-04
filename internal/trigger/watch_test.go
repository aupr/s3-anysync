package trigger

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func watchFor(t *testing.T, roots []Root, match func(string) bool) chan struct{} {
	t.Helper()
	fired := make(chan struct{}, 16)
	w, err := Watch(WatchOptions{Roots: roots, Debounce: 100 * time.Millisecond, Match: match, Fire: func() { fired <- struct{}{} }, Rescan: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	time.Sleep(50 * time.Millisecond) // let the watches settle
	return fired
}

func expectFire(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("no fire for %s", what)
	}
}

func expectQuiet(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("unexpected fire: %s", what)
	case <-time.After(400 * time.Millisecond):
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBurstFiresOnce(t *testing.T) {
	dir := t.TempDir()
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, nil)
	for i := 0; i < 5; i++ {
		write(t, filepath.Join(dir, "a.json"), strings.Repeat("x", i+1))
	}
	expectFire(t, fired, "burst")
	expectQuiet(t, fired, "burst should coalesce into one fire")
}

func TestNewSubdirectoriesAreWatched(t *testing.T) {
	dir := t.TempDir()
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, nil)
	write(t, filepath.Join(dir, "sub", "deep", "a.json"), "a")
	expectFire(t, fired, "new subdirectory")
	time.Sleep(100 * time.Millisecond)
	write(t, filepath.Join(dir, "sub", "deep", "b.json"), "b")
	expectFire(t, fired, "write inside the new subdirectory")
}

func TestMatchAndTempFilesFilterEvents(t *testing.T) {
	dir := t.TempDir()
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, func(p string) bool { return !strings.HasSuffix(p, ".log") })
	write(t, filepath.Join(dir, "x.log"), "ignored")
	write(t, filepath.Join(dir, ".s3-anysync-tmp-123"), "ignored")
	expectQuiet(t, fired, "excluded and temp files")
	write(t, filepath.Join(dir, "x.json"), "counted")
	expectFire(t, fired, "matching file")
}

func TestFileRootWatchesOnlyThatFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	write(t, target, "{}")
	fired := watchFor(t, []Root{{Path: target}}, nil)
	write(t, filepath.Join(dir, "other.json"), "x")
	expectQuiet(t, fired, "sibling of a file root")
	write(t, target, `{"a":1}`)
	expectFire(t, fired, "the file root itself")
}

func TestFileRootAtomicRenameReplacement(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	write(t, target, "{}")
	fired := watchFor(t, []Root{{Path: target}}, nil)
	tmp := filepath.Join(dir, "settings.json.tmp")
	write(t, tmp, `{"a":2}`)
	expectQuiet(t, fired, "temp sibling of a file root")
	if err := os.Rename(tmp, target); err != nil {
		t.Fatal(err)
	}
	expectFire(t, fired, "atomic rename onto the file root")
}

func TestDirRootEventsDoNotFire(t *testing.T) {
	dir := t.TempDir()
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, nil)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	expectQuiet(t, fired, "chmod of the directory root")
}

func TestRemovedSubdirectoryKeepsWatcherWorking(t *testing.T) {
	dir := t.TempDir()
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, nil)
	write(t, filepath.Join(dir, "sub", "a.json"), "a")
	expectFire(t, fired, "create")
	time.Sleep(100 * time.Millisecond)
	if err := os.RemoveAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	expectFire(t, fired, "removal of a subdirectory")
	time.Sleep(100 * time.Millisecond)
	write(t, filepath.Join(dir, "sub", "b.json"), "b") // recreated
	expectFire(t, fired, "recreated subdirectory")
	time.Sleep(100 * time.Millisecond)
	write(t, filepath.Join(dir, "top.json"), "t")
	expectFire(t, fired, "sibling after removal")
}

func TestMissingRootIsPickedUpLater(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "later")
	fired := watchFor(t, []Root{{Path: dir, Dir: true}}, nil)
	os.MkdirAll(dir, 0o755)
	time.Sleep(200 * time.Millisecond) // > Rescan
	write(t, filepath.Join(dir, "a.json"), "a")
	expectFire(t, fired, "root created after the watcher")
}

func TestCloseStopsFiring(t *testing.T) {
	dir := t.TempDir()
	fired := make(chan struct{}, 4)
	w, err := Watch(WatchOptions{Roots: []Root{{Path: dir, Dir: true}}, Debounce: 50 * time.Millisecond, Fire: func() { fired <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil { // second Close is a no-op
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "a.json"), "a")
	expectQuiet(t, fired, "after Close")
}

func TestNoFireAfterCloseReturns(t *testing.T) {
	dir := t.TempDir()
	var closed, late atomic.Bool
	w, err := Watch(WatchOptions{Roots: []Root{{Path: dir, Dir: true}}, Debounce: 20 * time.Millisecond, Fire: func() {
		if closed.Load() {
			late.Store(true)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 20; i++ {
		write(t, filepath.Join(dir, "a.json"), strings.Repeat("x", i+1))
		time.Sleep(10 * time.Millisecond)
	}
	w.Close()
	closed.Store(true)
	time.Sleep(200 * time.Millisecond)
	if late.Load() {
		t.Fatal("Fire called after Close returned")
	}
}

func TestLargeBurstCoalesces(t *testing.T) {
	dir := t.TempDir()
	var n atomic.Int32
	fired := make(chan struct{}, 16)
	w, err := Watch(WatchOptions{Roots: []Root{{Path: dir, Dir: true}}, Debounce: 150 * time.Millisecond, Fire: func() { n.Add(1); fired <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 500; i++ {
		write(t, filepath.Join(dir, "f"+strings.Repeat("a", i%7)+".json"), "x")
	}
	expectFire(t, fired, "large burst")
	expectQuiet(t, fired, "large burst should fire once")
	if n.Load() != 1 {
		t.Fatalf("fires = %d, want 1", n.Load())
	}
}

func TestRootsBehindASymlinkedParent(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	target := filepath.Join(real, "settings.json")
	write(t, target, "{}")
	fired := watchFor(t, []Root{{Path: filepath.Join(link, "settings.json")}, {Path: link, Dir: true}}, nil)
	write(t, target, `{"a":1}`)
	expectFire(t, fired, "write through the real path")
	time.Sleep(100 * time.Millisecond)
	write(t, filepath.Join(link, "settings.json"), `{"a":2}`)
	expectFire(t, fired, "write through the symlinked path")
}
