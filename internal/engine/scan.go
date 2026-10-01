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

	"github.com/niceit/s3-anysync/internal/model"
)

// scanLocal lists every mapped, non-excluded local file keyed by remote path.
// A file whose size and mtime match its baseline entry reuses the baseline
// hash. Symlinks to files are followed; symlinked directories and special
// files are skipped.
func scanLocal(ms Mappings, baseline map[string]model.FileEntry) (map[string]model.FileEntry, error) {
	out := map[string]model.FileEntry{}
	for _, m := range ms {
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

func scanFile(m Mapping, baseline, out map[string]model.FileEntry) error {
	info, err := os.Stat(m.LocalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("item %s expects a file but %s is a directory", m.RemoteName, m.LocalPath)
	}
	if !info.Mode().IsRegular() || m.matcher.Match(path.Base(m.RemoteName)) {
		return nil
	}
	e, err := entryFor(m.RemoteName, m.LocalPath, info, baseline)
	if err != nil {
		return err
	}
	out[m.RemoteName] = e
	return nil
}

func scanDir(m Mapping, baseline, out map[string]model.FileEntry) error {
	info, err := os.Stat(m.LocalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("item %s expects a directory but %s is a file", m.RemoteName, m.LocalPath)
	}
	return filepath.WalkDir(m.LocalPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == m.LocalPath {
			return nil
		}
		rel, err := filepath.Rel(m.LocalPath, p)
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
		info, err := os.Stat(p) // follows symlinks
		if errors.Is(err, fs.ErrNotExist) {
			return nil // dangling symlink
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		key := m.RemoteName + "/" + rel
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
