// Package glwm2m implements a minimal LwM2M server registration state machine.
//
// Transport/wire format are provided by gcoap and gnety. Registration, TLV
// read/write/execute, Observe and DTLS are implemented; object-model
// completeness, Bootstrap, Queue Mode and OSCORE are roadmap.
package glwm2m

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety"
	"github.com/maxid/gnety/codec/coap"

	"github.com/maxid/glwm2m/tlv"
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
	Addr    string
	Network string // "udp" (default) or "dtls"
	TLS     gnety.TLSConfig
}

// Server is a minimal LwM2M registration server.
type Server struct {
	coap *gcoap.Server
	mu   sync.RWMutex
	regs map[string]*Registration

	objMu   sync.RWMutex
	objects map[string][]byte
}

// New creates a server and registers the /rd endpoints.
func New(cfg Config) (*Server, error) {
	s := &Server{regs: make(map[string]*Registration), objects: make(map[string][]byte)}
	cs, err := gcoap.New(gcoap.Config{Addr: cfg.Addr, Network: cfg.Network, TLS: cfg.TLS})
	if err != nil {
		return nil, err
	}
	cs.Handle(coap.CodePOST, "/rd", s.handleRegister)
	cs.HandlePrefix(coap.CodePOST, "/rd", s.handleUpdate)
	cs.HandlePrefix(coap.CodeDELETE, "/rd", s.handleDelete)
	cs.HandlePrefix(coap.CodeGET, "/objects", s.handleRead)
	cs.HandlePrefix(coap.CodePUT, "/objects", s.handleWrite)
	cs.HandlePrefix(coap.CodePOST, "/objects", s.handleExecute)
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

// SetResource stores a LwM2M resource value (text/plain, single resource).
func (s *Server) SetResource(objectID, instanceID, resourceID uint16, value []byte) {
	s.objMu.Lock()
	s.objects[resourceKey(objectID, instanceID, resourceID)] = append([]byte(nil), value...)
	s.objMu.Unlock()
	s.notifyChanged(objectID, instanceID, resourceID)
}

// Resource returns a resource value.
func (s *Server) Resource(objectID, instanceID, resourceID uint16) ([]byte, bool) {
	s.objMu.RLock()
	defer s.objMu.RUnlock()
	v, ok := s.objects[resourceKey(objectID, instanceID, resourceID)]
	return append([]byte(nil), v...), ok
}

func resourceKey(objectID, instanceID, resourceID uint16) string {
	return strconv.Itoa(int(objectID)) + "/" + strconv.Itoa(int(instanceID)) + "/" + strconv.Itoa(int(resourceID))
}

func pathIDs(path string) ([]uint16, bool) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) < 2 || segs[0] != "objects" {
		return nil, false
	}
	ids := make([]uint16, 0, len(segs)-1)
	for _, seg := range segs[1:] {
		n, err := strconv.Atoi(seg)
		if err != nil || n < 0 || n > 65535 {
			return nil, false
		}
		ids = append(ids, uint16(n))
	}
	return ids, true
}

