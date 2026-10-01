package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// Object metadata written with every uploaded file.
const (
	metaSHA256  = "sha256"
	metaSize    = "size"
	metaMTime   = "mtime"
	metaMachine = "machine"
)

// loadManifest returns the cloud state and the manifest object's ETag ("" if
// it does not exist). A missing or unreadable manifest is rebuilt from the
// objects under current/.
func loadManifest(ctx context.Context, s storage.Storage, l Layout) (model.Manifest, string, error) {
	data, obj, err := s.Get(ctx, l.Manifest())
	if errors.Is(err, storage.ErrNotFound) {
		m, err := rebuildManifest(ctx, s, l)
		return m, "", err
	}
	if err != nil {
		return model.Manifest{}, "", fmt.Errorf("read manifest: %w", err)
	}
	var m model.Manifest
	if err := json.Unmarshal(data, &m); err == nil && m.Files != nil {
		return m, obj.ETag, nil
	}
	m, err = rebuildManifest(ctx, s, l)
	return m, obj.ETag, err
}

// rebuildManifest lists current/ and reads each object's metadata. Objects
// without our sha256 metadata were not written by s3-anysync and are skipped,
// so files placed under current/ by other tools are ignored by sync (they are
// neither downloaded nor deleted).
func rebuildManifest(ctx context.Context, s storage.Storage, l Layout) (model.Manifest, error) {
	m := model.Manifest{Files: map[string]model.FileEntry{}}
	prefix := l.CurrentPrefix()
	objs, err := s.List(ctx, prefix)
	if err != nil {
		return m, fmt.Errorf("list cloud files: %w", err)
	}
	for _, o := range objs {
		head, err := s.Head(ctx, o.Key)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return m, err
		}
		hash := head.Metadata[metaSHA256]
		if hash == "" {
			continue
		}
		size, _ := strconv.ParseInt(head.Metadata[metaSize], 10, 64)
		mtime, _ := strconv.ParseInt(head.Metadata[metaMTime], 10, 64)
		p := strings.TrimPrefix(o.Key, prefix)
		m.Files[p] = model.FileEntry{Path: p, Hash: hash, Size: size, MTime: mtime, ETag: head.ETag, Writer: head.Metadata[metaMachine]}
	}
	return m, nil
}

// manifestETag returns the manifest's current ETag, or "" if it does not exist.
func manifestETag(ctx context.Context, s storage.Storage, l Layout) (string, error) {
	obj, err := s.Head(ctx, l.Manifest())
	if errors.Is(err, storage.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return obj.ETag, nil
}

func saveManifest(ctx context.Context, s storage.Storage, l Layout, m model.Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.Put(ctx, l.Manifest(), data, nil)
	return err
}

func sortedEntries(files map[string]model.FileEntry) []model.FileEntry {
	out := make([]model.FileEntry, 0, len(files))
	for _, e := range files {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
