package paths

import "testing"

func TestMatcher(t *testing.T) {
	m, err := NewMatcher([]string{"*.log", "cache", "/root.txt", "build/"}, []string{"sub/*.tmp", "**/gen/**"})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"a.log":                 true,
		"deep/x/b.log":          true,
		"a.txt":                 false,
		"cache":                 true,
		"x/cache":               true,
		"root.txt":              true,
		"build":                 true,
		"sub/x.tmp":             true,
		"other/sub/x.tmp":       false,
		"a/gen/b":               true,
		".s3-anysync-tmp-123":   true,
		"dir/.s3-anysync-tmp-9": true,
	}
	for rel, want := range cases {
		if got := m.Match(rel); got != want {
			t.Errorf("Match(%q) = %v, want %v", rel, got, want)
		}
	}
	if !m.MatchPath("cache/x/y.json") {
		t.Error("MatchPath should match a file under an excluded directory")
	}
	if m.MatchPath("keep/x.json") {
		t.Error("MatchPath matched a kept file")
	}
}

func TestMatcherInvalidPattern(t *testing.T) {
	if _, err := NewMatcher([]string{"["}); err == nil {
		t.Fatal("want error for invalid pattern")
	}
}
