package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func dirMapping(t *testing.T, local string, excludes ...string) Mappings {
	t.Helper()
	a := model.Action{ID: "a", Name: "a", Items: []model.Item{
		{ID: "d", RemoteName: "snippets", Type: model.ItemDir, Excludes: excludes, LocalPaths: map[string]string{"M": local}},
	}}
	a.ApplyDefaults()
	ms, err := BuildMappings(a, "M", func(p string) (string, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func fileMapping(t *testing.T, local string, excludes ...string) Mappings {
	t.Helper()
	a := model.Action{ID: "a", Name: "a", Items: []model.Item{
		{ID: "f", RemoteName: "settings.json", Type: model.ItemFile, Excludes: excludes, LocalPaths: map[string]string{"M": local}},
	}}
	a.ApplyDefaults()
	ms, err := BuildMappings(a, "M", func(p string) (string, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestScanSymlinkedDirRoot(t *testing.T) {
	real := t.TempDir()
	writeFile(t, filepath.Join(real, "a.json"), "a")
	writeFile(t, filepath.Join(real, "sub", "b.json"), "bb")
	link := filepath.Join(t.TempDir(), "link")
	symlinkOrSkip(t, real, link)
	got, err := scanLocal(dirMapping(t, link), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"snippets/a.json", "snippets/sub/b.json"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("keys = %v, want %v", keys(got), want)
	}
}

func TestScanDanglingDirRoot(t *testing.T) {
	link := filepath.Join(t.TempDir(), "link")
	symlinkOrSkip(t, filepath.Join(t.TempDir(), "gone"), link)
	ms := dirMapping(t, link)
	got, err := scanLocal(ms, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("no baseline: %v %v", got, err)
	}
	_, err = scanLocal(ms, map[string]model.FileEntry{"snippets/a.json": {Path: "snippets/a.json", Hash: "h"}})
	if err == nil || !strings.Contains(err.Error(), "snippets") || !strings.Contains(err.Error(), link) {
		t.Fatalf("want error naming item and path, got %v", err)
	}
}

func TestScanDanglingFileItemSymlink(t *testing.T) {
	link := filepath.Join(t.TempDir(), "link")
	symlinkOrSkip(t, filepath.Join(t.TempDir(), "gone"), link)
	ms := fileMapping(t, link)
	got, err := scanLocal(ms, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("no baseline: %v %v", got, err)
	}
	_, err = scanLocal(ms, map[string]model.FileEntry{"settings.json": {Path: "settings.json", Hash: "h"}})
	if err == nil || !strings.Contains(err.Error(), link) {
		t.Fatalf("want error, got %v", err)
	}
}

func TestScanDanglingFileInDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.json"), "a")
	symlinkOrSkip(t, filepath.Join(t.TempDir(), "gone"), filepath.Join(dir, "dead.json"))
	ms := dirMapping(t, dir)
	got, err := scanLocal(ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"snippets/a.json"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("keys = %v", keys(got))
	}
	_, err = scanLocal(ms, map[string]model.FileEntry{"snippets/dead.json": {Path: "snippets/dead.json", Hash: "h"}})
	if err == nil || !strings.Contains(err.Error(), "dead.json") || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("want refusal naming path, got %v", err)
	}
}

func TestScanNilMatcher(t *testing.T) {
	ms := Mappings{{ItemID: "d", RemoteName: "snippets", Type: model.ItemDir, LocalPath: t.TempDir()}}
	if _, err := scanLocal(ms, nil); err == nil || !strings.Contains(err.Error(), "no exclude matcher") {
		t.Fatalf("got %v", err)
	}
	ms[0].Type = model.ItemFile
	if _, err := scanLocal(ms, nil); err == nil || !strings.Contains(err.Error(), "no exclude matcher") {
		t.Fatalf("got %v", err)
	}
}

func TestScanStatErrorNamesPath(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	symlinkOrSkip(t, b, a)
	symlinkOrSkip(t, a, b)
	_, err := scanLocal(fileMapping(t, a), nil)
	if err == nil || !strings.Contains(err.Error(), a) {
		t.Fatalf("want error naming %s, got %v", a, err)
	}
}

func TestScanFileItemExcluded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "s.json"), "x")
	got, err := scanLocal(fileMapping(t, filepath.Join(dir, "s.json"), "settings.json"), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestScanEmptyDirRoot(t *testing.T) {
	got, err := scanLocal(dirMapping(t, t.TempDir()), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestScanCacheSizeMismatchRehashes(t *testing.T) {
	_, ms := scanFixture(t)
	first, _ := scanLocal(ms, nil)
	c := first["settings.json"]
	c.Hash = "cached"
	got, _ := scanLocal(ms, map[string]model.FileEntry{"settings.json": c})
	if got["settings.json"].Hash != "cached" {
		t.Fatal("expected reuse")
	}
	c.Size++
	got, _ = scanLocal(ms, map[string]model.FileEntry{"settings.json": c})
	if got["settings.json"].Hash == "cached" {
		t.Fatal("hash reused although size differs")
	}
}

func TestScanExcludedSubdirNotListed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "keep", "k.json"), "k")
	writeFile(t, filepath.Join(dir, "skip", "deep", "s.json"), "s")
	got, err := scanLocal(dirMapping(t, dir, "skip"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"snippets/keep/k.json"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("keys = %v", keys(got))
	}
}

func TestScanSymlinkedSubdirWithBaselineRefuses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.json"), "a")
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "x.json"), "x")
	symlinkOrSkip(t, target, filepath.Join(dir, "sub"))
	ms := dirMapping(t, dir)
	_, err := scanLocal(ms, map[string]model.FileEntry{"snippets/sub/x.json": {Path: "snippets/sub/x.json", Hash: "h"}})
	if err == nil || !strings.Contains(err.Error(), "sub") || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("want refusal naming sub, got %v", err)
	}
}

func TestScanSymlinkedSubdirWithoutBaselineSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.json"), "a")
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "x.json"), "x")
	symlinkOrSkip(t, target, filepath.Join(dir, "sub"))
	got, err := scanLocal(dirMapping(t, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"snippets/a.json"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("keys = %v", keys(got))
	}
}

// NOTE: a Windows junction/reparse point root reports ModeIrregular (Go >= 1.23)
// and cannot be exercised here; scanDir resolves any root where !Lstat.IsDir().
