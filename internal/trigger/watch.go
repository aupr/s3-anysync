// Package trigger starts syncs: a file watcher with debounce, a schedule
// loop and a remote poller.
package trigger

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/niceit/s3-anysync/internal/paths"
)

// fileFDWatches: kqueue-style backends watch each file through its own
// descriptor, which a late event for a replaced file can drop.
var fileFDWatches = runtime.GOOS != "linux" && runtime.GOOS != "windows"

// Root is one watched location: a directory tree, or a single file (whose
// parent directory is watched).
type Root struct {
	Path string
	Dir  bool
}

// WatchOptions configure a Watcher.
type WatchOptions struct {
	Roots    []Root
	Debounce time.Duration
	// Match reports whether a changed path matters; nil means every path.
	Match func(abs string) bool
	// Fire runs on the watcher's own goroutine: it must not block (start the
	// work elsewhere) and must not call Close.
	Fire func()
	// Rescan is how often roots that don't exist yet are retried (default 30s).
	Rescan time.Duration
	Log    *slog.Logger
	// MaxWait bounds how long a continuous stream of events can postpone
	// Fire, counted from the first event of a burst (default 10 * Debounce).
	MaxWait time.Duration
	// MaxWatchedDirs caps the directories watched by this watcher
	// (default 20000); further directories are not watched.
	MaxWatchedDirs int
}

// Watcher calls Fire once changes under its roots settle.
type Watcher struct {
	o       WatchOptions
	fsw     *fsnotify.Watcher
	mu      sync.Mutex
	watched map[string]bool
	files   map[string]bool // file roots (configured and resolved)
	dirs    []string        // dir roots (configured and resolved)
	parents map[string]bool // parent directories of file roots
	warned  map[string]bool // dedupe of watch-failure warnings
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

// Watch starts watching.
func Watch(o WatchOptions) (*Watcher, error) {
	if o.Rescan <= 0 {
		o.Rescan = 30 * time.Second
	}
	if o.MaxWait <= 0 {
		o.MaxWait = 10 * o.Debounce
	}
	if o.MaxWatchedDirs <= 0 {
		o.MaxWatchedDirs = 20000
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{o: o, fsw: fsw, watched: map[string]bool{}, files: map[string]bool{}, parents: map[string]bool{}, warned: map[string]bool{}, stop: make(chan struct{}), done: make(chan struct{})}
	w.addRoots()
	go w.run()
	return w, nil
}

// Close stops the watcher; Fire is not called afterwards.
func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.stop)
		<-w.done
		err = w.fsw.Close()
	})
	return err
}

func withResolved(p string) []string {
	out := []string{filepath.Clean(p)}
	if real, err := filepath.EvalSymlinks(p); err == nil && real != out[0] {
		out = append(out, real)
	}
	return out
}

// addRoots (re)adds every root that exists; called at start and on rescans.
// The sets of file and dir roots are recomputed from scratch, so a root that
// now resolves elsewhere (a retargeted symlink) stops matching its old target.
// Configured and resolved paths are both watched; that double-watching is
// harmless apart from duplicate events, which the debounce coalesces.
// It reports whether any root, or the parent directory of a file root,
// became newly watched: content already there produced no event.
func (w *Watcher) addRoots() (fresh bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	files := map[string]bool{}
	parents := map[string]bool{}
	var dirs []string
	w.files, w.parents, w.dirs = files, parents, nil
	for _, r := range w.o.Roots {
		for _, p := range withResolved(r.Path) {
			if r.Dir {
				dirs = append(dirs, p)
				w.dirs = dirs
				if w.addTreeLocked(p) {
					fresh = true
				}
			} else {
				files[p] = true
				parent := filepath.Dir(p)
				parents[parent] = true
				if w.addDirLocked(parent) {
					fresh = true
				}
			}
		}
	}
	return fresh
}

// addDirLocked watches dir; it reports whether dir became newly watched.
func (w *Watcher) addDirLocked(dir string) bool {
	if w.watched[dir] {
		return false
	}
	if len(w.watched) >= w.o.MaxWatchedDirs {
		w.warnOnceLocked("cap", "watch limit reached: further directories are not watched and changes there will only be noticed by schedules and cloud polls", "max", w.o.MaxWatchedDirs, "path", dir)
		return false
	}
	if fileFDWatches {
		// Descriptors of files from a directory that was moved or removed
		// here stay registered under their paths and would shadow the new
		// files of the same names; drop them before watching afresh.
		if ents, err := os.ReadDir(dir); err == nil {
			for _, e := range ents {
				if !e.IsDir() {
					w.fsw.Remove(filepath.Join(dir, e.Name()))
				}
			}
		}
	}
	if err := w.fsw.Add(dir); err != nil {
		w.noteAddFailureLocked(dir, err)
		return false
	}
	w.watched[dir] = true
	return true
}

