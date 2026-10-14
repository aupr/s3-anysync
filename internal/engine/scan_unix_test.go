//go:build unix

package engine

import (
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/aupr/s3-anysync/internal/model"
)

func TestScanFIFO(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.json"), "a")
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	ms := dirMapping(t, dir)
	got, err := scanLocal(ms, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"snippets/a.json"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("keys = %v", keys(got))
	}
	_, err = scanLocal(ms, map[string]model.FileEntry{"snippets/pipe": {Path: "snippets/pipe", Hash: "h"}})
	if err == nil || !strings.Contains(err.Error(), "pipe") {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestScanFileItemFIFO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	ms := fileMapping(t, p)
	got, err := scanLocal(ms, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := scanLocal(ms, map[string]model.FileEntry{"settings.json": {Path: "settings.json", Hash: "h"}}); err == nil {
		t.Fatal("want refusal")
	}
}
