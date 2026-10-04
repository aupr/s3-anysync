package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/schedule"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/state"
)

const settingLogLevel = "log_level"

var logLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Action returns a local action.
func (a *App) Action(id string) (model.Action, error) { return a.DB.GetAction(id) }

// Profile returns a local profile.
func (a *App) Profile(id string) (model.Profile, error) { return a.DB.GetProfile(id) }

// UpdateRules changes an action's editable settings for every machine.
// expected is the UpdatedAt the editor loaded; if the bucket's copy has
// another version nothing is written, the local copy is replaced by the
// bucket's and engine.ErrEditConflict is returned.
func (a *App) UpdateRules(ctx context.Context, actionID string, r model.Rules, expected time.Time) (model.Action, error) {
	if expected.IsZero() {
		return model.Action{}, fmt.Errorf("%w: the version (updatedAt) of the edited action is required", engine.ErrInvalidInput)
	}
	if err := schedule.Validate(r.Triggers.Schedule); err != nil {
		return model.Action{}, err
	}
	var fresh model.Action
	updated, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		if !act.UpdatedAt.Equal(expected) {
			fresh = *act
			return fmt.Errorf("%w (now version %s)", engine.ErrEditConflict, act.UpdatedAt.Format(time.RFC3339Nano))
		}
		act.ApplyRules(r)
		return nil
	})
	if errors.Is(err, engine.ErrEditConflict) {
		if local, lerr := a.DB.GetAction(actionID); lerr == nil {
			fresh.ProfileID = local.ProfileID
			_ = a.savePutAction(fresh) // best effort: the conflict is reported either way
		}
	}
	return updated, err
}

// dropBaselineOfItem drops the baseline of an item known in the local copy
// of the action (an unknown item has no baseline to drop).
func (a *App) dropBaselineOfItem(actionID, itemID string) error {
	cur, err := a.DB.GetAction(actionID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, it := range cur.Items {
		if it.ID == itemID {
			return a.dropItemBaseline(actionID, it.RemoteName)
		}
	}
	return nil
}

// UnmapItem stops syncing an item on this machine. Local files and the cloud
// copy stay; the item's baseline is dropped first, so mapping it again later
// behaves like a first run.
func (a *App) UnmapItem(ctx context.Context, actionID, itemID string) error {
	if err := a.dropBaselineOfItem(actionID, itemID); err != nil {
		return err
	}
	_, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		for i := range act.Items {
			if act.Items[i].ID == itemID {
				delete(act.Items[i].LocalPaths, a.MachineID)
				return nil
			}
		}
		return fmt.Errorf("action has no item %s", itemID)
	})
	return err
}

// RemoveItem removes an item from the action on every machine. Local files
// and the cloud copies stay in place; the cloud copies are no longer synced.
func (a *App) RemoveItem(ctx context.Context, actionID, itemID string) error {
	if err := a.dropBaselineOfItem(actionID, itemID); err != nil {
		return err
	}
	_, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		for i := range act.Items {
			if act.Items[i].ID == itemID {
				act.Items = append(act.Items[:i], act.Items[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("action has no item %s", itemID)
	})
	return err
}

// LeaveAction stops syncing an action on this machine. This machine's paths,
// direction override and name are removed from the shared definition, then
// the local action, its baseline, history, conflicts, pending deletions,
// pause flag and passphrase are deleted. Local files and the cloud copy
// stay. With force, a failure to update the shared definition (for example
// when offline) is ignored.
func (a *App) LeaveAction(ctx context.Context, actionID string, force bool) error {
	act, err := a.DB.GetAction(actionID)
	if errors.Is(err, state.ErrNotFound) {
		// A retry after a partial failure: still clean up an orphan passphrase.
		_ = a.Secrets.Delete(secrets.ActionPassphraseKey(actionID))
		return err
	}
	if err != nil {
		return err
	}
	_, err = a.updateShared(ctx, actionID, func(x *model.Action) error {
		for i := range x.Items {
			delete(x.Items[i].LocalPaths, a.MachineID)
		}
		delete(x.MachineDirections, a.MachineID)
		delete(x.Machines, a.MachineID)
		return nil
	})
	if err != nil && !force {
		return err
	}
	if err := a.DB.DeleteAction(actionID); err != nil {
		return err
	}
	if act.Encrypt {
		if err := a.Secrets.Delete(secrets.ActionPassphraseKey(actionID)); err != nil {
			return fmt.Errorf("the action was removed locally, but removing its passphrase failed: %w", err)
		}
	}
	return nil
}

// History returns the newest runs of an action; limit <= 0 means 50.
func (a *App) History(actionID string, limit int) ([]model.HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	return a.DB.ListHistory(actionID, limit)
}

// Conflicts returns an action's saved conflicts.
func (a *App) Conflicts(actionID string) ([]model.Conflict, error) {
	return a.DB.LoadConflicts(actionID)
}

// PendingDeletes returns the deletions held back by the delete guard, or nil.
func (a *App) PendingDeletes(actionID string) (*model.PendingDeletes, error) {
	return a.DB.LoadPendingDeletes(actionID)
}

// SetPaused stores whether the user paused an action.
func (a *App) SetPaused(actionID string, paused bool) error { return a.DB.SetPaused(actionID, paused) }

// Paused reports whether the user paused an action.
func (a *App) Paused(actionID string) (bool, error) { return a.DB.Paused(actionID) }

// LogLevel returns the agent's log level (debug, info, warn or error).
func (a *App) LogLevel() (string, error) {
	v, ok, err := a.DB.GetSetting(settingLogLevel)
	if err != nil || !ok {
		return "info", err
	}
	return v, nil
}

// SetLogLevel stores the agent's log level.
func (a *App) SetLogLevel(level string) error {
	if !logLevels[level] {
		return fmt.Errorf("unknown log level %q (use debug, info, warn or error)", level)
	}
	return a.DB.SetSetting(settingLogLevel, level)
}
