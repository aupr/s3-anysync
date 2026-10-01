package storage_test

import (
	"context"
	"testing"

	"github.com/niceit/s3-anysync/internal/storage"
	"github.com/niceit/s3-anysync/internal/storage/s3test"
)

func TestS3Contract(t *testing.T) {
	s, prefix := s3test.New(t)
	runContract(t, s, prefix)
}

func TestS3Probe(t *testing.T) {
	s, prefix := s3test.New(t)
	res, err := storage.Probe(context.Background(), s, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if !res.List || !res.Put || !res.Get || !res.Delete {
		t.Fatalf("res = %+v", res)
	}
	t.Logf("conditional writes supported: %v", res.ConditionalWrites)
}
