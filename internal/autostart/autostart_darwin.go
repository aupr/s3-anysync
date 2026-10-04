//go:build darwin

package autostart

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type launchAgent struct {
	dir  string
	exe  string
	args []string
}

// New returns the LaunchAgent manager for exe run with args.
func New(exe string, args []string) (Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return newAt(filepath.Join(home, "Library", "LaunchAgents"), exe, args), nil
}

func newAt(dir, exe string, args []string) *launchAgent {
	return &launchAgent{dir: dir, exe: exe, args: args}
}

func (l *launchAgent) path() string { return filepath.Join(l.dir, Label+".plist") }

func (l *launchAgent) Enable() error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	tmp := l.path() + ".tmp"
	if err := os.WriteFile(tmp, []byte(plistContent(l.exe, l.args)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path())
}

func (l *launchAgent) Disable() error {
	if err := os.Remove(l.path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *launchAgent) Enabled() (bool, error) {
	_, err := os.Stat(l.path())
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (l *launchAgent) Target() (string, error) {
	data, err := os.ReadFile(l.path())
	if err != nil {
		return "", err
	}
	return plistTarget(data)
}
