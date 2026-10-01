package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

const snapshotTSLayout = "20060102T150405Z"

// Snapshot reasons recorded in the index.
const (
	ReasonManual      = "manual"
	ReasonAutoPreSync = "auto-pre-sync"
	ReasonPreResolve  = "pre-resolve"
	ReasonPreRestore  = "pre-restore"
)

// newSnapshotTS returns the current UTC second, moved forward one second at a
// time while a snapshot with that name already exists.
func (r *Runner) newSnapshotTS(ctx context.Context) (string, error) {
	t := r.d.now().UTC()
	for i := 0; i < 120; i++ {
		ts := t.Add(time.Duration(i) * time.Second).Format(snapshotTSLayout)
		_, err := r.d.Storage.Head(ctx, r.layout.SnapshotIndex(ts))
		if errors.Is(err, storage.ErrNotFound) {
			return ts, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not pick a free snapshot timestamp")
}

// writeSnapshot copies the given current/ files server-side into a new
// snapshot and writes its index. The caller must hold the action lock.
func (r *Runner) writeSnapshot(ctx context.Context, reason string, files []model.FileEntry) (model.SnapshotInfo, error) {
	ts, err := r.newSnapshotTS(ctx)
	if err != nil {
		return model.SnapshotInfo{}, err
	}
	files = append([]model.FileEntry(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		if _, err := r.d.Storage.Copy(ctx, r.layout.Current(f.Path), r.layout.SnapshotFile(ts, f.Path)); err != nil {
			return model.SnapshotInfo{}, fmt.Errorf("snapshot %s: %w", f.Path, err)
		}
	}
	info := model.SnapshotInfo{TS: ts, Reason: reason, MachineID: r.d.MachineID, CreatedAt: r.d.now().UTC(), Files: files}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return model.SnapshotInfo{}, err
	}
	if _, err := r.d.Storage.Put(ctx, r.layout.SnapshotIndex(ts), data, nil); err != nil {
		return model.SnapshotInfo{}, err
	}
	if err := r.applyRetention(ctx); err != nil {
		r.d.log().Warn("snapshot retention", "action", r.action.ID, "err", err)
	}
	return info, nil
}

// ListSnapshots returns the action's snapshots, newest first.
func (r *Runner) ListSnapshots(ctx context.Context) ([]model.SnapshotInfo, error) {
	prefix := r.layout.SnapshotsPrefix()
	objs, err := r.d.Storage.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []model.SnapshotInfo
	for _, o := range objs {
		rest := strings.TrimPrefix(o.Key, prefix)
		if strings.Contains(rest, "/") || !strings.HasSuffix(rest, ".json") {
			continue
		}
		info, err := r.snapshotInfo(ctx, strings.TrimSuffix(rest, ".json"))
		if err != nil {
			r.d.log().Warn("skip unreadable snapshot index", "key", o.Key, "err", err)
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out, nil
}

func (r *Runner) snapshotInfo(ctx context.Context, ts string) (model.SnapshotInfo, error) {
	data, _, err := r.d.Storage.Get(ctx, r.layout.SnapshotIndex(ts))
	if errors.Is(err, storage.ErrNotFound) {
		return model.SnapshotInfo{}, fmt.Errorf("snapshot %s not found", ts)
	}
	if err != nil {
		return model.SnapshotInfo{}, err
	}
	var info model.SnapshotInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return model.SnapshotInfo{}, fmt.Errorf("snapshot %s: %w", ts, err)
	}
	return info, nil
}

// applyRetention deletes the oldest snapshots beyond the action's retention.
func (r *Runner) applyRetention(ctx context.Context) error {
	snaps, err := r.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	keep := r.action.SnapshotRetention
	if keep < 1 || len(snaps) <= keep {
		return nil
	}
	for _, s := range snaps[keep:] {
		if err := r.deleteSnapshot(ctx, s.TS); err != nil {
			return err
		}
	}
	return nil
}

// deleteSnapshot removes the index first, so a half-deleted snapshot is never listed.
func (r *Runner) deleteSnapshot(ctx context.Context, ts string) error {
	if err := r.d.Storage.Delete(ctx, r.layout.SnapshotIndex(ts)); err != nil {
		return err
	}
	objs, err := r.d.Storage.List(ctx, r.layout.SnapshotsPrefix()+ts+"/")
	if err != nil {
		return err
	}
	for _, o := range objs {
		if err := r.d.Storage.Delete(ctx, o.Key); err != nil {
			return err
		}
	}
	return nil
}
