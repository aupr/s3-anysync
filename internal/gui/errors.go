// Package gui is the Go side of the desktop GUI: the connection to the agent
// and the methods bound to the frontend. It never opens the data folder.
package gui

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"

	"github.com/aupr/s3-anysync/internal/api"
)

// ErrUnreachable: there is no connection to the agent.
var ErrUnreachable = errors.New("the s3-anysync agent is not running or not reachable")

// encode turns err into an error whose message is the JSON {"code","message"}
// that the frontend parses.
func encode(err error) error {
	if err == nil {
		return nil
	}
	re := api.RemoteError{Code: api.Code(err), Message: err.Error()}
	var remote *api.RemoteError
	switch {
	case errors.As(err, &remote):
		re.Code = remote.Code
	case errors.Is(err, ErrUnreachable):
		re.Code = "unreachable"
	}
	b, _ := json.Marshal(re)
	return errors.New(string(b))
}

// lostConnection reports whether err means the connection to the agent is
// gone (as opposed to the agent answering with an error). unauthorized
// counts: the client already re-read the token once.
func lostConnection(err error) bool {
	var remote *api.RemoteError
	if errors.As(err, &remote) {
		return remote.Code == "unauthorized"
	}
	var ue *url.Error
	var ne net.Error
	return errors.As(err, &ue) || errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF)
}
