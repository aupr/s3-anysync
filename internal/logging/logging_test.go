package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDailyFileRotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 10, 1, 23, 59, 0, 0, time.Local)
	now := func() time.Time { return day }
	old := filepath.Join(dir, "agent-2026-09-20.log")
	keepMe := filepath.Join(dir, "agent-2026-09-28.log")
	other := filepath.Join(dir, "notes.txt")
	for _, p := range []string{old, keepMe, other} {
		os.WriteFile(p, []byte("x"), 0o600)
	}
	f, err := OpenDaily(dir, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write([]byte("first\n"))
	day = day.Add(2 * time.Minute) // past midnight
	f.Write([]byte("second\n"))

	b1, _ := os.ReadFile(filepath.Join(dir, "agent-2026-10-01.log"))
	b2, _ := os.ReadFile(filepath.Join(dir, "agent-2026-10-02.log"))
	if string(b1) != "first\n" || string(b2) != "second\n" {
		t.Fatalf("day files = %q / %q", b1, b2)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("log older than 7 days not deleted")
	}
	for _, p := range []string{keepMe, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s deleted", p)
		}
	}
}

func TestDailyFilePrunesBoundary(t *testing.T) {
	// With keep=7 and today 2026-10-01, cutoff is 2026-09-24.
	// Files on cutoff date must be KEPT (not before cutoff).
	dir := t.TempDir()
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	now := func() time.Time { return day }
	boundaryFile := filepath.Join(dir, "agent-2026-09-24.log")
	beforeCutoff := filepath.Join(dir, "agent-2026-09-23.log")
	os.WriteFile(boundaryFile, []byte("x"), 0o600)
	os.WriteFile(beforeCutoff, []byte("x"), 0o600)

	f, err := OpenDaily(dir, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write([]byte("log\n"))

	if _, err := os.Stat(boundaryFile); err != nil {
		t.Fatal("boundary file (2026-09-24) should be kept with keep=7 on 2026-10-01")
	}
	if _, err := os.Stat(beforeCutoff); !os.IsNotExist(err) {
		t.Fatal("file before boundary (2026-09-23) should be deleted")
	}
}

func TestLevels(t *testing.T) {
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("unknown level accepted")
	}
	var buf bytes.Buffer
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelWarn)
	log := New(&buf, lv)
	log.Info("hidden")
	log.Warn("shown", "k", "v")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), `"msg":"shown"`) {
		t.Fatalf("log = %s", buf.String())
	}
	lv.Set(slog.LevelInfo)
	log.Info("now visible")
	if !strings.Contains(buf.String(), "now visible") {
		t.Fatal("level change not applied")
	}
}
