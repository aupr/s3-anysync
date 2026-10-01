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

func TestBuildMappingsValidation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"relative path", "relative/path", true},
		{"root path", "/", true},
		{"double slash path (should clean)", "/cfg//snippets", false},
		{"absolute nested path", "/cfg/snippets", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := model.Action{ID: "a", Name: "a", Items: []model.Item{
				{ID: "f", RemoteName: "test", Type: model.ItemFile, LocalPaths: map[string]string{"M": tt.path}},
			}}
			a.ApplyDefaults()
			ms, err := BuildMappings(a, "M", identity)
			if (err != nil) != tt.wantErr {
				t.Errorf("BuildMappings err = %v, wantErr %v", err, tt.wantErr)
			}
			// For successful cases, verify path is cleaned
			if !tt.wantErr && len(ms) > 0 {
				wantClean := filepath.Clean(filepath.FromSlash(tt.path))
				if ms[0].LocalPath != wantClean {
					t.Errorf("LocalPath %q, want %q", ms[0].LocalPath, wantClean)
				}
			}
		})
	}
}

func TestLocalPathForTraversalAndOddKeys(t *testing.T) {
	ms, err := BuildMappings(mappingAction(), "M", identity)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]bool{
		"snippets/../../etc/passwd": false,
		"snippets/.":                false,
		"snippets/":                 false,
		"snippets":                  false,
		"/snippets/x":               false,
		"snippets/C:x":              false,
		"snippets/a:stream":         false,
		"snippets/\x00":             false,
		"snippets/a\\b":             false,
		"snippets/ok.txt":           true,
	}

	for remotePath, wantOk := range tests {
		_, ok := ms.LocalPathFor(remotePath)
		if ok != wantOk {
			t.Errorf("LocalPathFor(%q) ok = %v, want %v", remotePath, ok, wantOk)
		}
	}
}

func TestLocalPathForExcluded(t *testing.T) {
	ms, err := BuildMappings(mappingAction(), "M", identity)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that excluded paths return ok=false from LocalPathFor
	tests := map[string]bool{
		"snippets/x.log":        false, // Excluded by action's excludes (*.log)
		"snippets/cache/x.json": false, // Excluded by item's excludes (cache)
		"snippets/ok.json":      true,  // Not excluded
	}

	for remotePath, wantOk := range tests {
		_, ok := ms.LocalPathFor(remotePath)
		if ok != wantOk {
			t.Errorf("LocalPathFor(%q) ok = %v, want %v", remotePath, ok, wantOk)
		}
	}
}

func TestFileItemExclusion(t *testing.T) {
	// Test that file items respect basename exclusions
	a := model.Action{
		ID:       "a",
		Name:     "a",
		Excludes: []string{"*.json"},
		Items: []model.Item{
			{ID: "f", RemoteName: "settings.json", Type: model.ItemFile, LocalPaths: map[string]string{"M": "/cfg/settings.json"}},
		},
	}
	a.ApplyDefaults()

	ms, err := BuildMappings(a, "M", identity)
	if err != nil {
		t.Fatal(err)
	}

	if ok := ms.Owns("settings.json"); ok {
		t.Error("Owns('settings.json') should be false for excluded file item")
	}

	// LocalPathFor should also return false for excluded file item
	if _, ok := ms.LocalPathFor("settings.json"); ok {
		t.Error("LocalPathFor('settings.json') should be false for excluded file item")
	}
}

func TestFilterOwnedKeySurvival(t *testing.T) {
	// Verify that filterOwned keeps the correct key
	ms, err := BuildMappings(mappingAction(), "M", identity)
	if err != nil {
		t.Fatal(err)
	}

	files := map[string]model.FileEntry{
		"settings.json":    {},
		"other/x":          {},
		"snippets/x.log":   {},
		"snippets/cache/x": {},
	}
	got := filterOwned(files, ms)

	if len(got) != 1 {
		t.Fatalf("filterOwned length = %d, want 1", len(got))
	}
	if _, ok := got["settings.json"]; !ok {
		t.Errorf("filterOwned should keep 'settings.json', got keys: %v", got)
	}
}
