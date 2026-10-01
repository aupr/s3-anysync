// Command s3-anysync-agent is the s3-anysync background agent. In this plan
// it only has the debug subcommands; Plan 2 adds the agent run mode.
package main

import (
	"log/slog"
	"os"

	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/secrets"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, openDefault))
}

func openDefault() (*app.App, error) {
	dir, err := app.DefaultDataDir()
	if err != nil {
		return nil, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return app.Open(dir, secrets.NewKeyring(), log)
}
