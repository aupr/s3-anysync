package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aupr/s3-anysync/internal/crypto"
	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
)

// keyCheckPlaintext is encrypted into .meta/keycheck so a machine can verify
// its passphrase before uploading anything.
const keyCheckPlaintext = "s3-anysync-keycheck-v1"

var errParseAction = errors.New("unparseable action.json")

// FetchAction reads an action definition from the bucket. ProfileID is empty.
func FetchAction(ctx context.Context, s storage.Storage, root, id string) (model.Action, error) {
	data, _, err := s.Get(ctx, NewLayout(root, id).ActionJSON())
	if err != nil {
		return model.Action{}, err
	}
	var a model.Action
	if err := json.Unmarshal(data, &a); err != nil {
		return model.Action{}, fmt.Errorf("parse action %s: %w: %w", id, errParseAction, err)
	}
	a.ApplyDefaults()
	return a, nil
}

func publishAction(ctx context.Context, s storage.Storage, l Layout, a model.Action) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.Put(ctx, l.ActionJSON(), data, nil)
	return err
}

// DiscoverActions lists every action under root, sorted by name then ID.
// Folders whose action.json is missing, cannot be parsed, or carries a
// different ID than its folder are skipped so one damaged entry does not hide
// the others; storage errors (listing or reading) are returned.
func DiscoverActions(ctx context.Context, s storage.Storage, root string) ([]model.Action, error) {
	prefix := storage.JoinKey(root)
	if prefix != "" {
		prefix += "/"
	}
	dirs, err := s.ListPrefixes(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []model.Action
	for _, dir := range dirs {
		id := strings.TrimSuffix(strings.TrimPrefix(dir, prefix), "/")
		a, err := FetchAction(ctx, s, root, id)
		if errors.Is(err, storage.ErrNotFound) || errors.Is(err, errParseAction) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if a.ID == id {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// CreateSharedAction publishes a new action, writing the key check first
// for encrypted actions. A stale key check is overwritten: the user is
// explicitly creating the action with this passphrase.
func CreateSharedAction(ctx context.Context, d Deps, root string, a model.Action) error {
	if err := a.Validate(); err != nil {
		return err
	}
	l := NewLayout(root, a.ID)
	if a.Encrypt {
		seeded, err := seedKeyCheck(ctx, d, l)
		if err != nil {
			return err
		}
		if !seeded {
			body, err := keyCheckBody(d)
			if err != nil {
				return err
			}
			if _, err := d.Storage.Put(ctx, l.KeyCheck(), body, nil); err != nil {
				return err
			}
		}
	}
	return publishAction(ctx, d.Storage, l, a)
}

func keyCheckBody(d Deps) ([]byte, error) {
	if d.Passphrase == "" {
		return nil, ErrPassphraseMissing
	}
	return crypto.Encrypt([]byte(keyCheckPlaintext), d.Passphrase)
}

// seedKeyCheck writes the key check only if none exists. It reports false
// (and no error) when another writer got there first.
func seedKeyCheck(ctx context.Context, d Deps, l Layout) (bool, error) {
	body, err := keyCheckBody(d)
	if err != nil {
		return false, err
	}
	_, err = d.Storage.PutIfAbsent(ctx, l.KeyCheck(), body)
	if errors.Is(err, storage.ErrPreconditionFailed) {
		return false, nil
	}
	return err == nil, err
}

func verifyKeyCheck(ctx context.Context, d Deps, l Layout) error {
	data, _, err := d.Storage.Get(ctx, l.KeyCheck())
	if err != nil {
		return err
	}
	pt, err := crypto.Decrypt(data, d.Passphrase)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDecrypt, err)
	}
	if string(pt) != keyCheckPlaintext {
		return ErrDecrypt
	}
	return nil
}

// VerifyPassphrase checks d.Passphrase against the action's key check. A
// missing key check is only seeded for a fresh action (nothing under
// current/); otherwise ErrKeyCheckMissing is returned. If another machine
// seeds it concurrently, the passphrase is verified against theirs.
func VerifyPassphrase(ctx context.Context, d Deps, root string, a model.Action) error {
	if !a.Encrypt {
		return nil
	}
	if d.Passphrase == "" {
		return ErrPassphraseMissing
	}
	l := NewLayout(root, a.ID)
	err := verifyKeyCheck(ctx, d, l)
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	objs, err := d.Storage.List(ctx, l.CurrentPrefix())
	if err != nil {
		return err
	}
	if len(objs) > 0 {
		return ErrKeyCheckMissing
	}
	seeded, err := seedKeyCheck(ctx, d, l)
	if err != nil || seeded {
		return err
	}
	return verifyKeyCheck(ctx, d, l)
}

// ctxErr makes a cancellation caused by a lost lock visible to errors.Is.
func ctxErr(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); errors.Is(cause, storage.ErrLockLost) {
		return fmt.Errorf("%w: %w", cause, err)
	}
	return err
}

// UpdateSharedAction applies mutate to the bucket's copy of the action under
// the action lock, stamps UpdatedAt and republishes it. ProfileID is empty
// in the result. mutate must not change the action's ID or Encrypt flag.
func UpdateSharedAction(ctx context.Context, d Deps, root, id string, mutate func(*model.Action) error) (model.Action, error) {
	l := NewLayout(root, id)
	held, release, err := d.holdLock(ctx, l)
	if err != nil {
		return model.Action{}, err
	}
	defer release()
	ctx = held
	a, err := FetchAction(ctx, d.Storage, root, id)
	if err != nil {
		return model.Action{}, ctxErr(ctx, err)
	}
	origID, origEncrypt, fetched := a.ID, a.Encrypt, a.UpdatedAt
	if err := mutate(&a); err != nil {
		return model.Action{}, ctxErr(ctx, err)
	}
	if a.ID != origID {
		return model.Action{}, errors.New("mutate must not change the action id")
	}
	if a.Encrypt != origEncrypt {
		return model.Action{}, errors.New("encryption cannot be changed after the action is created")
	}
	// Strictly after the fetched version even if this clock is behind it.
	a.UpdatedAt = d.now().UTC()
	if min := fetched.Add(time.Millisecond); a.UpdatedAt.Before(min) {
		a.UpdatedAt = min
	}
	if err := a.Validate(); err != nil {
		return model.Action{}, err
	}
	if err := publishAction(ctx, d.Storage, l, a); err != nil {
		return model.Action{}, ctxErr(ctx, err)
	}
	return a, nil
}
