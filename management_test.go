package glwm2m

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety"
	"github.com/maxid/gnety/codec/coap"
)

func u16Ptr(value uint16) *uint16 { return &value }

func resourcePathOf(objectID, instanceID, resourceID uint16) Path {
	return Path{ObjectID: objectID, InstanceID: u16Ptr(instanceID), ResourceID: u16Ptr(resourceID)}
}

// startRegisteredDevice starts a fake device and registers it with srv.
func startRegisteredDevice(t *testing.T, srv *Server, resources map[string]string, endpoint string) (*fakeDevice, string) {
	t.Helper()
	device := newFakeDevice(t, resources)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := device.register(ctx, srv.Addr(), endpoint)
	if err != nil {
		t.Fatalf("device register: %v", err)
	}
	if id == "" {
		t.Fatal("device registered without an id")
	}
	return device, id
}

func TestDownlinkReadWriteExecute(t *testing.T) {
	srv := startServerWith(t, Config{})
	path := "/objects/3303/0/5700"
	device, id := startRegisteredDevice(t, srv, map[string]string{path: "21.5"}, "dev-1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	operation := Operation{RegistrationID: id, Path: resourcePathOf(3303, 0, 5700)}
	value, err := srv.Read(ctx, operation)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(value.Payload) != "21.5" {
		t.Fatalf("Read = %q", value.Payload)
	}

	operation.Payload = []byte("22.5")
	if err := srv.Write(ctx, operation); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, ok := device.value(path); !ok || got != "22.5" {
		t.Fatalf("device value = %q, %v", got, ok)
	}

	if err := srv.Execute(ctx, operation); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if count := device.executeCount(path); count != 1 {
		t.Fatalf("execute count = %d, want 1", count)
	}

	// The registration must still be live after its own downlink traffic.
	if srv.Count() != 1 {
		t.Fatalf("count = %d", srv.Count())
	}
}

func TestDownlinkObserve(t *testing.T) {
	srv := startServerWith(t, Config{})
	path := "/objects/3303/0/5700"
	device, id := startRegisteredDevice(t, srv, map[string]string{path: "21.5"}, "dev-obs")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	values := make(chan string, 4)
	observation, initial, err := srv.Observe(ctx, Operation{RegistrationID: id, Path: resourcePathOf(3303, 0, 5700)}, func(value Value) {
		select {
		case values <- string(value.Payload):
		default:
		}
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if string(initial.Payload) != "21.5" {
		t.Fatalf("initial value = %q", initial.Payload)
	}
	device.notify(path, "22.5")
	select {
	case value := <-values:
		if value != "22.5" {
			t.Fatalf("notification = %q", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no downlink notification arrived")
	}
	if err := observation.Cancel(ctx); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	select {
	case <-observation.Done():
	case <-time.After(time.Second):
		t.Fatal("observation did not end")
	}
}

func TestDownlinkOperationRejected(t *testing.T) {
	srv := startServerWith(t, Config{})
	_, id := startRegisteredDevice(t, srv, map[string]string{}, "dev-404")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := srv.Read(ctx, Operation{RegistrationID: id, Path: resourcePathOf(3303, 0, 9999)})
	var rejected *OperationError
	if !errors.As(err, &rejected) || rejected.Code != coap.CodeNotFound {
		t.Fatalf("Read = %v, want a 4.04 OperationError", err)
	}
}

func TestDownlinkRegistrationNotFound(t *testing.T) {
	srv := startServerWith(t, Config{})
	if _, err := srv.Read(context.Background(), Operation{RegistrationID: "missing", Path: resourcePathOf(3303, 0, 5700)}); !errors.Is(err, ErrRegistrationNotFound) {
		t.Fatalf("Read = %v, want ErrRegistrationNotFound", err)
	}
}

// TestDownlinkDefaultDTLS exercises the built-in DTLS callback: a registration
// recorded with the dtls transport is called back with Config.DownlinkTLS.
func TestDownlinkDefaultDTLS(t *testing.T) {
	certFile, keyFile := dtlsCert(t)
	device, err := gcoap.New(gcoap.Config{
		Network: "dtls",
		Addr:    "127.0.0.1:0",
		TLS:     gnety.TLSConfig{CertFile: certFile, KeyFile: keyFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	device.Handle(coap.CodeGET, "/objects/3303/0/5700", func(context.Context, gcoap.Request) gcoap.Response {
		return gcoap.Response{Code: coap.CodeContent, Payload: []byte("dtls-21.5")}
	})
	if err := device.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = device.Stop(context.Background()) }()

	srv, err := New(Config{Addr: "127.0.0.1:0", DownlinkTLS: gnety.TLSConfig{CAFile: certFile}})
	if err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.regs["dtls-dev"] = &Registration{
		ID: "dtls-dev", Lifetime: 300, Epoch: 1,
		Peer:      PeerInfo{Addr: device.Addr(), Transport: "dtls"},
		ExpiresAt: time.Now().Add(time.Minute),
	}
	srv.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := srv.Read(ctx, Operation{RegistrationID: "dtls-dev", Path: resourcePathOf(3303, 0, 5700)})
	if err != nil {
		t.Fatalf("DTLS Read: %v", err)
	}
	if string(value.Payload) != "dtls-21.5" {
		t.Fatalf("DTLS Read = %q", value.Payload)
	}
}
