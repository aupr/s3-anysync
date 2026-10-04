package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/secrets"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// ipcConnectTimeout bounds the first contact with a running agent.
var ipcConnectTimeout = 10 * time.Second

// connector opens the service for one CLI command; the returned func closes it.
type connector func(ctx context.Context) (api.Service, func(), error)

// connectTo serves the command in this process when it can take the data
// folder, or through the running agent when the agent owns it. configure
// adjusts an in-process App (tests replace its storage).
func connectTo(ctx context.Context, dataDir string, sec secrets.Store, auto agent.Autostart, configure func(*app.App)) (api.Service, func(), error) {
	lock, err := datalock.Acquire(dataDir)
	if errors.Is(err, datalock.ErrBusy) {
		cctx, cancel := context.WithTimeout(ctx, ipcConnectTimeout)
		defer cancel()
		c, err := ipc.Connect(cctx, dataDir)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(cctx.Err(), context.DeadlineExceeded) {
				return nil, nil, fmt.Errorf("the agent owns the data folder but does not answer (timed out after %s; it may be starting or stopping): %w", ipcConnectTimeout, err)
			}
			return nil, nil, fmt.Errorf("the agent owns the data folder but does not answer (it may be starting or stopping): %w", err)
		}
		return c, c.Close, nil
	}
	if err != nil {
		return nil, nil, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a, err := app.Open(dataDir, sec, log)
	if err != nil {
		lock.Release()
		return nil, nil, err
	}
	if configure != nil {
		configure(a)
	}
	m, err := agent.New(agent.Options{App: a, Autostart: auto, Version: version, Log: log})
	if err == nil {
		if err = m.Start(); err != nil {
			m.Close()
		}
	}
	if err != nil {
		a.Close()
		lock.Release()
		return nil, nil, err
	}
	return m, func() { m.Close(); a.Close(); lock.Release() }, nil
}
