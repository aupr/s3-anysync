// Package storage is the port the engine uses to reach object storage,
// with an S3 adapter, an in-memory implementation and a remote lock.
package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound               = errors.New("storage: object not found")
	ErrPreconditionFailed     = errors.New("storage: precondition failed")
	ErrAccessDenied           = errors.New("storage: access denied")
	ErrConditionalUnsupported = errors.New("storage: conditional writes not supported")
)

// Object describes a stored object. Metadata keys are lowercase.
type Object struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	Metadata     map[string]string
}

// Storage is a flat key/value object store. Keys are full object keys.
type Storage interface {
	Put(ctx context.Context, key string, body []byte, meta map[string]string) (Object, error)
	// PutIfAbsent fails with ErrPreconditionFailed if the key exists.
	PutIfAbsent(ctx context.Context, key string, body []byte) (Object, error)
	// PutIfMatch fails with ErrPreconditionFailed if the key's ETag differs.
	PutIfMatch(ctx context.Context, key string, body []byte, etag string) (Object, error)
	Get(ctx context.Context, key string) ([]byte, Object, error)
	Head(ctx context.Context, key string) (Object, error)
	// List returns every object under prefix, sorted by key, without metadata.
	List(ctx context.Context, prefix string) ([]Object, error)
	// ListPrefixes returns the immediate sub-"directories" of prefix, each ending in "/".
	ListPrefixes(ctx context.Context, prefix string) ([]string, error)
	// Copy copies an object server-side, keeping its metadata.
	Copy(ctx context.Context, srcKey, dstKey string) (Object, error)
	// Delete removes a key; a missing key is not an error.
	Delete(ctx context.Context, key string) error
}

// JoinKey joins non-empty parts with "/", trimming slashes around each part.
func JoinKey(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.Trim(p, "/"); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}
