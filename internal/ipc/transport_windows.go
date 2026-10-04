//go:build windows

package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// pipeName is unique per data folder.
func pipeName(dataDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	return `\\.\pipe\s3-anysync-` + hex.EncodeToString(sum[:])[:16]
}

func currentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// Listen opens the agent's named pipe, accessible only to the current user.
func Listen(dataDir string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(pipeName(dataDir), &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
}

// Dial connects to the agent of dataDir.
func Dial(ctx context.Context, dataDir string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName(dataDir))
}

// Cleanup is a no-op: named pipes disappear with their listener.
func Cleanup(string) {}
