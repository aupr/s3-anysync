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
// machineID, with paths expanded. Unmapped items are left out.
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
		m, err := paths.NewMatcher(a.Excludes, it.Excludes)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", it.RemoteName, err)
		}
		ms = append(ms, Mapping{ItemID: it.ID, RemoteName: it.RemoteName, Type: it.Type, LocalPath: abs, matcher: m})
	}
	return ms, nil
}

// safeRel rejects relative paths that could escape the item's directory.
func safeRel(rel string) bool {
	if rel == "" || strings.Contains(rel, `\`) {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func (ms Mappings) resolve(remotePath string) (Mapping, string, bool) {
	for _, m := range ms {
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
func (ms Mappings) LocalPathFor(remotePath string) (string, bool) {
	m, rel, ok := ms.resolve(remotePath)
	if !ok {
		return "", false
	}
	if m.Type == model.ItemFile {
		return m.LocalPath, true
	}
	return filepath.Join(m.LocalPath, filepath.FromSlash(rel)), true
}

// Owns reports whether a remote path belongs to a mapped item and is not excluded.
func (ms Mappings) Owns(remotePath string) bool {
	m, rel, ok := ms.resolve(remotePath)
	return ok && !m.matcher.MatchPath(rel)
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
