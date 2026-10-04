package gui

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
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
	kick chan bool // wakes the reconnect wait; true also starts the agent

	mu     sync.Mutex
	client Client
	drop   context.CancelFunc // ends the current session
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
	return &Conn{o: o, kick: make(chan bool, 1), state: ConnState{Reason: "connecting to the agent"}}
}

// Run connects (starting the agent on the first attempt), forwards events
// and reconnects until ctx ends.
func (c *Conn) Run(ctx context.Context) {
	launch := true
	for ctx.Err() == nil {
		cl, err := c.connect(ctx, launch)
		launch = false
		if err == nil {
			c.session(ctx, cl)
		} else if ctx.Err() == nil {
			c.o.Log.Info("agent not reachable", "err", err)
			c.set(nil, nil, ConnState{Reason: err.Error()})
		}
		select {
		case <-ctx.Done():
		case launch = <-c.kick:
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
	c.mu.Unlock()
	if changed {
		c.o.Emit(EventConnection, st)
	}
}

// Service returns the current connection, or ErrUnreachable.
func (c *Conn) Service() (api.Service, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return nil, ErrUnreachable
	}
	return c.client, nil
}

// State returns the connection state.
func (c *Conn) State() ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Retry reconnects now and starts the agent if it still does not answer.
func (c *Conn) Retry() {
	select {
	case c.kick <- true:
	default:
	}
}

// Lost ends the current session after a failed call; Run reconnects.
func (c *Conn) Lost() {
	c.mu.Lock()
	drop := c.drop
	c.mu.Unlock()
	if drop != nil {
		drop()
	}
	select {
	case c.kick <- false:
	default:
	}
}
