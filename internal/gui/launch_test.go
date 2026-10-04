package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if out := os.Getenv("GUI_TEST_FAKE_AGENT"); out != "" {
		// Acting as the agent: record the arguments and environment, then exit.
		os.WriteFile(out, []byte(strings.Join(os.Args[1:], " ")+"\n"+os.Getenv("S3_ANYSYNC_SECRETS")), 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLauncherStartsAgentWithRun(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args")
	t.Setenv("GUI_TEST_FAKE_AGENT", out)
	t.Setenv("S3_ANYSYNC_SECRETS", "file")
	if err := Launcher(os.Args[0])("/ignored"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			if got := string(b); got != "run\nfile" {
				t.Fatalf("fake agent saw %q", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent was not started")
}

func TestAgentPath(t *testing.T) {
	t.Setenv(AgentEnv, "/opt/agent")
	if p, err := AgentPath(); err != nil || p != "/opt/agent" {
		t.Fatalf("override = %q, %v", p, err)
	}
	t.Setenv(AgentEnv, "")
	// The test binary has no agent next to it.
	if _, err := AgentPath(); err == nil || !strings.Contains(err.Error(), AgentEnv) {
		t.Fatalf("missing agent: %v", err)
	}
}
