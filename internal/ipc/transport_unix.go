//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

const sockName = "agent.sock"

// maxSockPath is the usable length of sun_path on macOS (104 bytes incl. NUL).
const maxSockPath = 103

func socketPath(dataDir string) string { return filepath.Join(dataDir, sockName) }

// Listen opens the agent socket in dataDir (mode 0600), replacing a stale one.
func Listen(dataDir string) (net.Listener, error) {
	p := socketPath(dataDir)
	if len(p) > maxSockPath {
		return nil, fmt.Errorf("ipc: socket path %q is too long (%d > %d bytes); use a shorter S3_ANYSYNC_DATA_DIR", p, len(p), maxSockPath)
	}
	if c, err := net.DialTimeout("unix", p, time.Second); err == nil {
		c.Close()
		return nil, errors.New("ipc: another agent is already listening on " + p)
	}
	os.Remove(p) // stale socket of a crashed agent
	l, err := net.Listen("unix", p)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Dial connects to the agent of dataDir.
func Dial(ctx context.Context, dataDir string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath(dataDir))
}

// Cleanup removes the socket file after the server stopped.
func Cleanup(dataDir string) { os.Remove(socketPath(dataDir)) }
