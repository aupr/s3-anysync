package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niceit/s3-anysync/internal/crypto"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// Sync runs one sync of the action. A conflict is not an error: the result's
// Outcome is OutcomeConflict, nothing was changed, and the conflicts are saved.
func (r *Runner) Sync(ctx context.Context) (model.SyncResult, error) {
	return r.run(ctx, model.HistorySync, nil)
}

// Resolve settles conflicts (path → winning side) and runs the rest of the
// sync. The losing side is snapshotted or backed up first.
func (r *Runner) Resolve(ctx context.Context, choices map[string]model.Side) (model.SyncResult, error) {
	for p, side := range choices {
		if side != model.SideLocal && side != model.SideCloud {
			return model.SyncResult{}, fmt.Errorf("%s: unknown side %q", p, side)
		}
	}
	saved, err := r.d.Repo.LoadConflicts(r.action.ID)
	if err != nil {
		return model.SyncResult{}, fmt.Errorf("load conflicts: %w", err)
	}
	if len(saved) == 0 {
		return model.SyncResult{}, errors.New("no unresolved conflicts")
	}
	isSaved := make(map[string]bool, len(saved))
	for _, c := range saved {
		isSaved[c.Path] = true
	}
	var unknown, missing []string
	for p := range choices {
		if !isSaved[p] {
			unknown = append(unknown, p)
		}
	}
	for _, c := range saved {
		if _, ok := choices[c.Path]; !ok {
			missing = append(missing, c.Path)
		}
	}
	sort.Strings(unknown)
	sort.Strings(missing)
	if len(unknown) > 0 {
		return model.SyncResult{}, fmt.Errorf("not a conflict of this action: %s", strings.Join(unknown, ", "))
	}
	if len(missing) > 0 {
		return model.SyncResult{}, fmt.Errorf("choose a side for every conflict; missing: %s", strings.Join(missing, ", "))
	}
	return r.run(ctx, model.HistoryResolve, choices)
}

// Conflicts returns the conflicts saved by the last run.
func (r *Runner) Conflicts() ([]model.Conflict, error) {
	return r.d.Repo.LoadConflicts(r.action.ID)
}

func (r *Runner) run(ctx context.Context, kind string, overrides map[string]model.Side) (res model.SyncResult, err error) {
	started := r.d.now()
	defer func() {
		outcome, msg := res.Outcome, res.Summary()
		if err != nil {
			outcome, msg = model.OutcomeError, err.Error()
		}
		r.addHistory(kind, started, outcome, msg)
	}()
	if err = r.refreshAction(ctx); err != nil {
		return res, err
	}
	ms, err := BuildMappings(r.action, r.d.MachineID, r.d.expand)
	if err != nil {
		return res, err
	}
	for attempt := 0; attempt < maxSyncAttempts; attempt++ {
		var retry bool
		res, retry, err = r.syncOnce(ctx, ms, overrides)
		if !retry {
			return res, err
		}
		r.d.log().Info("cloud changed during sync, planning again", "action", r.action.ID, "attempt", attempt+1)
	}
	return res, errors.New("the cloud copy kept changing during sync; try again later")
}

