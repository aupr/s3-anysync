package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
)

// ErrInUse: the profile is used by actions on this machine.
var ErrInUse = errors.New("in use")

// normalizePrefix cleans a user-typed folder inside the bucket.
func normalizePrefix(s string) string {
	return strings.Trim(strings.ReplaceAll(strings.TrimSpace(s), `\`, "/"), "/")
}

// actionsUsing returns the names of local actions that use the profile.
func (a *App) actionsUsing(profileID string) ([]string, error) {
	acts, err := a.DB.ListActions()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, act := range acts {
		if act.ProfileID == profileID {
			names = append(names, act.Name)
		}
	}
	return names, nil
}

// UpdateProfile changes a profile; an empty secret keeps the stored one.
// Bucket and RootPrefix cannot change while an action uses the profile.
// Changing the endpoint, bucket or prefix makes conditional-write support
// unknown again (false until the next TestProfile).
func (a *App) UpdateProfile(p model.Profile, secret string) (model.Profile, error) {
	cur, err := a.DB.GetProfile(p.ID)
	if err != nil {
		return p, err
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Bucket) == "" {
		return p, fmt.Errorf("%w: profile name and bucket are required", engine.ErrInvalidInput)
	}
	p.RootPrefix = normalizePrefix(p.RootPrefix)
	if p.Bucket != cur.Bucket || p.RootPrefix != cur.RootPrefix {
		names, err := a.actionsUsing(p.ID)
		if err != nil {
			return p, err
		}
		if len(names) > 0 {
			return p, fmt.Errorf("%w: the bucket and folder cannot change while actions use this profile: %s", ErrInUse, strings.Join(names, ", "))
		}
	}
	p.ConditionalWrites = cur.ConditionalWrites
	if p.Endpoint != cur.Endpoint || p.Bucket != cur.Bucket || p.RootPrefix != cur.RootPrefix {
		p.ConditionalWrites = false
	}
	if secret != "" {
		if err := a.Secrets.Set(secrets.ProfileSecretKey(p.ID), secret); err != nil {
			return p, fmt.Errorf("store secret key: %w", err)
		}
	}
	return p, a.DB.PutProfile(p)
}

// RemoveProfile deletes a profile and its secret; refused while actions use it.
func (a *App) RemoveProfile(id string) error {
	if _, err := a.DB.GetProfile(id); err != nil {
		return err
	}
	names, err := a.actionsUsing(id)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		return fmt.Errorf("%w: actions use this profile: %s", ErrInUse, strings.Join(names, ", "))
	}
	if err := a.DB.DeleteProfile(id); err != nil {
		return err
	}
	return a.Secrets.Delete(secrets.ProfileSecretKey(id))
}
