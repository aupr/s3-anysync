package paths

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// TempFilePrefix names the temporary files written during atomic downloads.
// They are always excluded.
const TempFilePrefix = ".s3-anysync-tmp-"

// Matcher matches slash-separated relative paths against exclude globs.
// A pattern without "/" matches the last path element at any depth; a
// pattern with "/" matches the whole relative path. "**" is supported.
// Backslashes in patterns are treated as separators.
type Matcher struct {
	patterns []string
}

// NewMatcher combines several pattern lists (for example the action's and an item's).
func NewMatcher(lists ...[]string) (*Matcher, error) {
	m := &Matcher{patterns: []string{TempFilePrefix + "*"}}
	for _, list := range lists {
		for _, raw := range list {
			p := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/"), "/")
			if p == "" {
				continue
			}
			if !doublestar.ValidatePattern(p) {
				return nil, fmt.Errorf("invalid exclude pattern %q", raw)
			}
			m.patterns = append(m.patterns, p)
		}
	}
	return m, nil
}

// Match reports whether rel itself is excluded.
func (m *Matcher) Match(rel string) bool {
	rel = strings.ReplaceAll(rel, `\`, "/")
	base := path.Base(rel)
	for _, p := range m.patterns {
		target := base
		if strings.Contains(p, "/") {
			target = rel
		}
		if ok, _ := doublestar.Match(p, target); ok {
			return true
		}
	}
	return false
}

// MatchPath reports whether rel or any of its parent directories is excluded.
func (m *Matcher) MatchPath(rel string) bool {
	parts := strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/")
	for i := 1; i <= len(parts); i++ {
		if m.Match(strings.Join(parts[:i], "/")) {
			return true
		}
	}
	return false
}
