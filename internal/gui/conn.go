package gui

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aupr/s3-anysync/internal/api"
)

// Client is one connection to the agent.
type Client interface {
	api.Service
	Close()
}

// ConnState is sent to the frontend with EventConnection.
type ConnState struct {
	Connected bool   `json:"connected"`
	Reason    string `json:"reason,omitempty"`
}

// Events emitted to the frontend.
const (
	EventAgent      = "agent:event"      // data: api.Event
	EventConnection = "agent:connection" // data: ConnState
)

// ConnOptions configures a Conn. Zero durations take the defaults.
type ConnOptions struct {
	DataDir string
	Connect func(ctx context.Context, dataDir string) (Client, error)
	// Launch starts the agent (nil: never). It is used on the first attempt
	// and after Retry, never by the automatic reconnects.
	Launch    func(dataDir string) error
	Emit      func(event string, data any)
	Retry     time.Duration // between reconnect attempts (2s)
	StartWait time.Duration // how long a started agent may take to answer (10s)
	Poll      time.Duration // between attempts while waiting for it (250ms)
	Log       *slog.Logger
}

// Conn keeps the GUI connected to the agent.
type Conn struct {
	o    ConnOptions
	kick chan struct{} // wakes the reconnect wait
	// launchRequested: Retry asked for the agent to be started; consumed by
	// the next connect attempt.
	launchRequested atomic.Bool

	mu     sync.Mutex
	client Client
	drop   context.CancelFunc // ends the current session
	gen    uint64             // counts sessions; Lost ignores an older one
	state  ConnState
}

// NewConn returns a Conn; call Run to connect.
func NewConn(o ConnOptions) *Conn {
	if o.Retry <= 0 {
		o.Retry = 2 * time.Second
	}
	if o.StartWait <= 0 {
		o.StartWait = 10 * time.Second
	}
	if o.Poll <= 0 {
		o.Poll = 250 * time.Millisecond
	}
	if o.Emit == nil {
		o.Emit = func(string, any) {}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Conn{o: o, kick: make(chan struct{}, 1), state: ConnState{Reason: "connecting to the agent"}}
}

// Run connects (starting the agent on the first attempt), forwards events
// and reconnects until ctx ends.
func (c *Conn) Run(ctx context.Context) {
	first := true
	for ctx.Err() == nil {
		launch := c.launchRequested.Swap(false) || first
		first = false
		cl, err := c.connect(ctx, launch)
		if err == nil {
			c.session(ctx, cl)
		} else if ctx.Err() == nil {
			c.o.Log.Info("agent not reachable", "err", err)
			c.set(nil, nil, ConnState{Reason: err.Error()})
		}
		select {
		case <-ctx.Done():
		case <-c.kick:
		case <-time.After(c.o.Retry):
		}
	}
	c.set(nil, nil, ConnState{Reason: "the GUI is closing"})
}

// session forwards the agent's events until the stream ends.
func (c *Conn) session(ctx context.Context, cl Client) {
	defer cl.Close()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := cl.Subscribe(sctx)
	if err != nil {
		c.set(nil, nil, ConnState{Reason: err.Error()})
		return
	}
	c.set(cl, cancel, ConnState{Connected: true})
	c.launchRequested.Store(false)
	for ev := range events {
		c.o.Emit(EventAgent, ev)
	}
	if ctx.Err() == nil {
		c.set(nil, nil, ConnState{Reason: "the connection to the agent was lost"})
	}
}

func (c *Conn) connect(ctx context.Context, launch bool) (Client, error) {
	cl, err := c.try(ctx)
	if err == nil || !launch || c.o.Launch == nil {
		return cl, err
	}
	if lerr := c.o.Launch(c.o.DataDir); lerr != nil {
		return nil, fmt.Errorf("start the agent: %w", lerr)
	}
	deadline := time.Now().Add(c.o.StartWait)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.o.Poll):
		}
		if cl, err = c.try(ctx); err == nil {
			return cl, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the agent did not start within %s: %w", c.o.StartWait, err)
		}
	}
}

func (c *Conn) try(ctx context.Context) (Client, error) {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.o.Connect(tctx, c.o.DataDir)
}

// set changes the connection and announces a changed state.
func (c *Conn) set(cl Client, drop context.CancelFunc, st ConnState) {
	c.mu.Lock()
	changed := c.state != st
	c.client, c.drop, c.state = cl, drop, st
	if cl != nil {
		c.gen++
	}
	c.mu.Unlock()
	if changed {
		c.o.Emit(EventConnection, st)
	}
}

// Service returns the current connection, or ErrUnreachable.
func (c *Conn) Service() (api.Service, error) {
	s, _, err := c.service()
	return s, err
}

// service returns the current connection and the number of its session.
func (c *Conn) service() (api.Service, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return nil, 0, ErrUnreachable
	}
	return c.client, c.gen, nil
}

// State returns the connection state.
func (c *Conn) State() ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Retry reconnects now and starts the agent if it still does not answer.
// It does nothing while connected.
func (c *Conn) Retry() {
	if c.State().Connected {
		return
	}
	c.launchRequested.Store(true)
	c.wake()
}

func (c *Conn) wake() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// Lost ends session gen after a failed call; Run reconnects. A call that
// began on an earlier session cannot end a newer one.
func (c *Conn) Lost(gen uint64) {
	c.mu.Lock()
	drop, stale := c.drop, gen != c.gen
	c.mu.Unlock()
	if stale {
		return
	}
	if drop != nil {
		drop()
	}
	c.wake()
}
