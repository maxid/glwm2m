package glwm2m

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety/codec/coap"
)

// Errors returned by the server-initiated device management API.
var (
	// ErrRegistrationNotFound is returned when an operation names a registration
	// that does not exist or already expired.
	ErrRegistrationNotFound = errors.New("glwm2m: registration not found")
	// ErrInvalidPath is returned for an object path that cannot be addressed.
	ErrInvalidPath = errors.New("glwm2m: invalid object path")
	// ErrDownlinkUnsupported is returned when the registration has no usable
	// transport address and no custom dialer is configured.
	ErrDownlinkUnsupported = errors.New("glwm2m: downlink transport is not available")
)

// Path addresses one object, object instance or resource of a LwM2M client.
type Path struct {
	ObjectID uint16
	// InstanceID is required when ResourceID is set.
	InstanceID *uint16
	ResourceID *uint16
}

// String renders the path as the CoAP URI used on the wire.
func (p Path) String() string {
	out := "/objects/" + strconv.Itoa(int(p.ObjectID))
	if p.InstanceID != nil {
		out += "/" + strconv.Itoa(int(*p.InstanceID))
	}
	if p.ResourceID != nil {
		if p.InstanceID == nil {
			return ""
		}
		out += "/" + strconv.Itoa(int(*p.ResourceID))
	}
	return out
}

// valid reports whether the path can be addressed.
func (p Path) valid() bool {
	return p.String() != ""
}

// Operation is one server-initiated device management request. The zero
// Timeout selects Config.DownlinkTimeout.
type Operation struct {
	RegistrationID string
	Path           Path
	// Payload is the Write or Execute argument; Read ignores it.
	Payload []byte
	// ContentFormat is the request content format; zero leaves it unset.
	ContentFormat uint16
	Timeout       time.Duration
}

// Value is one value read from, or notified by, a device.
type Value struct {
	ContentFormat uint16
	Payload       []byte
}

// OperationError reports a device that answered an operation with an error
// response. A 4.04 is a valid answer, not a transport failure.
type OperationError struct {
	RegistrationID string
	Code           coap.Code
}

// Error implements error.
func (e *OperationError) Error() string {
	return fmt.Sprintf("glwm2m: registration %s answered %s", e.RegistrationID, e.Code)
}

// downlinkState caches one CoAP client per registration and the observations it
// owns. Closing a registration closes its client, which also ends the
// observations.
type downlinkState struct {
	mu           sync.Mutex
	clients      map[string]*downlinkClient
	observations map[string][]*DownlinkObservation
}

type downlinkClient struct {
	addr  string
	epoch uint64
	cli   *gcoap.Client
}

func newDownlinkState() *downlinkState {
	return &downlinkState{
		clients:      make(map[string]*downlinkClient),
		observations: make(map[string][]*DownlinkObservation),
	}
}

// registration returns a snapshot of a live registration.
func (s *Server) registration(id string) (Registration, error) {
	s.mu.RLock()
	reg := s.regs[id]
	s.mu.RUnlock()
	if reg == nil || !s.now().Before(reg.ExpiresAt) {
		return Registration{}, ErrRegistrationNotFound
	}
	return reg.snapshot(), nil
}

