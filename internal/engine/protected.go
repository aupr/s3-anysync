package engine

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// foldPathCase: file systems here are usually case-insensitive, so overlap
// checks ignore case (a false positive only refuses a folder).
var foldPathCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// CheckProtected returns ErrProtectedPath (wrapped with the item name and the
// offending path) when an item's local path equals, contains or lies inside
// one of the protected paths. Paths are compared as cleaned absolute paths and
// with symlinks resolved.
func CheckProtected(ms Mappings, protected []string) error {
	for _, m := range ms {
		if p, ok := overlapsProtected(m.LocalPath, protected); ok {
			return fmt.Errorf("item %s: %s overlaps %s, %w", m.RemoteName, m.LocalPath, p, ErrProtectedPath)
		}
	}
	return nil
}

// CheckLocalPath is CheckProtected for one path that is not mapped yet.
func CheckLocalPath(local string, protected []string) error {
	if p, ok := overlapsProtected(local, protected); ok {
		return fmt.Errorf("%s overlaps %s, %w", local, p, ErrProtectedPath)
	}
	return nil
}

func overlapsProtected(local string, protected []string) (string, bool) {
	lf := pathForms(local)
	for _, p := range protected {
		if p == "" {
			continue
		}
		for _, pf := range pathForms(p) {
			for _, l := range lf {
				if pathsOverlap(l, pf) {
					return p, true
				}
			}
		}
	}
	return "", false
}

// pathForms returns the cleaned absolute path and, when it differs, its
// symlink-resolved form (the nearest existing ancestor is resolved).
func pathForms(p string) []string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	forms := []string{p}
	if r := resolveExisting(p); r != p {
		forms = append(forms, r)
	}
	return forms
}

func resolveExisting(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// pathsOverlap: a == b, a inside b or b inside a (whole segments only).
func pathsOverlap(a, b string) bool {
	if foldPathCase {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return within(a, b) || within(b, a)
}

// within reports whether child equals parent or lies below it.
func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
