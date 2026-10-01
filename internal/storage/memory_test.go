package storage

import (
	"context"
	"testing"
)

func TestMemoryNoConditionalIgnoresPreconditions(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	m.NoConditional = true
	if _, err := m.PutIfAbsent(ctx, "k", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutIfAbsent(ctx, "k", []byte("2")); err != nil {
		t.Fatalf("NoConditional should overwrite silently, got %v", err)
	}
	if _, err := m.PutIfMatch(ctx, "k", []byte("3"), "wrong"); err != nil {
		t.Fatalf("NoConditional should ignore If-Match, got %v", err)
	}
}

func TestJoinKey(t *testing.T) {
	cases := map[string][]string{
		"a/b/c": {"a", "/b/", "c"},
		"b":     {"", "b"},
		"":      {"", "/"},
	}
	for want, parts := range cases {
		if got := JoinKey(parts...); got != want {
			t.Errorf("JoinKey(%q) = %q, want %q", parts, got, want)
		}
	}
}
