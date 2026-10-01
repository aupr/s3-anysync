package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

const snapshotTSLayout = "20060102T150405Z"

// snapshotCleanupTimeout bounds the best-effort cleanup of a failed snapshot.
const snapshotCleanupTimeout = 15 * time.Second

var snapshotTSRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

// errCorruptIndex marks a snapshot index that exists but cannot be parsed.
var errCorruptIndex = errors.New("corrupt snapshot index")

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
// snapshot and writes its index. ctx must be the context held under the
// action lock: the caller is responsible for holding the lock for the whole
// call, because retention and failure cleanup assume no concurrent snapshotter.
// If any step fails, everything already written for this snapshot is removed
// (best effort, even when ctx was cancelled) and the original error returned.
func (r *Runner) writeSnapshot(ctx context.Context, reason string, files []model.FileEntry) (model.SnapshotInfo, error) {
	return r.writeSnapshotProtecting(ctx, reason, files)
}

// writeSnapshotProtecting is writeSnapshot whose retention pass also never
// deletes the extraProtect snapshots (for example the source of a restore).
func (r *Runner) writeSnapshotProtecting(ctx context.Context, reason string, files []model.FileEntry, extraProtect ...string) (model.SnapshotInfo, error) {
	ts, err := r.newSnapshotTS(ctx)
	if err != nil {
		return model.SnapshotInfo{}, err
	}
	files = append([]model.FileEntry(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	fail := func(err error) (model.SnapshotInfo, error) {
		r.cleanupSnapshot(ctx, ts)
		return model.SnapshotInfo{}, err
	}
	for _, f := range files {
		if _, err := r.d.Storage.Copy(ctx, r.layout.Current(f.Path), r.layout.SnapshotFile(ts, f.Path)); err != nil {
			return fail(fmt.Errorf("snapshot %s: %w", f.Path, err))
		}
	}
	info := model.SnapshotInfo{TS: ts, Reason: reason, MachineID: r.d.MachineID, CreatedAt: r.d.now().UTC(), Files: files}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fail(err)
	}
	if _, err := r.d.Storage.Put(ctx, r.layout.SnapshotIndex(ts), data, nil); err != nil {
		return fail(err)
	}
	if err := r.applyRetentionKeeping(ctx, append([]string{ts}, extraProtect...)...); err != nil {
		r.d.log().Warn("snapshot retention", "action", r.action.ID, "err", err)
	}
	return info, nil
}

// cleanupSnapshot best-effort removes a partially written snapshot, ignoring
// cancellation of ctx.
func (r *Runner) cleanupSnapshot(ctx context.Context, ts string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), snapshotCleanupTimeout)
	defer cancel()
	if err := r.deleteSnapshot(cctx, ts); err != nil {
		r.d.log().Warn("cleanup failed snapshot", "action", r.action.ID, "ts", ts, "err", err)
	}
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
		if errors.Is(err, errCorruptIndex) || errors.Is(err, storage.ErrNotFound) {
			r.d.log().Warn("skip unreadable snapshot index", "key", o.Key, "err", err)
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out, nil
}

func (r *Runner) snapshotInfo(ctx context.Context, ts string) (model.SnapshotInfo, error) {
	data, _, err := r.d.Storage.Get(ctx, r.layout.SnapshotIndex(ts))
	if errors.Is(err, storage.ErrNotFound) {
		return model.SnapshotInfo{}, fmt.Errorf("snapshot %s not found: %w", ts, storage.ErrNotFound)
	}
	if err != nil {
		return model.SnapshotInfo{}, err
	}
	var info model.SnapshotInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return model.SnapshotInfo{}, fmt.Errorf("snapshot %s: %w: %w", ts, errCorruptIndex, err)
	}
	return info, nil
}

// applyRetention deletes the oldest snapshots beyond the action's retention.
// It works from object keys only, so unreadable indexes neither block pruning
// nor shrink the keep window. Snapshots with an index count toward the
// retention (newest first by timestamp); timestamps that have objects but no
// index are leftovers of crashed snapshots and are always deleted (safe: the
// caller holds the action lock and its own index is already written).
// SnapshotRetention < 1 keeps everything.
func (r *Runner) applyRetention(ctx context.Context) error {
	return r.applyRetentionKeeping(ctx)
}

// applyRetentionKeeping is applyRetention that never deletes the protected
// snapshots, even if this machine's clock is behind the machines that wrote
// the other snapshots. Protected snapshots count toward keep; the oldest
// non-protected ones are deleted until the total fits.
// A failing delete does not stop the others; all errors are joined.
func (r *Runner) applyRetentionKeeping(ctx context.Context, protect ...string) error {
	keep := r.action.SnapshotRetention
	if keep < 1 {
		return nil
	}
	prefix := r.layout.SnapshotsPrefix()
	objs, err := r.d.Storage.List(ctx, prefix)
	if err != nil {
		return err
	}
	indexed := map[string]bool{}
	hasObjects := map[string]bool{}
	for _, o := range objs {
		rest := strings.TrimPrefix(o.Key, prefix)
		if i := strings.Index(rest, "/"); i >= 0 {
			if ts := rest[:i]; snapshotTSRe.MatchString(ts) {
				hasObjects[ts] = true
			}
		} else if ts, ok := strings.CutSuffix(rest, ".json"); ok && snapshotTSRe.MatchString(ts) {
			indexed[ts] = true
		}
	}
	prot := map[string]bool{}
	for _, ts := range protect {
		if ts != "" {
			prot[ts] = true
		}
	}
	var kept []string
	for ts := range indexed {
		kept = append(kept, ts)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(kept)))
	var doomed []string
	if len(kept) > keep {
		slots := keep
		for _, ts := range kept {
			if prot[ts] {
				slots--
			}
		}
		for _, ts := range kept {
			if prot[ts] {
				continue
			}
			if slots > 0 {
				slots--
				continue
			}
			doomed = append(doomed, ts)
		}
	}
	for ts := range hasObjects {
		if !indexed[ts] && !prot[ts] {
			doomed = append(doomed, ts)
		}
	}
	sort.Strings(doomed)
	var errs []error
	for _, ts := range doomed {
		if err := r.deleteSnapshot(ctx, ts); err != nil {
			errs = append(errs, fmt.Errorf("delete snapshot %s: %w", ts, err))
		}
	}
	return errors.Join(errs...)
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

// Snapshot takes a manual snapshot of everything in current/. Unless this
// machine is cloud-to-local, it syncs first so local changes are included.
func (r *Runner) Snapshot(ctx context.Context) (info model.SnapshotInfo, err error) {
	started := r.d.now()
	defer func() {
		outcome, msg := model.OutcomeOK, fmt.Sprintf("snapshot %s with %d file(s)", info.TS, len(info.Files))
		if err != nil {
			outcome, msg = model.OutcomeError, err.Error()
		}
		r.addHistory(model.HistorySnapshot, started, outcome, msg)
	}()
	if r.action.DirectionFor(r.d.MachineID) != model.CloudToLocal {
		res, err := r.Sync(ctx)
		if err != nil {
			return info, err
		}
		if res.Outcome == model.OutcomeConflict {
			return info, ErrConflictPending
		}
	} else if err := r.refreshAction(ctx); err != nil {
		return info, err
	}
	held, release, err := r.d.holdLock(ctx, r.layout)
	if err != nil {
		return info, err
	}
	defer release()
	m, _, err := loadManifest(held, r.d.Storage, r.layout)
	if err != nil {
		return info, err
	}
	return r.writeSnapshot(held, ReasonManual, sortedEntries(m.Files))
}
