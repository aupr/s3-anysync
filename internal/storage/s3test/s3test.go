// Package s3test connects tests to a real S3-compatible server configured by
// S3_TEST_ENDPOINT, S3_TEST_KEY, S3_TEST_SECRET, S3_TEST_BUCKET and
// optionally S3_TEST_REGION.
package s3test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/aupr/s3-anysync/internal/storage"
)

// New returns a store and a unique key prefix, or skips the test when no
// server is configured. Everything under the prefix is deleted on cleanup.
func New(t testing.TB) (*storage.S3, string) {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set S3_TEST_ENDPOINT, S3_TEST_KEY, S3_TEST_SECRET and S3_TEST_BUCKET to run S3 integration tests")
	}
	region := os.Getenv("S3_TEST_REGION")
	if region == "" {
		region = "us-east-1"
	}
	s, err := storage.NewS3(storage.S3Config{
		Endpoint: endpoint, Region: region, Bucket: os.Getenv("S3_TEST_BUCKET"),
		AccessKeyID: os.Getenv("S3_TEST_KEY"), SecretAccessKey: os.Getenv("S3_TEST_SECRET"), PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	rand.Read(b)
	prefix := "s3-anysync-it-" + hex.EncodeToString(b)
	t.Cleanup(func() {
		ctx := context.Background()
		objs, _ := s.List(ctx, prefix+"/")
		for _, o := range objs {
			_ = s.Delete(ctx, o.Key)
		}
	})
	return s, prefix
}
