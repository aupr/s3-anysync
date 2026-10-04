package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

// Client is api.Service over IPC.
//
// Cancelling the ctx of a call drops the job if it is still queued in the
// agent; a job that already runs is not cancelled (the agent finishes it, or
// cancels it at shutdown), so only the wait for the answer ends. A wrong or missing token yields a *api.RemoteError with
// Code "unauthorized"; callers (the CLI) should check that code rather than
// use errors.Is, as it stands for no sentinel error. A 401 makes the client
// re-read the token file once (the agent rotates it at every start) and retry.
type Client struct {
	hc      *http.Client
	dataDir string
	mu      sync.Mutex
	token   string
}

func (c *Client) currentToken() string { c.mu.Lock(); defer c.mu.Unlock(); return c.token }

// refreshToken re-reads the agent's token; it reports whether it changed.
func (c *Client) refreshToken() bool {
	tok, err := ReadToken(c.dataDir)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if tok == c.token {
		return false
	}
	c.token = tok
	return true
}

var _ api.Service = (*Client)(nil)

// NewClient returns a client for the agent of dataDir.
func NewClient(dataDir, token string) *Client {
	tr := &http.Transport{
		DialContext:        func(ctx context.Context, _, _ string) (net.Conn, error) { return Dial(ctx, dataDir) },
		DisableCompression: true,
	}
	return &Client{hc: &http.Client{Transport: tr}, dataDir: dataDir, token: token}
}

