package glwm2m

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety/codec/coap"
)

// PeerInfo is the neutral transport identity of a registration: the address a
// downlink is sent to, the session that registered it and the transport it
// arrived on. It never carries a device, tenant or product identity.
type PeerInfo struct {
	Addr      string
	SessionID string
	Transport string
}

// Registration is one LwM2M client registration.
type Registration struct {
	ID       string
	Endpoint string
	// Lifetime is the registration lifetime in seconds.
	Lifetime int
	Binding  string
	Version  string
	// ObjectLinks is the object/instance list the client announced, normally
	// "</1/0>,</3/0>". It is bounded and may be empty.
	ObjectLinks []string
	Peer        PeerInfo
	// RegisteredAt and LastSeen are server-side timestamps.
	RegisteredAt time.Time
	LastSeen     time.Time
	// ExpiresAt is LastSeen plus the current lifetime.
	ExpiresAt time.Time
	// Epoch increments when an endpoint is re-registered. A downlink records the
	// epoch it was created for, so an operation never reaches a replaced client.
	Epoch uint64
}

// EventType is a registration lifecycle event.
type EventType string

// Registration lifecycle events.
const (
	EventRegistered   EventType = "registered"
	EventUpdated      EventType = "updated"
	EventDeregistered EventType = "deregistered"
	EventExpired      EventType = "expired"
)

// RegistrationEvent is one lifecycle notification handed to EventSink.
type RegistrationEvent struct {
	Type         EventType
	Registration Registration
	// Reason is "client" or "replaced" for a deregistration and "lifetime" for
	// an expiration.
	Reason string
}

// EventSink receives registration lifecycle events. It runs outside the
// server's lock on the request or sweeper goroutine, so it must not block; a
// panic is recovered by the server and discarded.
type EventSink interface {
	OnRegistration(ctx context.Context, event RegistrationEvent)
}

// EventSinkFunc adapts a function to EventSink.
type EventSinkFunc func(context.Context, RegistrationEvent)

// OnRegistration implements EventSink.
func (f EventSinkFunc) OnRegistration(ctx context.Context, event RegistrationEvent) { f(ctx, event) }

// emit hands one event to the sink, isolating the server from a bad sink.
func (s *Server) emit(event RegistrationEvent) {
	if s.cfg.Events == nil {
		return
	}
	defer func() { _ = recover() }()
	s.cfg.Events.OnRegistration(context.Background(), event)
}

// emitAll reports several events in order.
func (s *Server) emitAll(events []RegistrationEvent) {
	for _, event := range events {
		s.emit(event)
	}
}

// peerOf derives the neutral peer identity of a request.
func (s *Server) peerOf(req gcoap.Request) PeerInfo {
	peer := PeerInfo{Transport: s.transport}
	if req.Session != nil {
		peer.SessionID = req.Session.ID
		if req.Session.RemoteAddr != nil {
			peer.Addr = req.Session.RemoteAddr.String()
		}
	}
	return peer
}

// samePeer reports whether two peer identities belong to the same client: the
// same session, or the same transport address after a reconnect.
func samePeer(a, b PeerInfo) bool {
	if a.SessionID != "" && a.SessionID == b.SessionID {
		return true
	}
	if a.Addr == "" || b.Addr == "" {
		return false
	}
	return a.Addr == b.Addr
}

// snapshot returns a deep copy of a registration.
func (r Registration) snapshot() Registration {
	out := r
	out.ObjectLinks = append([]string(nil), r.ObjectLinks...)
	return out
}

func validEndpoint(endpoint string) bool {
	if endpoint == "" || len(endpoint) > 255 {
		return false
	}
	return !strings.ContainsAny(endpoint, ", \t\r\n")
}

// parseLifetime reads the "lt" query value. An invalid or non-positive value is
// an error; a value above the maximum is clamped, never turned into a permanent
// registration.
func parseLifetime(value string, def, max time.Duration) (time.Duration, error) {
	if value == "" {
		return def, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("glwm2m: invalid lifetime %q", value)
	}
	lifetime := time.Duration(seconds) * time.Second
	if lifetime > max {
		return max, nil
	}
	return lifetime, nil
}

