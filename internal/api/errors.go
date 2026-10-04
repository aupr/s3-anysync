package api

import (
	"errors"
	"net/http"

	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

var (
	// ErrNotFound: the profile, action, item or snapshot does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: the request is malformed.
	ErrInvalid = errors.New("invalid request")
	// ErrUnsupported: the operation is not available on this system.
	ErrUnsupported = errors.New("not supported on this system")
)

// codes lists error codes with the sentinels they stand for, most specific first.
var codes = []struct {
	code   string
	status int
	errs   []error
}{
	{"conflict-pending", http.StatusConflict, []error{engine.ErrConflictPending}},
	{"passphrase-missing", http.StatusUnprocessableEntity, []error{engine.ErrPassphraseMissing}},
	{"decrypt", http.StatusUnprocessableEntity, []error{engine.ErrDecrypt}},
	{"keycheck-missing", http.StatusUnprocessableEntity, []error{engine.ErrKeyCheckMissing}},
	{"root-missing", http.StatusUnprocessableEntity, []error{engine.ErrRootMissing}},
	{"locked", http.StatusConflict, []error{storage.ErrLocked}},
	{"lock-lost", http.StatusConflict, []error{storage.ErrLockLost}},
	{"access-denied", http.StatusForbidden, []error{storage.ErrAccessDenied}},
	{"no-such-bucket", http.StatusUnprocessableEntity, []error{storage.ErrNoSuchBucket}},
	{"busy", http.StatusConflict, []error{datalock.ErrBusy}},
	{"not-found", http.StatusNotFound, []error{ErrNotFound, state.ErrNotFound, storage.ErrNotFound}},
	{"invalid", http.StatusBadRequest, []error{ErrInvalid}},
	{"unsupported", http.StatusNotImplemented, []error{ErrUnsupported}},
}

// Code returns the stable code of err ("internal" if none matches, "" for nil).
func Code(err error) string {
	if err == nil {
		return ""
	}
	for _, c := range codes {
		for _, e := range c.errs {
			if errors.Is(err, e) {
				return c.code
			}
		}
	}
	return "internal"
}

// HTTPStatus is the HTTP status the IPC server uses for a code.
func HTTPStatus(code string) int {
	for _, c := range codes {
		if c.code == code {
			return c.status
		}
	}
	return http.StatusInternalServerError
}

// RemoteError is an error reported by the agent over IPC. errors.Is matches
// the sentinel errors of its code, so callers handle it like a local error.
type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RemoteError) Error() string { return e.Message }

// Unwrap returns the sentinel errors the code stands for.
func (e *RemoteError) Unwrap() []error {
	for _, c := range codes {
		if c.code == e.Code {
			return c.errs
		}
	}
	return nil
}
