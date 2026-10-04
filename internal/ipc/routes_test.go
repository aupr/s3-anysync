package ipc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// rec is an api.Service that records "Method arg|arg..." per call and returns
// zero values (or err when set).
type rec struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *rec) log(method string, args ...any) error {
	var parts []string
	for _, a := range args {
		b, _ := json.Marshal(a)
		parts = append(parts, string(b))
	}
	r.mu.Lock()
	r.calls = append(r.calls, method+" "+strings.Join(parts, "|"))
	r.mu.Unlock()
	return r.err
}

func (r *rec) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return ""
	}
	return r.calls[len(r.calls)-1]
}

func (r *rec) Status(context.Context) (api.Status, error) { return api.Status{}, r.log("Status") }
func (r *rec) Settings(context.Context) (api.Settings, error) {
	return api.Settings{}, r.log("Settings")
}
func (r *rec) UpdateSettings(_ context.Context, s api.Settings) (api.Settings, error) {
	return s, r.log("UpdateSettings", s)
}
func (r *rec) Profiles(context.Context) ([]model.Profile, error) { return nil, r.log("Profiles") }
func (r *rec) AddProfile(_ context.Context, q api.AddProfileRequest) (model.Profile, error) {
	return model.Profile{}, r.log("AddProfile", q)
}
func (r *rec) UpdateProfile(_ context.Context, q api.UpdateProfileRequest) (model.Profile, error) {
	return model.Profile{}, r.log("UpdateProfile", q)
}
func (r *rec) RemoveProfile(_ context.Context, id string) error { return r.log("RemoveProfile", id) }
func (r *rec) TestProfile(_ context.Context, id string) (storage.ProbeResult, error) {
	return storage.ProbeResult{}, r.log("TestProfile", id)
}
func (r *rec) Discover(_ context.Context, id string) ([]model.Action, error) {
	return nil, r.log("Discover", id)
}
func (r *rec) Actions(context.Context) ([]api.ActionStatus, error) { return nil, r.log("Actions") }
func (r *rec) Action(_ context.Context, id string) (api.ActionStatus, error) {
	return api.ActionStatus{}, r.log("Action", id)
}
func (r *rec) CreateAction(_ context.Context, q api.CreateActionRequest) (model.Action, error) {
	return model.Action{}, r.log("CreateAction", q)
}
func (r *rec) JoinAction(_ context.Context, q api.JoinActionRequest) (model.Action, error) {
	return model.Action{}, r.log("JoinAction", q)
}
func (r *rec) UpdateRules(_ context.Context, id string, q api.UpdateRulesRequest) (model.Action, error) {
	return model.Action{}, r.log("UpdateRules", id, q)
}
func (r *rec) SetMachineDirection(_ context.Context, id string, d model.Direction) error {
	return r.log("SetMachineDirection", id, d)
}
func (r *rec) LeaveAction(_ context.Context, id string, force bool) error {
	return r.log("LeaveAction", id, force)
}
func (r *rec) AddItem(_ context.Context, id string, q api.AddItemRequest) (model.Item, error) {
	return model.Item{}, r.log("AddItem", id, q)
}
func (r *rec) MapItem(_ context.Context, id, item, p string) error {
	return r.log("MapItem", id, item, p)
}
func (r *rec) UnmapItem(_ context.Context, id, item string) error {
	return r.log("UnmapItem", id, item)
}
func (r *rec) RemoveItem(_ context.Context, id, item string) error {
	return r.log("RemoveItem", id, item)
}
func (r *rec) Sync(_ context.Context, id string) (model.SyncResult, error) {
	return model.SyncResult{}, r.log("Sync", id)
}
func (r *rec) Snapshot(_ context.Context, id string) (model.SnapshotInfo, error) {
	return model.SnapshotInfo{}, r.log("Snapshot", id)
}
func (r *rec) Pause(_ context.Context, id string) error      { return r.log("Pause", id) }
func (r *rec) CancelSync(_ context.Context, id string) error { return r.log("CancelSync", id) }
func (r *rec) Resume(_ context.Context, id string) error     { return r.log("Resume", id) }
func (r *rec) PendingDeletes(_ context.Context, id string) (*model.PendingDeletes, error) {
	return nil, r.log("PendingDeletes", id)
}
func (r *rec) ApproveDeletes(_ context.Context, id string, paths []string) (model.SyncResult, error) {
	return model.SyncResult{}, r.log("ApproveDeletes", id, paths)
}
func (r *rec) Snapshots(_ context.Context, id string) ([]model.SnapshotInfo, error) {
	return nil, r.log("Snapshots", id)
}
func (r *rec) Restore(_ context.Context, id string, q api.RestoreRequest) (engine.RestoreResult, error) {
	return engine.RestoreResult{}, r.log("Restore", id, q)
}
func (r *rec) Conflicts(_ context.Context, id string) ([]model.Conflict, error) {
	return nil, r.log("Conflicts", id)
}
func (r *rec) ConflictDetail(_ context.Context, id, p string) (engine.ConflictDetail, error) {
	return engine.ConflictDetail{}, r.log("ConflictDetail", id, p)
}
func (r *rec) Resolve(_ context.Context, id string, q api.ResolveRequest) (model.SyncResult, error) {
	return model.SyncResult{}, r.log("Resolve", id, q)
}
func (r *rec) History(_ context.Context, id string, limit int) ([]model.HistoryEntry, error) {
	return nil, r.log("History", id, limit)
}
func (r *rec) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	return nil, r.log("Subscribe")
}

