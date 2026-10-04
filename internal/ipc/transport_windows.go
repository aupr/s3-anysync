//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func currentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// pipeName is unique per user and data folder.
func pipeName(dataDir string) (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return pipeNameFor(sid, dataDir), nil
}

// Listen opens the agent's named pipe, accessible only to the current user.
func Listen(dataDir string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(pipeNameFor(sid, dataDir), &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")"})
}

// Dial connects to the agent of dataDir and checks that the pipe server runs
// as the current user, so an impostor that pre-created the pipe never sees
// the token.
func Dial(ctx context.Context, dataDir string) (net.Conn, error) {
	name, err := pipeName(dataDir)
	if err != nil {
		return nil, err
	}
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := verifyPipeServer(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func verifyPipeServer(conn net.Conn) error {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("ipc: cannot verify the pipe server: connection exposes no handle")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(f.Fd()), &pid); err != nil {
		return fmt.Errorf("ipc: query pipe server: %w", err)
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("ipc: open pipe server process: %w", err)
	}
	defer windows.CloseHandle(proc)
	var tok windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &tok); err != nil {
		return fmt.Errorf("ipc: open pipe server token: %w", err)
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("ipc: read pipe server user: %w", err)
	}
	me, err := currentUserSID()
	if err != nil {
		return err
	}
	if !sameSID(u.User.Sid.String(), me) {
		return errors.New("ipc: the pipe server is not owned by the current user")
	}
	return nil
}

// Cleanup is a no-op: named pipes disappear with their listener.
func Cleanup(string) {}
