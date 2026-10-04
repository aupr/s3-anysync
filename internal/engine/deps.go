// Package engine runs one action's sync: scan, plan, execute and commit,
// plus snapshots, conflict resolution and restore.
package engine

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
	"github.com/niceit/s3-anysync/internal/storage"
)

var (
	// ErrPassphraseMissing: the action is encrypted and no passphrase is set here.
	ErrPassphraseMissing = errors.New("this action is encrypted and this machine has no passphrase for it")
	// ErrDecrypt: wrong passphrase or damaged data. Never retried automatically.
	ErrDecrypt = errors.New("cannot decrypt: wrong passphrase or damaged data")
	// ErrKeyCheckMissing: an encrypted action has files but no .meta/keycheck.
	ErrKeyCheckMissing = errors.New("keycheck missing but the action already has files: cannot verify the passphrase; repair by restoring .meta/keycheck")
	// ErrConflictPending: the action is paused until its conflicts are resolved.
	ErrConflictPending = errors.New("the action has unresolved conflicts")
	// ErrInvalidInput: the caller's request is wrong (not a sync failure):
	// an unknown side, nothing to resolve or approve, a missing choice.
	ErrInvalidInput = errors.New("invalid input")
	// ErrRootMissing: an item's local folder (or a file item's parent folder)
	// is missing although content under it was synced before; it is refused
	// rather than treated as a mass deletion.
	ErrRootMissing = errors.New("item folder is missing but its content was synced before")
)

const (
	// LockTTL is how long a remote lock is valid without renewal.
	LockTTL = 2 * time.Minute
	// LocalBackupKeep is how many local backup folders are kept per action.
	LocalBackupKeep = 20
	maxSyncAttempts = 3
)

// Repo is the local state the engine needs. *state.DB implements it.
type Repo interface {
	PutAction(a model.Action) error
	LoadBaseline(actionID string) (map[string]model.FileEntry, error)
	SaveBaseline(actionID string, baseline map[string]model.FileEntry) error
	SaveConflicts(actionID string, conflicts []model.Conflict) error
	LoadConflicts(actionID string) ([]model.Conflict, error)
	AddHistory(h model.HistoryEntry) error
	SavePendingDeletes(actionID string, p *model.PendingDeletes) error
	LoadPendingDeletes(actionID string) (*model.PendingDeletes, error)
}

// Deps is everything a Runner needs for one action on this machine.
type Deps struct {
	Storage     storage.Storage
	Repo        Repo
	MachineID   string
	MachineName string
	// Passphrase is empty for unencrypted actions.
	Passphrase string
	// BackupRoot is where local files are copied before being overwritten or deleted.
	BackupRoot string
	LockMode   storage.LockMode
	LockSettle time.Duration
	// Expand resolves item paths; nil means paths.Expand.
	Expand func(string) (string, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Log is optional.
	Log *slog.Logger
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (d Deps) expand(p string) (string, error) {
	if d.Expand != nil {
		return d.Expand(p)
	}
	return paths.Expand(p)
}

// holdLock takes the action lock and keeps it renewed until release is
// called. Use the returned context for all remote work: it is cancelled with
// cause storage.ErrLockLost if the lock is lost. release is always non-nil
// and safe to call more than once, even when err != nil.
func (d Deps) holdLock(ctx context.Context, l Layout) (context.Context, func(), error) {
	lk := &storage.Locker{
		Store: d.Storage, Key: l.Lock(), MachineID: d.MachineID, TTL: LockTTL,
		Mode: d.LockMode, Settle: d.LockSettle, Now: d.Now, Logger: d.Log,
	}
	return lk.Hold(ctx)
}