// downlinkClient returns the cached CoAP client of a registration or dials a
// new one. The dial runs under the state lock, so two concurrent operations use
// one connection.
func (s *Server) downlinkClient(ctx context.Context, reg Registration) (*gcoap.Client, error) {
	s.dl.mu.Lock()
	defer s.dl.mu.Unlock()
	if cached := s.dl.clients[reg.ID]; cached != nil {
		if cached.addr == reg.Peer.Addr && cached.epoch == reg.Epoch {
			return cached.cli, nil
		}
		delete(s.dl.clients, reg.ID)
		_ = cached.cli.Close()
	}
	if reg.Peer.Addr == "" {
		return nil, ErrDownlinkUnsupported
	}
	var (
		cli *gcoap.Client
		err error
	)
	if s.cfg.DownlinkDialer != nil {
		cli, err = s.cfg.DownlinkDialer(ctx, reg)
	} else {
		network := reg.Peer.Transport
		if network == "" {
			network = "udp"
		}
		cli, err = gcoap.DialConfig(ctx, gcoap.ClientConfig{
			Network:        network,
			Addr:           reg.Peer.Addr,
			TLS:            s.cfg.DownlinkTLS,
			MaxRequestSize: s.cfg.DownlinkMaxRequestSize,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("glwm2m: dial %s: %w", reg.Peer.Addr, err)
	}
	s.dl.clients[reg.ID] = &downlinkClient{addr: reg.Peer.Addr, epoch: reg.Epoch, cli: cli}
	return cli, nil
}

// closeDownlink drops the cached client of one registration and ends its
// observations. It is safe to call for an unknown id.
func (s *Server) closeDownlink(registrationID string) {
	s.dl.mu.Lock()
	cached := s.dl.clients[registrationID]
	delete(s.dl.clients, registrationID)
	list := s.dl.observations[registrationID]
	delete(s.dl.observations, registrationID)
	s.dl.mu.Unlock()
	for _, obs := range list {
		obs.finish()
	}
	if cached != nil {
		_ = cached.cli.Close()
	}
}

// closeAllDownlinks closes every cached client and observation.
func (s *Server) closeAllDownlinks() {
	s.dl.mu.Lock()
	ids := make([]string, 0, len(s.dl.clients))
	for id := range s.dl.clients {
		ids = append(ids, id)
	}
	for id := range s.dl.observations {
		if _, ok := s.dl.clients[id]; !ok {
			ids = append(ids, id)
		}
	}
	s.dl.mu.Unlock()
	for _, id := range ids {
		s.closeDownlink(id)
	}
}

// operationContext applies the per-operation timeout.
func (s *Server) operationContext(ctx context.Context, op Operation) (context.Context, context.CancelFunc) {
	timeout := op.Timeout
	if timeout <= 0 {
		timeout = s.cfg.DownlinkTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// do performs one operation against the device and rejects an error response.
func (s *Server) do(ctx context.Context, reg Registration, req gcoap.ClientRequest, op Operation) (gcoap.ClientResponse, error) {
	cli, err := s.downlinkClient(ctx, reg)
	if err != nil {
		return gcoap.ClientResponse{}, err
	}
	opCtx, cancel := s.operationContext(ctx, op)
	defer cancel()
	resp, err := cli.Do(opCtx, req)
	if err != nil {
		return gcoap.ClientResponse{}, err
	}
	if resp.Code.Class() != 2 {
		return gcoap.ClientResponse{}, &OperationError{RegistrationID: reg.ID, Code: resp.Code}
	}
	return resp, nil
}

// Read performs a LwM2M Read against a registered client.
func (s *Server) Read(ctx context.Context, op Operation) (Value, error) {
	if !op.Path.valid() {
		return Value{}, ErrInvalidPath
	}
	reg, err := s.registration(op.RegistrationID)
	if err != nil {
		return Value{}, err
	}
	resp, err := s.do(ctx, reg, gcoap.ClientRequest{Code: coap.CodeGET, Path: op.Path.String()}, op)
	if err != nil {
		return Value{}, err
	}
	return Value{ContentFormat: contentFormatOf(resp.Options), Payload: append([]byte(nil), resp.Payload...)}, nil
}

// Write performs a LwM2M Write against a registered client.
func (s *Server) Write(ctx context.Context, op Operation) error {
	if !op.Path.valid() {
		return ErrInvalidPath
	}
	reg, err := s.registration(op.RegistrationID)
	if err != nil {
		return err
	}
	req := gcoap.ClientRequest{Code: coap.CodePUT, Path: op.Path.String(), Payload: op.Payload}
	if op.ContentFormat != 0 {
		req.Options = []coap.Option{contentFormatOption(op.ContentFormat)}
	}
	_, err = s.do(ctx, reg, req, op)
	return err
}

// Execute performs a LwM2M Execute against a registered client.
func (s *Server) Execute(ctx context.Context, op Operation) error {
	if !op.Path.valid() || op.Path.ResourceID == nil {
		return ErrInvalidPath
	}
	reg, err := s.registration(op.RegistrationID)
	if err != nil {
		return err
	}
	req := gcoap.ClientRequest{Code: coap.CodePOST, Path: op.Path.String(), Payload: op.Payload}
	if op.ContentFormat != 0 {
		req.Options = []coap.Option{contentFormatOption(op.ContentFormat)}
	}
	_, err = s.do(ctx, reg, req, op)
	return err
}

// DownlinkObservation is one Observe relation started by the server.
type DownlinkObservation struct {
	srv   *Server
	regID string
	done  chan struct{}
	once  sync.Once

	mu     sync.Mutex
	obs    *gcoap.Observation
	cancel context.CancelFunc
}

// Done is closed when the observation ends.
func (o *DownlinkObservation) Done() <-chan struct{} { return o.done }

// Cancel stops the observation and deregisters it from the client.
func (o *DownlinkObservation) Cancel(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.srv.removeObservation(o)
	o.mu.Lock()
	obs := o.obs
	o.mu.Unlock()
	var err error
	if obs != nil {
		err = obs.Cancel(ctx)
	}
	o.finish()
	return err
}

// finish marks the observation ended without a deregistration. Closing the
// client already stops the underlying observation; the cancellation context is
// released here so its goroutine can exit.
func (o *DownlinkObservation) finish() {
	o.once.Do(func() {
		o.mu.Lock()
		o.obs = nil
		cancel := o.cancel
		o.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		close(o.done)
	})
}

// removeObservation drops the wrapper from the server tracking.
func (s *Server) removeObservation(o *DownlinkObservation) {
	s.dl.mu.Lock()
	list := s.dl.observations[o.regID]
	out := list[:0]
	for _, item := range list {
		if item != o {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		delete(s.dl.observations, o.regID)
	} else {
		s.dl.observations[o.regID] = out
	}
	s.dl.mu.Unlock()
}

// Observe starts a LwM2M Observe against a registered client. It returns the
// registration value and the observation; later values go to handler.
func (s *Server) Observe(ctx context.Context, op Operation, handler func(Value)) (*DownlinkObservation, Value, error) {
	if !op.Path.valid() || handler == nil {
		return nil, Value{}, ErrInvalidPath
	}
	reg, err := s.registration(op.RegistrationID)
	if err != nil {
		return nil, Value{}, err
	}
	cli, err := s.downlinkClient(ctx, reg)
	if err != nil {
		return nil, Value{}, err
	}
	// The observation outlives this call: it is detached from the caller's
	// deadline, and the registration exchange gets its own timer below.
	obsCtx, obsCancel := context.WithCancel(context.WithoutCancel(ctx))
	type started struct {
		obs     *gcoap.Observation
		initial gcoap.ClientResponse
		err     error
	}
	startedCh := make(chan started, 1)
	go func() {
		obs, initial, err := cli.Observe(obsCtx, gcoap.ClientRequest{Code: coap.CodeGET, Path: op.Path.String()}, func(resp gcoap.ClientResponse) {
			handler(Value{ContentFormat: contentFormatOf(resp.Options), Payload: append([]byte(nil), resp.Payload...)})
		})
		startedCh <- started{obs: obs, initial: initial, err: err}
	}()
	timeout := op.Timeout
	if timeout <= 0 {
		timeout = s.cfg.DownlinkTimeout
	}
	var result started
	select {
	case result = <-startedCh:
	case <-time.After(timeout):
		obsCancel()
		return nil, Value{}, context.DeadlineExceeded
	case <-ctx.Done():
		obsCancel()
		return nil, Value{}, ctx.Err()
	}
	if result.err != nil {
		obsCancel()
		return nil, Value{}, result.err
	}
	if result.initial.Code.Class() != 2 {
		_ = result.obs.Cancel(context.Background())
		obsCancel()
		return nil, Value{}, &OperationError{RegistrationID: reg.ID, Code: result.initial.Code}
	}
	wrapped := &DownlinkObservation{srv: s, regID: reg.ID, obs: result.obs, cancel: obsCancel, done: make(chan struct{})}
	go func() {
		<-result.obs.Done()
		wrapped.finish()
		s.removeObservation(wrapped)
	}()
	s.dl.mu.Lock()
	s.dl.observations[reg.ID] = append(s.dl.observations[reg.ID], wrapped)
	s.dl.mu.Unlock()
	return wrapped, Value{ContentFormat: contentFormatOf(result.initial.Options), Payload: append([]byte(nil), result.initial.Payload...)}, nil
}

// contentFormatOf returns the Content-Format option value.
func contentFormatOf(options []coap.Option) uint16 {
	for _, opt := range options {
		if opt.Number != coap.OptionContentFormat {
			continue
		}
		var value uint16
		for _, b := range opt.Value {
			value = value<<8 | uint16(b)
		}
		return value
	}
	return 0
}

// contentFormatOption encodes one Content-Format option.
func contentFormatOption(format uint16) coap.Option {
	value := []byte{}
	if format > 0xff {
		value = append(value, byte(format>>8))
	}
	value = append(value, byte(format))
	return coap.Option{Number: coap.OptionContentFormat, Value: value}
}
