package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/niceit/s3-anysync/internal/paths"
)

// writeFileAtomic writes data to a temp file next to dst, then renames it
// into place, keeping dst's permissions if it existed and setting mtime.
func writeFileAtomic(dst string, data []byte, mtime time.Time) (fs.FileInfo, error) {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(dst); err == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory", dst)
		}
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, paths.TempFilePrefix+"*")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	fail := func(err error) (fs.FileInfo, error) {
		tmp.Close()
		os.Remove(name)
		return nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return fail(err)
	}
	if err := os.Chtimes(name, mtime, mtime); err != nil {
		return fail(err)
	}
	if err := os.Rename(name, dst); err != nil {
		return fail(err)
	}
	return os.Stat(dst)
}

// backupLocal copies files (remote path → absolute local path) into
// root/actionID/ts/<remote path>, skipping files that no longer exist, then
// prunes old backups.
func backupLocal(root, actionID, ts string, files map[string]string) error {
	if root == "" {
		return errors.New("local backup folder is not configured")
	}
	for remotePath, abs := range files {
		data, err := os.ReadFile(abs)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		dst := filepath.Join(root, actionID, ts, filepath.FromSlash(remotePath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
	}
	return pruneBackups(filepath.Join(root, actionID), LocalBackupKeep)
}

// pruneBackups keeps the newest `keep` timestamped folders in dir.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.RemoveAll(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// removeLocal deletes a file; a missing file is not an error.
func removeLocal(abs string) error {
	err := os.Remove(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
