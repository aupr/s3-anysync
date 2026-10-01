package engine

import (
	"errors"
	"io/fs"
	"os"

	"github.com/niceit/s3-anysync/internal/model"
)

// localState is how a local path compares with what the plan saw at scan time.
type localState int

const (
	localSame    localState = iota // as planned
	localChanged                   // appeared, or its content/kind changed
	localGone                      // the planned file no longer exists
)

// checkLocal re-examines abs right before a destructive local operation.
// seen is the scan entry the plan was based on; nil means the path was absent.
// Size and mtime equal to the scan mean unchanged; otherwise the content is
// hashed so a touch without a content change still counts as unchanged. A
// path that cannot be examined counts as changed (the op is deferred).
func checkLocal(abs string, seen *model.FileEntry) localState {
	_, lerr := os.Lstat(abs)
	absent := errors.Is(lerr, fs.ErrNotExist)
	if seen == nil {
		if absent {
			return localSame
		}
		// A dangling symlink holds no file: the write goes through the link.
		if lerr == nil {
			if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
				return localSame
			}
		}
		return localChanged
	}
	if absent {
		return localGone
	}
	if lerr != nil {
		return localChanged
	}
	info, err := os.Stat(abs) // follows symlinks, like the scan
	if err != nil || !info.Mode().IsRegular() {
		return localChanged
	}
	if info.Size() == seen.Size && info.ModTime().UnixNano() == seen.MTime {
		return localSame
	}
	h, err := hashFile(abs)
	if err != nil || h != seen.Hash {
		return localChanged
	}
	return localSame
}
