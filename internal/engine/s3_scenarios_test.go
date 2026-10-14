package engine

import (
	"os"
	"testing"

	"github.com/aupr/s3-anysync/internal/storage"
	"github.com/aupr/s3-anysync/internal/storage/s3test"
)

func TestScenariosS3(t *testing.T) {
	if os.Getenv("S3_TEST_ENDPOINT") == "" {
		t.Skip("S3_TEST_ENDPOINT not set")
	}
	runScenarios(t, func(t *testing.T) (storage.Storage, string) {
		s, prefix := s3test.New(t)
		return s, prefix
	})
}
