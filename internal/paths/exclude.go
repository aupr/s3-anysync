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
// A pattern with a leading "/" is anchored to the item root and matches only
// the whole relative path (e.g., "/root.txt" matches "root.txt" but not "a/b/root.txt").
// A pattern without a leading "/" and without "/" matches the last path element at any depth;
// a pattern without a leading "/" but with "/" matches the whole relative path. "**" is supported.
// Matching is case-sensitive. Trailing "/" is ignored.
// Backslashes in patterns are treated as separators.
type Matcher struct {
	patterns []string
}

// NewMatcher combines several pattern lists (for example the action's and an item's).
func NewMatcher(lists ...[]string) (*Matcher, error) {
	m := &Matcher{patterns: []string{TempFilePrefix + "*"}}
	for _, list := range lists {
		for _, raw := range list {
			p := strings.TrimSpace(raw)
			// Check if pattern is anchored (starts with "/")
			anchored := strings.HasPrefix(p, "/")
			// Replace backslashes and trim leading/trailing "/"
			p = strings.Trim(strings.ReplaceAll(p, `\`, "/"), "/")
			if p == "" {
				continue
			}
			// Validate the pattern before adding marker
			if !doublestar.ValidatePattern(p) {
				return nil, fmt.Errorf("invalid exclude pattern %q", raw)
			}
			// Mark anchored patterns with "^" prefix for later detection
			if anchored {
				p = "^" + p
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
		// Check if pattern is anchored (marked with "^")
		anchored := strings.HasPrefix(p, "^")
		if anchored {
			p = p[1:] // Remove the "^" marker
		}

		target := base
		if anchored || strings.Contains(p, "/") {
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
