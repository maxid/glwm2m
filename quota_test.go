package glwm2m

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitQuota waits until one peer holds want downlink slots; the release after an
// observation ends happens on the goroutine watching its Done channel.
func waitQuota(t *testing.T, srv *Server, addr string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := srv.quota.count(addr); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("downlink slots for %s = %d, want %d", addr, srv.quota.count(addr), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestDownlinkQuotaPerPeer bounds one peer: an observation holds a slot, a Read is
// refused while the peer is at its limit, and the slot comes back when the
// observation ends.
func TestDownlinkQuotaPerPeer(t *testing.T) {
	srv := startServerWith(t, Config{MaxDownlinksPerPeer: 1})
	device, id := startRegisteredDevice(t, srv, map[string]string{"/3303/0/5700": "21.5"}, "dev-quota")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	observation, initial, err := srv.Observe(ctx, Operation{
		RegistrationID: id, Path: resourcePathOf(3303, 0, 5700),
	}, func(Value) {})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if string(initial.Payload) != "21.5" {
		t.Fatalf("initial value = %q", initial.Payload)
	}
	if got := srv.quota.count(device.addr); got != 1 {
		t.Fatalf("downlink slots after Observe = %d, want 1", got)
	}

	if _, err := srv.Read(ctx, Operation{RegistrationID: id, Path: resourcePathOf(3303, 0, 5700)}); !errors.Is(err, ErrTooManyDownlinks) {
		t.Fatalf("Read with the peer at its limit = %v, want ErrTooManyDownlinks", err)
	}

	if err := observation.Cancel(ctx); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitQuota(t, srv, device.addr, 0)
	if _, err := srv.Read(ctx, Operation{RegistrationID: id, Path: resourcePathOf(3303, 0, 5700)}); err != nil {
		t.Fatalf("Read after the observation ended: %v", err)
	}
}

// TestDownlinkQuotaPerPeerIsolation gives every peer address its own budget.
func TestDownlinkQuotaPerPeerIsolation(t *testing.T) {
	srv := startServerWith(t, Config{MaxDownlinksPerPeer: 1})
	first, firstID := startRegisteredDevice(t, srv, map[string]string{"/3303/0/5700": "1"}, "dev-quota-a")
	_, secondID := startRegisteredDevice(t, srv, map[string]string{"/3303/0/5700": "2"}, "dev-quota-b")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	observation, _, err := srv.Observe(ctx, Operation{
		RegistrationID: firstID, Path: resourcePathOf(3303, 0, 5700),
	}, func(Value) {})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	t.Cleanup(func() { _ = observation.Cancel(context.Background()) })

	if _, err := srv.Read(ctx, Operation{RegistrationID: secondID, Path: resourcePathOf(3303, 0, 5700)}); err != nil {
		t.Fatalf("another peer's Read = %v, want it to work", err)
	}
	if got := srv.quota.count(first.addr); got != 1 {
		t.Fatalf("slots for the first peer = %d, want 1", got)
	}
}

// TestDownlinkQuotaRejectsRequest counts a request that is in flight and refuses a
// second one from the same peer.
func TestDownlinkQuotaRejectsRequest(t *testing.T) {
	srv := startServerWith(t, Config{MaxDownlinksPerPeer: 1})
	device, id := startRegisteredDevice(t, srv, map[string]string{"/3303/0/5700": "1"}, "dev-quota-busy")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Simulate a Read that has not finished yet.
	if err := srv.quota.acquire(device.addr); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer srv.quota.release(device.addr)

	if err := srv.Write(ctx, Operation{RegistrationID: id, Path: resourcePathOf(3303, 0, 5700), Payload: []byte("2")}); !errors.Is(err, ErrTooManyDownlinks) {
		t.Fatalf("Write with the peer at its limit = %v, want ErrTooManyDownlinks", err)
	}
}

// TestDownlinkQuotaDefaultsAndValidation checks the default and the rejection of a
// negative limit.
func TestDownlinkQuotaDefaultsAndValidation(t *testing.T) {
	srv := startServerWith(t, Config{})
	if srv.quota.max != DefaultMaxDownlinksPerPeer {
		t.Fatalf("default MaxDownlinksPerPeer = %d, want %d", srv.quota.max, DefaultMaxDownlinksPerPeer)
	}
	if _, err := New(Config{MaxDownlinksPerPeer: -1}); err == nil {
		t.Fatal("New accepted a negative MaxDownlinksPerPeer")
	}
}
