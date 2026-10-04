package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/niceit/s3-anysync/internal/model"
)

// massDeletes returns the paths this run would delete (on either side) when
// they trip the action's delete guard, or nil. Deletions chosen in a conflict
// resolution (resolved) don't count. tracked is the number of synced files
// (the baseline).
func massDeletes(ops []Op, resolved map[string]bool, tracked int, a model.Action) []string {
	var del []string
	for _, op := range ops {
		if (op.Kind == OpDeleteRemote || op.Kind == OpDeleteLocal) && !resolved[op.Path] {
			del = append(del, op.Path)
		}
	}
	if len(del) == 0 || len(del) < a.DeleteGuardMinFiles || len(del)*100 < a.DeleteGuardPercent*tracked {
		return nil
	}
	sort.Strings(del)
	return del
}

// covers reports whether every path in del is in the approved pending set.
func covers(p *model.PendingDeletes, del []string) bool {
	if p == nil || !p.Approved {
		return false
	}
	ok := make(map[string]bool, len(p.Paths))
	for _, x := range p.Paths {
		ok[x] = true
	}
	for _, x := range del {
		if !ok[x] {
			return false
		}
	}
	return true
}

// sameSet reports whether a and b hold the same paths, ignoring order and
// duplicates.
func sameSet(a, b []string) bool {
	set := func(l []string) map[string]bool {
		m := make(map[string]bool, len(l))
		for _, x := range l {
			m[x] = true
		}
		return m
	}
	sa, sb := set(a), set(b)
	if len(sa) != len(sb) {
		return false
	}
	for x := range sa {
		if !sb[x] {
			return false
		}
	}
	return true
}

// ApproveDeletes approves the deletions held back by the delete guard and
// runs a sync. reviewed is the list the user looked at: it must equal the
// saved pending paths as a set (order and duplicates don't matter). If it
// differs (the pending set changed since the user looked) nothing is approved
// and nothing changes: the result is OutcomeDeletesPending with the saved
// paths so the caller can show the fresh list. The approval covers exactly
// the saved paths and lasts until a run applies them; a run that would delete
// more trips the guard again.
func (r *Runner) ApproveDeletes(ctx context.Context, reviewed []string) (model.SyncResult, error) {
	p, err := r.d.Repo.LoadPendingDeletes(r.action.ID)
	if err != nil {
		return model.SyncResult{}, fmt.Errorf("load pending deletions: %w", err)
	}
	if p == nil {
		return model.SyncResult{}, errors.New("no deletions are waiting for approval")
	}
	if !sameSet(reviewed, p.Paths) {
		saved := append([]string(nil), p.Paths...)
		sort.Strings(saved)
		return model.SyncResult{Outcome: model.OutcomeDeletesPending, PendingDeletes: saved}, nil
	}
	p.Approved = true
	if err := r.d.Repo.SavePendingDeletes(r.action.ID, p); err != nil {
		return model.SyncResult{}, fmt.Errorf("save deletion approval: %w", err)
	}
	res, err := r.run(ctx, model.HistoryApproveDeletes, nil)
	if err == nil && res.Outcome == model.OutcomeConflict {
		// The conflict stop cleared the saved record, but the user approved
		// this exact list just now and the conflict is not new: keep the
		// approval so that resolving the conflict can apply the deletions
		// (otherwise resolve and approve would chase each other forever).
		if serr := r.d.Repo.SavePendingDeletes(r.action.ID, p); serr != nil {
			return res, fmt.Errorf("keep deletion approval: %w", serr)
		}
	}
	return res, err
}
