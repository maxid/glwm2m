package glwm2m

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety"
	"github.com/maxid/gnety/codec/coap"
	"github.com/maxid/gnety/codec/tlv"
)

// Defaults used when a Config field is zero.
const (
	// DefaultLifetime is the registration lifetime when the client omits "lt".
	DefaultLifetime = 24 * time.Hour
	// DefaultMaxLifetime is the longest lifetime a client may request.
	DefaultMaxLifetime = 7 * 24 * time.Hour
	// DefaultExpiryInterval is how often expired registrations are scanned.
	DefaultExpiryInterval = time.Second
	// DefaultMaxRegistrations bounds concurrent registrations.
	DefaultMaxRegistrations = 1024
	// DefaultDownlinkTimeout bounds one server-initiated operation.
	DefaultDownlinkTimeout = 10 * time.Second
	// DefaultMaxDownlinksPerPeer bounds the downlinks in flight to one peer
	// address. A LwM2M device answers one operation at a time in practice, and
	// an unbounded server would pile requests onto a slow one.
	DefaultMaxDownlinksPerPeer = 8
)

// Config configures the LwM2M server.
type Config struct {
	Addr    string
	Network string // "udp" (default) or "dtls"
	TLS     gnety.TLSConfig

	// DefaultLifetime is used when a registration carries no "lt".
	DefaultLifetime time.Duration
	// MaxLifetime caps the lifetime a client may request.
	MaxLifetime time.Duration
	// ExpiryInterval is the registration expiry scan interval. Zero keeps
	// DefaultExpiryInterval; a negative value disables the scan.
	ExpiryInterval time.Duration
	// MaxRegistrations bounds the number of live registrations. Zero keeps
	// DefaultMaxRegistrations.
	MaxRegistrations int
	// RejectEndpointConflict answers a duplicate endpoint with 4.09 instead of
	// replacing the previous registration.
	RejectEndpointConflict bool

	// DownlinkTimeout bounds one server-initiated operation when the Operation
	// carries no timeout. Zero keeps DefaultDownlinkTimeout.
	DownlinkTimeout time.Duration
	// MaxDownlinksPerPeer bounds the downlinks in flight to one peer address: a
	// Read/Write/Execute running right now plus every observation registered on
	// it. Zero keeps DefaultMaxDownlinksPerPeer; the peer's existing operations
	// keep working when a new one is refused, and other peers are unaffected.
	MaxDownlinksPerPeer int
	// DownlinkMaxRequestSize bounds one downlink request body, including a
	// Block1 binary-data upload. Zero keeps gcoap's default (1 MiB).
	DownlinkMaxRequestSize int
	// DownlinkTLS configures the DTLS client used to call back a registration
	// that arrived over DTLS. The same files can carry the server certificate
	// (client certificate for the callback) and the CA that signs the device.
	DownlinkTLS gnety.TLSConfig
	// DownlinkDialer replaces the built-in downlink dialer. It receives the
	// registration and must return a connected CoAP client.
	DownlinkDialer func(ctx context.Context, reg Registration) (*gcoap.Client, error)

	// Store persists registrations across a restart. Nil keeps them in memory
	// only, which is the default: a restarted server waits for devices to
	// re-register. When set, Start loads the live records and every registration
	// change is written through it.
	Store RegistrationStore
	// StoreError receives a persistence failure. Nil discards it; a failing store
	// never fails the protocol request itself.
	StoreError func(error)

	// Events receives registration lifecycle events. Nil discards them.
	Events EventSink
	// Objects, when set, validates local object reads and writes and enforces
	// the declared operations, multiplicity and value types.
	Objects *ObjectRegistry
}

// Server is a LwM2M server engine.
type Server struct {
	cfg       Config
	coap      *gcoap.Server
	transport string

	mu         sync.RWMutex
	regs       map[string]*Registration
	byEndpoint map[string]string
	now        func() time.Time

	sweepStop chan struct{}
	sweepWG   sync.WaitGroup
	stopSweep sync.Once
	stopOnce  sync.Once

	store    *objectStore
	registry *ObjectRegistry
	dl       *downlinkState
	quota    *downlinkQuota
}

