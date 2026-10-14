package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aupr/s3-anysync/internal/api"
	"github.com/aupr/s3-anysync/internal/engine"
	"github.com/aupr/s3-anysync/internal/model"
)

// fakeClient is an agent connection; only the methods below are used.
type fakeClient struct {
	api.Service
	events chan api.Event
	gone   chan struct{} // closed: the agent died
	closed atomic.Bool
	status api.Status
	err    error
}

func newFake() *fakeClient {
	return &fakeClient{events: make(chan api.Event), gone: make(chan struct{}), status: api.Status{MachineName: "mac"}}
}

func (f *fakeClient) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	out := make(chan api.Event)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-f.gone:
				return
			case ev := <-f.events:
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (f *fakeClient) Close()                                     { f.closed.Store(true) }
func (f *fakeClient) Status(context.Context) (api.Status, error) { return f.status, f.err }

type emitted struct {
	name string
	data any
}

type recorder struct {
	mu   sync.Mutex
	list []emitted
}

func (r *recorder) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, emitted{name, data})
}

func (r *recorder) find(f func(emitted) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.list {
		if f(e) {
			return true
		}
	}
	return false
}

func (r *recorder) count(f func(emitted) bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.list {
		if f(e) {
			n++
		}
	}
	return n
}

func connected(want bool) func(emitted) bool {
	return func(e emitted) bool {
		s, ok := e.data.(ConnState)
		return e.name == EventConnection && ok && s.Connected == want
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// start runs a Conn with fast timings; connect is called for each attempt.
func start(t *testing.T, connect func() (Client, error), launch func(string) error) (*Conn, *recorder) {
	t.Helper()
	rec := &recorder{}
	c := NewConn(ConnOptions{
		DataDir:   "/data",
		Connect:   func(context.Context, string) (Client, error) { return connect() },
		Launch:    launch,
		Emit:      rec.emit,
		Retry:     10 * time.Millisecond,
		StartWait: 60 * time.Millisecond,
		Poll:      5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return c, rec
}

func decodeErr(t *testing.T, err error) api.RemoteError {
	t.Helper()
	var re api.RemoteError
	if err == nil || json.Unmarshal([]byte(err.Error()), &re) != nil {
		t.Fatalf("not a JSON error: %v", err)
	}
	return re
}

func TestConnectsAndForwardsEvents(t *testing.T) {
	f := newFake()
	c, rec := start(t, func() (Client, error) { return f, nil }, nil)
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	a := NewApp(c, nil, "/data", "dev")
	if st, err := a.Status(); err != nil || st.MachineName != "mac" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	f.events <- api.Event{Type: api.EventState, ActionID: "a1"}
	waitFor(t, "event forwarded", func() bool {
		return rec.find(func(e emitted) bool {
			ev, ok := e.data.(api.Event)
			return e.name == EventAgent && ok && ev.ActionID == "a1"
		})
	})
	if !a.ConnectionState().Connected {
		t.Fatal("ConnectionState not connected")
	}
}

func TestStartsTheAgentOnce(t *testing.T) {
	var launched atomic.Int32
	f := newFake()
	c, rec := start(t, func() (Client, error) {
		if launched.Load() == 0 {
			return nil, errors.New("no agent")
		}
		return f, nil
	}, func(dir string) error {
		if dir != "/data" {
			t.Errorf("launch dir = %q", dir)
		}
		launched.Add(1)
		return nil
	})
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	if launched.Load() != 1 {
		t.Fatalf("launched %d times", launched.Load())
	}
	_ = c
}

func TestAgentThatNeverStarts(t *testing.T) {
	var launched atomic.Int32
	c, rec := start(t, func() (Client, error) { return nil, errors.New("no agent") },
		func(string) error { launched.Add(1); return nil })
	waitFor(t, "start timeout reported", func() bool {
		return rec.find(func(e emitted) bool {
			s, ok := e.data.(ConnState)
			return ok && !s.Connected && strings.Contains(s.Reason, "did not start")
		})
	})
	time.Sleep(50 * time.Millisecond) // several plain retries
	if n := launched.Load(); n != 1 {
		t.Fatalf("plain retries launched the agent: %d", n)
	}
	c.Retry()
	waitFor(t, "explicit retry launches again", func() bool { return launched.Load() == 2 })
	a := NewApp(c, nil, "/data", "dev")
	if _, err := a.Status(); decodeErr(t, err).Code != "unreachable" {
		t.Fatalf("Status while down: %v", err)
	}
}

func TestReconnectsAfterTheAgentDies(t *testing.T) {
	first, second := newFake(), newFake()
	var n atomic.Int32
	c, rec := start(t, func() (Client, error) {
		if n.Add(1) == 1 {
			return first, nil
		}
		return second, nil
	}, nil)
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	close(first.gone)
	waitFor(t, "disconnect reported", func() bool { return rec.find(connected(false)) })
	waitFor(t, "reconnected", func() bool { return rec.count(connected(true)) == 2 })
	if !first.closed.Load() {
		t.Fatal("dead client not closed")
	}
	if s, _ := c.Service(); s != second {
		t.Fatal("not using the new client")
	}
}

func TestTransportErrorDropsTheSession(t *testing.T) {
	f := newFake()
	f.err = &url.Error{Op: "Get", URL: "http://agent/status", Err: errors.New("connection refused")}
	c, rec := start(t, func() (Client, error) { return f, nil }, nil)
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	a := NewApp(c, nil, "/data", "dev")
	if _, err := a.Status(); decodeErr(t, err).Code != "unreachable" {
		t.Fatalf("Status = %v", err)
	}
	waitFor(t, "session dropped", func() bool { return rec.find(connected(false)) })
}

func TestRetryWhileConnectedDoesNotLaunchOnReconnect(t *testing.T) {
	first, second := newFake(), newFake()
	var n, launched atomic.Int32
	c, rec := start(t, func() (Client, error) {
		switch n.Add(1) {
		case 1:
			return first, nil
		case 2:
			return nil, errors.New("down")
		}
		return second, nil
	}, func(string) error { launched.Add(1); return nil })
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	c.Retry()
	close(first.gone)
	waitFor(t, "reconnected", func() bool { return rec.count(connected(true)) == 2 })
	if l := launched.Load(); l != 0 {
		t.Fatalf("a Retry while connected launched the agent %d times on reconnect", l)
	}
}

func TestLostDoesNotSwallowRetry(t *testing.T) {
	var launched, calls atomic.Int32
	var armed atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	c, rec := start(t, func() (Client, error) {
		calls.Add(1)
		if launched.Load() >= 1 && armed.CompareAndSwap(true, false) {
			entered <- struct{}{}
			<-release
		}
		return nil, errors.New("no agent")
	}, func(string) error { launched.Add(1); return nil })
	waitFor(t, "start timeout reported", func() bool {
		return rec.find(func(e emitted) bool {
			s, ok := e.data.(ConnState)
			return ok && strings.Contains(s.Reason, "did not start")
		})
	})
	armed.Store(true)
	<-entered
	c.Lost(0)
	c.Retry()
	close(release)
	waitFor(t, "retry launches", func() bool { return launched.Load() == 2 })
}

func TestStaleLostKeepsTheCurrentSession(t *testing.T) {
	first, second := newFake(), newFake()
	var n atomic.Int32
	c, rec := start(t, func() (Client, error) {
		if n.Add(1) == 1 {
			return first, nil
		}
		return second, nil
	}, nil)
	waitFor(t, "connected", func() bool { return rec.find(connected(true)) })
	_, oldGen, err := c.service()
	if err != nil {
		t.Fatal(err)
	}
	close(first.gone)
	waitFor(t, "reconnected", func() bool { return rec.count(connected(true)) == 2 })
	c.Lost(oldGen)
	time.Sleep(50 * time.Millisecond)
	if !c.State().Connected || second.closed.Load() {
		t.Fatal("a stale Lost ended the healthy session")
	}
	_, gen, _ := c.service()
	c.Lost(gen)
	waitFor(t, "current Lost drops the session", func() bool { return second.closed.Load() })
}

func TestLostConnection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unauthorized", &api.RemoteError{Code: "unauthorized"}, true},
		{"other remote", &api.RemoteError{Code: "in-use"}, false},
		{"url error", &url.Error{Op: "Get", URL: "http://a", Err: errors.New("refused")}, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"EOF", io.EOF, false},
		{"plain", errors.New("boom"), false},
		{"wrapped url error", fmt.Errorf("call: %w", &url.Error{Op: "Get", URL: "http://a", Err: errors.New("refused")}), true},
	}
	for _, tc := range cases {
		if got := lostConnection(tc.err); got != tc.want {
			t.Errorf("%s: lostConnection = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestErrorEncoding(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&api.RemoteError{Code: "in-use", Message: "used by Editor"}, "in-use"},
		{&api.RemoteError{Code: "unauthorized", Message: "bad token"}, "unauthorized"},
		{engine.ErrConflictPending, "conflict-pending"},
		{ErrUnreachable, "unreachable"},
		{errors.New("boom"), "internal"},
	}
	for _, tc := range cases {
		re := decodeErr(t, encode(tc.err))
		if re.Code != tc.code || re.Message != tc.err.Error() {
			t.Errorf("encode(%v) = %+v, want code %s", tc.err, re, tc.code)
		}
	}
	if encode(nil) != nil {
		t.Fatal("encode(nil) != nil")
	}
}

// Every api.Service method except Subscribe is bound with the same name and
// the same parameters minus the context.
func TestBindingsCoverTheService(t *testing.T) {
	svc := reflect.TypeOf((*api.Service)(nil)).Elem()
	app := reflect.TypeOf(&App{})
	for i := 0; i < svc.NumMethod(); i++ {
		m := svc.Method(i)
		if m.Name == "Subscribe" {
			continue
		}
		b, ok := app.MethodByName(m.Name)
		if !ok {
			t.Errorf("App lacks %s", m.Name)
			continue
		}
		// b.Type has the receiver first; m.Type has ctx first.
		if b.Type.NumIn()-1 != m.Type.NumIn()-1 {
			t.Errorf("%s: %d params, want %d", m.Name, b.Type.NumIn()-1, m.Type.NumIn()-1)
			continue
		}
		for j := 1; j < m.Type.NumIn(); j++ {
			if b.Type.In(j) != m.Type.In(j) {
				t.Errorf("%s param %d = %v, want %v", m.Name, j, b.Type.In(j), m.Type.In(j))
			}
		}
		if b.Type.NumOut() != m.Type.NumOut() {
			t.Errorf("%s: %d results, want %d", m.Name, b.Type.NumOut(), m.Type.NumOut())
		}
	}
}

func TestPreviewSchedule(t *testing.T) {
	a := NewApp(NewConn(ConnOptions{}), nil, "/data", "dev")
	a.now = func() time.Time { return time.Date(2026, 10, 4, 8, 0, 0, 0, time.Local) }
	runs, err := a.PreviewSchedule(model.Schedule{Kind: model.ScheduleInterval, EveryMinutes: 15})
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	want := time.Date(2026, 10, 4, 8, 15, 0, 0, time.Local).Format(time.RFC3339)
	if runs[0] != want {
		t.Fatalf("first run %s, want %s", runs[0], want)
	}
	for _, bad := range []model.Schedule{{Kind: model.ScheduleCron, Cron: "nope"}, {Kind: model.ScheduleCron, Cron: "@every 1s"}} {
		if _, err := a.PreviewSchedule(bad); decodeErr(t, err).Code != "invalid" {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if runs, err := a.PreviewSchedule(model.Schedule{Kind: model.ScheduleNone}); err != nil || len(runs) != 0 {
		t.Fatalf("none = %v, %v", runs, err)
	}
}

type fakeDialogs struct{ titles []string }

func (d *fakeDialogs) PickFile(title string) (string, error) {
	d.titles = append(d.titles, title)
	return "/f", nil
}
func (d *fakeDialogs) PickFolder(title string) (string, error) {
	d.titles = append(d.titles, title)
	return "/d", nil
}

func TestLocalHelpers(t *testing.T) {
	d := &fakeDialogs{}
	a := NewApp(NewConn(ConnOptions{}), d, "/data", "dev")
	var opened []string
	a.open = func(p string) error { opened = append(opened, p); return nil }
	if p, err := a.PickFolder("Pick folder"); err != nil || p != "/d" {
		t.Fatal(p, err)
	}
	if p, err := a.PickFile("Pick file"); err != nil || p != "/f" {
		t.Fatal(p, err)
	}
	if want := []string{"Pick folder", "Pick file"}; !reflect.DeepEqual(d.titles, want) {
		t.Fatalf("dialog titles = %v, want %v", d.titles, want)
	}
	if err := a.OpenDataFolder(); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenLogFolder(); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 || opened[0] != "/data" || !strings.HasSuffix(opened[1], "logs") {
		t.Fatalf("opened = %v", opened)
	}
	if a.DataDir() != "/data" {
		t.Fatal(a.DataDir())
	}
}

func TestVersion(t *testing.T) {
	if got := NewApp(NewConn(ConnOptions{}), nil, "/data", "1.2.3").Version(); got != "1.2.3" {
		t.Fatalf("Version = %q", got)
	}
}
