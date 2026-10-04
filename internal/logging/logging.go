// Package logging writes the agent's JSON logs to daily files.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const prefix, suffix = "agent-", ".log"

// DailyFile writes to <dir>/agent-YYYY-MM-DD.log (local date), switching files
// when the date changes and deleting logs older than keep days.
type DailyFile struct {
	dir  string
	keep int
	now  func() time.Time
	mu   sync.Mutex
	day  string
	f    *os.File
}

// OpenDaily opens today's log file in dir (created with mode 0700).
func OpenDaily(dir string, keep int, now func() time.Time) (*DailyFile, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d := &DailyFile{dir: dir, keep: keep, now: now}
	if err := d.rotate(now().Format("2006-01-02")); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DailyFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if day := d.now().Format("2006-01-02"); day != d.day {
		if err := d.rotate(day); err != nil {
			return 0, err
		}
	}
	return d.f.Write(p)
}

// Close closes the current file.
func (d *DailyFile) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return nil
	}
	err := d.f.Close()
	d.f = nil
	return err
}

func (d *DailyFile) rotate(day string) error {
	if d.f != nil {
		d.f.Close()
	}
	f, err := os.OpenFile(filepath.Join(d.dir, prefix+day+suffix), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	d.f, d.day = f, day
	d.prune()
	return nil
}

// prune deletes agent-*.log files dated more than keep days before today.
func (d *DailyFile) prune() {
	today, err := time.ParseInLocation("2006-01-02", d.day, time.Local)
	if err != nil {
		return
	}
	cutoff := today.AddDate(0, 0, -d.keep)
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix), time.Local)
		if err == nil && t.Before(cutoff) {
			os.Remove(filepath.Join(d.dir, name))
		}
	}
}

// ParseLevel parses debug, info, warn or error.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

// New returns a JSON logger writing to w at a level that can change later.
func New(w io.Writer, level *slog.LevelVar) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}