// syncOnce scans, plans and applies. retry=true means another machine
// changed the manifest between planning and locking.
func (r *Runner) syncOnce(ctx context.Context, ms Mappings, overrides map[string]model.Side) (model.SyncResult, bool, error) {
	id := r.action.ID
	baseline, err := r.d.Repo.LoadBaseline(id)
	if err != nil {
		return model.SyncResult{}, false, fmt.Errorf("load baseline: %w", err)
	}
	fullBaseline := baseline
	baseline = filterOwned(fullBaseline, ms)
	local, err := scanLocal(ms, baseline)
	if err != nil {
		return model.SyncResult{}, false, fmt.Errorf("scan local files: %w", err)
	}
	local = filterOwned(local, ms)
	manifest, etag, err := loadManifest(ctx, r.d.Storage, r.layout)
	if err != nil {
		return model.SyncResult{}, false, err
	}
	remote := filterOwned(manifest.Files, ms)

	ops := Plan(PlanInput{
		Direction: r.action.DirectionFor(r.d.MachineID), PropagateDeletes: r.action.PropagateDeletes,
		ReceiverRestoresCloud: r.action.ReceiverRestoresCloud,
		Local:                 local, Remote: remote, Baseline: baseline,
	})
	if !hasConflict(ops) {
		// Nothing is in conflict any more (e.g. settled by hand): forget the
		// saved ones now, even if the rest of this run fails.
		if err := r.d.Repo.SaveConflicts(id, nil); err != nil {
			return model.SyncResult{}, false, fmt.Errorf("clear conflicts: %w", err)
		}
	}
	ops, conflicts := applyOverrides(ops, overrides, r.d.now().UTC())
	if len(conflicts) > 0 {
		if err := r.d.Repo.SaveConflicts(id, conflicts); err != nil {
			return model.SyncResult{}, false, fmt.Errorf("save conflicts: %w", err)
		}
		return model.SyncResult{Outcome: model.OutcomeConflict, Conflicts: conflicts}, false, nil
	}

	opCtx := ctx
	if hasWrites(ops) {
		held, release, err := r.d.holdLock(ctx, r.layout)
		if err != nil {
			return model.SyncResult{}, false, err
		}
		defer release()
		opCtx = held
		current, err := manifestETag(opCtx, r.d.Storage, r.layout)
		if err != nil {
			return model.SyncResult{}, false, err
		}
		if current != etag {
			return model.SyncResult{}, true, nil
		}
		reason := ReasonAutoPreSync
		if overrides != nil {
			reason = ReasonPreResolve
		}
		if err := r.guard(opCtx, ms, ops, reason); err != nil {
			return model.SyncResult{}, false, err
		}
	}

	res, err := r.apply(opCtx, ms, ops, baseline, fullBaseline, &manifest)
	if err != nil {
		return res, false, err
	}
	if err := r.d.Repo.SaveConflicts(id, nil); err != nil {
		return res, false, fmt.Errorf("clear conflicts: %w", err)
	}
	res.Outcome = model.OutcomeOK
	return res, false, nil
}

func hasConflict(ops []Op) bool {
	for _, op := range ops {
		if op.Kind == OpConflict {
			return true
		}
	}
	return false
}

// applyOverrides turns chosen conflicts into uploads/downloads/deletes. If
// any conflict is left without a choice, nothing is applied: it returns no
// ops and ALL current conflicts (chosen or not), so none is silently dropped
// and the whole action stays paused.
func applyOverrides(ops []Op, overrides map[string]model.Side, now time.Time) ([]Op, []model.Conflict) {
	var all []model.Conflict
	unchosen := false
	for _, op := range ops {
		if op.Kind != OpConflict {
			continue
		}
		all = append(all, model.Conflict{Path: op.Path, Local: op.Local, Remote: op.Remote, DetectedAt: now})
		if s := overrides[op.Path]; s != model.SideLocal && s != model.SideCloud {
			unchosen = true
		}
	}
	if unchosen {
		return nil, all
	}
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		if op.Kind == OpConflict {
			switch overrides[op.Path] {
			case model.SideLocal:
				op.Kind = OpUpload
				if op.Local == nil {
					op.Kind = OpDeleteRemote
				}
			case model.SideCloud:
				op.Kind = OpDownload
				if op.Remote == nil {
					op.Kind = OpDeleteLocal
				}
			}
		}
		out = append(out, op)
	}
	return out, nil
}

func hasWrites(ops []Op) bool {
	for _, op := range ops {
		switch op.Kind {
		case OpUpload, OpDownload, OpDeleteRemote, OpDeleteLocal:
			return true
		}
	}
	return false
}

