package engine

import (
	"strings"
	"testing"

	"github.com/niceit/s3-anysync/internal/model"
)

func conflictedPair(t *testing.T, act model.Action, aPass, bPass, aContent, bContent string) (*harness, *machine, *machine) {
	t.Helper()
	h := newHarness(t, memoryStore)
	a, b := h.machine("A", aPass), h.machine("B", bPass)
	a.write("app/settings.json", `{"v":1}`)
	act = h.create(a, act)
	a.sync()
	h.join(b, act.ID, defaultPaths)
	b.sync()
	a.write("app/settings.json", aContent)
	a.sync()
	b.write("app/settings.json", bContent)
	if res := b.sync(); res.Outcome != model.OutcomeConflict {
		t.Fatalf("setup: %+v", res)
	}
	return h, a, b
}

func TestConflictDetailText(t *testing.T) {
	_, _, b := conflictedPair(t, testAction("detail"), "", "", `{"v":"A"}`, `{"v":"B!"}`)
	d, err := b.runner.ConflictDetail(ctx, "settings.json")
	if err != nil {
		t.Fatal(err)
	}
	if d.LocalText != `{"v":"B!"}` || d.RemoteText != `{"v":"A"}` || d.Binary || d.TooLarge || d.Conflict.Path != "settings.json" {
		t.Fatalf("detail = %+v", d)
	}
	if _, err := b.runner.ConflictDetail(ctx, "nope.json"); err == nil || !strings.Contains(err.Error(), "not a conflict") {
		t.Fatalf("unknown path: %v", err)
	}
	if etag, err := b.runner.ManifestETag(ctx); err != nil || etag == "" {
		t.Fatalf("ManifestETag = %q, %v", etag, err)
	}
}

func TestConflictDetailEncryptedAndBinary(t *testing.T) {
	act := testAction("detailenc")
	act.Encrypt = true
	_, _, b := conflictedPair(t, act, "pw", "pw", "plain A", "plain BB")
	d, err := b.runner.ConflictDetail(ctx, "settings.json")
	if err != nil || d.RemoteText != "plain A" || d.LocalText != "plain BB" {
		t.Fatalf("encrypted detail = %+v, %v", d, err)
	}
	_, _, c := conflictedPair(t, testAction("detailbin"), "", "", "\xff\xfe A", "\xff\xfe BB")
	d, err = c.runner.ConflictDetail(ctx, "settings.json")
	if err != nil || !d.Binary || d.LocalText != "" || d.RemoteText != "" {
		t.Fatalf("binary detail = %+v, %v", d, err)
	}
}

func TestConflictDetailTooLarge(t *testing.T) {
	big := strings.Repeat("x", MaxDiffBytes+1)
	_, _, b := conflictedPair(t, testAction("detailbig"), "", "", big, big+"y")
	d, err := b.runner.ConflictDetail(ctx, "settings.json")
	if err != nil || !d.TooLarge || d.LocalText != "" || d.RemoteText != "" {
		t.Fatalf("large detail: TooLarge=%v err=%v", d.TooLarge, err)
	}
}

func TestConflictDetailCloudChangedSince(t *testing.T) {
	_, a, b := conflictedPair(t, testAction("detailstale"), "", "", `{"v":"A"}`, `{"v":"B!"}`)
	a.write("app/settings.json", `{"v":"A again"}`)
	a.sync()
	if _, err := b.runner.ConflictDetail(ctx, "settings.json"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("want a 'changed since' error, got %v", err)
	}
}
