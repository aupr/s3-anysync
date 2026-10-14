package state

import (
	"time"

	"github.com/aupr/s3-anysync/internal/model"
)

// AddHistory appends a run to the action's history.
func (d *DB) AddHistory(h model.HistoryEntry) error {
	_, err := d.sql.Exec(`INSERT INTO history(action_id, kind, started_at, finished_at, outcome, message) VALUES(?, ?, ?, ?, ?, ?)`,
		h.ActionID, h.Kind, h.StartedAt.UTC().Format(time.RFC3339Nano), h.FinishedAt.UTC().Format(time.RFC3339Nano), h.Outcome, h.Message)
	return err
}

// ListHistory returns the newest entries first.
func (d *DB) ListHistory(actionID string, limit int) ([]model.HistoryEntry, error) {
	rows, err := d.sql.Query(`SELECT id, action_id, kind, started_at, finished_at, outcome, message FROM history WHERE action_id = ? ORDER BY id DESC LIMIT ?`, actionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.HistoryEntry
	for rows.Next() {
		var h model.HistoryEntry
		var started, finished string
		if err := rows.Scan(&h.ID, &h.ActionID, &h.Kind, &started, &finished, &h.Outcome, &h.Message); err != nil {
			return nil, err
		}
		if h.StartedAt, err = time.Parse(time.RFC3339Nano, started); err != nil {
			return nil, err
		}
		if h.FinishedAt, err = time.Parse(time.RFC3339Nano, finished); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
