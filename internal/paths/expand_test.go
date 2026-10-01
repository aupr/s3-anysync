package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandWith(t *testing.T) {
	home := t.TempDir()
	appdata := filepath.Join(home, "AppData", "Roaming")
	env := map[string]string{"APPDATA": appdata}
	getenv := func(k string) string { return env[k] }

	ok := map[string]string{
		"~":                   home,
		"~/x/y.json":          filepath.Join(home, "x", "y.json"),
		"$HOME/.config/a":     filepath.Join(home, ".config", "a"),
		"%APPDATA%/Code/User": filepath.Join(appdata, "Code", "User"),
		"%appdata%/Code":      filepath.Join(appdata, "Code"),
		home + "/a/../b":      filepath.Join(home, "b"),
		"  ~/trimmed  ":       filepath.Join(home, "trimmed"),
	}
	for in, want := range ok {
		got, err := ExpandWith(in, home, getenv)
		if err != nil || got != want {
			t.Errorf("ExpandWith(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	bad := map[string]string{
		"":                 "empty",
		"relative/x":       "not absolute",
		"%LOCALAPPDATA%/x": "LOCALAPPDATA",
		"%PROGRAMFILES%/x": "unsupported variable",
	}
	for in, want := range bad {
		_, err := ExpandWith(in, home, getenv)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ExpandWith(%q): want error containing %q, got %v", in, want, err)
		}
	}
}