// startStub serves svc with the given token on a short temp dir.
func startStub(t *testing.T, svc api.Service, token string, log *slog.Logger) string {
	t.Helper()
	dir := shortDir(t)
	l, err := ipc.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer(svc, token, log)
	go srv.Serve(l)
	t.Cleanup(func() {
		srv.Shutdown(context.Background())
		ipc.Cleanup(dir)
	})
	return dir
}

// rawDo sends one request over the socket and returns status and body.
func rawDo(t *testing.T, dir string, req *http.Request) (int, string) {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(c context.Context, _, _ string) (net.Conn, error) { return ipc.Dial(c, dir) }}}
	defer hc.CloseIdleConnections()
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestEveryClientMethodReachesTheRightRoute(t *testing.T) {
	r := &rec{}
	dir := startStub(t, r, "tok", nil)
	c := ipc.NewClient(dir, "tok")
	defer c.Close()

	const weird = "a b?c"
	rules := model.Rules{Name: "R", Direction: model.TwoWay}
	req := api.UpdateRulesRequest{Rules: rules, ExpectedUpdatedAt: time.Date(2026, 10, 4, 1, 2, 3, 4, time.UTC)}
	tests := []struct {
		name string
		call func() error
		want string
	}{
		{"Status", func() error { _, e := c.Status(ctx); return e }, `Status `},
		{"Settings", func() error { _, e := c.Settings(ctx); return e }, `Settings `},
		{"UpdateSettings", func() error {
			_, e := c.UpdateSettings(ctx, api.Settings{MachineName: "m", AutostartOffered: true})
			return e
		},
			`UpdateSettings ` + mj(api.Settings{MachineName: "m", AutostartOffered: true})},
		{"Profiles", func() error { _, e := c.Profiles(ctx); return e }, `Profiles `},
		{"AddProfile", func() error {
			_, e := c.AddProfile(ctx, api.AddProfileRequest{Profile: model.Profile{Name: "p"}, Secret: "s"})
			return e
		}, `AddProfile ` + mj(api.AddProfileRequest{Profile: model.Profile{Name: "p"}, Secret: "s"})},
		{"UpdateProfile", func() error {
			_, e := c.UpdateProfile(ctx, api.UpdateProfileRequest{Profile: model.Profile{ID: "a/b", Name: "p"}, Secret: "s"})
			return e
		}, `UpdateProfile ` + mj(api.UpdateProfileRequest{Profile: model.Profile{ID: "a/b", Name: "p"}, Secret: "s"})},
		{"RemoveProfile", func() error { return c.RemoveProfile(ctx, weird) }, `RemoveProfile "a b?c"`},
		{"TestProfile", func() error { _, e := c.TestProfile(ctx, "a/b"); return e }, `TestProfile "a/b"`},
		{"Discover", func() error { _, e := c.Discover(ctx, weird); return e }, `Discover "a b?c"`},
		{"Actions", func() error { _, e := c.Actions(ctx); return e }, `Actions `},
		{"Action", func() error { _, e := c.Action(ctx, "a/b"); return e }, `Action "a/b"`},
		{"CreateAction", func() error {
			_, e := c.CreateAction(ctx, api.CreateActionRequest{ProfileID: "p", Passphrase: "x"})
			return e
		}, `CreateAction ` + mj(api.CreateActionRequest{ProfileID: "p", Passphrase: "x"})},
		{"JoinAction", func() error {
			_, e := c.JoinAction(ctx, api.JoinActionRequest{ProfileID: "p", ActionID: "a"})
			return e
		}, `JoinAction ` + mj(api.JoinActionRequest{ProfileID: "p", ActionID: "a"})},
		{"UpdateRules", func() error { _, e := c.UpdateRules(ctx, weird, req); return e }, `UpdateRules "a b?c"|` + mj(req)},
		{"SetMachineDirection", func() error { return c.SetMachineDirection(ctx, "a/b", model.TwoWay) }, `SetMachineDirection "a/b"|"two-way"`},
		{"LeaveAction", func() error { return c.LeaveAction(ctx, weird, false) }, `LeaveAction "a b?c"|false`},
		{"LeaveActionForce", func() error { return c.LeaveAction(ctx, "a/b", true) }, `LeaveAction "a/b"|true`},
		{"AddItem", func() error {
			_, e := c.AddItem(ctx, "a/b", api.AddItemRequest{Item: model.Item{RemoteName: "x"}, LocalPath: "/p"})
			return e
		}, `AddItem "a/b"|` + mj(api.AddItemRequest{Item: model.Item{RemoteName: "x"}, LocalPath: "/p"})},
		{"MapItem", func() error { return c.MapItem(ctx, "a/b", weird, `C:\x y\z`) }, `MapItem "a/b"|"a b?c"|"C:\\x y\\z"`},
		{"UnmapItem", func() error { return c.UnmapItem(ctx, "a/b", weird) }, `UnmapItem "a/b"|"a b?c"`},
		{"RemoveItem", func() error { return c.RemoveItem(ctx, "a/b", weird) }, `RemoveItem "a/b"|"a b?c"`},
		{"Sync", func() error { _, e := c.Sync(ctx, weird); return e }, `Sync "a b?c"`},
		{"Snapshot", func() error { _, e := c.Snapshot(ctx, "a/b"); return e }, `Snapshot "a/b"`},
		{"Pause", func() error { return c.Pause(ctx, "a/b") }, `Pause "a/b"`},
		{"Resume", func() error { return c.Resume(ctx, weird) }, `Resume "a b?c"`},
		{"CancelSync", func() error { return c.CancelSync(ctx, weird) }, `CancelSync "a b?c"`},
		{"PendingDeletes", func() error { _, e := c.PendingDeletes(ctx, "a/b"); return e }, `PendingDeletes "a/b"`},
		{"ApproveDeletes", func() error { _, e := c.ApproveDeletes(ctx, "a/b", []string{"x/1", "y z"}); return e },
			`ApproveDeletes "a/b"|["x/1","y z"]`},
		{"Snapshots", func() error { _, e := c.Snapshots(ctx, "a/b"); return e }, `Snapshots "a/b"`},
		{"Restore", func() error {
			_, e := c.Restore(ctx, "a/b", api.RestoreRequest{TS: "t1", Paths: []string{"p"}, To: engine.RestoreBoth})
			return e
		}, `Restore "a/b"|` + mj(api.RestoreRequest{TS: "t1", Paths: []string{"p"}, To: engine.RestoreBoth})},
		{"Conflicts", func() error { _, e := c.Conflicts(ctx, "a/b"); return e }, `Conflicts "a/b"`},
		{"ConflictDetail", func() error { _, e := c.ConflictDetail(ctx, "a/b", "dir/f&x=1 y.txt"); return e },
			`ConflictDetail "a/b"|"dir/f\u0026x=1 y.txt"`},
		{"Resolve", func() error {
			_, e := c.Resolve(ctx, "a/b", resolveReq)
			return e
		}, `Resolve "a/b"|` + mj(resolveReq)},
		{"History", func() error { _, e := c.History(ctx, weird, 7); return e }, `History "a b?c"|7`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err != nil {
				t.Fatal(err)
			}
			if got := r.last(); got != tt.want {
				t.Fatalf("stub saw\n %s\nwant\n %s", got, tt.want)
			}
		})
	}
}

