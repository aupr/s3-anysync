package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/niceit/s3-anysync/internal/model"
)

// scanLocal lists every mapped, non-excluded local file keyed by remote path.
// A file whose size and mtime match its baseline entry reuses the baseline
// hash. Symlinks to files are followed. A symlinked directory ROOT of a dir
// item is followed; symlinks to directories inside a dir item are not followed
// (skipped, or an error if content under them was synced before), as are
// special files.
func scanLocal(ms Mappings, baseline map[string]model.FileEntry) (map[string]model.FileEntry, error) {
	out := map[string]model.FileEntry{}
	for _, m := range ms {
		if m.matcher == nil {
			return nil, fmt.Errorf("scan: item %s has no exclude matcher", m.RemoteName)
		}
		var err error
		if m.Type == model.ItemFile {
			err = scanFile(m, baseline, out)
		} else {
			err = scanDir(m, baseline, out)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func refuseDeleted(p string) error {
	return fmt.Errorf("scan: %s is a dangling symlink (or special file) but was synced before; refusing to treat it as deleted", p)
}

func rootMissing(m Mapping) error {
	return fmt.Errorf("scan: item %s: %s does not exist but its content was synced before; refusing to treat it as deleted (if it was removed on purpose, unmap the item and map it again): %w", m.RemoteName, m.LocalPath, ErrRootMissing)
}

func hasBaselineUnder(baseline map[string]model.FileEntry, remoteName string) bool {
	for k := range baseline {
		if strings.HasPrefix(k, remoteName+"/") {
			return true
		}
	}
	return false
}

func scanFile(m Mapping, baseline, out map[string]model.FileEntry) error {
	if m.matcher.Match(path.Base(m.RemoteName)) {
		return nil
	}
	if _, err := os.Lstat(m.LocalPath); errors.Is(err, fs.ErrNotExist) {
		if _, ok := baseline[m.RemoteName]; ok {
			if _, perr := os.Stat(filepath.Dir(m.LocalPath)); errors.Is(perr, fs.ErrNotExist) {
				return rootMissing(m)
			}
		}
		return nil
	}
	info, err := os.Stat(m.LocalPath)
	if errors.Is(err, fs.ErrNotExist) { // dangling symlink
		if _, ok := baseline[m.RemoteName]; ok {
			return refuseDeleted(m.LocalPath)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("scan: stat %s: %w", m.LocalPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("item %s expects a file but %s is a directory", m.RemoteName, m.LocalPath)
	}
	if !info.Mode().IsRegular() {
		if _, ok := baseline[m.RemoteName]; ok {
			return refuseDeleted(m.LocalPath)
		}
		return nil
	}
	e, err := entryFor(m.RemoteName, m.LocalPath, info, baseline)
	if err != nil {
		return err
	}
	out[m.RemoteName] = e
	return nil
}

func skipOrRefuse(baseline map[string]model.FileEntry, key, p string) error {
	if _, ok := baseline[key]; ok || hasBaselineUnder(baseline, key) {
		return refuseDeleted(p)
	}
	return nil
}

func scanDir(m Mapping, baseline, out map[string]model.FileEntry) error {
	root := m.LocalPath
	li, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		if hasBaselineUnder(baseline, m.RemoteName) {
			return rootMissing(m)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("scan: stat %s: %w", root, err)
	}
	if !li.IsDir() { // symlink, or Windows junction/reparse point (ModeIrregular)
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			if hasBaselineUnder(baseline, m.RemoteName) {
				return fmt.Errorf("scan: item %s: cannot resolve symlink %s but items were synced before; refusing to treat as deleted: %w", m.RemoteName, root, err)
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("scan: resolve %s: %w", root, err)
		}
		root = resolved
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("scan: stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("item %s expects a directory but %s is a file", m.RemoteName, m.LocalPath)
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if m.matcher.Match(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if m.matcher.Match(rel) {
			return nil
		}
		key := m.RemoteName + "/" + rel
		info, err := os.Stat(p)             // follows symlinks
		if errors.Is(err, fs.ErrNotExist) { // dangling symlink
			return skipOrRefuse(baseline, key, p)
		}
		if err != nil {
			return fmt.Errorf("scan: stat %s: %w", p, err)
		}
		if !info.Mode().IsRegular() {
			return skipOrRefuse(baseline, key, p)
		}
		e, err := entryFor(key, p, info, baseline)
		if err != nil {
			return err
		}
		out[key] = e
		return nil
	})
}

func entryFor(key, abs string, info fs.FileInfo, baseline map[string]model.FileEntry) (model.FileEntry, error) {
	e := model.FileEntry{Path: key, Size: info.Size(), MTime: info.ModTime().UnixNano()}
	if b, ok := baseline[key]; ok && b.Hash != "" && b.Size == e.Size && b.MTime == e.MTime {
		e.Hash = b.Hash
		return e, nil
	}
	h, err := hashFile(abs)
	if err != nil {
		return e, err
	}
	e.Hash = h
	return e, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
