package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/aupr/s3-anysync/internal/app"
	"github.com/aupr/s3-anysync/internal/datalock"
	"github.com/aupr/s3-anysync/internal/engine"
	"github.com/aupr/s3-anysync/internal/state"
	"github.com/aupr/s3-anysync/internal/storage"
)

func TestCodesRoundTrip(t *testing.T) {
	cases := map[error]string{
		engine.ErrConflictPending:   "conflict-pending",
		engine.ErrPassphraseMissing: "passphrase-missing",
		engine.ErrDecrypt:           "decrypt",
		engine.ErrKeyCheckMissing:   "keycheck-missing",
		engine.ErrRootMissing:       "root-missing",
		storage.ErrLocked:           "locked",
		storage.ErrLockLost:         "lock-lost",
		storage.ErrAccessDenied:     "access-denied",
		storage.ErrNoSuchBucket:     "no-such-bucket",
		app.ErrInUse:                "in-use",
		engine.ErrEditConflict:      "conflict-edit",
		engine.ErrConflictChanged:   "conflict-changed",
		datalock.ErrBusy:            "busy",
		state.ErrNotFound:           "not-found",
		storage.ErrNotFound:         "not-found",
		ErrNotFound:                 "not-found",
		ErrInvalid:                  "invalid",
		engine.ErrInvalidInput:      "invalid",
		engine.ErrProtectedPath:     "protected-path",
		ErrUnsupported:              "unsupported",
		ErrUnavailable:              "unavailable",
		ErrCancelled:                "cancelled",
	}
	for sentinel, code := range cases {
		wrapped := fmt.Errorf("context: %w", sentinel)
		if got := Code(wrapped); got != code {
			t.Errorf("Code(%v) = %q, want %q", sentinel, got, code)
		}
		remote := &RemoteError{Code: code, Message: wrapped.Error()}
		if !errors.Is(remote, sentinel) {
			t.Errorf("RemoteError{%s} does not match %v", code, sentinel)
		}
		if remote.Error() != wrapped.Error() {
			t.Errorf("message lost: %q", remote.Error())
		}
	}
	if Code(errors.New("boom")) != "internal" || Code(nil) != "" {
		t.Error("unexpected code for plain or nil error")
	}
	if Code(errors.Join(errors.New("x"), storage.ErrLockLost)) != "lock-lost" {
		t.Error("joined errors not matched")
	}
}

func TestHTTPStatus(t *testing.T) {
	cases := map[string]int{
		"not-found": http.StatusNotFound, "invalid": http.StatusBadRequest, "conflict-pending": http.StatusConflict,
		"in-use": http.StatusConflict, "locked": http.StatusConflict, "busy": http.StatusConflict, "conflict-edit": http.StatusConflict, "access-denied": http.StatusForbidden,
		"unsupported": http.StatusNotImplemented, "protected-path": http.StatusBadRequest, "unavailable": http.StatusServiceUnavailable, "internal": http.StatusInternalServerError, "decrypt": http.StatusUnprocessableEntity,
	}
	for code, want := range cases {
		if got := HTTPStatus(code); got != want {
			t.Errorf("HTTPStatus(%s) = %d, want %d", code, got, want)
		}
	}
}
