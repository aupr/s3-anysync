package storage

import (
	"context"
	"errors"
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

// probeStore wraps a Storage to inject failures into Probe.
type probeStore struct {
	Storage
	failGet        bool
	failFirstDel   bool
	cancelAfterPut context.CancelFunc
	deletes        int
}

func (f *probeStore) Put(ctx context.Context, key string, body []byte, meta map[string]string) (Object, error) {
	o, err := f.Storage.Put(ctx, key, body, meta)
	if err == nil && f.cancelAfterPut != nil {
		f.cancelAfterPut()
	}
	return o, err
}

func (f *probeStore) Get(ctx context.Context, key string) ([]byte, Object, error) {
	if f.failGet {
		return nil, Object{}, errors.New("boom")
	}
	return f.Storage.Get(ctx, key)
}

func (f *probeStore) Delete(ctx context.Context, key string) error {
	f.deletes++
	if f.failFirstDel && f.deletes == 1 {
		return errors.New("boom")
	}
	if err := ctx.Err(); err != nil && f.cancelAfterPut != nil {
		return err
	}
	return f.Storage.Delete(ctx, key)
}

func TestProbeCleansUpAfterGetFailure(t *testing.T) {
	mem := NewMemory()
	_, err := Probe(context.Background(), &probeStore{Storage: mem, failGet: true}, "root")
	if err == nil {
		t.Fatal("want error")
	}
	if keys := mem.Keys(); len(keys) != 0 {
		t.Fatalf("leftovers: %v", keys)
	}
}

func TestProbeCleansUpAfterDeleteFailure(t *testing.T) {
	mem := NewMemory()
	res, err := Probe(context.Background(), &probeStore{Storage: mem, failFirstDel: true}, "root")
	if err == nil {
		t.Fatal("want error")
	}
	if res.Delete {
		t.Fatal("Delete must be false when the explicit delete failed")
	}
	if keys := mem.Keys(); len(keys) != 0 {
		t.Fatalf("leftovers: %v", keys)
	}
}

func TestProbeCleansUpAfterCancel(t *testing.T) {
	mem := NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := Probe(ctx, &probeStore{Storage: mem, cancelAfterPut: cancel}, "root")
	_ = err
	if keys := mem.Keys(); len(keys) != 0 {
		t.Fatalf("leftovers: %v", keys)
	}
}
