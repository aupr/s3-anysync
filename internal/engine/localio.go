package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aupr/s3-anysync/internal/paths"
)

// resolveLink follows dst if it is a symlink (even a dangling one) and returns
// the final path to write to. A non-symlink or missing dst is returned as is.
func resolveLink(dst string) (string, error) {
	info, err := os.Lstat(dst)
	if errors.Is(err, fs.ErrNotExist) {
		return dst, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return dst, nil
	}
	if r, err := filepath.EvalSymlinks(dst); err == nil {
		return r, nil
	}
	// Dangling (or looping) link: follow it manually.
	cur := dst
	for i := 0; i < 40; i++ {
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return cur, nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			return cur, nil
		}
		t, err := os.Readlink(cur)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(cur), t)
		}
		cur = t
	}
	return "", fmt.Errorf("too many levels of symbolic links: %s", dst)
}

// writeTempRename writes data to a temp file in path's directory and renames
// it onto path (creating parent dirs), then best-effort fsyncs the directory.
func writeTempRename(path string, data []byte, mode fs.FileMode, mtime time.Time) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, paths.TempFilePrefix+"*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
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
	if err := os.Rename(name, path); err != nil {
		return fail(err)
	}
	if runtime.GOOS != "windows" {
		if d, err := os.Open(dir); err == nil {
			_ = d.Sync() // best effort
			d.Close()
		}
	}
	return nil
}

// writeFileAtomic writes data to a temp file next to dst, then renames it
// into place, keeping dst's permissions if it existed and setting mtime. If
// dst is a symlink the write goes through the link (the link stays a link and
// its target gets the new content; a dangling link's target is created).
// The returned FileInfo comes from a final os.Stat of the resolved path; a
// failure of that stat is returned as an error even though the data was
// already written.
func writeFileAtomic(dst string, data []byte, mtime time.Time) (fs.FileInfo, error) {
	resolved, err := resolveLink(dst)
	if err != nil {
		return nil, err
	}
	mode := fs.FileMode(0o644)
	info, err := os.Stat(resolved)
	switch {
	case err == nil:
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory", dst)
		}
		mode = info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	if err := writeTempRename(resolved, data, mode, mtime); err != nil {
		return nil, err
	}
	return os.Stat(resolved)
}

var backupTSRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

func validElement(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) && !strings.ContainsRune(s, 0)
}

// backupLocal copies files (remote path → absolute local path) into
// root/actionID/ts/<remote path>, skipping files that no longer exist, then
// prunes old backups. Existing backup files are never overwritten (a numeric
// suffix is appended) and the new ts folder is never pruned.
func backupLocal(root, actionID, ts string, files map[string]string) error {
	if root == "" {
		return errors.New("local backup folder is not configured")
	}
	if !validElement(actionID) {
		return fmt.Errorf("invalid backup action id %q", actionID)
	}
	if !validElement(ts) {
		return fmt.Errorf("invalid backup timestamp %q", ts)
	}
	base := filepath.Join(root, actionID, ts)
	// Validate everything before writing anything.
	dsts := make(map[string]string, len(files))
	for remotePath := range files {
		dst := filepath.Join(base, filepath.FromSlash(remotePath))
		rel, err := filepath.Rel(base, dst)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("backup path %q escapes backup folder", remotePath)
		}
		dsts[remotePath] = dst
	}
	for remotePath, abs := range files {
		data, err := os.ReadFile(abs)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		mtime := time.Now()
		if st, err := os.Stat(abs); err == nil {
			mtime = st.ModTime()
		}
		dst := dsts[remotePath]
		target := dst
		for i := 1; ; i++ {
			if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
				break
			} else if err != nil {
				return err
			}
			target = fmt.Sprintf("%s.%d", dst, i)
		}
		if err := writeTempRename(target, data, 0o600, mtime); err != nil {
			return err
		}
	}
	return pruneBackupsKeeping(filepath.Join(root, actionID), LocalBackupKeep, ts)
}

// pruneBackups keeps the newest `keep` timestamped folders in dir.
func pruneBackups(dir string, keep int) error {
	return pruneBackupsKeeping(dir, keep, "")
}

// pruneBackupsKeeping keeps at most `keep` timestamp-named folders in dir,
// deleting the oldest first. Other entries are ignored. The protect folder
// counts toward keep but is never deleted.
func pruneBackupsKeeping(dir string, keep int, protect string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && backupTSRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	total := len(names)
	for _, n := range names {
		if total <= keep {
			break
		}
		if n == protect {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
			return err
		}
		total--
	}
	return nil
}

// removeLocal deletes a file or symlink (the link only); a missing path is not
// an error and directories are refused.
func removeLocal(abs string) error {
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("refusing to remove directory %s", abs)
	}
	err = os.Remove(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
