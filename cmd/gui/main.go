//go:build cgo || windows

// Command s3-anysync is the desktop GUI. It talks only to the agent.
package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/gui"
	"github.com/niceit/s3-anysync/internal/ipc"
)

//go:embed all:dist
var assets embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "s3-anysync:", err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := app.DefaultDataDir()
	if err != nil {
		return err
	}
	rt := &wailsRuntime{}
	var launch func(string) error
	if p, err := gui.AgentPath(); err == nil {
		launch = gui.Launcher(p)
	} else {
		slog.Warn("cannot start the agent automatically", "err", err)
	}
	conn := gui.NewConn(gui.ConnOptions{
		DataDir: dir,
		Connect: func(ctx context.Context, d string) (gui.Client, error) {
			c, err := ipc.Connect(ctx, d)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		Launch: launch,
		Emit:   rt.emit,
	})
	bound := gui.NewApp(conn, rt, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sum := sha256.Sum256([]byte(dir))
	return wails.Run(&options.App{
		Title:       "s3-anysync",
		Width:       1100,
		Height:      720,
		MinWidth:    900,
		MinHeight:   600,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup: func(wctx context.Context) {
			rt.setContext(wctx)
			go conn.Run(ctx)
		},
		OnShutdown: func(context.Context) { cancel() },
		Bind:       []interface{}{bound},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com.niceit.s3-anysync.gui." + hex.EncodeToString(sum[:8]),
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { rt.focus() },
		},
	})
}
