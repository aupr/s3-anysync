// Package app wires the state store, secrets, storage and engine together.
// The debug CLI uses it now; the agent's IPC server and the GUI use it later.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

// App is one machine's view of s3-anysync.
type App struct {
	DB          *state.DB
	Secrets     secrets.Store
	DataDir     string
	MachineID   string
	MachineName string
	// NewStorage builds the storage for a profile; tests replace it.
	NewStorage func(p model.Profile, secret string) (storage.Storage, error)
	// LockSettle is the best-effort lock read-back delay.
	LockSettle time.Duration
	Log        *slog.Logger

	// putAction stores an action locally; a test seam, defaults to DB.PutAction.
	putAction func(model.Action) error
}

func (a *App) savePutAction(act model.Action) error {
	if a.putAction != nil {
		return a.putAction(act)
	}
	return a.DB.PutAction(act)
}

// DefaultDataDir is S3_ANYSYNC_DATA_DIR if set, otherwise
// <user config dir>/s3-anysync (~/Library/Application Support on macOS,
// %APPDATA% on Windows).
func DefaultDataDir() (string, error) {
	if d := os.Getenv("S3_ANYSYNC_DATA_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "s3-anysync"), nil
}

// Open opens the state store in dataDir and loads the machine identity.
func Open(dataDir string, sec secrets.Store, log *slog.Logger) (*App, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := state.Open(filepath.Join(dataDir, "state.db"))
	if err != nil {
		return nil, err
	}
	id, name, err := db.MachineIdentity()
	if err != nil {
		db.Close()
		return nil, err
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &App{
		DB: db, Secrets: sec, DataDir: dataDir, MachineID: id, MachineName: name,
		NewStorage: NewS3Storage, LockSettle: time.Second, Log: log,
		putAction: db.PutAction,
	}, nil
}

// NewS3Storage is the production storage factory.
func NewS3Storage(p model.Profile, secret string) (storage.Storage, error) {
	return storage.NewS3(storage.S3Config{
		Endpoint: p.Endpoint, Region: p.Region, Bucket: p.Bucket,
		AccessKeyID: p.AccessKeyID, SecretAccessKey: secret, PathStyle: p.PathStyle,
	})
}

// Close closes the state store.
func (a *App) Close() error { return a.DB.Close() }

// SetMachineName changes this machine's friendly name.
func (a *App) SetMachineName(name string) error {
	if err := a.DB.SetMachineName(name); err != nil {
		return err
	}
	a.MachineName = strings.TrimSpace(name)
	return nil
}

func (a *App) storageFor(p model.Profile) (storage.Storage, error) {
	secret, err := a.Secrets.Get(secrets.ProfileSecretKey(p.ID))
	if err != nil {
		return nil, fmt.Errorf("secret key for profile %s: %w", p.Name, err)
	}
	return a.NewStorage(p, secret)
}

func lockMode(p model.Profile) storage.LockMode {
	if p.ConditionalWrites {
		return storage.LockConditional
	}
	return storage.LockBestEffort
}

// deps builds engine dependencies. The passphrase is loaded for encrypted actions.
func (a *App) deps(p model.Profile, actionID string, encrypted bool) (engine.Deps, error) {
	st, err := a.storageFor(p)
	if err != nil {
		return engine.Deps{}, err
	}
	d := engine.Deps{
		Storage: st, Repo: a.DB, MachineID: a.MachineID, MachineName: a.MachineName,
		BackupRoot: filepath.Join(a.DataDir, "local-backups"),
		LockMode:   lockMode(p), LockSettle: a.LockSettle, Log: a.Log,
	}
	if encrypted {
		pass, err := a.Secrets.Get(secrets.ActionPassphraseKey(actionID))
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return engine.Deps{}, err
		}
		d.Passphrase = pass
	}
	return d, nil
}

// AddProfile stores a profile and its secret key.
func (a *App) AddProfile(p model.Profile, secret string) (model.Profile, error) {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Bucket) == "" {
		return p, errors.New("profile name and bucket are required")
	}
	if secret == "" {
		return p, errors.New("secret access key is required")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	p.RootPrefix = strings.Trim(strings.ReplaceAll(strings.TrimSpace(p.RootPrefix), `\`, "/"), "/")
	// Conditional-write support is only known after TestProfile probes the provider.
	p.ConditionalWrites = false
	if err := a.Secrets.Set(secrets.ProfileSecretKey(p.ID), secret); err != nil {
		return p, fmt.Errorf("store secret key: %w", err)
	}
	if err := a.DB.PutProfile(p); err != nil {
		_ = a.Secrets.Delete(secrets.ProfileSecretKey(p.ID)) // best effort
		return p, err
	}
	return p, nil
}

// TestProfile probes the bucket and records whether conditional writes work.
func (a *App) TestProfile(ctx context.Context, id string) (storage.ProbeResult, error) {
	p, err := a.DB.GetProfile(id)
	if err != nil {
		return storage.ProbeResult{}, err
	}
	st, err := a.storageFor(p)
	if err != nil {
		return storage.ProbeResult{}, err
	}
	res, err := storage.Probe(ctx, st, p.RootPrefix)
	if err != nil {
		return res, err
	}
	p.ConditionalWrites = res.ConditionalWrites
	return res, a.DB.PutProfile(p)
}

// CreateAction publishes a new action from this machine.
func (a *App) CreateAction(ctx context.Context, profileID string, act model.Action, passphrase string) (model.Action, error) {
	p, err := a.DB.GetProfile(profileID)
	if err != nil {
		return act, err
	}
	if act.Encrypt && passphrase == "" {
		return act, engine.ErrPassphraseMissing
	}
	act.ID = uuid.NewString()
	act.ProfileID = profileID
	act.ApplyDefaults()
	act.Machines[a.MachineID] = a.MachineName
	act.UpdatedAt = time.Now().UTC()
	d, err := a.deps(p, act.ID, false)
	if err != nil {
		return act, err
	}
	d.Passphrase = passphrase
	// Keep the passphrase before publishing, so a bucket never holds an
	// encrypted action this machine cannot open.
	if act.Encrypt {
		if err := a.Secrets.Set(secrets.ActionPassphraseKey(act.ID), passphrase); err != nil {
			return act, fmt.Errorf("store passphrase: %w", err)
		}
	}
	if err := engine.CreateSharedAction(ctx, d, p.RootPrefix, act); err != nil {
		if act.Encrypt {
			_ = a.Secrets.Delete(secrets.ActionPassphraseKey(act.ID)) // best effort
		}
		return act, err
	}
	return act, a.savePutAction(act)
}

// updateShared changes the bucket's copy of an action and stores the result locally.
func (a *App) updateShared(ctx context.Context, actionID string, mutate func(*model.Action) error) (model.Action, error) {
	act, err := a.DB.GetAction(actionID)
	if err != nil {
		return act, err
	}
	p, err := a.DB.GetProfile(act.ProfileID)
	if err != nil {
		return act, err
	}
	d, err := a.deps(p, act.ID, act.Encrypt)
	if err != nil {
		return act, err
	}
	updated, err := engine.UpdateSharedAction(ctx, d, p.RootPrefix, actionID, func(x *model.Action) error {
		x.Machines[a.MachineID] = a.MachineName
		return mutate(x)
	})
	if err != nil {
		return act, err
	}
	updated.ProfileID = act.ProfileID
	return updated, a.savePutAction(updated)
}

// AddItem adds a file or directory, mapped to localPath on this machine.
func (a *App) AddItem(ctx context.Context, actionID string, it model.Item, localPath string) (model.Item, error) {
	name, err := model.NormalizeRemoteName(it.RemoteName)
	if err != nil {
		return it, err
	}
	if _, err := paths.Expand(localPath); err != nil {
		return it, err
	}
	it.ID = uuid.NewString()
	it.RemoteName = name
	it.LocalPaths = map[string]string{a.MachineID: localPath}
	_, err = a.updateShared(ctx, actionID, func(act *model.Action) error {
		act.Items = append(act.Items, it)
		return nil
	})
	return it, err
}

// MapItem sets this machine's local path for an existing item.
func (a *App) MapItem(ctx context.Context, actionID, itemID, localPath string) error {
	if _, err := paths.Expand(localPath); err != nil {
		return err
	}
	// Pointing an item at a (new) local path starts it from scratch: its old
	// baseline would make an empty or different folder look like mass
	// deletions. Drop it BEFORE publishing, so a failure after the publish
	// cannot leave a stale baseline behind (an empty baseline is always safe).
	var remoteName string
	if err := a.dropBaselineOfItem(actionID, itemID); err != nil {
		return err
	}
	_, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		for i := range act.Items {
			if act.Items[i].ID == itemID {
				act.Items[i].LocalPaths[a.MachineID] = localPath
				remoteName = act.Items[i].RemoteName
				return nil
			}
		}
		return fmt.Errorf("action has no item %s", itemID)
	})
	if err != nil {
		return err
	}
	// Belt and braces: covers an item missing from the local copy.
	return a.dropItemBaseline(actionID, remoteName)
}

// dropItemBaseline removes the baseline entries of one item (by remote name).
func (a *App) dropItemBaseline(actionID, remoteName string) error {
	base, err := a.DB.LoadBaseline(actionID)
	if err != nil {
		return err
	}
	for p := range base {
		if p == remoteName || strings.HasPrefix(p, remoteName+"/") {
			delete(base, p)
		}
	}
	return a.DB.SaveBaseline(actionID, base)
}

// SetMachineDirection overrides the action's direction on this machine; "" removes the override.
func (a *App) SetMachineDirection(ctx context.Context, actionID string, d model.Direction) error {
	if d != "" && !d.Valid() {
		return fmt.Errorf("unknown direction %q", d)
	}
	_, err := a.updateShared(ctx, actionID, func(act *model.Action) error {
		if d == "" {
			delete(act.MachineDirections, a.MachineID)
		} else {
			act.MachineDirections[a.MachineID] = d
		}
		return nil
	})
	return err
}

// Discover lists the actions stored under a profile's root prefix.
func (a *App) Discover(ctx context.Context, profileID string) ([]model.Action, error) {
	p, err := a.DB.GetProfile(profileID)
	if err != nil {
		return nil, err
	}
	st, err := a.storageFor(p)
	if err != nil {
		return nil, err
	}
	return engine.DiscoverActions(ctx, st, p.RootPrefix)
}

// Join registers this machine on an existing action. Items still need MapItem.
func (a *App) Join(ctx context.Context, profileID, actionID, passphrase string) (model.Action, error) {
	p, err := a.DB.GetProfile(profileID)
	if err != nil {
		return model.Action{}, err
	}
	d, err := a.deps(p, actionID, false)
	if err != nil {
		return model.Action{}, err
	}
	d.Passphrase = passphrase
	act, err := engine.FetchAction(ctx, d.Storage, p.RootPrefix, actionID)
	if err != nil {
		return model.Action{}, err
	}
	if err := engine.VerifyPassphrase(ctx, d, p.RootPrefix, act); err != nil {
		return model.Action{}, err
	}
	if act.Encrypt {
		if err := a.Secrets.Set(secrets.ActionPassphraseKey(actionID), passphrase); err != nil {
			return model.Action{}, fmt.Errorf("store passphrase: %w", err)
		}
	}
	updated, err := engine.UpdateSharedAction(ctx, d, p.RootPrefix, actionID, func(x *model.Action) error {
		x.Machines[a.MachineID] = a.MachineName
		return nil
	})
	if err != nil {
		return model.Action{}, err
	}
	updated.ProfileID = profileID
	return updated, a.DB.PutAction(updated)
}

// Runner returns an engine runner for a local action.
func (a *App) Runner(actionID string) (*engine.Runner, error) {
	act, err := a.DB.GetAction(actionID)
	if err != nil {
		return nil, err
	}
	p, err := a.DB.GetProfile(act.ProfileID)
	if err != nil {
		return nil, err
	}
	d, err := a.deps(p, act.ID, act.Encrypt)
	if err != nil {
		return nil, err
	}
	return engine.NewRunner(d, p.RootPrefix, act), nil
}

// Profiles lists local profiles.
func (a *App) Profiles() ([]model.Profile, error) { return a.DB.ListProfiles() }

// Actions lists local actions.
func (a *App) Actions() ([]model.Action, error) { return a.DB.ListActions() }
