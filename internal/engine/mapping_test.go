package engine

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func identity(p string) (string, error) { return filepath.FromSlash(p), nil }

func mappingAction() model.Action {
	a := model.Action{ID: "a", Name: "a", Excludes: []string{"*.log"}, Items: []model.Item{
		{ID: "f", RemoteName: "settings.json", Type: model.ItemFile, LocalPaths: map[string]string{"M": "/cfg/settings.json"}},
		{ID: "d", RemoteName: "snippets", Type: model.ItemDir, Excludes: []string{"cache"}, LocalPaths: map[string]string{"M": "/cfg/snippets"}},
		{ID: "u", RemoteName: "other", Type: model.ItemDir, LocalPaths: map[string]string{"X": "/x"}},
	}}
	a.ApplyDefaults()
	return a
}

func TestBuildMappings(t *testing.T) {
	ms, err := BuildMappings(mappingAction(), "M", identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("want 2 mapped items, got %d", len(ms))
	}
	if p, ok := ms.LocalPathFor("settings.json"); !ok || p != filepath.FromSlash("/cfg/settings.json") {
		t.Fatalf("file item: %q %v", p, ok)
	}
	if p, ok := ms.LocalPathFor("snippets/a/b.json"); !ok || p != filepath.Join(filepath.FromSlash("/cfg/snippets"), "a", "b.json") {
		t.Fatalf("dir item: %q %v", p, ok)
	}
	if _, ok := ms.LocalPathFor("other/x"); ok {
		t.Fatal("unmapped item resolved")
	}
	owns := map[string]bool{
		"settings.json":          true,
		"snippets/ok.json":       true,
		"snippets/x.log":         false,
		"snippets/cache/x.json":  false,
		"snippetsX/a":            false,
		"snippets/../etc/passwd": false,
		"snippets/a//b":          false,
		`snippets/a\..\b`:        false,
		"other/x":                false,
	}
	for p, want := range owns {
		if got := ms.Owns(p); got != want {
			t.Errorf("Owns(%q) = %v, want %v", p, got, want)
		}
	}
	files := map[string]model.FileEntry{"settings.json": {}, "other/x": {}, "snippets/x.log": {}}
	if got := filterOwned(files, ms); len(got) != 1 {
		t.Fatalf("filterOwned = %v", got)
	}
}

func TestBuildMappingsExpandError(t *testing.T) {
	_, err := BuildMappings(mappingAction(), "M", func(string) (string, error) { return "", errors.New("no APPDATA") })
	if err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("want error naming the item, got %v", err)
	}
}
