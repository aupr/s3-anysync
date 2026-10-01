package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/niceit/s3-anysync/internal/crypto"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// keyCheckPlaintext is encrypted into .meta/keycheck so a machine can verify
// its passphrase before uploading anything.
const keyCheckPlaintext = "s3-anysync-keycheck-v1"

// FetchAction reads an action definition from the bucket. ProfileID is empty.
func FetchAction(ctx context.Context, s storage.Storage, root, id string) (model.Action, error) {
	data, _, err := s.Get(ctx, NewLayout(root, id).ActionJSON())
	if err != nil {
		return model.Action{}, err
	}
	var a model.Action
	if err := json.Unmarshal(data, &a); err != nil {
		return model.Action{}, fmt.Errorf("parse action %s: %w", id, err)
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

// DiscoverActions lists every action under root, sorted by name.
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
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if a.ID == id {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CreateSharedAction publishes a new action, writing the key check first
// for encrypted actions.
func CreateSharedAction(ctx context.Context, d Deps, root string, a model.Action) error {
	if err := a.Validate(); err != nil {
		return err
	}
	l := NewLayout(root, a.ID)
	if a.Encrypt {
		if err := writeKeyCheck(ctx, d, l); err != nil {
			return err
		}
	}
	return publishAction(ctx, d.Storage, l, a)
}

func writeKeyCheck(ctx context.Context, d Deps, l Layout) error {
	if d.Passphrase == "" {
		return ErrPassphraseMissing
	}
	body, err := crypto.Encrypt([]byte(keyCheckPlaintext), d.Passphrase)
	if err != nil {
		return err
	}
	_, err = d.Storage.Put(ctx, l.KeyCheck(), body, nil)
	return err
}

// VerifyPassphrase checks d.Passphrase against the action's key check. For an
// encrypted action without a key check, it writes one.
func VerifyPassphrase(ctx context.Context, d Deps, root string, a model.Action) error {
	if !a.Encrypt {
		return nil
	}
	if d.Passphrase == "" {
		return ErrPassphraseMissing
	}
	l := NewLayout(root, a.ID)
	data, _, err := d.Storage.Get(ctx, l.KeyCheck())
	if errors.Is(err, storage.ErrNotFound) {
		return writeKeyCheck(ctx, d, l)
	}
	if err != nil {
		return err
	}
	pt, err := crypto.Decrypt(data, d.Passphrase)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	if string(pt) != keyCheckPlaintext {
		return ErrDecrypt
	}
	return nil
}

// UpdateSharedAction applies mutate to the bucket's copy of the action under
// the action lock, stamps UpdatedAt and republishes it. ProfileID is empty
// in the result.
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
		return model.Action{}, err
	}
	if err := mutate(&a); err != nil {
		return model.Action{}, err
	}
	a.UpdatedAt = d.now().UTC()
	if err := a.Validate(); err != nil {
		return model.Action{}, err
	}
	if err := publishAction(ctx, d.Storage, l, a); err != nil {
		return model.Action{}, err
	}
	return a, nil
}