func (s *Server) handleRead(_ context.Context, req gcoap.Request) gcoap.Response {
	ids, ok := pathIDs(req.Path)
	if !ok {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	switch len(ids) {
	case 1:
		return s.readObject(ids[0])
	case 2:
		return s.readInstance(ids[0], ids[1])
	case 3:
		value, ok := s.Resource(ids[0], ids[1], ids[2])
		if !ok {
			return gcoap.Response{Code: coap.CodeNotFound}
		}
		return gcoap.Response{
			Code:    coap.CodeContent,
			Options: []coap.Option{{Number: coap.OptionContentFormat, Value: []byte{0}}},
			Payload: value,
		}
	default:
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
}

func (s *Server) readObject(objectID uint16) gcoap.Response {
	payload, ok := s.objectTLV(objectID)
	if !ok {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	return gcoap.Response{
		Code:    coap.CodeContent,
		Options: []coap.Option{{Number: coap.OptionContentFormat, Value: contentFormatTLV}},
		Payload: payload,
	}
}

func (s *Server) readInstance(objectID, instanceID uint16) gcoap.Response {
	payload, ok := s.instanceTLV(objectID, instanceID)
	if !ok {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	return gcoap.Response{
		Code:    coap.CodeContent,
		Options: []coap.Option{{Number: coap.OptionContentFormat, Value: contentFormatTLV}},
		Payload: payload,
	}
}

func (s *Server) objectTLV(objectID uint16) ([]byte, bool) {
	s.objMu.RLock()
	defer s.objMu.RUnlock()
	instances := make(map[uint16][]tlv.Entry)
	for key, value := range s.objects {
		oid, iid, rid, ok := splitResourceKey(key)
		if !ok || oid != objectID {
			continue
		}
		instances[iid] = append(instances[iid], tlv.Entry{Type: tlv.ResourceValue, ID: rid, Value: append([]byte(nil), value...)})
	}
	if len(instances) == 0 {
		return nil, false
	}
	ids := make([]uint16, 0, len(instances))
	for iid := range instances {
		ids = append(ids, iid)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	entries := make([]tlv.Entry, 0, len(ids))
	for _, iid := range ids {
		resources := instances[iid]
		sort.Slice(resources, func(i, j int) bool { return resources[i].ID < resources[j].ID })
		entries = append(entries, tlv.Entry{Type: tlv.ObjectInstance, ID: iid, Children: resources})
	}
	payload, err := tlv.Marshal(entries)
	return payload, err == nil
}

func (s *Server) instanceTLV(objectID, instanceID uint16) ([]byte, bool) {
	s.objMu.RLock()
	defer s.objMu.RUnlock()
	var entries []tlv.Entry
	for key, value := range s.objects {
		oid, iid, rid, ok := splitResourceKey(key)
		if !ok || oid != objectID || iid != instanceID {
			continue
		}
		entries = append(entries, tlv.Entry{Type: tlv.ResourceValue, ID: rid, Value: append([]byte(nil), value...)})
	}
	if len(entries) == 0 {
		return nil, false
	}
	// Map iteration is unordered; sort so TLV output is deterministic.
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	payload, err := tlv.Marshal(entries)
	return payload, err == nil
}

func resourcePath(oid, iid, rid uint16) string {
	return "/objects/" + strconv.Itoa(int(oid)) + "/" + strconv.Itoa(int(iid)) + "/" + strconv.Itoa(int(rid))
}

func instancePath(oid, iid uint16) string {
	return "/objects/" + strconv.Itoa(int(oid)) + "/" + strconv.Itoa(int(iid))
}

func objectPath(oid uint16) string {
	return "/objects/" + strconv.Itoa(int(oid))
}

func (s *Server) notifyChanged(objectID, instanceID, resourceID uint16) {
	if s.coap == nil {
		return
	}
	if value, ok := s.Resource(objectID, instanceID, resourceID); ok {
		s.coap.Notify(resourcePath(objectID, instanceID, resourceID), gcoap.Response{
			Code:    coap.CodeContent,
			Options: []coap.Option{{Number: coap.OptionContentFormat, Value: []byte{0}}},
			Payload: value,
		})
	}
	if payload, ok := s.instanceTLV(objectID, instanceID); ok {
		s.coap.Notify(instancePath(objectID, instanceID), gcoap.Response{
			Code:    coap.CodeContent,
			Options: []coap.Option{{Number: coap.OptionContentFormat, Value: contentFormatTLV}},
			Payload: payload,
		})
	}
	if payload, ok := s.objectTLV(objectID); ok {
		s.coap.Notify(objectPath(objectID), gcoap.Response{
			Code:    coap.CodeContent,
			Options: []coap.Option{{Number: coap.OptionContentFormat, Value: contentFormatTLV}},
			Payload: payload,
		})
	}
}

func (s *Server) handleWrite(_ context.Context, req gcoap.Request) gcoap.Response {
	ids, ok := pathIDs(req.Path)
	if !ok {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	switch len(ids) {
	case 2:
		entries, err := tlv.Unmarshal(req.Payload)
		if err != nil {
			return gcoap.Response{Code: coap.CodeBadRequest}
		}
		s.applyTLV(ids[0], ids[1], entries)
		return gcoap.Response{Code: coap.CodeChanged}
	case 3:
		s.SetResource(ids[0], ids[1], ids[2], req.Payload)
		return gcoap.Response{Code: coap.CodeChanged}
	default:
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
}

func (s *Server) applyTLV(objectID, instanceID uint16, entries []tlv.Entry) {
	for _, e := range entries {
		switch e.Type {
		case tlv.ObjectInstance:
			s.applyTLV(objectID, e.ID, e.Children)
		case tlv.ResourceValue:
			s.SetResource(objectID, instanceID, e.ID, e.Value)
		case tlv.MultipleResource:
			for _, child := range e.Children {
				s.SetResource(objectID, instanceID, e.ID, child.Value)
			}
		}
	}
}

func (s *Server) handleExecute(_ context.Context, req gcoap.Request) gcoap.Response {
	ids, ok := pathIDs(req.Path)
	if !ok || len(ids) != 3 {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	if _, ok := s.Resource(ids[0], ids[1], ids[2]); !ok {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	return gcoap.Response{Code: coap.CodeChanged}
}

func splitResourceKey(key string) (objectID, instanceID, resourceID uint16, ok bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	vals := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			return 0, 0, 0, false
		}
		vals[i] = n
	}
	return uint16(vals[0]), uint16(vals[1]), uint16(vals[2]), true
}

var contentFormatTLV = []byte{0x2D, 0x16} // application/vnd.oma.lwm2m+tlv (11542)

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
