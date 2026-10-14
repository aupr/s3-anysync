package engine

import "github.com/aupr/s3-anysync/internal/storage"

// Layout builds the object keys for one action.
type Layout struct {
	base string
}

// NewLayout returns the layout for <root>/<actionID>.
func NewLayout(root, actionID string) Layout {
	return Layout{base: storage.JoinKey(root, actionID)}
}

func (l Layout) ActionJSON() string      { return l.base + "/.meta/action.json" }
func (l Layout) Manifest() string        { return l.base + "/.meta/manifest.json" }
func (l Layout) Lock() string            { return l.base + "/.meta/lock" }
func (l Layout) KeyCheck() string        { return l.base + "/.meta/keycheck" }
func (l Layout) CurrentPrefix() string   { return l.base + "/current/" }
func (l Layout) Current(p string) string { return l.CurrentPrefix() + p }
func (l Layout) SnapshotsPrefix() string { return l.base + "/snapshots/" }

func (l Layout) SnapshotFile(ts, p string) string { return l.SnapshotsPrefix() + ts + "/" + p }
func (l Layout) SnapshotIndex(ts string) string   { return l.SnapshotsPrefix() + ts + ".json" }
