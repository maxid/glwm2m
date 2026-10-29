// Package glwm2m implements a minimal LwM2M server registration state machine.
//
// Transport/wire format are provided by gcoap and gnety; DTLS and the full
// object model (read/write/execute, observe) are roadmap.
package glwm2m

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety/codec/coap"
)

// Registration is one LwM2M client registration.
type Registration struct {
	ID       string
	Endpoint string
	Lifetime int
	Binding  string
	Version  string
	LastSeen time.Time
}

// Config configures the LwM2M server.
type Config struct {
	Addr string
}

// Server is a minimal LwM2M registration server.
type Server struct {
	coap *gcoap.Server
	mu   sync.RWMutex
	regs map[string]*Registration
}

// New creates a server and registers the /rd endpoints.
func New(cfg Config) (*Server, error) {
	s := &Server{regs: make(map[string]*Registration)}
	cs, err := gcoap.New(gcoap.Config{Addr: cfg.Addr})
	if err != nil {
		return nil, err
	}
	cs.Handle(coap.CodePOST, "/rd", s.handleRegister)
	cs.HandlePrefix(coap.CodePOST, "/rd", s.handleUpdate)
	cs.HandlePrefix(coap.CodeDELETE, "/rd", s.handleDelete)
	s.coap = cs
	return s, nil
}

// Start starts the CoAP/UDP listener.
func (s *Server) Start(ctx context.Context) error { return s.coap.Start(ctx) }

// Stop stops the listener.
func (s *Server) Stop(ctx context.Context) error { return s.coap.Stop(ctx) }

// Addr returns the listen address.
func (s *Server) Addr() string { return s.coap.Addr() }

// Registration returns a snapshot of a registration.
func (s *Server) Registration(id string) (Registration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.regs[id]
	if !ok {
		return Registration{}, false
	}
	return *r, true
}

// Count returns the number of registered clients.
func (s *Server) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.regs)
}

func (s *Server) handleRegister(_ context.Context, req gcoap.Request) gcoap.Response {
	ep := req.Query.Get("ep")
	if ep == "" {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	lifetime := 86400
	if v := req.Query.Get("lt"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			lifetime = n
		}
	}
	binding := req.Query.Get("b")
	if binding == "" {
		binding = "U"
	}
	version := req.Query.Get("lwm2m")
	id := newID()
	reg := &Registration{
		ID: id, Endpoint: ep, Lifetime: lifetime,
		Binding: binding, Version: version, LastSeen: time.Now(),
	}
	s.mu.Lock()
	s.regs[id] = reg
	s.mu.Unlock()
	return gcoap.Response{
		Code: coap.CodeCreated,
		Options: []coap.Option{
			{Number: coap.OptionURIPath, Value: []byte("rd")},
			{Number: coap.OptionURIPath, Value: []byte(id)},
		},
	}
}

func (s *Server) handleUpdate(_ context.Context, req gcoap.Request) gcoap.Response {
	id := registrationID(req.Path)
	if id == "" {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, ok := s.regs[id]
	if !ok {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	if v := req.Query.Get("lt"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			reg.Lifetime = n
		}
	}
	if v := req.Query.Get("b"); v != "" {
		reg.Binding = v
	}
	reg.LastSeen = time.Now()
	return gcoap.Response{Code: coap.CodeChanged}
}

func (s *Server) handleDelete(_ context.Context, req gcoap.Request) gcoap.Response {
	id := registrationID(req.Path)
	if id == "" {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.regs[id]; !ok {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	delete(s.regs, id)
	return gcoap.Response{Code: coap.CodeDeleted}
}

func registrationID(path string) string {
	id := strings.TrimPrefix(path, "/rd/")
	if id == path || id == "" {
		return ""
	}
	return id
}

func newID() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
