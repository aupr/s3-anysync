package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRunAllowed(t *testing.T) {
	if err := checkRunAllowed(""); err != nil {
		t.Fatal(err)
	}
	if err := checkRunAllowed("agent"); err != nil {
		t.Fatal(err)
	}
	if err := checkRunAllowed("cli"); err == nil || !strings.Contains(err.Error(), agentExeName+" run") {
		t.Fatalf("cli role: %v", err)
	}
}

func TestAutostartTarget(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "s3-anysync-cli.exe")
	agentExe := filepath.Join(dir, agentExeName)
	for _, role := range []string{"", "agent"} {
		if got, err := autostartTarget(cli, role); err != nil || got != cli {
			t.Fatalf("role %q: %q, %v", role, got, err)
		}
	}
	if _, err := autostartTarget(cli, "cli"); err == nil {
		t.Fatal("missing sibling agent accepted")
	}
	if err := os.WriteFile(agentExe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := autostartTarget(cli, "cli"); err != nil || got != agentExe {
		t.Fatalf("cli role: %q, %v", got, err)
	}
}

// In the CLI role the login item needs the sibling agent; the test binary has
// none next to it, so defaultAutostart must be unsupported (nil).
func TestDefaultAutostartCLIRoleWithoutSiblingAgent(t *testing.T) {
	t.Setenv("S3_ANYSYNC_DATA_DIR", "")
	old := role
	role = "cli"
	t.Cleanup(func() { role = old })
	if a := defaultAutostart(); a != nil {
		t.Fatalf("defaultAutostart() = %v in cli role without sibling agent, want nil", a)
	}
}