var resolveReq = api.ResolveRequest{
	Choices:  map[string]model.Side{"f": model.SideLocal},
	Reviewed: map[string]api.ConflictVersion{"f": {LocalHash: "l1", RemoteHash: ""}},
}

func mj(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestEmptyServerTokenRejectsEverything(t *testing.T) {
	dir := startStub(t, &rec{}, "", nil)
	for _, h := range []string{"", "Bearer", "Bearer ", "Bearer  "} {
		req, _ := http.NewRequest("GET", "http://agent/status", nil)
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		if code, body := rawDo(t, dir, req); code != 401 || !strings.Contains(body, "unauthorized") {
			t.Fatalf("header %q: %d %s", h, code, body)
		}
	}
	c := ipc.NewClient(dir, "")
	defer c.Close()
	_, err := c.Status(ctx)
	var re *api.RemoteError
	if !errors.As(err, &re) || re.Code != "unauthorized" {
		t.Fatalf("client error = %v", err)
	}
}

func TestOversizedBodyIs413(t *testing.T) {
	dir := startStub(t, &rec{}, "tok", nil)
	big := `{"machineName":"` + strings.Repeat("x", 2<<20) + `"}`
	req, _ := http.NewRequest("PUT", "http://agent/settings", strings.NewReader(big))
	req.Header.Set("Authorization", "Bearer tok")
	code, body := rawDo(t, dir, req)
	var re api.RemoteError
	if code != 413 || json.Unmarshal([]byte(body), &re) != nil || re.Code != "invalid" {
		t.Fatalf("oversized: %d %s", code, body)
	}
}

func TestTrailingGarbageIsRejected(t *testing.T) {
	r := &rec{}
	dir := startStub(t, r, "tok", nil)
	for _, b := range []string{`{"machineName":"m"} x`, `{"machineName":"m"}{"machineName":"n"}`} {
		req, _ := http.NewRequest("PUT", "http://agent/settings", strings.NewReader(b))
		req.Header.Set("Authorization", "Bearer tok")
		if code, body := rawDo(t, dir, req); code != 400 || !strings.Contains(body, "invalid") {
			t.Fatalf("%q: %d %s", b, code, body)
		}
	}
	if r.last() != "" {
		t.Fatalf("service was called: %s", r.last())
	}
	// Unknown fields and trailing whitespace stay fine.
	req, _ := http.NewRequest("PUT", "http://agent/settings", strings.NewReader("{\"machineName\":\"m\",\"future\":1}\n \n"))
	req.Header.Set("Authorization", "Bearer tok")
	if code, body := rawDo(t, dir, req); code != 200 {
		t.Fatalf("tolerant decode: %d %s", code, body)
	}
}

type panicSvc struct{ api.Service }

func (panicSvc) Status(context.Context) (api.Status, error) { panic("boom") }

func TestHandlerPanicIs500AndLogged(t *testing.T) {
	var buf syncBuf
	log := slog.New(slog.NewTextHandler(&buf, nil))
	dir := startStub(t, panicSvc{}, "tok", log)
	req, _ := http.NewRequest("GET", "http://agent/status", nil)
	req.Header.Set("Authorization", "Bearer tok")
	code, body := rawDo(t, dir, req)
	if code != 500 || !strings.Contains(body, `"internal"`) {
		t.Fatalf("panic: %d %s", code, body)
	}
	if out := buf.String(); !strings.Contains(out, "boom") || !strings.Contains(out, "goroutine") {
		t.Fatalf("panic not logged with stack: %s", out)
	}
	// The server keeps serving.
	if code, _ := rawDo(t, dir, req); code != 500 {
		t.Fatalf("second request: %d", code)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