// Connect reads the agent's token and checks that the agent answers.
func Connect(ctx context.Context, dataDir string) (*Client, error) {
	tok, err := ReadToken(dataDir)
	if err != nil {
		return nil, fmt.Errorf("ipc: read token: %w", err)
	}
	c := NewClient(dataDir, tok)
	if _, err := c.Status(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

func esc(s string) string { return url.PathEscape(s) }

func (c *Client) request(ctx context.Context, method, path string, in any) (*http.Response, error) {
	var b []byte
	if in != nil {
		var err error
		if b, err = json.Marshal(in); err != nil {
			return nil, err
		}
	}
	send := func() (*http.Response, error) {
		var body io.Reader
		if b != nil {
			body = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.currentToken())
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ipc: %w", err)
		}
		return resp, nil
	}
	resp, err := send()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// Re-read the token only to learn whether the agent rotated it; if it
		// did not change, the 401 answer is reported as it is.
		if c.refreshToken() {
			resp.Body.Close()
			if resp, err = send(); err != nil {
				return nil, err
			}
		}
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var re api.RemoteError
		if json.NewDecoder(resp.Body).Decode(&re) == nil && re.Code != "" {
			return nil, &re
		}
		return nil, fmt.Errorf("ipc: %s", resp.Status)
	}
	return resp, nil
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	resp, err := c.request(ctx, method, path, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Status(ctx context.Context) (api.Status, error) {
	var v api.Status
	return v, c.call(ctx, "GET", "/status", nil, &v)
}

func (c *Client) Settings(ctx context.Context) (api.Settings, error) {
	var v api.Settings
	return v, c.call(ctx, "GET", "/settings", nil, &v)
}

func (c *Client) UpdateSettings(ctx context.Context, s api.Settings) (api.Settings, error) {
	var v api.Settings
	return v, c.call(ctx, "PUT", "/settings", s, &v)
}

func (c *Client) Profiles(ctx context.Context) ([]model.Profile, error) {
	var v []model.Profile
	return v, c.call(ctx, "GET", "/profiles", nil, &v)
}

func (c *Client) AddProfile(ctx context.Context, req api.AddProfileRequest) (model.Profile, error) {
	var v model.Profile
	return v, c.call(ctx, "POST", "/profiles", req, &v)
}

func (c *Client) UpdateProfile(ctx context.Context, req api.UpdateProfileRequest) (model.Profile, error) {
	var v model.Profile
	return v, c.call(ctx, "PUT", "/profiles/"+esc(req.Profile.ID), req, &v)
}

func (c *Client) RemoveProfile(ctx context.Context, id string) error {
	return c.call(ctx, "DELETE", "/profiles/"+esc(id), nil, nil)
}

func (c *Client) TestProfile(ctx context.Context, id string) (storage.ProbeResult, error) {
	var v storage.ProbeResult
	return v, c.call(ctx, "POST", "/profiles/"+esc(id)+"/test", nil, &v)
}

func (c *Client) Discover(ctx context.Context, profileID string) ([]model.Action, error) {
	var v []model.Action
	return v, c.call(ctx, "GET", "/profiles/"+esc(profileID)+"/discover", nil, &v)
}

func (c *Client) Actions(ctx context.Context) ([]api.ActionStatus, error) {
	var v []api.ActionStatus
	return v, c.call(ctx, "GET", "/actions", nil, &v)
}

func (c *Client) Action(ctx context.Context, id string) (api.ActionStatus, error) {
	var v api.ActionStatus
	return v, c.call(ctx, "GET", "/actions/"+esc(id), nil, &v)
}

func (c *Client) CreateAction(ctx context.Context, req api.CreateActionRequest) (model.Action, error) {
	var v model.Action
	return v, c.call(ctx, "POST", "/actions", req, &v)
}

func (c *Client) JoinAction(ctx context.Context, req api.JoinActionRequest) (model.Action, error) {
	var v model.Action
	return v, c.call(ctx, "POST", "/actions/join", req, &v)
}

func (c *Client) UpdateRules(ctx context.Context, id string, req api.UpdateRulesRequest) (model.Action, error) {
	var v model.Action
	return v, c.call(ctx, "PUT", "/actions/"+esc(id)+"/rules", req, &v)
}

func (c *Client) SetMachineDirection(ctx context.Context, id string, d model.Direction) error {
	return c.call(ctx, "PUT", "/actions/"+esc(id)+"/direction", map[string]model.Direction{"direction": d}, nil)
}

func (c *Client) LeaveAction(ctx context.Context, id string, force bool) error {
	path := "/actions/" + esc(id)
	if force {
		path += "?force=1"
	}
	return c.call(ctx, "DELETE", path, nil, nil)
}

func (c *Client) AddItem(ctx context.Context, id string, req api.AddItemRequest) (model.Item, error) {
	var v model.Item
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/items", req, &v)
}

func (c *Client) MapItem(ctx context.Context, id, itemID, localPath string) error {
	return c.call(ctx, "PUT", "/actions/"+esc(id)+"/items/"+esc(itemID)+"/path", map[string]string{"localPath": localPath}, nil)
}

func (c *Client) UnmapItem(ctx context.Context, id, itemID string) error {
	return c.call(ctx, "DELETE", "/actions/"+esc(id)+"/items/"+esc(itemID)+"/path", nil, nil)
}

func (c *Client) RemoveItem(ctx context.Context, id, itemID string) error {
	return c.call(ctx, "DELETE", "/actions/"+esc(id)+"/items/"+esc(itemID), nil, nil)
}

func (c *Client) Sync(ctx context.Context, id string) (model.SyncResult, error) {
	var v model.SyncResult
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/sync", nil, &v)
}

func (c *Client) Snapshot(ctx context.Context, id string) (model.SnapshotInfo, error) {
	var v model.SnapshotInfo
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/snapshot", nil, &v)
}

func (c *Client) Pause(ctx context.Context, id string) error {
	return c.call(ctx, "POST", "/actions/"+esc(id)+"/pause", nil, nil)
}

func (c *Client) Resume(ctx context.Context, id string) error {
	return c.call(ctx, "POST", "/actions/"+esc(id)+"/resume", nil, nil)
}

func (c *Client) CancelSync(ctx context.Context, id string) error {
	return c.call(ctx, "POST", "/actions/"+esc(id)+"/cancel", nil, nil)
}

func (c *Client) PendingDeletes(ctx context.Context, id string) (*model.PendingDeletes, error) {
	var v *model.PendingDeletes
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/pending-deletes", nil, &v)
}

func (c *Client) ApproveDeletes(ctx context.Context, id string, reviewed []string) (model.SyncResult, error) {
	var v model.SyncResult
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/approve-deletes", map[string][]string{"paths": reviewed}, &v)
}

func (c *Client) Snapshots(ctx context.Context, id string) ([]model.SnapshotInfo, error) {
	var v []model.SnapshotInfo
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/snapshots", nil, &v)
}

func (c *Client) Restore(ctx context.Context, id string, req api.RestoreRequest) (engine.RestoreResult, error) {
	var v engine.RestoreResult
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/restore", req, &v)
}

func (c *Client) Conflicts(ctx context.Context, id string) ([]model.Conflict, error) {
	var v []model.Conflict
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/conflicts", nil, &v)
}

func (c *Client) ConflictDetail(ctx context.Context, id, path string) (engine.ConflictDetail, error) {
	var v engine.ConflictDetail
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/conflicts/diff?path="+url.QueryEscape(path), nil, &v)
}

func (c *Client) Resolve(ctx context.Context, id string, choices map[string]model.Side) (model.SyncResult, error) {
	var v model.SyncResult
	return v, c.call(ctx, "POST", "/actions/"+esc(id)+"/resolve", map[string]any{"choices": choices}, &v)
}

func (c *Client) History(ctx context.Context, id string, limit int) ([]model.HistoryEntry, error) {
	var v []model.HistoryEntry
	return v, c.call(ctx, "GET", "/actions/"+esc(id)+"/history?limit="+strconv.Itoa(limit), nil, &v)
}

// Subscribe streams the agent's events until ctx ends or the agent stops.
func (c *Client) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	resp, err := c.request(ctx, "GET", "/events", nil)
	if err != nil {
		return nil, err
	}
	ch := make(chan api.Event, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var ev api.Event
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
