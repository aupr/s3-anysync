// Package trigger starts syncs: a file watcher with debounce, a schedule
// loop and a remote poller.
package trigger

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/niceit/s3-anysync/internal/paths"
)

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
	Fire  func()
	// Rescan is how often roots that don't exist yet are retried (default 30s).
	Rescan time.Duration
	Log    *slog.Logger
}

// Watcher calls Fire once changes under its roots settle.
type Watcher struct {
	o       WatchOptions
	fsw     *fsnotify.Watcher
	mu      sync.Mutex
	watched map[string]bool
	files   map[string]bool // file roots (configured and resolved)
	dirs    []string        // dir roots (configured and resolved)
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

// Watch starts watching.
func Watch(o WatchOptions) (*Watcher, error) {
	if o.Rescan <= 0 {
		o.Rescan = 30 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{o: o, fsw: fsw, watched: map[string]bool{}, files: map[string]bool{}, stop: make(chan struct{}), done: make(chan struct{})}
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
func (w *Watcher) addRoots() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dirs = w.dirs[:0]
	for _, r := range w.o.Roots {
		for _, p := range withResolved(r.Path) {
			if r.Dir {
				w.dirs = append(w.dirs, p)
				w.addTreeLocked(p)
			} else {
				w.files[p] = true
				w.addDirLocked(filepath.Dir(p))
			}
		}
	}
}

func (w *Watcher) addDirLocked(dir string) {
	if w.watched[dir] {
		return
	}
	if err := w.fsw.Add(dir); err == nil {
		w.watched[dir] = true
	}
}

// addTreeLocked watches dir and its subdirectories, not following symlinks.
func (w *Watcher) addTreeLocked(dir string) {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // missing or unreadable: retried on the next rescan
		}
		if d.IsDir() {
			w.addDirLocked(p)
		}
		return nil
	})
}

// relevant reports whether an event path belongs to a root and matters.
func (w *Watcher) relevant(p string) bool {
	if strings.HasPrefix(filepath.Base(p), paths.TempFilePrefix) {
		return false
	}
	w.mu.Lock()
	inRoot := w.files[p]
	for _, d := range w.dirs {
		if p == d || strings.HasPrefix(p, d+string(filepath.Separator)) {
			inRoot = true
			break
		}
	}
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
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
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
			if ev.Has(fsnotify.Create) {
				if info, err := os.Lstat(p); err == nil && info.IsDir() {
					w.mu.Lock()
					w.addTreeLocked(p)
					w.mu.Unlock()
				}
			}
			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				w.mu.Lock()
				delete(w.watched, p)
				w.mu.Unlock()
			}
			if !w.relevant(p) {
				continue
			}
			if debounce == nil {
				debounce = time.NewTimer(w.o.Debounce)
			} else {
				debounce.Reset(w.o.Debounce)
			}
			fireC = debounce.C
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.o.Log.Warn("file watcher", "err", err)
		case <-rescan.C:
			w.addRoots()
		case <-fireC:
			fireC = nil
			w.o.Fire()
		}
	}
}
