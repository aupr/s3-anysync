// Package ipc carries api.Service between the CLI/GUI and the agent over a
// Unix socket (macOS) or a named pipe (Windows), as JSON over HTTP/1.1 with a
// bearer token and a Server-Sent Events stream.
package ipc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/model"
)

// Server serves an api.Service.
type Server struct {
	svc   api.Service
	token string
	log   *slog.Logger
	hs    *http.Server
	// base is cancelled when Shutdown starts so open event streams end at once
	// (http.Server.Shutdown would otherwise wait for them).
	base       context.Context
	stopStream context.CancelFunc
}

// NewServer returns a server for svc that accepts only the given token.
func NewServer(svc api.Service, token string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{svc: svc, token: token, log: log}
	s.base, s.stopStream = context.WithCancel(context.Background())
	s.hs = &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	return s
}

// Serve serves on l until Shutdown; it then returns http.ErrServerClosed.
func (s *Server) Serve(l net.Listener) error { return s.hs.Serve(l) }

// Shutdown stops accepting requests, ends open event streams and waits for
// running requests; on ctx expiry it closes the remaining connections.
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopStream()
	err := s.hs.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return s.hs.Close()
	}
	return err
}

type handlerFunc func(r *http.Request) (any, error)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn handlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := fn(r)
			writeResult(w, v, err)
		})
	}
	svc := s.svc
	id := func(r *http.Request) string { return r.PathValue("id") }

	h("GET /status", func(r *http.Request) (any, error) { return svc.Status(r.Context()) })
	h("GET /settings", func(r *http.Request) (any, error) { return svc.Settings(r.Context()) })
	h("PUT /settings", func(r *http.Request) (any, error) {
		var in api.Settings
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.UpdateSettings(r.Context(), in)
	})

	h("GET /profiles", func(r *http.Request) (any, error) { return svc.Profiles(r.Context()) })
	h("POST /profiles", func(r *http.Request) (any, error) {
		var in api.AddProfileRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.AddProfile(r.Context(), in)
	})
	h("POST /profiles/{id}/test", func(r *http.Request) (any, error) { return svc.TestProfile(r.Context(), id(r)) })
	h("GET /profiles/{id}/discover", func(r *http.Request) (any, error) { return svc.Discover(r.Context(), id(r)) })

	h("GET /actions", func(r *http.Request) (any, error) { return svc.Actions(r.Context()) })
	h("POST /actions", func(r *http.Request) (any, error) {
		var in api.CreateActionRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.CreateAction(r.Context(), in)
	})
	h("POST /actions/join", func(r *http.Request) (any, error) {
		var in api.JoinActionRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.JoinAction(r.Context(), in)
	})
	h("GET /actions/{id}", func(r *http.Request) (any, error) { return svc.Action(r.Context(), id(r)) })
	h("DELETE /actions/{id}", func(r *http.Request) (any, error) {
		return nil, svc.LeaveAction(r.Context(), id(r), r.URL.Query().Get("force") == "1")
	})
	h("PUT /actions/{id}/rules", func(r *http.Request) (any, error) {
		var in model.Rules
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.UpdateRules(r.Context(), id(r), in)
	})
	h("PUT /actions/{id}/direction", func(r *http.Request) (any, error) {
		var in struct {
			Direction model.Direction `json:"direction"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return nil, svc.SetMachineDirection(r.Context(), id(r), in.Direction)
	})
	h("POST /actions/{id}/items", func(r *http.Request) (any, error) {
		var in api.AddItemRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.AddItem(r.Context(), id(r), in)
	})
	h("PUT /actions/{id}/items/{item}/path", func(r *http.Request) (any, error) {
		var in struct {
			LocalPath string `json:"localPath"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return nil, svc.MapItem(r.Context(), id(r), r.PathValue("item"), in.LocalPath)
	})
	h("DELETE /actions/{id}/items/{item}/path", func(r *http.Request) (any, error) {
		return nil, svc.UnmapItem(r.Context(), id(r), r.PathValue("item"))
	})
	h("DELETE /actions/{id}/items/{item}", func(r *http.Request) (any, error) {
		return nil, svc.RemoveItem(r.Context(), id(r), r.PathValue("item"))
	})

	h("POST /actions/{id}/sync", func(r *http.Request) (any, error) { return svc.Sync(r.Context(), id(r)) })
	h("POST /actions/{id}/snapshot", func(r *http.Request) (any, error) { return svc.Snapshot(r.Context(), id(r)) })
	h("POST /actions/{id}/pause", func(r *http.Request) (any, error) { return nil, svc.Pause(r.Context(), id(r)) })
	h("POST /actions/{id}/resume", func(r *http.Request) (any, error) { return nil, svc.Resume(r.Context(), id(r)) })
	h("GET /actions/{id}/pending-deletes", func(r *http.Request) (any, error) { return svc.PendingDeletes(r.Context(), id(r)) })
	h("POST /actions/{id}/approve-deletes", func(r *http.Request) (any, error) {
		var in struct {
			Paths []string `json:"paths"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.ApproveDeletes(r.Context(), id(r), in.Paths)
	})
	h("GET /actions/{id}/snapshots", func(r *http.Request) (any, error) { return svc.Snapshots(r.Context(), id(r)) })
	h("POST /actions/{id}/restore", func(r *http.Request) (any, error) {
		var in api.RestoreRequest
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.Restore(r.Context(), id(r), in)
	})
	h("GET /actions/{id}/conflicts", func(r *http.Request) (any, error) { return svc.Conflicts(r.Context(), id(r)) })
	h("GET /actions/{id}/conflicts/diff", func(r *http.Request) (any, error) {
		return svc.ConflictDetail(r.Context(), id(r), r.URL.Query().Get("path"))
	})
	h("POST /actions/{id}/resolve", func(r *http.Request) (any, error) {
		var in struct {
			Choices map[string]model.Side `json:"choices"`
		}
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return svc.Resolve(r.Context(), id(r), in.Choices)
	})
	h("GET /actions/{id}/history", func(r *http.Request) (any, error) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		return svc.History(r.Context(), id(r), limit)
	})
	mux.HandleFunc("GET /events", s.events)
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeJSON(w, http.StatusUnauthorized, api.RemoteError{Code: "unauthorized", Message: "missing or wrong IPC token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, api.RemoteError{Code: "internal", Message: "streaming unsupported"})
		return
	}
	// The stream ends when the client goes away or the server shuts down.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(s.base, cancel)
	defer stop()
	ch, err := s.svc.Subscribe(ctx)
	if err != nil {
		writeResult(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-ctx.Done():
			return
		}
	}
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		return fmt.Errorf("%w: %v", api.ErrInvalid, err)
	}
	return nil
}

func writeResult(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := api.Code(err)
		writeJSON(w, api.HTTPStatus(code), api.RemoteError{Code: code, Message: err.Error()})
		return
	}
	if v == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
