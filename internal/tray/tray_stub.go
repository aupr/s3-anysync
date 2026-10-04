//go:build !cgo && !windows

package tray

import (
	"context"

	"github.com/niceit/s3-anysync/internal/api"
)

// Available reports whether this build has a real tray.
func Available() bool { return false }

// Run has no tray to show in this build: it waits for ctx.
func Run(ctx context.Context, svc api.Service, o Options) error {
	<-ctx.Done()
	return nil
}