// guard snapshots cloud files about to be overwritten or deleted, and backs
// up local files about to be overwritten or deleted.
func (r *Runner) guard(ctx context.Context, ms Mappings, ops []Op, reason string) error {
	var remoteVictims []model.FileEntry
	localVictims := map[string]string{}
	for _, op := range ops {
		switch op.Kind {
		case OpUpload, OpDeleteRemote:
			if op.Remote != nil {
				remoteVictims = append(remoteVictims, *op.Remote)
			}
		case OpDownload, OpDeleteLocal:
			if op.Local != nil {
				if abs, ok := ms.LocalPathFor(op.Path); ok {
					localVictims[op.Path] = abs
				}
			}
		}
	}
	if len(remoteVictims) > 0 {
		if _, err := r.writeSnapshot(ctx, reason, remoteVictims); err != nil {
			return fmt.Errorf("safety snapshot: %w", err)
		}
	}
	if len(localVictims) > 0 {
		ts := r.d.now().UTC().Format(snapshotTSLayout)
		if err := backupLocal(r.d.BackupRoot, r.action.ID, ts, localVictims); err != nil {
			return fmt.Errorf("local backup: %w", err)
		}
	}
	return nil
}

// apply executes ops in order, then writes the manifest (if the cloud
// changed) and the new baseline. On a failed op it stops, but still records
// what was done. baseline is the owned subset used for planning; fullBaseline
// also holds entries for paths this machine does not currently map, which are
// carried over untouched.
func (r *Runner) apply(ctx context.Context, ms Mappings, ops []Op, baseline, fullBaseline map[string]model.FileEntry, manifest *model.Manifest) (model.SyncResult, error) {
	var res model.SyncResult
	next := make(map[string]model.FileEntry, len(fullBaseline))
	for p, e := range fullBaseline {
		next[p] = e
	}
	remoteDirty := false
	var opErr error
loop:
	for _, op := range ops {
		if ctx.Err() != nil {
			opErr = fmt.Errorf("sync interrupted: %w", context.Cause(ctx))
			break
		}
		switch op.Kind {
		case OpKeep:
			next[op.Path] = *op.Local
		case OpAdopt:
			next[op.Path] = *op.Local
			res.Adopted++
		case OpDrop:
			delete(next, op.Path)
		case OpUpload:
			e, err := r.upload(ctx, ms, *op.Local)
			if err != nil {
				opErr = err
				break loop
			}
			manifest.Files[op.Path] = e
			next[op.Path] = e
			remoteDirty = true
			res.Uploaded++
		case OpDownload:
			e, err := r.download(ctx, ms, *op.Remote, op.Local)
			if errors.Is(err, errLocalChanged) {
				res.Deferred = append(res.Deferred, op.Path)
				continue
			}
			if err != nil {
				opErr = err
				break loop
			}
			next[op.Path] = e
			res.Downloaded++
		case OpDeleteRemote:
			if err := r.d.Storage.Delete(ctx, r.layout.Current(op.Path)); err != nil {
				opErr = fmt.Errorf("delete %s in the cloud: %w", op.Path, err)
				break loop
			}
			delete(manifest.Files, op.Path)
			delete(next, op.Path)
			remoteDirty = true
			res.DeletedRemote++
		case OpDeleteLocal:
			if abs, ok := ms.LocalPathFor(op.Path); ok {
				if checkLocal(abs, op.Local) == localChanged {
					res.Deferred = append(res.Deferred, op.Path)
					continue
				}
				if err := removeLocal(abs); err != nil {
					opErr = fmt.Errorf("delete %s: %w", abs, err)
					break loop
				}
			}
			delete(next, op.Path)
			res.DeletedLocal++
		}
	}
	if opErr != nil && errors.Is(context.Cause(ctx), storage.ErrLockLost) && !errors.Is(opErr, storage.ErrLockLost) {
		opErr = errors.Join(opErr, storage.ErrLockLost)
	}
	if remoteDirty {
		if errors.Is(context.Cause(ctx), storage.ErrLockLost) {
			// Another machine may now be writing the manifest. Keep the old
			// baseline too; the next run re-plans from what is really there.
			return res, errors.Join(opErr, storage.ErrLockLost)
		}
		manifest.UpdatedAt = r.d.now().UTC()
		manifest.UpdatedBy = r.d.MachineID
		if err := saveManifest(context.WithoutCancel(ctx), r.d.Storage, r.layout, *manifest); err != nil {
			// Without the new manifest the new baseline would misread the
			// cloud next time, so keep the old baseline.
			return res, errors.Join(opErr, fmt.Errorf("write manifest: %w", err))
		}
	}
	if err := r.d.Repo.SaveBaseline(r.action.ID, next); err != nil {
		return res, errors.Join(opErr, fmt.Errorf("save baseline: %w", err))
	}
	return res, opErr
}

