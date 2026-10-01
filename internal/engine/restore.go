package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// RestoreTarget says which side a restore writes to.
type RestoreTarget string

const (
	RestoreLocal RestoreTarget = "local"
	RestoreCloud RestoreTarget = "cloud"
	RestoreBoth  RestoreTarget = "both"
)

// RestoreResult lists restored paths and paths skipped because they have no
// local path on this machine.
type RestoreResult struct {
	Restored        []string `json:"restored"`
	SkippedUnmapped []string `json:"skippedUnmapped"`
}

// Restore copies files from snapshot ts back to the cloud, the local machine
// or both. only limits the files (nil means all). The current versions are
// snapshotted (cloud) or backed up (local) first. Restore never deletes files.
func (r *Runner) Restore(ctx context.Context, ts string, only []string, target RestoreTarget) (res RestoreResult, err error) {
	started := r.d.now()
	defer func() {
		outcome, msg := model.OutcomeOK, fmt.Sprintf("restored %d file(s) from %s to %s", len(res.Restored), ts, target)
		if err != nil {
			outcome, msg = model.OutcomeError, err.Error()
		}
		r.addHistory(model.HistoryRestore, started, outcome, msg)
	}()
	if target != RestoreLocal && target != RestoreCloud && target != RestoreBoth {
		return res, fmt.Errorf("unknown restore target %q", target)
	}
	if err = r.refreshAction(ctx); err != nil {
		return res, err
	}
	info, err := r.snapshotInfo(ctx, ts)
	if err != nil {
		return res, err
	}
	files, err := selectFiles(info.Files, only)
	if err != nil {
		return res, err
	}
	ms, err := BuildMappings(r.action, r.d.MachineID, r.d.expand)
	if err != nil {
		return res, err
	}

	held, release, err := r.d.holdLock(ctx, r.layout)
	if err != nil {
		return res, err
	}
	defer release()
	ctx = held

	restored := map[string]bool{}
	if target != RestoreLocal {
		if err = r.restoreCloud(ctx, ts, files); err != nil {
			return res, err
		}
		for _, f := range files {
			restored[f.Path] = true
		}
	}
	if target != RestoreCloud {
		done, skipped, err := r.restoreLocal(ctx, ts, files, ms)
		res.SkippedUnmapped = skipped
		if err != nil {
			return res, err
		}
		for _, p := range done {
			restored[p] = true
		}
	}
	for _, f := range files {
		if restored[f.Path] {
			res.Restored = append(res.Restored, f.Path)
		}
	}
	return res, nil
}

func selectFiles(all []model.FileEntry, only []string) ([]model.FileEntry, error) {
	if len(only) == 0 {
		return all, nil
	}
	byPath := map[string]model.FileEntry{}
	for _, f := range all {
		byPath[f.Path] = f
	}
	var out []model.FileEntry
	for _, p := range only {
		f, ok := byPath[strings.TrimSpace(p)]
		if !ok {
			return nil, fmt.Errorf("the snapshot has no file %q", p)
		}
		out = append(out, f)
	}
	return out, nil
}

func (r *Runner) restoreCloud(ctx context.Context, ts string, files []model.FileEntry) error {
	m, _, err := loadManifest(ctx, r.d.Storage, r.layout)
	if err != nil {
		return err
	}
	var victims []model.FileEntry
	for _, f := range files {
		if cur, ok := m.Files[f.Path]; ok {
			victims = append(victims, cur)
		}
	}
	if len(victims) > 0 {
		if _, err := r.writeSnapshot(ctx, ReasonPreRestore, victims); err != nil {
			return fmt.Errorf("safety snapshot: %w", err)
		}
	}
	for _, f := range files {
		obj, err := r.d.Storage.Copy(ctx, r.layout.SnapshotFile(ts, f.Path), r.layout.Current(f.Path))
		if err != nil {
			return fmt.Errorf("restore %s to the cloud: %w", f.Path, err)
		}
		e := f
		e.ETag, e.Writer = obj.ETag, r.d.MachineID
		m.Files[f.Path] = e
	}
	if errors.Is(context.Cause(ctx), storage.ErrLockLost) {
		return fmt.Errorf("manifest not updated: %w", storage.ErrLockLost)
	}
	m.UpdatedAt, m.UpdatedBy = r.d.now().UTC(), r.d.MachineID
	return saveManifest(context.WithoutCancel(ctx), r.d.Storage, r.layout, m)
}

func (r *Runner) restoreLocal(ctx context.Context, ts string, files []model.FileEntry, ms Mappings) (done, skipped []string, err error) {
	targets := map[string]string{}
	victims := map[string]string{}
	for _, f := range files {
		abs, ok := ms.LocalPathFor(f.Path)
		if !ok || !ms.Owns(f.Path) {
			skipped = append(skipped, f.Path)
			continue
		}
		targets[f.Path] = abs
		if _, err := os.Stat(abs); err == nil {
			victims[f.Path] = abs
		}
	}
	if len(victims) > 0 {
		if err := backupLocal(r.d.BackupRoot, r.action.ID, r.d.now().UTC().Format(snapshotTSLayout), victims); err != nil {
			return nil, skipped, fmt.Errorf("local backup: %w", err)
		}
	}
	for _, f := range files {
		abs, ok := targets[f.Path]
		if !ok {
			continue
		}
		body, _, err := r.d.Storage.Get(ctx, r.layout.SnapshotFile(ts, f.Path))
		if err != nil {
			return done, skipped, fmt.Errorf("read snapshot copy of %s: %w", f.Path, err)
		}
		data, err := r.decode(f.Path, body, f.Hash)
		if err != nil {
			return done, skipped, err
		}
		if _, err := writeFileAtomic(abs, data, time.Unix(0, f.MTime)); err != nil {
			return done, skipped, fmt.Errorf("write %s: %w", abs, err)
		}
		done = append(done, f.Path)
	}
	return done, skipped, nil
}
