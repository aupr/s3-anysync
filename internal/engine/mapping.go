package engine

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
)

// Mapping ties one item to its local path on this machine.
type Mapping struct {
	ItemID     string
	RemoteName string
	Type       model.ItemType
	LocalPath  string
	matcher    *paths.Matcher
}

// Mappings are the items mapped on this machine.
type Mappings []Mapping

// BuildMappings returns the action's items that have a local path for
// machineID, with paths expanded and validated. Unmapped items are left out.
// Rejects paths that are not absolute, or are filesystem roots.
func BuildMappings(a model.Action, machineID string, expand func(string) (string, error)) (Mappings, error) {
	var ms Mappings
	for _, it := range a.Items {
		raw := strings.TrimSpace(it.LocalPaths[machineID])
		if raw == "" {
			continue
		}
		abs, err := expand(raw)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", it.RemoteName, err)
		}
		clean := filepath.Clean(abs)
		if !filepath.IsAbs(clean) {
			return nil, fmt.Errorf("item %s: local path %q is not an absolute path to a file or folder", it.RemoteName, clean)
		}
		if filepath.Dir(clean) == clean {
			return nil, fmt.Errorf("item %s: local path %q is not an absolute path to a file or folder", it.RemoteName, clean)
		}
		m, err := paths.NewMatcher(a.Excludes, it.Excludes)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", it.RemoteName, err)
		}
		ms = append(ms, Mapping{ItemID: it.ID, RemoteName: it.RemoteName, Type: it.Type, LocalPath: clean, matcher: m})
	}
	return ms, nil
}

// safeRel rejects relative paths that could escape the item's directory,
// or contain characters unsafe on Windows (like ':' in alternate data streams).
func safeRel(rel string) bool {
	if rel == "" || strings.Contains(rel, `\`) {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
		// Reject segments with ':', NUL, or control characters (< 0x20)
		for _, ch := range seg {
			if ch == ':' || ch == 0 || (ch > 0 && ch < 0x20) {
				return false
			}
		}
	}
	return true
}

func (ms Mappings) resolve(remotePath string) (Mapping, string, bool) {
	for _, m := range ms {
		// Guard against malformed RemoteName (empty or starts/ends with "/")
		if m.RemoteName == "" || strings.HasPrefix(m.RemoteName, "/") || strings.HasSuffix(m.RemoteName, "/") {
			continue
		}
		switch m.Type {
		case model.ItemFile:
			if remotePath == m.RemoteName {
				return m, path.Base(m.RemoteName), true
			}
		case model.ItemDir:
			if rel, ok := strings.CutPrefix(remotePath, m.RemoteName+"/"); ok && safeRel(rel) {
				return m, rel, true
			}
		}
	}
	return Mapping{}, "", false
}

// LocalPathFor returns where a remote path lives on this machine.
// It only returns ok=true for paths that are mapped and not excluded by this machine.
func (ms Mappings) LocalPathFor(remotePath string) (string, bool) {
	m, rel, ok := ms.resolve(remotePath)
	if !ok {
		return "", false
	}
	// Check excludes: treat nil matcher as "no excludes"
	if m.matcher != nil && m.matcher.MatchPath(rel) {
		return "", false
	}
	if m.Type == model.ItemFile {
		return m.LocalPath, true
	}
	return filepath.Join(m.LocalPath, filepath.FromSlash(rel)), true
}

// Owns reports whether a remote path belongs to a mapped item and is not excluded.
// This is consistent with LocalPathFor.
func (ms Mappings) Owns(remotePath string) bool {
	_, ok := ms.LocalPathFor(remotePath)
	return ok
}

// filterOwned keeps only entries this machine owns.
func filterOwned(files map[string]model.FileEntry, ms Mappings) map[string]model.FileEntry {
	out := make(map[string]model.FileEntry, len(files))
	for p, e := range files {
		if ms.Owns(p) {
			out[p] = e
		}
	}
	return out
}
