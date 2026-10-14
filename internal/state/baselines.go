package state

import "github.com/aupr/s3-anysync/internal/model"

// LoadBaseline returns the last committed state of an action on this
// machine, keyed by remote path. A missing baseline is an empty map.
func (d *DB) LoadBaseline(actionID string) (map[string]model.FileEntry, error) {
	rows, err := d.sql.Query(`SELECT path, hash, size, mtime FROM baselines WHERE action_id = ?`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.FileEntry{}
	for rows.Next() {
		var e model.FileEntry
		if err := rows.Scan(&e.Path, &e.Hash, &e.Size, &e.MTime); err != nil {
			return nil, err
		}
		out[e.Path] = e
	}
	return out, rows.Err()
}

// SaveBaseline replaces the action's baseline.
func (d *DB) SaveBaseline(actionID string, baseline map[string]model.FileEntry) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM baselines WHERE action_id = ?`, actionID); err != nil {
		tx.Rollback()
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO baselines(action_id, path, hash, size, mtime) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for p, e := range baseline {
		if _, err := stmt.Exec(actionID, p, e.Hash, e.Size, e.MTime); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
