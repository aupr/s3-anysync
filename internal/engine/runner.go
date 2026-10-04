package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
)

// Runner performs syncs, snapshots, resolution and restores for one action
// on this machine. Use one Runner per action; calls must not overlap.
type Runner struct {
	d      Deps
	root   string
	action model.Action
	layout Layout
	// kinds caches what retention needs to know about each snapshot; snapshot
	// indexes never change, so entries stay valid until the snapshot is deleted.
	kinds map[string]snapKind
}

// NewRunner returns a runner for action a stored under root.
func NewRunner(d Deps, root string, a model.Action) *Runner {
	return &Runner{d: d, root: root, action: a, layout: NewLayout(root, a.ID)}
}

// Action returns the runner's current copy of the action definition.
func (r *Runner) Action() model.Action { return r.action }

// refreshAction picks up rule changes made on other machines and checks the
// passphrase of encrypted actions. The bucket's definition replaces the local
// one whenever its UpdatedAt differs (the bucket is authoritative, so a
// machine whose clock is ahead cannot mask later edits); an equal UpdatedAt
// keeps the local copy. The local ProfileID is preserved, and the new
// definition is persisted when a Repo is configured.
func (r *Runner) refreshAction(ctx context.Context) error {
	remote, err := FetchAction(ctx, r.d.Storage, r.root, r.action.ID)
	if err != nil {
		return fmt.Errorf("read action definition: %w", err)
	}
	if !remote.UpdatedAt.Equal(r.action.UpdatedAt) {
		remote.ProfileID = r.action.ProfileID
		if r.d.Repo != nil {
			if err := r.d.Repo.PutAction(remote); err != nil {
				return err
			}
		}
		r.action = remote
	}
	return VerifyPassphrase(ctx, r.d, r.root, r.action)
}

func (r *Runner) addHistory(kind string, started time.Time, outcome, message string) {
	if r.d.Repo == nil {
		return
	}
	h := model.HistoryEntry{ActionID: r.action.ID, Kind: kind, StartedAt: started, FinishedAt: r.d.now(), Outcome: outcome, Message: message}
	if err := r.d.Repo.AddHistory(h); err != nil {
		r.d.log().Warn("record history", "action", r.action.ID, "err", err)
	}
}
