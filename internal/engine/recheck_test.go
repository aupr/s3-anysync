package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aupr/s3-anysync/internal/model"
)

func TestCheckLocal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")

	if got := checkLocal(p, nil); got != localSame {
		t.Fatalf("absent vs nil: %v", got)
	}
	writeFile(t, p, "hello")
	if got := checkLocal(p, nil); got != localChanged {
		t.Fatalf("appeared vs nil: %v", got)
	}
	info, _ := os.Stat(p)
	seen := &model.FileEntry{Path: "f.txt", Hash: hashBytes([]byte("hello")), Size: info.Size(), MTime: info.ModTime().UnixNano()}
	if got := checkLocal(p, seen); got != localSame {
		t.Fatalf("unchanged: %v", got)
	}
	// touch without content change
	later := info.ModTime().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if got := checkLocal(p, seen); got != localSame {
		t.Fatalf("touched: %v", got)
	}
	// same size, different content, same mtime as seen
	writeFile(t, p, "HELLO")
	if err := os.Chtimes(p, info.ModTime().Add(2*time.Hour), info.ModTime().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := checkLocal(p, seen); got != localChanged {
		t.Fatalf("edited: %v", got)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got := checkLocal(p, seen); got != localGone {
		t.Fatalf("vanished: %v", got)
	}
}
