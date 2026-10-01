package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/niceit/s3-anysync/internal/model"
)

// PutProfile inserts or replaces a profile.
func (d *DB) PutProfile(p model.Profile) error {
	if p.ID == "" {
		return errors.New("profile id is empty")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`INSERT INTO profiles(id, data) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, p.ID, string(data))
	return err
}

// GetProfile returns a profile or ErrNotFound.
func (d *DB) GetProfile(id string) (model.Profile, error) {
	var data string
	err := d.sql.QueryRow(`SELECT data FROM profiles WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Profile{}, fmt.Errorf("profile %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.Profile{}, err
	}
	var p model.Profile
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return model.Profile{}, err
	}
	return p, nil
}

// ListProfiles returns all profiles sorted by name.
func (d *DB) ListProfiles() ([]model.Profile, error) {
	rows, err := d.sql.Query(`SELECT data FROM profiles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Profile
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p model.Profile
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, rows.Err()
}

// DeleteProfile removes a profile that no action uses.
func (d *DB) DeleteProfile(id string) error {
	var n int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM actions WHERE profile_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("profile is used by %d action(s)", n)
	}
	_, err := d.sql.Exec(`DELETE FROM profiles WHERE id = ?`, id)
	return err
}
