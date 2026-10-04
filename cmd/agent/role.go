package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// role is set at build time (-ldflags "-X main.role=..."): "" for the single
// macOS/dev binary, "agent" for the windowless Windows agent, "cli" for the
// Windows console CLI.
var role = ""

// agentExeName is the Windows agent executable installed next to the CLI.
const agentExeName = "s3-anysync-agent.exe"

// checkRunAllowed refuses "run" in the CLI build: the agent must run from the
// windowless executable so login never opens a console window.
func checkRunAllowed(role string) error {
	if role == "cli" {
		return fmt.Errorf("this is the command-line tool; start the agent with %s run", agentExeName)
	}
	return nil
}

// autostartTarget is the executable the login item starts: the running one,
// except for the CLI build, which registers the agent next to it.
func autostartTarget(exe, role string) (string, error) {
	if role != "cli" {
		return exe, nil
	}
	p := filepath.Join(filepath.Dir(exe), agentExeName)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("the agent is not installed next to the command-line tool (%s): %w", p, err)
	}
	return p, nil
}
