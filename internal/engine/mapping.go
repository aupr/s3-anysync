package engine

import (
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/paths"
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
		// Guard against malformed RemoteName: must be non-empty, clean, normalized,
		// and contain no unsafe characters (backslash, ':', NUL, control chars).
		// Fail closed on nil matcher (gates deletes).
		if m.RemoteName == "" || m.RemoteName == "." || path.Clean(m.RemoteName) != m.RemoteName || !safeRel(m.RemoteName) || m.matcher == nil {
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

// targetOS is the OS whose file-name rules apply to local paths (a test seam).
var targetOS = runtime.GOOS

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// localNameOK reports whether every segment of rel can be a file name on targetOS.
func localNameOK(rel string) bool {
	if targetOS != "windows" {
		return true
	}
	for _, seg := range strings.Split(rel, "/") {
		if strings.ContainsAny(seg, `<>:"|?*`) || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return false
		}
		base := strings.ToUpper(seg)
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if windowsReserved[base] {
			return false
		}
	}
	return true
}

type lookupStatus int

const (
	notMapped lookupStatus = iota
	excluded
	unsupportedName
	owned
)

func (ms Mappings) lookup(remotePath string) (string, lookupStatus) {
	m, rel, ok := ms.resolve(remotePath)
	if !ok {
		return "", notMapped
	}
	if m.matcher.MatchPath(rel) {
		return "", excluded
	}
	if !localNameOK(rel) {
		return "", unsupportedName
	}
	if m.Type == model.ItemFile {
		return m.LocalPath, owned
	}
	return filepath.Join(m.LocalPath, filepath.FromSlash(rel)), owned
}

// LocalPathFor returns where a remote path lives on this machine. ok is true
// only for paths this machine owns: mapped, not excluded, and a valid name on
// this OS. It is safe to use as the only gate before reading or writing.
func (ms Mappings) LocalPathFor(remotePath string) (string, bool) {
	p, st := ms.lookup(remotePath)
	return p, st == owned
}

// Unsupported reports whether a remote path belongs to a mapped item and is
// not excluded, but cannot exist on this OS.
func (ms Mappings) Unsupported(remotePath string) bool {
	_, st := ms.lookup(remotePath)
	return st == unsupportedName
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

// RemotePathFor maps a local path back to the remote path it syncs to, when
// this machine owns it (used to filter file-watcher events). A symlinked
// root matches both as configured and as resolved. Item roots themselves
// map to nothing.
func (ms Mappings) RemotePathFor(abs string) (string, bool) {
	abs = filepath.Clean(abs)
	// Watchers may report the path with or without symlinks resolved (e.g.
	// /var vs /private/var on macOS); try both. The file itself may be gone,
	// so only its parent directory is resolved.
	abss := []string{abs}
	if real, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		if r := filepath.Join(real, filepath.Base(abs)); r != abs {
			abss = append(abss, r)
		}
	}
	for _, m := range ms {
		roots := []string{m.LocalPath}
		if real, err := filepath.EvalSymlinks(m.LocalPath); err == nil && real != m.LocalPath {
			roots = append(roots, real)
		}
		for _, root := range roots {
			for _, abs := range abss {
				var key string
				switch m.Type {
				case model.ItemFile:
					if abs != root {
						continue
					}
					key = m.RemoteName
				case model.ItemDir:
					rel, err := filepath.Rel(root, abs)
					if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						continue
					}
					key = m.RemoteName + "/" + filepath.ToSlash(rel)
				}
				if ms.Owns(key) {
					return key, true
				}
			}
		}
	}
	return "", false
}
