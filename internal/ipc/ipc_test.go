package ipc_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/storage"
)

var ctx = context.Background()

// shortDir returns a temp dir with a short path (Unix socket paths are limited).
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// serve starts a Manager behind an IPC server and returns a connected client.
func serve(t *testing.T) (*ipc.Client, string) {
	t.Helper()
	dir := shortDir(t)
	a, err := app.Open(dir, secrets.NewMemory(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewMemory()
	a.NewStorage = func(model.Profile, string) (storage.Storage, error) { return store, nil }
	a.LockSettle = 0
	m, err := agent.New(agent.Options{App: a, Version: "ipc-test"})
	if err != nil {
		t.Fatal(err)
	}
	m.Start()
	tok, err := ipc.NewToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := ipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer(m, tok, nil)
	go srv.Serve(l)
	c, err := ipc.Connect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		ipc.Cleanup(dir)
		m.Close()
		a.Close()
	})
	return c, dir
}

func TestEndToEndOverIPC(t *testing.T) {
	c, _ := serve(t)
	st, err := c.Status(ctx)
	if err != nil || st.Version != "ipc-test" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	p, err := c.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s3cr3t-value"})
	if err != nil {
		t.Fatal(err)
	}
	act, err := c.CreateAction(ctx, api.CreateActionRequest{ProfileID: p.ID, Action: model.Action{Name: "Editor", PropagateDeletes: true}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "snippets")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.json"), []byte("a"), 0o644)
	if _, err := c.AddItem(ctx, act.ID, api.AddItemRequest{Item: model.Item{RemoteName: "snippets", Type: model.ItemDir}, LocalPath: dir}); err != nil {
		t.Fatal(err)
	}
	res, err := c.Sync(ctx, act.ID)
	if err != nil || res.Uploaded != 1 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	if _, err := c.Snapshot(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if snaps, err := c.Snapshots(ctx, act.ID); err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %v, %v", snaps, err)
	}
	if p, err := c.PendingDeletes(ctx, act.ID); err != nil || p != nil {
		t.Fatalf("pending = %+v, %v", p, err)
	}
	rules := act.Rules()
	rules.Name = "Renamed"
	if a, err := c.UpdateRules(ctx, act.ID, rules); err != nil || a.Name != "Renamed" {
		t.Fatalf("rules = %+v, %v", a, err)
	}
	if h, err := c.History(ctx, act.ID, 10); err != nil || len(h) == 0 {
		t.Fatalf("history = %v, %v", h, err)
	}
	if err := c.Pause(ctx, act.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.Action(ctx, act.ID); s.State != api.StatePausedUser {
		t.Fatalf("state = %s", s.State)
	}
	if err := c.Resume(ctx, act.ID); err != nil {
		t.Fatal(err)
	}

	// Typed errors survive the trip.
	if _, err := c.Sync(ctx, "nope"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	os.RemoveAll(dir)
	if _, err := c.Sync(ctx, act.ID); !errors.Is(err, engine.ErrRootMissing) {
		t.Fatalf("root missing: %v", err)
	}
	if err := c.LeaveAction(ctx, act.ID, false); err != nil {
		t.Fatal(err)
	}
	if acts, _ := c.Actions(ctx); len(acts) != 0 {
		t.Fatalf("actions after leave = %+v", acts)
	}
}

func TestSecretsNeverInResponses(t *testing.T) {
	_, dir := serve(t)
	tok, _ := ipc.ReadToken(dir)
	c := ipc.NewClient(dir, tok)
	defer c.Close()
	if _, err := c.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s3cr3t-value"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/profiles", "/status", "/actions", "/settings"} {
		if body := rawGet(t, dir, tok, path); strings.Contains(body, "s3cr3t-value") {
			t.Fatalf("secret in GET %s: %s", path, body)
		}
	}
	// Error bodies carry no secret either.
	if body := rawGet(t, dir, tok, "/actions/nope"); !strings.HasPrefix(body, "404") || strings.Contains(body, "s3cr3t-value") {
		t.Fatalf("error response: %s", body)
	}
}

func rawGet(t *testing.T, dir, token, path string) string {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(c context.Context, _, _ string) (net.Conn, error) { return ipc.Dial(c, dir) }}}
	req, _ := http.NewRequest("GET", "http://agent"+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.Status + " " + string(b)
}

func TestWrongTokenIsRejected(t *testing.T) {
	_, dir := serve(t)
	if got := rawGet(t, dir, "wrong", "/status"); !strings.HasPrefix(got, "401") {
		t.Fatalf("wrong token: %s", got)
	}
	if got := rawGet(t, dir, "", "/status"); !strings.HasPrefix(got, "401") {
		t.Fatalf("no token: %s", got)
	}
	c := ipc.NewClient(dir, "wrong")
	defer c.Close()
	if _, err := c.Status(ctx); err == nil {
		t.Fatal("client with a wrong token succeeded")
	}
}

func TestEventsStreamOverIPC(t *testing.T) {
	c, _ := serve(t)
	p, _ := c.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, Secret: "s"})
	act, _ := c.CreateAction(ctx, api.CreateActionRequest{ProfileID: p.ID, Action: model.Action{Name: "E"}})
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := c.Subscribe(sctx)
	if err != nil {
		t.Fatal(err)
	}
	go c.Sync(ctx, act.ID)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("stream closed early")
			}
			if ev.Type == api.EventResult && ev.ActionID == act.ID {
				return
			}
		case <-timeout:
			t.Fatal("no result event over IPC")
		}
	}
}

func TestSecondListenerIsRefused(t *testing.T) {
	_, dir := serve(t)
	if _, err := ipc.Listen(dir); err == nil {
		t.Fatal("second Listen on the same data dir succeeded")
	}
}

// streamSvc is a Service whose only method is an event stream that never ends.
type streamSvc struct{ api.Service }

func (streamSvc) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	ch := make(chan api.Event)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

func TestShutdownEndsOpenEventStreams(t *testing.T) {
	dir := shortDir(t)
	tok, _ := ipc.NewToken(dir)
	l, err := ipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer(streamSvc{}, tok, nil)
	go srv.Serve(l)
	c := ipc.NewClient(dir, tok)
	defer c.Close()
	ch, err := c.Subscribe(ctx) // never cancelled by the client
	if err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(sctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("shutdown took %v", d)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client stream not closed after shutdown")
	}
	ipc.Cleanup(dir)
}
