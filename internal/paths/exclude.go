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

// pattern represents a compiled exclude pattern.
type pattern struct {
	glob      string
	wholePath bool
}

// Matcher matches slash-separated relative paths against exclude globs.
// A pattern with a leading "/" is anchored to the item root and matches only
// the whole relative path (e.g., "/root.txt" matches "root.txt" but not "a/b/root.txt").
// A pattern without a leading "/" and without "/" matches the last path element at any depth;
// a pattern without a leading "/" but with "/" matches the whole relative path. "**" is supported.
// Matching is case-sensitive. Trailing "/" is ignored.
// Backslashes in patterns are treated as separators.
type Matcher struct {
	patterns []pattern
}

// NewMatcher combines several pattern lists (for example the action's and an item's).
func NewMatcher(lists ...[]string) (*Matcher, error) {
	m := &Matcher{patterns: []pattern{{glob: TempFilePrefix + "*", wholePath: false}}}
	for _, list := range lists {
		for _, raw := range list {
			p := strings.TrimSpace(raw)
			// Normalise backslashes first so `\root.txt` is anchored like "/root.txt".
			p = strings.ReplaceAll(p, `\`, "/")
			anchored := strings.HasPrefix(p, "/")
			p = strings.Trim(p, "/")
			if p == "" {
				continue
			}
			// Validate the pattern
			if !doublestar.ValidatePattern(p) {
				return nil, fmt.Errorf("invalid exclude pattern %q", raw)
			}
			// wholePath is true if anchored OR if the trimmed pattern contains "/"
			wholePath := anchored || strings.Contains(p, "/")
			m.patterns = append(m.patterns, pattern{glob: p, wholePath: wholePath})
		}
	}
	return m, nil
}

// Match reports whether rel itself is excluded.
func (m *Matcher) Match(rel string) bool {
	rel = strings.ReplaceAll(rel, `\`, "/")
	base := path.Base(rel)
	for _, pat := range m.patterns {
		target := base
		if pat.wholePath {
			target = rel
		}
		if ok, _ := doublestar.Match(pat.glob, target); ok {
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
