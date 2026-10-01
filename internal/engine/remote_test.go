package engine

import (
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

func TestLayout(t *testing.T) {
	l := NewLayout("/cfg/", "a1")
	cases := map[string]string{
		l.ActionJSON():                 "cfg/a1/.meta/action.json",
		l.Manifest():                   "cfg/a1/.meta/manifest.json",
		l.Lock():                       "cfg/a1/.meta/lock",
		l.KeyCheck():                   "cfg/a1/.meta/keycheck",
		l.Current("d/x.json"):          "cfg/a1/current/d/x.json",
		l.SnapshotFile("TS", "d/x"):    "cfg/a1/snapshots/TS/d/x",
		l.SnapshotIndex("TS"):          "cfg/a1/snapshots/TS.json",
		NewLayout("", "a1").Manifest(): "a1/.meta/manifest.json",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestManifestRoundTripAndRebuild(t *testing.T) {
	mem := storage.NewMemory()
	l := NewLayout("cfg", "a1")

	m, etag, err := loadManifest(ctx, mem, l)
	if err != nil || etag != "" || m.Files == nil || len(m.Files) != 0 {
		t.Fatalf("empty: %+v %q %v", m, etag, err)
	}

	// Objects written by s3-anysync carry metadata; foreign ones are skipped.
	mem.Put(ctx, l.Current("a.json"), []byte("x"), map[string]string{metaSHA256: "h1", metaSize: "1", metaMTime: "42", metaMachine: "A"})
	mem.Put(ctx, l.Current("foreign.txt"), []byte("y"), nil)
	m, _, err = loadManifest(ctx, mem, l)
	if err != nil || len(m.Files) != 1 || m.Files["a.json"].Hash != "h1" || m.Files["a.json"].MTime != 42 || m.Files["a.json"].Writer != "A" {
		t.Fatalf("rebuilt: %+v %v", m, err)
	}

	m.Files["b.json"] = model.FileEntry{Path: "b.json", Hash: "h2"}
	if err := saveManifest(ctx, mem, l, m); err != nil {
		t.Fatal(err)
	}
	got, etag, err := loadManifest(ctx, mem, l)
	if err != nil || etag == "" || len(got.Files) != 2 {
		t.Fatalf("saved: %+v %q %v", got, etag, err)
	}
	if cur, _ := manifestETag(ctx, mem, l); cur != etag {
		t.Fatalf("manifestETag = %q want %q", cur, etag)
	}

	mem.Put(ctx, l.Manifest(), []byte("not json"), nil)
	got, etag, err = loadManifest(ctx, mem, l)
	if err != nil || etag == "" || len(got.Files) != 1 {
		t.Fatalf("corrupt manifest should be rebuilt from objects: %+v %q %v", got, etag, err)
	}
}
