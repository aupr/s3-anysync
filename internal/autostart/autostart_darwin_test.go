//go:build darwin

package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentEnableDisable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "LaunchAgents")
	m := newAt(dir, "/usr/local/bin/s3-anysync-agent", []string{"run"})
	if on, err := m.Enabled(); err != nil || on {
		t.Fatalf("enabled before Enable: %v %v", on, err)
	}
	if err := m.Enable(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, Label+".plist"))
	if err != nil || !strings.Contains(string(data), "/usr/local/bin/s3-anysync-agent") {
		t.Fatalf("plist = %s, %v", data, err)
	}
	if on, _ := m.Enabled(); !on {
		t.Fatal("not enabled after Enable")
	}
	if err := m.Disable(); err != nil {
		t.Fatal(err)
	}
	if on, _ := m.Enabled(); on {
		t.Fatal("still enabled after Disable")
	}
	if err := m.Disable(); err != nil {
		t.Fatalf("second Disable: %v", err)
	}
}
