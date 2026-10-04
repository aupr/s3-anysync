package gui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// AgentEnv overrides where the GUI finds the agent executable.
const AgentEnv = "S3_ANYSYNC_AGENT"

func agentName() string {
	if runtime.GOOS == "windows" {
		return "s3-anysync-agent.exe"
	}
	return "s3-anysync-agent"
}

// AgentPath returns $S3_ANYSYNC_AGENT, else the agent next to this executable.
func AgentPath() (string, error) {
	if p := os.Getenv(AgentEnv); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	p := filepath.Join(filepath.Dir(exe), agentName())
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("the agent is not installed next to the GUI (%s); set %s: %w", p, AgentEnv, err)
	}
	return p, nil
}

// Launcher returns a function that starts the agent at path with "run",
// detached from the GUI. The agent inherits the GUI's environment (so
// S3_ANYSYNC_DATA_DIR and S3_ANYSYNC_SECRETS match); the data dir argument
// is that same folder and is not passed again.
func Launcher(path string) func(dataDir string) error {
	return func(string) error {
		cmd := exec.Command(path, "run")
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait() //nolint:errcheck // reaps the agent if it exits while the GUI runs
		return nil
	}
}
