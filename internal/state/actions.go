package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/aupr/s3-anysync/internal/model"
)

// PutAction inserts or replaces an action; ProfileID is stored in its own column.
func (d *DB) PutAction(a model.Action) error {
	if a.ID == "" || a.ProfileID == "" {
		return errors.New("action id and profile id are required")
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`INSERT INTO actions(id, profile_id, data) VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET profile_id = excluded.profile_id, data = excluded.data`, a.ID, a.ProfileID, string(data))
	return err
}

func scanAction(profileID, data string) (model.Action, error) {
	var a model.Action
	if err := json.Unmarshal([]byte(data), &a); err != nil {
		return model.Action{}, err
	}
	a.ProfileID = profileID
	a.ApplyDefaults()
	return a, nil
}

// GetAction returns an action or ErrNotFound.
func (d *DB) GetAction(id string) (model.Action, error) {
	var pid, data string
	err := d.sql.QueryRow(`SELECT profile_id, data FROM actions WHERE id = ?`, id).Scan(&pid, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Action{}, fmt.Errorf("action %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.Action{}, err
	}
	return scanAction(pid, data)
}

// ListActions returns all actions sorted by name.
func (d *DB) ListActions() ([]model.Action, error) {
	rows, err := d.sql.Query(`SELECT profile_id, data FROM actions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Action
	for rows.Next() {
		var pid, data string
		if err := rows.Scan(&pid, &data); err != nil {
			return nil, err
		}
		a, err := scanAction(pid, data)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, rows.Err()
}

// DeleteAction removes an action with its baseline, conflicts and history.
func (d *DB) DeleteAction(id string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM baselines WHERE action_id = ?`,
		`DELETE FROM conflicts WHERE action_id = ?`,
		`DELETE FROM history WHERE action_id = ?`,
		`DELETE FROM actions WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			tx.Rollback()
			return err
		}
	}
	for _, key := range []string{settingPendingDeletesPrefix + id, settingPausedPrefix + id} {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, key); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
