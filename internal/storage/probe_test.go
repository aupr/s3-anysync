package storage

import (
	"context"
	"testing"
)

func TestProbe(t *testing.T) {
	mem := NewMemory()
	res, err := Probe(context.Background(), mem, "root")
	if err != nil {
		t.Fatal(err)
	}
	if !res.List || !res.Put || !res.Get || !res.Delete || !res.ConditionalWrites {
		t.Fatalf("res = %+v", res)
	}
	if keys := mem.Keys(); len(keys) != 0 {
		t.Fatalf("probe left objects behind: %v", keys)
	}
}

func TestProbeWithoutConditionalWrites(t *testing.T) {
	mem := NewMemory()
	mem.NoConditional = true
	res, err := Probe(context.Background(), mem, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.ConditionalWrites || !res.Delete {
		t.Fatalf("res = %+v", res)
	}
}
