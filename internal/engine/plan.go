package engine

import (
	"sort"

	"github.com/aupr/s3-anysync/internal/model"
)

// OpKind is what the executor does with one path.
type OpKind string

const (
	OpUpload       OpKind = "upload"
	OpDownload     OpKind = "download"
	OpDeleteRemote OpKind = "delete-remote"
	OpDeleteLocal  OpKind = "delete-local"
	OpAdopt        OpKind = "adopt"    // both sides already equal; record as baseline
	OpDrop         OpKind = "drop"     // gone on both sides; forget it
	OpKeep         OpKind = "keep"     // unchanged; refresh baseline from the scan
	OpConflict     OpKind = "conflict" // both sides changed differently (two-way only)
)

// Op is the planned action for one remote path. Local and Remote are nil
// when the file does not exist on that side.
type Op struct {
	Path   string
	Kind   OpKind
	Local  *model.FileEntry
	Remote *model.FileEntry
}

// PlanInput is the three-way comparison input. All maps are keyed by remote path.
type PlanInput struct {
	Direction        model.Direction
	PropagateDeletes bool
	// ReceiverRestoresCloud lets a cloud-to-local machine upload its local copy
	// of a file the cloud deleted while PropagateDeletes is off.
	ReceiverRestoresCloud bool
	Local                 map[string]model.FileEntry
	Remote                map[string]model.FileEntry
	Baseline              map[string]model.FileEntry
}

// Plan compares local, remote and baseline and returns one op per path that
// needs handling, sorted by path. It is a pure function.
func Plan(in PlanInput) []Op {
	seen := map[string]bool{}
	for _, m := range []map[string]model.FileEntry{in.Local, in.Remote, in.Baseline} {
		for p := range m {
			seen[p] = true
		}
	}
	all := make([]string, 0, len(seen))
	for p := range seen {
		all = append(all, p)
	}
	sort.Strings(all)

	var ops []Op
	for _, p := range all {
		l, lok := in.Local[p]
		r, rok := in.Remote[p]
		b, bok := in.Baseline[p]
		lc := changed(lok, l, bok, b)
		rc := changed(rok, r, bok, b)
		kind, ok := decide(in.Direction, in.PropagateDeletes, in.ReceiverRestoresCloud, lok, rok, lc, rc, lok && rok && l.Hash == r.Hash)
		if !ok {
			continue
		}
		op := Op{Path: p, Kind: kind}
		if lok {
			op.Local = &l
		}
		if rok {
			op.Remote = &r
		}
		ops = append(ops, op)
	}
	return ops
}

func changed(ok bool, e model.FileEntry, bok bool, b model.FileEntry) bool {
	if ok != bok {
		return true
	}
	return ok && e.Hash != b.Hash
}

// decide maps the change matrix to an op. ok=false means leave the path alone.
func decide(dir model.Direction, propagate, restore, lok, rok, lc, rc, same bool) (OpKind, bool) {
	switch {
	case !lc && !rc:
		return OpKeep, true
	case lc && rc:
		if !lok && !rok {
			return OpDrop, true
		}
		if same {
			return OpAdopt, true
		}
		switch dir {
		case model.LocalToCloud:
			if lok {
				return OpUpload, true
			}
			if propagate {
				return OpDeleteRemote, true
			}
			return OpDownload, true
		case model.CloudToLocal:
			if rok {
				return OpDownload, true
			}
			if propagate {
				return OpDeleteLocal, true
			}
			if restore {
				return OpUpload, true
			}
			return "", false // receive-only: never write to the cloud
		default:
			return OpConflict, true
		}
	case lc: // only the local side changed
		if dir == model.CloudToLocal {
			if rok {
				return OpDownload, true
			}
			return "", false // untracked local-only file
		}
		if lok {
			return OpUpload, true
		}
		if propagate {
			return OpDeleteRemote, true
		}
		return OpDownload, true
	default: // only the cloud side changed
		if dir == model.LocalToCloud {
			if lok {
				return OpUpload, true
			}
			return "", false // untracked cloud-only file
		}
		if rok {
			return OpDownload, true
		}
		if propagate {
			return OpDeleteLocal, true
		}
		if dir == model.CloudToLocal {
			if restore {
				return OpUpload, true
			}
			return "", false // receive-only: never write to the cloud
		}
		return OpUpload, true // two-way: restore the cloud copy
	}
}