// New creates a server and registers the /rd and /objects endpoints.
func New(cfg Config) (*Server, error) {
	cfg = withConfigDefaults(cfg)
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	s := &Server{
		cfg:        cfg,
		transport:  networkOf(cfg.Network),
		regs:       make(map[string]*Registration),
		byEndpoint: make(map[string]string),
		now:        time.Now,
		sweepStop:  make(chan struct{}),
		store:      newObjectStore(),
		registry:   cfg.Objects,
		dl:         newDownlinkState(),
		quota:      newDownlinkQuota(cfg.MaxDownlinksPerPeer),
	}
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

func withConfigDefaults(cfg Config) Config {
	if cfg.DefaultLifetime == 0 {
		cfg.DefaultLifetime = DefaultLifetime
	}
	if cfg.MaxLifetime == 0 {
		cfg.MaxLifetime = DefaultMaxLifetime
	}
	if cfg.ExpiryInterval == 0 {
		cfg.ExpiryInterval = DefaultExpiryInterval
	}
	if cfg.MaxRegistrations == 0 {
		cfg.MaxRegistrations = DefaultMaxRegistrations
	}
	if cfg.DownlinkTimeout == 0 {
		cfg.DownlinkTimeout = DefaultDownlinkTimeout
	}
	if cfg.MaxDownlinksPerPeer == 0 {
		cfg.MaxDownlinksPerPeer = DefaultMaxDownlinksPerPeer
	}
	return cfg
}

func validateConfig(cfg Config) error {
	if cfg.DefaultLifetime < 0 || cfg.MaxLifetime < 0 || cfg.ExpiryInterval < 0 || cfg.DownlinkTimeout < 0 {
		return errors.New("glwm2m: durations must not be negative")
	}
	if cfg.MaxLifetime < cfg.DefaultLifetime {
		return errors.New("glwm2m: MaxLifetime is below DefaultLifetime")
	}
	if cfg.MaxRegistrations < 0 {
		return errors.New("glwm2m: MaxRegistrations must not be negative")
	}
	if cfg.MaxDownlinksPerPeer < 0 {
		return errors.New("glwm2m: MaxDownlinksPerPeer must not be negative")
	}
	return nil
}

func networkOf(network string) string {
	if network == "" {
		return "udp"
	}
	return network
}

// Start starts the CoAP listener and the registration expiry scan.
func (s *Server) Start(ctx context.Context) error {
	if err := s.restore(ctx); err != nil {
		return err
	}
	if err := s.coap.Start(ctx); err != nil {
		return err
	}
	s.startSweeper()
	return nil
}

// Stop stops the listener, the expiry scan and every cached downlink.
func (s *Server) Stop(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() {
		s.stopSweeper()
		s.closeAllDownlinks()
		err = s.coap.Stop(ctx)
	})
	return err
}

// Addr returns the listen address.
func (s *Server) Addr() string { return s.coap.Addr() }

// Registration returns a snapshot of a registration.
func (s *Server) Registration(id string) (Registration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	reg, ok := s.regs[id]
	if !ok {
		return Registration{}, false
	}
	return reg.snapshot(), true
}

// Count returns the number of live registrations.
func (s *Server) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.regs)
}

// SetResource stores a single-resource value (instance id 0). When an object
// registry is configured the write is validated against it.
func (s *Server) SetResource(objectID, instanceID, resourceID uint16, value []byte) error {
	return s.SetResourceInstance(objectID, instanceID, resourceID, 0, value)
}

// SetResourceInstance stores one instance of a resource. A multiple resource
// keeps the other instances; a single resource is replaced.
func (s *Server) SetResourceInstance(objectID, instanceID, resourceID, instanceIDValue uint16, value []byte) error {
	update := objectUpdate{objectID: objectID, instanceID: instanceID, resourceID: resourceID, instance: instanceIDValue}
	update.value = append([]byte(nil), value...)
	if err := s.validateUpdate(&update); err != nil {
		return err
	}
	s.applyUpdates([]objectUpdate{update})
	s.notifyChanged(objectID, instanceID, resourceID)
	return nil
}

