//go:build unix

package engine

import (
	"os"
	"testing"
)

func TestDownloadThroughDanglingSymlink(t *testing.T) {
	_, a, b, _ := syncedPair(t, memoryStore, testAction("dangling"))
	a.write("app/snippets/new.json", "new")
	expectCounts(t, a.sync(), 1, 0, 0, 0)
	target := b.path("elsewhere.txt")
	if err := os.Symlink(target, b.path("app/snippets/new.json")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	res := b.sync()
	expectCounts(t, res, 0, 1, 0, 0)
	if len(res.Deferred) != 0 {
		t.Fatalf("deferred forever: %+v", res.Deferred)
	}
	if got := b.read("app/snippets/new.json"); got != "new" {
		t.Fatalf("content = %q", got)
	}
	expectCounts(t, b.sync(), 0, 0, 0, 0)
}
