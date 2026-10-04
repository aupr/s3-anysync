package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niceit/s3-anysync/internal/autostart"
)

func TestDefaultAutostartUnsupportedUnderDevDataFolder(t *testing.T) {
	t.Setenv("S3_ANYSYNC_DATA_DIR", t.TempDir())
	if a := defaultAutostart(); a != nil {
		t.Fatalf("defaultAutostart() = %v with S3_ANYSYNC_DATA_DIR set, want nil", a)
	}
}

func TestDefaultAutostartSupportedWithoutEnv(t *testing.T) {
	t.Setenv("S3_ANYSYNC_DATA_DIR", "")
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if _, err := autostart.New(filepath.Clean(exe), []string{"run"}); err != nil {
		t.Skipf("autostart unsupported on this OS: %v", err)
	}
	// Constructing only; Enable/Disable are never called.
	if defaultAutostart() == nil {
		t.Fatal("defaultAutostart() = nil without S3_ANYSYNC_DATA_DIR on a supported OS")
	}
}
