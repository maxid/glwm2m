package glwm2m

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety/codec/coap"
)

// eventRecorder collects registration events for assertions.
type eventRecorder struct {
	mu     sync.Mutex
	events []RegistrationEvent
}

func (r *eventRecorder) OnRegistration(_ context.Context, event RegistrationEvent) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *eventRecorder) snapshot() []RegistrationEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RegistrationEvent(nil), r.events...)
}

func startServerWith(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv
}

func newDeviceClient(t *testing.T, srv *Server) *gcoap.Client {
	t.Helper()
	cli, err := gcoap.Dial(context.Background(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// register performs POST /rd and returns the registration id.
func register(t *testing.T, cli *gcoap.Client, endpoint, lifetime, payload string) (string, gcoap.ClientResponse) {
	t.Helper()
	query := map[string][]string{"ep": {endpoint}, "b": {"U"}}
	if lifetime != "" {
		query["lt"] = []string{lifetime}
	}
	resp, err := cli.Do(context.Background(), gcoap.ClientRequest{
		Code: coap.CodePOST, Path: "/rd", Query: query, Payload: []byte(payload),
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	id := ""
	for _, option := range resp.Options {
		if option.Number == coap.OptionLocationPath && string(option.Value) != "rd" {
			id = string(option.Value)
		}
	}
	return id, resp
}

func TestRegistrationEndpointConflictAndEpoch(t *testing.T) {
	recorder := &eventRecorder{}
	srv := startServerWith(t, Config{Events: recorder})
	first := newDeviceClient(t, srv)
	second := newDeviceClient(t, srv)

	id1, resp := register(t, first, "dev-1", "60", "")
	if resp.Code != coap.CodeCreated {
		t.Fatalf("first register = %v", resp.Code)
	}
	id2, resp := register(t, second, "dev-1", "60", "")
	if resp.Code != coap.CodeCreated {
		t.Fatalf("second register = %v", resp.Code)
	}
	if srv.Count() != 1 {
		t.Fatalf("count = %d, want 1", srv.Count())
	}
	if _, ok := srv.Registration(id1); ok {
		t.Fatal("the replaced registration is still present")
	}
	reg, ok := srv.Registration(id2)
	if !ok {
		t.Fatal("the new registration is missing")
	}
	if reg.Epoch != 2 {
		t.Fatalf("epoch = %d, want 2", reg.Epoch)
	}
	events := recorder.snapshot()
	if len(events) != 3 ||
		events[0].Type != EventRegistered ||
		events[1].Type != EventDeregistered || events[1].Reason != "replaced" || events[1].Registration.ID != id1 ||
		events[2].Type != EventRegistered || events[2].Registration.ID != id2 {
		t.Fatalf("events = %+v", events)
	}
}

func TestRegistrationRejectsEndpointConflictWhenConfigured(t *testing.T) {
	srv := startServerWith(t, Config{RejectEndpointConflict: true})
	first := newDeviceClient(t, srv)
	second := newDeviceClient(t, srv)
	id, _ := register(t, first, "dev-1", "60", "")
	if _, resp := register(t, second, "dev-1", "60", ""); resp.Code != coap.CodeConflict {
		t.Fatalf("conflict register = %v, want 4.09", resp.Code)
	}
	if _, ok := srv.Registration(id); !ok {
		t.Fatal("the original registration was dropped")
	}
}

func TestRegistrationPeerBinding(t *testing.T) {
	srv := startServerWith(t, Config{})
	owner := newDeviceClient(t, srv)
	other := newDeviceClient(t, srv)
	id, _ := register(t, owner, "dev-1", "60", "")
	if id == "" {
		t.Fatal("missing registration id")
	}
	update := func(cli *gcoap.Client) gcoap.ClientResponse {
		resp, err := cli.Do(context.Background(), gcoap.ClientRequest{
			Code: coap.CodePOST, Path: "/rd/" + id, Query: map[string][]string{"lt": {"120"}},
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		return resp
	}
	if resp := update(other); resp.Code != coap.CodeNotFound {
		t.Fatalf("update from another peer = %v, want 4.04", resp.Code)
	}
	if resp := update(owner); resp.Code != coap.CodeChanged {
		t.Fatalf("update from the owner = %v", resp.Code)
	}
	del, err := other.Do(context.Background(), gcoap.ClientRequest{Code: coap.CodeDELETE, Path: "/rd/" + id})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if del.Code != coap.CodeNotFound {
		t.Fatalf("delete from another peer = %v, want 4.04", del.Code)
	}
	if srv.Count() != 1 {
		t.Fatalf("count = %d", srv.Count())
	}
}

func TestRegistrationExpiry(t *testing.T) {
	recorder := &eventRecorder{}
	srv := startServerWith(t, Config{Events: recorder})
	cli := newDeviceClient(t, srv)
	id, _ := register(t, cli, "dev-1", "1", "")
	if _, ok := srv.Registration(id); !ok {
		t.Fatal("registration missing")
	}
	srv.expire(srv.now().Add(2 * time.Second))
	if srv.Count() != 0 {
		t.Fatalf("count after expiry = %d", srv.Count())
	}
	events := recorder.snapshot()
	if len(events) != 2 || events[1].Type != EventExpired || events[1].Reason != "lifetime" {
		t.Fatalf("events = %+v", events)
	}
	// A second scan must not report the same expiration again.
	srv.expire(srv.now().Add(4 * time.Second))
	if got := len(recorder.snapshot()); got != 2 {
		t.Fatalf("events after a second scan = %d, want 2", got)
	}
}

func TestRegistrationLimitsAndValidation(t *testing.T) {
	srv := startServerWith(t, Config{MaxRegistrations: 1, DefaultLifetime: 30 * time.Minute, MaxLifetime: time.Hour})
	first := newDeviceClient(t, srv)
	second := newDeviceClient(t, srv)
	firstID, resp := register(t, first, "dev-1", "60", "")
	if resp.Code != coap.CodeCreated {
		t.Fatalf("first register = %v", resp.Code)
	}
	if _, resp := register(t, second, "dev-2", "60", ""); resp.Code != coap.CodeServiceUnavailable {
		t.Fatalf("second register = %v, want 5.03", resp.Code)
	}
	if del, err := first.Do(context.Background(), gcoap.ClientRequest{Code: coap.CodeDELETE, Path: "/rd/" + firstID}); err != nil || del.Code != coap.CodeDeleted {
		t.Fatalf("delete = %v, %v", del.Code, err)
	}
	for _, lifetime := range []string{"0", "-1", "abc"} {
		if _, resp := register(t, first, "dev-3", lifetime, ""); resp.Code != coap.CodeBadRequest {
			t.Fatalf("lt=%s register = %v, want 4.00", lifetime, resp.Code)
		}
	}
	// A lifetime above the maximum is clamped, not turned into a permanent
	// registration.
	id, resp := register(t, first, "dev-4", "999999", "")
	if resp.Code != coap.CodeCreated {
		t.Fatalf("clamped register = %v", resp.Code)
	}
	reg, _ := srv.Registration(id)
	if reg.Lifetime != int(time.Hour/time.Second) {
		t.Fatalf("lifetime = %d, want %d", reg.Lifetime, int(time.Hour/time.Second))
	}
}

func TestRegistrationObjectLinks(t *testing.T) {
	srv := startServerWith(t, Config{})
	cli := newDeviceClient(t, srv)
	id, _ := register(t, cli, "dev-links", "60", "</1/0>,</3/0>,</3303/0>")
	reg, ok := srv.Registration(id)
	if !ok {
		t.Fatal("registration missing")
	}
	if len(reg.ObjectLinks) != 3 || reg.ObjectLinks[1] != "</3/0>" {
		t.Fatalf("object links = %v", reg.ObjectLinks)
	}
	if reg.Peer.Addr == "" || reg.Peer.Transport != "udp" {
		t.Fatalf("peer = %+v", reg.Peer)
	}
}

func TestRegistrationEmptyEndpointRejected(t *testing.T) {
	srv := startServerWith(t, Config{})
	cli := newDeviceClient(t, srv)
	resp, err := cli.Do(context.Background(), gcoap.ClientRequest{
		Code: coap.CodePOST, Path: "/rd", Query: map[string][]string{"lt": {"60"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Code != coap.CodeBadRequest {
		t.Fatalf("register without ep = %v, want 4.00", resp.Code)
	}
}
