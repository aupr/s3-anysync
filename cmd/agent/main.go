// Command s3-anysync-agent is the s3-anysync background agent and its CLI.
// "s3-anysync-agent run" starts the agent; every other command talks to the
// running agent, or works in-process when no agent runs.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/autostart"
	"github.com/niceit/s3-anysync/internal/secrets"
)

// The tray (macOS, Windows) must run on the process's main thread.
func init() { runtime.LockOSThread() }

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "run" {
		if err := checkRunAllowed(role); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		os.Exit(runAgentCommand(args[1:], os.Stderr))
	}
	os.Exit(run(args, os.Stdout, os.Stderr, connectDefault))
}

// connectDefault connects to the user's real data folder and keychain.
func connectDefault(ctx context.Context) (api.Service, func(), error) {
	dir, err := app.DefaultDataDir()
	if err != nil {
		return nil, nil, err
	}
	sec, err := secrets.FromEnv(dir)
	if err != nil {
		return nil, nil, err
	}
	return connectTo(ctx, dir, sec, defaultAutostart(), nil)
}

// defaultAutostart registers this executable (the sibling agent in the CLI role) with "run"; nil if unsupported.
// It is also nil whenever S3_ANYSYNC_DATA_DIR is set: the login item is per
// user and starts the default data folder, so a development data folder must
// never touch it.
func defaultAutostart() agent.Autostart {
	if os.Getenv("S3_ANYSYNC_DATA_DIR") != "" {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	// Persist the real path, not a symlink or a translocated app copy.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	target, err := autostartTarget(exe, role)
	if err != nil {
		return nil
	}
	m, err := autostart.New(target, []string{"run"})
	if err != nil {
		return nil
	}
	return m
}
