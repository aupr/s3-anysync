package state

import (
	"database/sql"
	"errors"
	"os"
	"strings"

	"github.com/google/uuid"
)

const (
	settingMachineID   = "machine_id"
	settingMachineName = "machine_name"
)

// GetSetting returns the value and whether it exists.
func (d *DB) GetSetting(key string) (string, bool, error) {
	var v string
	err := d.sql.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting inserts or replaces a setting.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.sql.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// MachineIdentity returns this machine's id and friendly name, creating
// them on first use (a new UUID and the hostname).
func (d *DB) MachineIdentity() (string, string, error) {
	id, ok, err := d.GetSetting(settingMachineID)
	if err != nil {
		return "", "", err
	}
	if !ok {
		id = uuid.NewString()
		if err := d.SetSetting(settingMachineID, id); err != nil {
			return "", "", err
		}
	}
	name, ok, err := d.GetSetting(settingMachineName)
	if err != nil {
		return "", "", err
	}
	if !ok {
		name, _ = os.Hostname()
		if name == "" {
			name = "machine"
		}
		if err := d.SetSetting(settingMachineName, name); err != nil {
			return "", "", err
		}
	}
	return id, name, nil
}

// SetMachineName changes the friendly name shown to other machines.
func (d *DB) SetMachineName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("machine name is empty")
	}
	return d.SetSetting(settingMachineName, name)
}