// parseBinding validates the binding mode.
func parseBinding(value string) (string, error) {
	if value == "" {
		return "U", nil
	}
	if len(value) > 8 {
		return "", fmt.Errorf("glwm2m: invalid binding %q", value)
	}
	return value, nil
}

// parseObjectLinks reads the registration payload. It accepts the LwM2M form
// "</1/0>,</3/0>" and keeps each link, bounded in count and length.
func parseObjectLinks(payload []byte) ([]string, error) {
	text := strings.TrimSpace(string(payload))
	if text == "" {
		return nil, nil
	}
	const (
		maxLinks = 256
		maxLink  = 255
	)
	parts := strings.Split(text, ",")
	if len(parts) > maxLinks {
		return nil, fmt.Errorf("glwm2m: %d object links exceed %d", len(parts), maxLinks)
	}
	links := make([]string, 0, len(parts))
	for _, part := range parts {
		link := strings.TrimSpace(part)
		if link == "" {
			continue
		}
		if len(link) > maxLink {
			return nil, fmt.Errorf("glwm2m: object link is longer than %d bytes", maxLink)
		}
		links = append(links, link)
	}
	return links, nil
}

// handleRegister serves POST /rd.
func (s *Server) handleRegister(_ context.Context, req gcoap.Request) gcoap.Response {
	endpoint := req.Query.Get("ep")
	if !validEndpoint(endpoint) {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	lifetime, err := parseLifetime(req.Query.Get("lt"), s.cfg.DefaultLifetime, s.cfg.MaxLifetime)
	if err != nil {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	binding, err := parseBinding(req.Query.Get("b"))
	if err != nil {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	links, err := parseObjectLinks(req.Payload)
	if err != nil {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	peer := s.peerOf(req)
	now := s.now()
	reg := &Registration{
		ID:           newID(),
		Endpoint:     endpoint,
		Lifetime:     int(lifetime / time.Second),
		Binding:      binding,
		Version:      req.Query.Get("lwm2m"),
		ObjectLinks:  links,
		Peer:         peer,
		RegisteredAt: now,
		LastSeen:     now,
		ExpiresAt:    now.Add(lifetime),
		Epoch:        1,
	}

	var (
		events  []RegistrationEvent
		replace string
	)
	s.mu.Lock()
	oldID := s.byEndpoint[endpoint]
	if oldID == "" && len(s.regs) >= s.cfg.MaxRegistrations {
		s.mu.Unlock()
		return gcoap.Response{Code: coap.CodeServiceUnavailable}
	}
	if oldID != "" {
		if s.cfg.RejectEndpointConflict {
			s.mu.Unlock()
			return gcoap.Response{Code: coap.CodeConflict}
		}
		if old := s.regs[oldID]; old != nil {
			reg.Epoch = old.Epoch + 1
			delete(s.regs, oldID)
			replace = oldID
			events = append(events, RegistrationEvent{
				Type: EventDeregistered, Registration: old.snapshot(), Reason: "replaced",
			})
		}
	}
	s.regs[reg.ID] = reg
	s.byEndpoint[endpoint] = reg.ID
	s.mu.Unlock()

	if replace != "" {
		s.closeDownlink(replace)
	}
	s.emitAll(events)
	s.emit(RegistrationEvent{Type: EventRegistered, Registration: reg.snapshot()})
	if replace != "" {
		s.storeDelete(replace)
	}
	s.storeSave(reg.snapshot())
	return gcoap.Response{
		Code: coap.CodeCreated,
		Options: []coap.Option{
			{Number: coap.OptionLocationPath, Value: []byte("rd")},
			{Number: coap.OptionLocationPath, Value: []byte(reg.ID)},
		},
	}
}

// handleUpdate serves POST /rd/{id}.
func (s *Server) handleUpdate(_ context.Context, req gcoap.Request) gcoap.Response {
	id := registrationID(req.Path)
	if id == "" {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	peer := s.peerOf(req)
	now := s.now()
	var (
		event   RegistrationEvent
		oldAddr string
		updated Registration
	)
	s.mu.Lock()
	reg := s.regs[id]
	if reg == nil || !samePeer(reg.Peer, peer) || !now.Before(reg.ExpiresAt) {
		s.mu.Unlock()
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	lifetime, err := parseLifetime(req.Query.Get("lt"), time.Duration(reg.Lifetime)*time.Second, s.cfg.MaxLifetime)
	if err != nil {
		s.mu.Unlock()
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	binding, err := parseBinding(firstNonEmpty(req.Query.Get("b"), reg.Binding))
	if err != nil {
		s.mu.Unlock()
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	if len(req.Payload) > 0 {
		links, linkErr := parseObjectLinks(req.Payload)
		if linkErr != nil {
			s.mu.Unlock()
			return gcoap.Response{Code: coap.CodeBadRequest}
		}
		reg.ObjectLinks = links
	}
	oldAddr = reg.Peer.Addr
	reg.Lifetime = int(lifetime / time.Second)
	reg.Binding = binding
	reg.LastSeen = now
	reg.ExpiresAt = now.Add(lifetime)
	reg.Peer = peer
	updated = reg.snapshot()
	event = RegistrationEvent{Type: EventUpdated, Registration: updated}
	s.mu.Unlock()

	if oldAddr != updated.Peer.Addr {
		// The client moved; the cached downlink connection belongs to the old
		// address and its observations are stale.
		s.closeDownlink(id)
	}
	s.emit(event)
	s.storeSave(updated)
	return gcoap.Response{Code: coap.CodeChanged}
}

// handleDelete serves DELETE /rd/{id}.
func (s *Server) handleDelete(_ context.Context, req gcoap.Request) gcoap.Response {
	id := registrationID(req.Path)
	if id == "" {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	peer := s.peerOf(req)
	s.mu.Lock()
	reg := s.regs[id]
	if reg == nil || !samePeer(reg.Peer, peer) || !s.now().Before(reg.ExpiresAt) {
		s.mu.Unlock()
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	snapshot := reg.snapshot()
	delete(s.regs, id)
	if s.byEndpoint[reg.Endpoint] == id {
		delete(s.byEndpoint, reg.Endpoint)
	}
	s.mu.Unlock()

	s.closeDownlink(id)
	s.emit(RegistrationEvent{Type: EventDeregistered, Registration: snapshot, Reason: "client"})
	s.storeDelete(id)
	return gcoap.Response{Code: coap.CodeDeleted}
}

// expire removes every registration whose lifetime has passed and reports each
// one exactly once.
func (s *Server) expire(now time.Time) {
	var expired []Registration
	s.mu.Lock()
	for id, reg := range s.regs {
		if now.Before(reg.ExpiresAt) {
			continue
		}
		expired = append(expired, reg.snapshot())
		delete(s.regs, id)
		if s.byEndpoint[reg.Endpoint] == id {
			delete(s.byEndpoint, reg.Endpoint)
		}
	}
	s.mu.Unlock()
	for _, reg := range expired {
		s.closeDownlink(reg.ID)
		s.emit(RegistrationEvent{Type: EventExpired, Registration: reg, Reason: "lifetime"})
		s.storeDelete(reg.ID)
	}
}

// startSweeper starts the periodic expiry scan.
func (s *Server) startSweeper() {
	if s.cfg.ExpiryInterval <= 0 {
		return
	}
	s.sweepWG.Add(1)
	go func() {
		defer s.sweepWG.Done()
		ticker := time.NewTicker(s.cfg.ExpiryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.expire(s.now())
			case <-s.sweepStop:
				return
			}
		}
	}()
}

// stopSweeper stops the expiry scan and waits for it.
func (s *Server) stopSweeper() {
	s.stopSweep.Do(func() { close(s.sweepStop) })
	s.sweepWG.Wait()
}

// Registrations returns a snapshot of every registration in id order.
func (s *Server) Registrations() []Registration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Registration, 0, len(s.regs))
	for _, reg := range s.regs {
		out = append(out, reg.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
