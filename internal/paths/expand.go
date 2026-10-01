// Package paths expands user-typed local paths and matches exclude patterns.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var winVar = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)

// windowsVars are the only %VAR% names accepted in item paths.
var windowsVars = map[string]bool{"APPDATA": true, "USERPROFILE": true, "LOCALAPPDATA": true}

// Expand resolves ~, $HOME and the supported %VAR% names against this
// machine and returns a clean absolute path.
func Expand(p string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return ExpandWith(p, home, os.Getenv)
}

// ExpandWith is Expand with the home directory and environment injected.
func ExpandWith(p, home string, getenv func(string) string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("path is empty")
	}
	p = replaceHome(p, "~", home)
	p = replaceHome(p, "$HOME", home)
	var expandErr error
	p = winVar.ReplaceAllStringFunc(p, func(m string) string {
		name := strings.ToUpper(m[1 : len(m)-1])
		if !windowsVars[name] {
			if expandErr == nil {
				expandErr = fmt.Errorf("unsupported variable %s (use ~, $HOME, %%APPDATA%%, %%USERPROFILE%% or %%LOCALAPPDATA%%)", m)
			}
			return m
		}
		v := getenv(name)
		if v == "" && expandErr == nil {
			expandErr = fmt.Errorf("environment variable %%%s%% is not set on this machine", name)
		}
		return v
	})
	if expandErr != nil {
		return "", expandErr
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("path %q is not absolute", p)
	}
	return p, nil
}

func replaceHome(p, token, home string) string {
	if p == token {
		return home
	}
	if strings.HasPrefix(p, token+"/") || strings.HasPrefix(p, token+`\`) {
		return home + p[len(token):]
	}
	return p
}
