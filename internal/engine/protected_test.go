package engine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func msFor(paths ...string) Mappings {
	var ms Mappings
	for _, p := range paths {
		ms = append(ms, Mapping{RemoteName: "x", LocalPath: p})
	}
	return ms
}

func TestCheckProtected(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	data2 := filepath.Join(root, "data2")
	for _, d := range []string{filepath.Join(data, "logs"), data2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prot := []string{data}
	for name, p := range map[string]string{
		"equal":           data,
		"contains":        root,
		"inside":          filepath.Join(data, "logs"),
		"inside, missing": filepath.Join(data, "nope", "x"),
	} {
		if err := CheckProtected(msFor(p), prot); !errors.Is(err, ErrProtectedPath) {
			t.Errorf("%s: want ErrProtectedPath, got %v", name, err)
		}
	}
	for name, p := range map[string]string{
		"sibling with same prefix": data2,
		"unrelated":                t.TempDir(),
	} {
		if err := CheckProtected(msFor(p), prot); err != nil {
			t.Errorf("%s: unexpected %v", name, err)
		}
	}
	if err := CheckProtected(msFor(root), nil); err != nil {
		t.Errorf("nothing protected: %v", err)
	}
}

func TestCheckProtectedSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	os.MkdirAll(filepath.Join(data, "logs"), 0o755)
	os.MkdirAll(filepath.Join(root, "other"), 0o755)
	linkToData := filepath.Join(root, "other", "link")
	if err := os.Symlink(data, linkToData); err != nil {
		t.Fatal(err)
	}
	linkToRoot := filepath.Join(t.TempDir(), "rootlink")
	if err := os.Symlink(root, linkToRoot); err != nil {
		t.Fatal(err)
	}
	// A symlink to the data folder, and a symlink to its parent.
	if err := CheckProtected(msFor(linkToData), []string{data}); !errors.Is(err, ErrProtectedPath) {
		t.Errorf("link to data: %v", err)
	}
	if err := CheckProtected(msFor(linkToRoot), []string{data}); !errors.Is(err, ErrProtectedPath) {
		t.Errorf("link to parent: %v", err)
	}
	// The protected path itself given through a symlink.
	if err := CheckProtected(msFor(filepath.Join(data, "logs")), []string{linkToData}); !errors.Is(err, ErrProtectedPath) {
		t.Errorf("protected via link: %v", err)
	}
	if err := CheckProtected(msFor(filepath.Join(root, "other")), []string{data}); err != nil {
		t.Errorf("sibling folder: %v", err)
	}
}
