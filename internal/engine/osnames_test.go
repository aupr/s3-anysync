package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func withTargetOS(t *testing.T, goos string) {
	t.Helper()
	old := targetOS
	targetOS = goos
	t.Cleanup(func() { targetOS = old })
}

func TestWindowsNameRules(t *testing.T) {
	ms, err := BuildMappings(mappingAction(), "M", identity)
	if err != nil {
		t.Fatal(err)
	}
	withTargetOS(t, "windows")
	bad := []string{"snippets/what?.json", "snippets/a<b", "snippets/x|y", `snippets/q"q`, "snippets/star*", "snippets/CON", "snippets/nul.txt", "snippets/con.d/x", "snippets/Com1.json", "snippets/trail.", "snippets/space ", "snippets/LPT9/x.json"}
	for _, p := range bad {
		if _, ok := ms.LocalPathFor(p); ok {
			t.Errorf("windows accepted %q", p)
		}
		if !ms.Unsupported(p) {
			t.Errorf("Unsupported(%q) = false", p)
		}
	}
	for _, p := range []string{"snippets/ok.json", "snippets/console/x", "snippets/nul2.txt", "settings.json"} {
		if _, ok := ms.LocalPathFor(p); !ok {
			t.Errorf("windows rejected %q", p)
		}
		if ms.Unsupported(p) {
			t.Errorf("Unsupported(%q) = true", p)
		}
	}
	if ms.Unsupported("snippets/x.log") || ms.Unsupported("other/x") {
		t.Error("excluded or unmapped paths reported as unsupported")
	}
	withTargetOS(t, "darwin")
	if _, ok := ms.LocalPathFor("snippets/what?.json"); !ok {
		t.Error("darwin rejected a name that is valid there")
	}
}

func TestUnsupportedNamesAreReportedAndNeverDeleted(t *testing.T) {
	h := newHarness(t, memoryStore)
	a, b := h.machine("A", ""), h.machine("B", "")
	a.write("app/snippets/what?.json", "odd name")
	a.write("app/snippets/ok.json", "ok")
	act := h.create(a, testAction("osnames"))
	expectCounts(t, a.sync(), 2, 0, 0, 0)
	h.join(b, act.ID, defaultPaths)

	withTargetOS(t, "windows")
	res := b.sync()
	expectCounts(t, res, 0, 1, 0, 0)
	if len(res.Unsupported) != 1 || res.Unsupported[0] != "snippets/what?.json" {
		t.Fatalf("unsupported = %v", res.Unsupported)
	}
	if res := b.sync(); res.DeletedRemote != 0 || len(res.Unsupported) != 1 {
		t.Fatalf("second sync = %+v", res)
	}
	withTargetOS(t, "darwin")
	if _, err := h.store.Head(ctx, NewLayout(h.root, act.ID).Current("snippets/what?.json")); err != nil {
		t.Fatalf("cloud copy removed: %v", err)
	}
	expectCounts(t, a.sync(), 0, 0, 0, 0)
}

func TestRemotePathFor(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	a := model.Action{ID: "a", Name: "a", Excludes: []string{"*.log"}, Items: []model.Item{
		{ID: "f", RemoteName: "settings.json", Type: model.ItemFile, LocalPaths: map[string]string{"M": filepath.Join(base, "settings.json")}},
		{ID: "d", RemoteName: "snippets", Type: model.ItemDir, LocalPaths: map[string]string{"M": link}},
	}}
	a.ApplyDefaults()
	ms, err := BuildMappings(a, "M", func(p string) (string, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(base, "settings.json"): "settings.json",
		filepath.Join(link, "a", "b.json"):   "snippets/a/b.json",
		filepath.Join(real, "c.json"):        "snippets/c.json",
	}
	for abs, want := range cases {
		if got, ok := ms.RemotePathFor(abs); !ok || got != want {
			t.Errorf("RemotePathFor(%s) = %q, %v; want %q", abs, got, ok, want)
		}
	}
	for _, abs := range []string{filepath.Join(real, "x.log"), filepath.Join(base, "other.json"), real, link, base} {
		if got, ok := ms.RemotePathFor(abs); ok {
			t.Errorf("RemotePathFor(%s) = %q, want not owned", abs, got)
		}
	}
}
