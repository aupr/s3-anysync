package state

import (
	"encoding/json"

	"github.com/aupr/s3-anysync/internal/model"
)

// SaveConflicts replaces the action's open conflicts; nil clears them.
func (d *DB) SaveConflicts(actionID string, conflicts []model.Conflict) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM conflicts WHERE action_id = ?`, actionID); err != nil {
		tx.Rollback()
		return err
	}
	for _, c := range conflicts {
		data, err := json.Marshal(c)
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`INSERT INTO conflicts(action_id, path, data) VALUES(?, ?, ?)`, actionID, c.Path, string(data)); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// LoadConflicts returns the action's open conflicts sorted by path.
func (d *DB) LoadConflicts(actionID string) ([]model.Conflict, error) {
	rows, err := d.sql.Query(`SELECT data FROM conflicts WHERE action_id = ? ORDER BY path`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Conflict
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var c model.Conflict
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
