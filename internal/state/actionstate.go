package state

import (
	"encoding/json"
	"fmt"

	"github.com/aupr/s3-anysync/internal/model"
)

const (
	settingPendingDeletesPrefix = "pending_deletes:"
	settingPausedPrefix         = "paused:"
)

// DeleteSetting removes a setting; a missing key is not an error.
func (d *DB) DeleteSetting(key string) error {
	_, err := d.sql.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// SavePendingDeletes stores the deletions held back for an action; nil or an
// empty path list clears them.
func (d *DB) SavePendingDeletes(actionID string, p *model.PendingDeletes) error {
	key := settingPendingDeletesPrefix + actionID
	if p == nil || len(p.Paths) == 0 {
		return d.DeleteSetting(key)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return d.SetSetting(key, string(data))
}

// LoadPendingDeletes returns the held-back deletions, or nil if there are none.
func (d *DB) LoadPendingDeletes(actionID string) (*model.PendingDeletes, error) {
	v, ok, err := d.GetSetting(settingPendingDeletesPrefix + actionID)
	if err != nil || !ok {
		return nil, err
	}
	var p model.PendingDeletes
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return nil, fmt.Errorf("pending deletes of %s: %w", actionID, err)
	}
	return &p, nil
}

// SetPaused stores whether the user paused an action.
func (d *DB) SetPaused(actionID string, paused bool) error {
	if !paused {
		return d.DeleteSetting(settingPausedPrefix + actionID)
	}
	return d.SetSetting(settingPausedPrefix+actionID, "1")
}

// Paused reports whether the user paused an action.
func (d *DB) Paused(actionID string) (bool, error) {
	_, ok, err := d.GetSetting(settingPausedPrefix + actionID)
	return ok, err
}