func (w *Watcher) warnOnceLocked(key, msg string, args ...any) {
	if w.warned[key] {
		return
	}
	w.warned[key] = true
	w.o.Log.Warn(msg, args...)
}

func (w *Watcher) noteAddFailure(dir string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.noteAddFailureLocked(dir, err)
}

// noteAddFailureLocked logs a failed fsw.Add once per path and error class.
func (w *Watcher) noteAddFailureLocked(dir string, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		return // vanished meanwhile: retried on the next rescan
	}
	class := "other"
	hint := ""
	switch {
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		class = "EMFILE/ENFILE"
		hint = " (too many open files: EMFILE/ENFILE)"
	case errors.Is(err, syscall.ENOSPC):
		class = "ENOSPC"
		hint = " (watch limit reached: ENOSPC)"
	}
	w.warnOnceLocked(dir+"|"+class, "directory not watched"+hint+": changes there will only be noticed by schedules and cloud polls", "path", dir, "err", err)
}

// addTreeLocked watches dir and its subdirectories, not following symlinks.
// It reports whether dir itself became newly watched.
func (w *Watcher) addTreeLocked(dir string) bool {
	was := w.watched[dir]
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			w.o.Log.Debug("watch walk", "path", p, "err", err)
			return nil // missing or unreadable: retried on the next rescan
		}
		if d.IsDir() {
			w.addDirLocked(p)
		}
		return nil
	})
	return !was && w.watched[dir]
}

// inDirRootLocked reports whether p is a directory root or inside one.
func (w *Watcher) inDirRootLocked(p string) bool {
	for _, d := range w.dirs {
		if p == d || strings.HasPrefix(p, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// forgetTreeLocked stops watching p and everything below it.
func (w *Watcher) forgetTreeLocked(p string) {
	prefix := p + string(filepath.Separator)
	for d := range w.watched {
		if d == p || strings.HasPrefix(d, prefix) {
			w.fsw.Remove(d) // may already be gone
			delete(w.watched, d)
		}
	}
}

// relevant reports whether an event path belongs to a root and matters.
func (w *Watcher) relevant(p string, op fsnotify.Op) bool {
	if strings.HasPrefix(filepath.Base(p), paths.TempFilePrefix) {
		return false
	}
	w.mu.Lock()
	if w.parents[p] && op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		// the directory holding a file root went away: whatever replaces it
		// is only seen by the sync that follows
		w.mu.Unlock()
		return true
	}
	inRoot := w.files[p] || w.inDirRootLocked(p)
	w.mu.Unlock()
	if !inRoot {
		return false
	}
	return w.o.Match == nil || w.o.Match(p)
}

func (w *Watcher) run() {
	defer close(w.done)
	rescan := time.NewTicker(w.o.Rescan)
	defer rescan.Stop()
	var debounce *time.Timer
	var fireC <-chan time.Time
	var burstStart time.Time
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	arm := func() {
		now := time.Now()
		if fireC == nil {
			burstStart = now
		}
		d := w.o.Debounce
		if left := w.o.MaxWait - now.Sub(burstStart); left < d {
			d = max(left, 0)
		}
		if debounce == nil {
			debounce = time.NewTimer(d)
		} else {
			debounce.Reset(d)
		}
		fireC = debounce.C
	}
	for {
		select {
		case <-w.stop:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			p := filepath.Clean(ev.Name)
			if ev.Op == fsnotify.Chmod {
				continue
			}
			gone := ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename)
			if gone {
				w.mu.Lock()
				w.forgetTreeLocked(p)
				w.mu.Unlock()
			}
			// A created directory is watched. So is one that reappeared at p
			// after a remove/rename: the backend drops watches by path when it
			// processes the event, which can hit the new one.
			if ev.Has(fsnotify.Create) || gone {
				if info, err := os.Lstat(p); err == nil {
					w.mu.Lock()
					if info.IsDir() {
						if w.inDirRootLocked(p) {
							w.addTreeLocked(p)
						} else if w.parents[p] {
							w.addDirLocked(p)
						}
					}
					w.mu.Unlock()
				}
			}
			if !w.relevant(p, ev.Op) {
				continue
			}
			arm()
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.o.Log.Warn("file watcher", "err", err)
		case <-rescan.C:
			if w.addRoots() {
				arm()
			}
		case <-fireC:
			fireC = nil
			w.o.Fire()
		}
	}
}
