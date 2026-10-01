package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanFixture(t *testing.T) (string, Mappings) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "settings.json"), "{}")
	writeFile(t, filepath.Join(dir, "snippets", "a.json"), "a")
	writeFile(t, filepath.Join(dir, "snippets", "sub", "b.json"), "bb")
	writeFile(t, filepath.Join(dir, "snippets", "cache", "c.json"), "c")
	writeFile(t, filepath.Join(dir, "snippets", "x.log"), "log")
	a := model.Action{ID: "a", Name: "a", Excludes: []string{"*.log"}, Items: []model.Item{
		{ID: "f", RemoteName: "settings.json", Type: model.ItemFile, LocalPaths: map[string]string{"M": filepath.Join(dir, "settings.json")}},
		{ID: "d", RemoteName: "snippets", Type: model.ItemDir, Excludes: []string{"cache"}, LocalPaths: map[string]string{"M": filepath.Join(dir, "snippets")}},
	}}
	a.ApplyDefaults()
	ms, err := BuildMappings(a, "M", func(p string) (string, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	return dir, ms
}

func keys(m map[string]model.FileEntry) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestScanLocal(t *testing.T) {
	_, ms := scanFixture(t)
	got, err := scanLocal(ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"settings.json", "snippets/a.json", "snippets/sub/b.json"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("keys = %v, want %v", keys(got), want)
	}
	if e := got["snippets/a.json"]; e.Hash != hashBytes([]byte("a")) || e.Size != 1 || e.MTime == 0 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestScanReusesBaselineHash(t *testing.T) {
	_, ms := scanFixture(t)
	first, _ := scanLocal(ms, nil)
	cached := first["settings.json"]
	cached.Hash = "cached"
	got, _ := scanLocal(ms, map[string]model.FileEntry{"settings.json": cached})
	if got["settings.json"].Hash != "cached" {
		t.Fatal("hash not reused when size and mtime match")
	}
	cached.MTime++
	got, _ = scanLocal(ms, map[string]model.FileEntry{"settings.json": cached})
	if got["settings.json"].Hash == "cached" {
		t.Fatal("hash reused although mtime differs")
	}
}

func TestScanMissingAndMismatched(t *testing.T) {
	dir, ms := scanFixture(t)
	os.RemoveAll(filepath.Join(dir, "snippets"))
	os.Remove(filepath.Join(dir, "settings.json"))
	got, err := scanLocal(ms, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing paths: %v %v", got, err)
	}
	os.MkdirAll(filepath.Join(dir, "settings.json"), 0o755)
	if _, err := scanLocal(ms, nil); err == nil || !strings.Contains(err.Error(), "expects a file") {
		t.Fatalf("want type mismatch error, got %v", err)
	}
}

func TestScanFollowsFileSymlinks(t *testing.T) {
	dir, ms := scanFixture(t)
	target := filepath.Join(t.TempDir(), "real.json")
	writeFile(t, target, "real")
	if err := os.Symlink(target, filepath.Join(dir, "snippets", "link.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "snippets", "linkdir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := scanLocal(ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["snippets/link.json"].Hash != hashBytes([]byte("real")) {
		t.Fatal("file symlink not followed")
	}
	for k := range got {
		if strings.HasPrefix(k, "snippets/linkdir") {
			t.Fatal("descended into a symlinked directory")
		}
	}
}
