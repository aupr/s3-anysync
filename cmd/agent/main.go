// Command s3-anysync-agent is the s3-anysync background agent and its CLI.
// "s3-anysync-agent run" starts the agent; every other command talks to the
// running agent, or works in-process when no agent runs.
package main

import (
	"context"
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, connectDefault))
}

// connectDefault connects to the user's real data folder and keychain.
func connectDefault(ctx context.Context) (api.Service, func(), error) {
	dir, err := app.DefaultDataDir()
	if err != nil {
		return nil, nil, err
	}
	return connectTo(ctx, dir, secrets.NewKeyring(), defaultAutostart(), nil)
}

// defaultAutostart registers this executable with "run"; nil if unsupported.
func defaultAutostart() agent.Autostart {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	// Persist the real path, not a symlink or a translocated app copy.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	m, err := autostart.New(exe, []string{"run"})
	if err != nil {
		return nil
	}
	return m
}