// Resource returns one resource value: the instance with id 0 when present,
// otherwise the first instance in id order.
func (s *Server) Resource(objectID, instanceID, resourceID uint16) ([]byte, bool) {
	instances := s.store.get(resourceKey(objectID, instanceID, resourceID))
	if len(instances) == 0 {
		return nil, false
	}
	best := instances[0]
	for _, inst := range instances[1:] {
		if inst.ID < best.ID {
			best = inst
		}
	}
	return best.Value, true
}

// ResourceInstances returns every instance of a resource.
func (s *Server) ResourceInstances(objectID, instanceID, resourceID uint16) []Instance {
	return s.store.get(resourceKey(objectID, instanceID, resourceID))
}

// handleRead serves GET /objects/{oid}[/{iid}[/{rid}]].
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
		return s.readResource(ids[0], ids[1], ids[2])
	default:
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
}

func (s *Server) readResource(objectID, instanceID, resourceID uint16) gcoap.Response {
	instances := s.store.get(resourceKey(objectID, instanceID, resourceID))
	if len(instances) == 0 {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	if s.registry != nil {
		def, _, ok := s.registry.Resource(objectID, resourceID)
		if !ok {
			return gcoap.Response{Code: coap.CodeNotFound}
		}
		if !def.Operations.Has(OpRead) {
			return gcoap.Response{Code: coap.CodeMethodNotAllowed}
		}
		if def.Multiple {
			return s.resourceTLVResponse(multipleResourceEntry(resourceID, instances))
		}
		if len(instances) > 1 {
			return s.resourceTLVResponse(resourceEntries(resourceID, instances))
		}
	}
	if len(instances) > 1 {
		return s.resourceTLVResponse(resourceEntries(resourceID, instances))
	}
	return gcoap.Response{
		Code:    coap.CodeContent,
		Options: []coap.Option{{Number: coap.OptionContentFormat, Value: []byte{0}}},
		Payload: instances[0].Value,
	}
}

func (s *Server) readInstance(objectID, instanceID uint16) gcoap.Response {
	if s.registry != nil {
		def, ok := s.registry.Object(objectID)
		if !ok {
			return gcoap.Response{Code: coap.CodeNotFound}
		}
		if !def.Multiple && instanceID != 0 {
			return gcoap.Response{Code: coap.CodeNotFound}
		}
	}
	entries := s.instanceEntries(objectID, instanceID)
	if len(entries) == 0 {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	return s.tlvResponse(entries)
}

func (s *Server) readObject(objectID uint16) gcoap.Response {
	if s.registry != nil {
		if _, ok := s.registry.Object(objectID); !ok {
			return gcoap.Response{Code: coap.CodeNotFound}
		}
	}
	entries := s.objectEntries(objectID)
	if len(entries) == 0 {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	return s.tlvResponse(entries)
}

func (s *Server) resourceTLVResponse(entry tlv.Entry) gcoap.Response {
	return s.tlvResponse([]tlv.Entry{entry})
}

func (s *Server) tlvResponse(entries []tlv.Entry) gcoap.Response {
	payload, err := tlv.Marshal(entries)
	if err != nil {
		return gcoap.Response{Code: coap.CodeInternalServerError}
	}
	return gcoap.Response{
		Code:    coap.CodeContent,
		Options: []coap.Option{{Number: coap.OptionContentFormat, Value: contentFormatTLV}},
		Payload: payload,
	}
}

// objectEntries renders every instance of an object.
func (s *Server) objectEntries(objectID uint16) []tlv.Entry {
	snapshot := s.store.snapshot()
	instances := make(map[uint16]bool)
	prefix := strconv.Itoa(int(objectID)) + "/"
	for key := range snapshot {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := key[len(prefix):]
		iidText, _, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		iid, err := strconv.Atoi(iidText)
		if err != nil {
			continue
		}
		instances[uint16(iid)] = true
	}
	ids := make([]uint16, 0, len(instances))
	for iid := range instances {
		ids = append(ids, iid)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	entries := make([]tlv.Entry, 0, len(ids))
	for _, iid := range ids {
		resources := s.instanceEntriesFrom(snapshot, objectID, iid)
		if len(resources) == 0 {
			continue
		}
		entries = append(entries, tlv.Entry{Type: tlv.ObjectInstance, ID: iid, Children: resources})
	}
	return entries
}

// instanceEntries renders the resources of one instance.
func (s *Server) instanceEntries(objectID, instanceID uint16) []tlv.Entry {
	return s.instanceEntriesFrom(s.store.snapshot(), objectID, instanceID)
}

func (s *Server) instanceEntriesFrom(snapshot map[string][]Instance, objectID, instanceID uint16) []tlv.Entry {
	prefix := resourceKey(objectID, instanceID, 0)
	prefix = prefix[:strings.LastIndex(prefix, "/")+1]
	rides := make([]uint16, 0)
	byRID := make(map[uint16][]Instance)
	for key, instances := range snapshot {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		ridText := key[len(prefix):]
		rid, err := strconv.Atoi(ridText)
		if err != nil {
			continue
		}
		byRID[uint16(rid)] = instances
		rides = append(rides, uint16(rid))
	}
	sort.Slice(rides, func(i, j int) bool { return rides[i] < rides[j] })
	entries := make([]tlv.Entry, 0, len(rides))
	for _, rid := range rides {
		multiple := len(byRID[rid]) > 1
		if s.registry != nil {
			def, _, ok := s.registry.Resource(objectID, rid)
			if !ok || !def.Operations.Has(OpRead) {
				continue
			}
			multiple = def.Multiple
		}
		if multiple {
			entries = append(entries, multipleResourceEntry(rid, byRID[rid]))
		} else {
			entries = append(entries, resourceEntries(rid, byRID[rid]))
		}
	}
	return entries
}

// objectUpdate is one validated resource write.
type objectUpdate struct {
	objectID   uint16
	instanceID uint16
	resourceID uint16
	instance   uint16
	multiple   bool
	value      []byte
}

// handleWrite serves PUT /objects/{oid}/{iid} and /objects/{oid}/{iid}/{rid}.
func (s *Server) handleWrite(_ context.Context, req gcoap.Request) gcoap.Response {
	ids, ok := pathIDs(req.Path)
	if !ok {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	switch len(ids) {
	case 1:
		entries, err := tlv.Unmarshal(req.Payload)
		if err != nil {
			return gcoap.Response{Code: coap.CodeBadRequest}
		}
		updates, err := s.collectUpdates(ids[0], nil, entries)
		if err != nil {
			return gcoap.Response{Code: writeErrorCode(err)}
		}
		s.applyUpdates(updates)
		s.notifyUpdates(updates)
		return gcoap.Response{Code: coap.CodeCreated}
	case 2:
		entries, err := tlv.Unmarshal(req.Payload)
		if err != nil {
			return gcoap.Response{Code: coap.CodeBadRequest}
		}
		updates, err := s.collectUpdates(ids[0], &ids[1], entries)
		if err != nil {
			return gcoap.Response{Code: writeErrorCode(err)}
		}
		s.applyUpdates(updates)
		s.notifyUpdates(updates)
		return gcoap.Response{Code: coap.CodeChanged}
	case 3:
		update := objectUpdate{
			objectID: ids[0], instanceID: ids[1], resourceID: ids[2],
			value: append([]byte(nil), req.Payload...),
		}
		if err := s.validateUpdate(&update); err != nil {
			return gcoap.Response{Code: writeErrorCode(err)}
		}
		s.applyUpdates([]objectUpdate{update})
		s.notifyChanged(ids[0], ids[1], ids[2])
		return gcoap.Response{Code: coap.CodeChanged}
	default:
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
}

// writeErrorCode maps a validation error to a CoAP code.
func writeErrorCode(err error) coap.Code {
	switch {
	case errors.Is(err, errUnknownObject), errors.Is(err, errUnknownResource):
		return coap.CodeNotFound
	case errors.Is(err, errNotWritable), errors.Is(err, errNotExecutable):
		return coap.CodeMethodNotAllowed
	case errors.Is(err, errBadValue):
		return coap.CodeBadRequest
	default:
		return coap.CodeBadRequest
	}
}

var (
	errUnknownObject   = errors.New("glwm2m: unknown object")
	errUnknownResource = errors.New("glwm2m: unknown resource")
	errNotWritable     = errors.New("glwm2m: resource is not writable")
	errNotExecutable   = errors.New("glwm2m: resource is not executable")
	errBadValue        = errors.New("glwm2m: invalid value")
	errBadTLV          = errors.New("glwm2m: invalid object payload")
)

// collectUpdates flattens an instance- or object-level TLV write into validated
// resource updates. Nothing is applied until every update is valid.
func (s *Server) collectUpdates(objectID uint16, instanceID *uint16, entries []tlv.Entry) ([]objectUpdate, error) {
	const maxUpdates = 4096
	var updates []objectUpdate
	var walk func(instance *uint16, entries []tlv.Entry) error
	walk = func(instance *uint16, entries []tlv.Entry) error {
		for _, entry := range entries {
			if len(updates) >= maxUpdates {
				return errBadTLV
			}
			switch entry.Type {
			case tlv.ObjectInstance:
				if instance != nil {
					return errBadTLV
				}
				iid := entry.ID
				if err := walk(&iid, entry.Children); err != nil {
					return err
				}
			case tlv.MultipleResource:
				if instance == nil {
					return errBadTLV
				}
				for _, child := range entry.Children {
					if child.Type != tlv.ResourceInstance {
						return errBadTLV
					}
					update := objectUpdate{
						objectID: objectID, instanceID: *instance, resourceID: entry.ID,
						instance: child.ID, multiple: true,
						value: append([]byte(nil), child.Value...),
					}
					if err := s.validateUpdate(&update); err != nil {
						return err
					}
					updates = append(updates, update)
				}
			case tlv.ResourceValue:
				if instance == nil {
					return errBadTLV
				}
				update := objectUpdate{
					objectID: objectID, instanceID: *instance, resourceID: entry.ID,
					value: append([]byte(nil), entry.Value...),
				}
				if err := s.validateUpdate(&update); err != nil {
					return err
				}
				updates = append(updates, update)
			default:
				return errBadTLV
			}
		}
		return nil
	}
	if err := walk(instanceID, entries); err != nil {
		return nil, err
	}
	return updates, nil
}

// validateUpdate checks one update against the object registry.
func (s *Server) validateUpdate(update *objectUpdate) error {
	if s.registry == nil {
		return nil
	}
	object, ok := s.registry.Object(update.objectID)
	if !ok {
		return errUnknownObject
	}
	res, ok := object.Resources[update.resourceID]
	if !ok {
		return errUnknownResource
	}
	if !res.Operations.Has(OpWrite) {
		return errNotWritable
	}
	if !object.Multiple && update.instanceID != 0 {
		return errUnknownObject
	}
	if res.Multiple {
		if !update.multiple {
			// A multiple resource is written as a TLV resource container.
			return errBadValue
		}
	} else if update.multiple {
		return errBadValue
	}
	if err := validateValue(res, update.value); err != nil {
		return fmt.Errorf("%w: %v", errBadValue, err)
	}
	return nil
}

// applyUpdates writes every update under one lock, so a multi-resource write is
// atomic.
func (s *Server) applyUpdates(updates []objectUpdate) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for _, update := range updates {
		key := resourceKey(update.objectID, update.instanceID, update.resourceID)
		if !update.multiple {
			s.store.setLocked(key, []Instance{{ID: 0, Value: update.value}})
			continue
		}
		existing := s.store.values[key]
		replaced := false
		for i := range existing {
			if existing[i].ID == update.instance {
				existing[i].Value = append([]byte(nil), update.value...)
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, Instance{ID: update.instance, Value: append([]byte(nil), update.value...)})
		}
		s.store.setLocked(key, existing)
	}
}

// notifyUpdates reports each changed resource once.
func (s *Server) notifyUpdates(updates []objectUpdate) {
	seen := make(map[string]bool, len(updates))
	for _, update := range updates {
		key := resourceKey(update.objectID, update.instanceID, update.resourceID)
		if seen[key] {
			continue
		}
		seen[key] = true
		s.notifyChanged(update.objectID, update.instanceID, update.resourceID)
	}
}

// handleExecute serves POST /objects/{oid}/{iid}/{rid}.
func (s *Server) handleExecute(_ context.Context, req gcoap.Request) gcoap.Response {
	ids, ok := pathIDs(req.Path)
	if !ok || len(ids) != 3 {
		return gcoap.Response{Code: coap.CodeBadRequest}
	}
	if s.registry != nil {
		res, _, ok := s.registry.Resource(ids[0], ids[2])
		if !ok {
			return gcoap.Response{Code: coap.CodeNotFound}
		}
		if !res.Operations.Has(OpExecute) {
			return gcoap.Response{Code: coap.CodeMethodNotAllowed}
		}
	} else if _, ok := s.Resource(ids[0], ids[1], ids[2]); !ok {
		return gcoap.Response{Code: coap.CodeNotFound}
	}
	return gcoap.Response{Code: coap.CodeChanged}
}

// notifyChanged notifies any observer of the resource, its instance and its
// object.
func (s *Server) notifyChanged(objectID, instanceID, resourceID uint16) {
	if s.coap == nil {
		return
	}
	instances := s.store.get(resourceKey(objectID, instanceID, resourceID))
	if len(instances) == 0 {
		return
	}
	multiple := len(instances) > 1
	if s.registry != nil {
		if def, _, ok := s.registry.Resource(objectID, resourceID); ok && def.Multiple {
			multiple = true
		}
	}
	if multiple {
		entry := multipleResourceEntry(resourceID, instances)
		if payload, err := tlv.Marshal([]tlv.Entry{entry}); err == nil {
			s.coap.Notify(resourcePath(objectID, instanceID, resourceID), gcoap.Response{
				Code:    coap.CodeContent,
				Options: []coap.Option{{Number: coap.OptionContentFormat, Value: contentFormatTLV}},
				Payload: payload,
			})
		}
	} else {
		s.coap.Notify(resourcePath(objectID, instanceID, resourceID), gcoap.Response{
			Code:    coap.CodeContent,
			Options: []coap.Option{{Number: coap.OptionContentFormat, Value: []byte{0}}},
			Payload: instances[0].Value,
		})
	}
	if entries := s.instanceEntries(objectID, instanceID); len(entries) > 0 {
		if payload, err := tlv.Marshal(entries); err == nil {
			s.coap.Notify(instancePath(objectID, instanceID), gcoap.Response{
				Code:    coap.CodeContent,
				Options: []coap.Option{{Number: coap.OptionContentFormat, Value: contentFormatTLV}},
				Payload: payload,
			})
		}
	}
	if entries := s.objectEntries(objectID); len(entries) > 0 {
		if payload, err := tlv.Marshal(entries); err == nil {
			s.coap.Notify(objectPath(objectID), gcoap.Response{
				Code:    coap.CodeContent,
				Options: []coap.Option{{Number: coap.OptionContentFormat, Value: contentFormatTLV}},
				Payload: payload,
			})
		}
	}
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

func resourcePath(oid, iid, rid uint16) string {
	return "/objects/" + strconv.Itoa(int(oid)) + "/" + strconv.Itoa(int(iid)) + "/" + strconv.Itoa(int(rid))
}

func instancePath(oid, iid uint16) string {
	return "/objects/" + strconv.Itoa(int(oid)) + "/" + strconv.Itoa(int(iid))
}

func objectPath(oid uint16) string {
	return "/objects/" + strconv.Itoa(int(oid))
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
