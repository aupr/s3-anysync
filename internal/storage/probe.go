package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// ProbeResult reports which operations the credentials and provider support.
type ProbeResult struct {
	List              bool `json:"list"`
	Put               bool `json:"put"`
	Get               bool `json:"get"`
	Delete            bool `json:"delete"`
	ConditionalWrites bool `json:"conditionalWrites"`
}

// Probe exercises list, put, get, delete and conditional writes under
// prefix, and removes everything it wrote.
func Probe(ctx context.Context, s Storage, prefix string) (ProbeResult, error) {
	var res ProbeResult
	token := make([]byte, 8)
	rand.Read(token)
	base := JoinKey(prefix, ".s3-anysync-probe-"+hex.EncodeToString(token))
	dataKey, condKey := base+"-data", base+"-cond"

	listPrefix := JoinKey(prefix)
	if listPrefix != "" {
		listPrefix += "/"
	}
	if _, err := s.ListPrefixes(ctx, listPrefix); err != nil {
		return res, fmt.Errorf("list: %w", err)
	}
	res.List = true

	payload := []byte("s3-anysync probe")
	if _, err := s.Put(ctx, dataKey, payload, nil); err != nil {
		return res, fmt.Errorf("put: %w", err)
	}
	res.Put = true

	got, _, err := s.Get(ctx, dataKey)
	if err != nil {
		return res, fmt.Errorf("get: %w", err)
	}
	if !bytes.Equal(got, payload) {
		return res, errors.New("get: content differs from what was written")
	}
	res.Get = true

	res.ConditionalWrites = probeConditional(ctx, s, condKey)

	for _, k := range []string{dataKey, condKey} {
		if err := s.Delete(ctx, k); err != nil {
			return res, fmt.Errorf("delete: %w", err)
		}
	}
	res.Delete = true
	return res, nil
}

// probeConditional is true only when a second If-None-Match:* write is rejected.
func probeConditional(ctx context.Context, s Storage, key string) bool {
	if _, err := s.PutIfAbsent(ctx, key, []byte("1")); err != nil {
		return false
	}
	_, err := s.PutIfAbsent(ctx, key, []byte("2"))
	return errors.Is(err, ErrPreconditionFailed)
}
