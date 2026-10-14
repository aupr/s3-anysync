package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"unicode/utf8"

	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/storage"
)

// MaxDiffBytes is the largest side ConflictDetail returns as text.
const MaxDiffBytes = 1 << 20

// ConflictDetail is a saved conflict with both sides' contents for a diff view.
type ConflictDetail struct {
	Conflict   model.Conflict `json:"conflict"`
	LocalText  string         `json:"localText"`
	RemoteText string         `json:"remoteText"`
	// Binary: a side is not valid UTF-8 (no text returned).
	Binary bool `json:"binary"`
	// TooLarge: a side is larger than MaxDiffBytes (no text returned).
	TooLarge bool `json:"tooLarge"`
}

// ManifestETag returns the ETag of the action's manifest ("" if none yet);
// a remote poller compares it to notice changes made by other machines.
func (r *Runner) ManifestETag(ctx context.Context) (string, error) {
	return manifestETag(ctx, r.d.Storage, r.layout)
}

// ConflictDetail returns a saved conflict with both sides' contents. A nil
// side (deleted) has empty text. It fails if the cloud copy changed since the
// conflict was detected.
func (r *Runner) ConflictDetail(ctx context.Context, p string) (ConflictDetail, error) {
	saved, err := r.d.Repo.LoadConflicts(r.action.ID)
	if err != nil {
		return ConflictDetail{}, err
	}
	var c *model.Conflict
	for i := range saved {
		if saved[i].Path == p {
			c = &saved[i]
		}
	}
	if c == nil {
		return ConflictDetail{}, fmt.Errorf("%s is not a conflict of this action", p)
	}
	d := ConflictDetail{Conflict: *c}
	if (c.Local != nil && c.Local.Size > MaxDiffBytes) || (c.Remote != nil && c.Remote.Size > MaxDiffBytes) {
		d.TooLarge = true
		return d, nil
	}
	var local, remote []byte
	if c.Local != nil {
		ms, err := BuildMappings(r.action, r.d.MachineID, r.d.expand)
		if err != nil {
			return d, err
		}
		if abs, ok := ms.LocalPathFor(p); ok {
			local, err = os.ReadFile(abs)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return d, err
			}
		}
	}
	if c.Remote != nil {
		body, _, err := r.d.Storage.Get(ctx, r.layout.Current(p))
		if errors.Is(err, storage.ErrNotFound) {
			return d, fmt.Errorf("%s: the cloud copy changed since the conflict was detected; sync again", p)
		}
		if err != nil {
			return d, err
		}
		if remote, err = r.decode(p, body, c.Remote.Hash); err != nil {
			if errors.Is(err, ErrDecrypt) {
				return d, err
			}
			return d, fmt.Errorf("%s: the cloud copy changed since the conflict was detected; sync again: %w", p, err)
		}
	}
	if len(local) > MaxDiffBytes || len(remote) > MaxDiffBytes {
		d.TooLarge = true
		return d, nil
	}
	if !utf8.Valid(local) || !utf8.Valid(remote) {
		d.Binary = true
		return d, nil
	}
	d.LocalText, d.RemoteText = string(local), string(remote)
	return d, nil
}
