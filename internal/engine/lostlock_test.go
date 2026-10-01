package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// loseLockOnPut cancels a context with ErrLockLost right after the first Put
// of a file under current/, as if the lock renewal had failed.
type loseLockOnPut struct {
	*storage.Memory
	cancel context.CancelCauseFunc
	prefix string
}

func (s *loseLockOnPut) Put(c context.Context, key string, body []byte, meta map[string]string) (storage.Object, error) {
	obj, err := s.Memory.Put(c, key, body, meta)
	if err == nil && len(key) >= len(s.prefix) && key[:len(s.prefix)] == s.prefix {
		s.cancel(storage.ErrLockLost)
	}
	return obj, err
}

func TestApplySkipsManifestAndBaselineWhenLockIsLost(t *testing.T) {
	mem := storage.NewMemory()
	l := NewLayout("cfg", "act")
	lockCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	d := memDeps(mem, "A")
	d.Storage = &loseLockOnPut{Memory: mem, cancel: cancel, prefix: l.CurrentPrefix()}
	d.Repo = newDB(t)
	a := basicAction("act")

	home := t.TempDir()
	d.Expand = func(p string) (string, error) { return p, nil }
	a.Items[0].LocalPaths["A"] = home + "/settings.json"
	writeFile(t, home+"/settings.json", `{"v":1}`)
	r := NewRunner(d, "cfg", a)
	ms, err := BuildMappings(a, "A", d.Expand)
	if err != nil {
		t.Fatal(err)
	}
	local, err := scanLocal(ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := local["settings.json"]
	ops := []Op{{Path: "settings.json", Kind: OpUpload, Local: &entry}, {Path: "zzz-never-uploaded.json", Kind: OpDeleteRemote}}
	manifest := model.Manifest{Files: map[string]model.FileEntry{}}

	_, err = r.apply(lockCtx, ms, ops, map[string]model.FileEntry{}, &manifest)
	if !errors.Is(err, storage.ErrLockLost) {
		t.Fatalf("want ErrLockLost, got %v", err)
	}
	if _, err := mem.Head(context.Background(), l.Manifest()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("manifest was written although the lock was lost (err=%v)", err)
	}
	if b, _ := d.Repo.LoadBaseline("act"); len(b) != 0 {
		t.Fatalf("baseline was saved although the lock was lost: %+v", b)
	}
}
