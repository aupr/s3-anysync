//go:build !darwin && !windows

package autostart

import "errors"

// New reports that autostart is not supported on this OS.
func New(string, []string) (Manager, error) {
	return nil, errors.New("autostart is not supported on this OS")
}