func (r *Runner) upload(ctx context.Context, ms Mappings, e model.FileEntry) (model.FileEntry, error) {
	abs, ok := ms.LocalPathFor(e.Path)
	if !ok {
		return e, fmt.Errorf("upload %s: no local path on this machine", e.Path)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return e, fmt.Errorf("upload %s: %w", e.Path, err)
	}
	// The file may have changed since the scan; upload what is on disk now.
	e.Hash, e.Size = hashBytes(data), int64(len(data))
	body := data
	if r.action.Encrypt {
		if body, err = crypto.Encrypt(data, r.d.Passphrase); err != nil {
			return e, fmt.Errorf("encrypt %s: %w", e.Path, err)
		}
	}
	meta := map[string]string{
		metaSHA256:  e.Hash,
		metaSize:    strconv.FormatInt(e.Size, 10),
		metaMTime:   strconv.FormatInt(e.MTime, 10),
		metaMachine: r.d.MachineID,
	}
	obj, err := r.d.Storage.Put(ctx, r.layout.Current(e.Path), body, meta)
	if err != nil {
		return e, fmt.Errorf("upload %s: %w", e.Path, err)
	}
	e.ETag, e.Writer = obj.ETag, r.d.MachineID
	return e, nil
}

// errLocalChanged: the local destination no longer matches what the plan saw.
var errLocalChanged = errors.New("local file changed during sync")

// download fetches e from the cloud and writes it locally. seen is the local
// entry the plan was based on (nil: absent). Right before writing, the
// destination is re-examined; if it changed meanwhile nothing is written and
// errLocalChanged is returned.
func (r *Runner) download(ctx context.Context, ms Mappings, e model.FileEntry, seen *model.FileEntry) (model.FileEntry, error) {
	abs, ok := ms.LocalPathFor(e.Path)
	if !ok {
		return e, fmt.Errorf("download %s: no local path on this machine", e.Path)
	}
	body, _, err := r.d.Storage.Get(ctx, r.layout.Current(e.Path))
	if err != nil {
		return e, fmt.Errorf("download %s: %w", e.Path, err)
	}
	data, err := r.decode(e.Path, body, e.Hash)
	if err != nil {
		return e, err
	}
	if checkLocal(abs, seen) != localSame {
		return e, errLocalChanged
	}
	info, err := writeFileAtomic(abs, data, time.Unix(0, e.MTime))
	if err != nil {
		return e, fmt.Errorf("write %s: %w", abs, err)
	}
	return model.FileEntry{Path: e.Path, Hash: e.Hash, Size: info.Size(), MTime: info.ModTime().UnixNano()}, nil
}

// decode decrypts (for encrypted actions) and verifies the plaintext hash.
func (r *Runner) decode(p string, body []byte, wantHash string) ([]byte, error) {
	data := body
	if r.action.Encrypt {
		var err error
		if data, err = crypto.Decrypt(body, r.d.Passphrase); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrDecrypt, p, err)
		}
	}
	if got := hashBytes(data); got != wantHash {
		return nil, fmt.Errorf("%s: cloud content does not match its recorded hash", p)
	}
	return data, nil
}
