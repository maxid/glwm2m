package glwm2m

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newStoreServer starts a server that persists to store and returns it.
func newStoreServer(t *testing.T, store RegistrationStore, cfg Config) *Server {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	cfg.Store = store
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv
}

func TestFileStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registrations.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// A missing file is an empty store.
	if regs, err := store.List(ctx); err != nil || len(regs) != 0 {
		t.Fatalf("List on a fresh store = %d entries, %v", len(regs), err)
	}

	now := time.Now()
	first := Registration{ID: "a", Endpoint: "dev-a", Lifetime: 300, RegisteredAt: now, LastSeen: now, ExpiresAt: now.Add(time.Minute)}
	second := Registration{ID: "b", Endpoint: "dev-b", Lifetime: 60, RegisteredAt: now, LastSeen: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Save replaces by id.
	first.Lifetime = 120
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	regs, err := reopened.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 2 || regs[0].ID != "a" || regs[1].ID != "b" {
		t.Fatalf("List = %+v, want a and b in order", regs)
	}
	if regs[0].Lifetime != 120 {
		t.Fatalf("lifetime = %d, want the replaced value 120", regs[0].Lifetime)
	}

	if err := reopened.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Delete(ctx, "missing"); err != nil {
		t.Fatalf("deleting an unknown id = %v, want nil", err)
	}
	regs, err = reopened.List(ctx)
	if err != nil || len(regs) != 1 || regs[0].ID != "b" {
		t.Fatalf("List after Delete = %+v, %v", regs, err)
	}
}

// TestRegistrationSurvivesRestart registers a device, stops the server and starts
// a new one on the same store: the registration is back, with its fields.
func TestRegistrationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registrations.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first := newStoreServer(t, store, Config{})
	_, id := startRegisteredDevice(t, first, map[string]string{"/3303/0/5700": "21.5"}, "dev-restart")
	before, ok := first.Registration(id)
	if !ok {
		t.Fatal("registration missing before the restart")
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	second := newStoreServer(t, reopened, Config{})
	if got := second.Count(); got != 1 {
		t.Fatalf("registrations after the restart = %d, want 1", got)
	}
	after, ok := second.Registration(id)
	if !ok {
		t.Fatal("registration missing after the restart")
	}
	if after.Endpoint != before.Endpoint || after.Lifetime != before.Lifetime || after.Epoch != before.Epoch {
		t.Fatalf("restored registration = %+v, want %+v", after, before)
	}
	if len(after.ObjectLinks) != len(before.ObjectLinks) {
		t.Fatalf("restored links = %v, want %v", after.ObjectLinks, before.ObjectLinks)
	}
}

// TestRestoreDropsExpiredRegistrations keeps a live record and drops an expired
// one — from memory and from the store.
func TestRestoreDropsExpiredRegistrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registrations.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	live := Registration{ID: "live", Endpoint: "dev-live", ExpiresAt: now.Add(time.Hour), RegisteredAt: now, LastSeen: now}
	dead := Registration{ID: "dead", Endpoint: "dev-dead", ExpiresAt: now.Add(-time.Minute), RegisteredAt: now, LastSeen: now}
	for _, reg := range []Registration{live, dead} {
		if err := store.Save(ctx, reg); err != nil {
			t.Fatal(err)
		}
	}

	srv := newStoreServer(t, store, Config{})
	if got := srv.Count(); got != 1 {
		t.Fatalf("registrations = %d, want only the live one", got)
	}
	if _, ok := srv.Registration("live"); !ok {
		t.Fatal("the live registration was not restored")
	}
	regs, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) != 1 || regs[0].ID != "live" {
		t.Fatalf("store after restore = %+v, want only live", regs)
	}
}

// failingStore fails every write.
type failingStore struct {
	mu   sync.Mutex
	errs int
}

func (f *failingStore) Save(context.Context, Registration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs++
	return errors.New("disk is full")
}
func (f *failingStore) Delete(context.Context, string) error { return errors.New("disk is full") }
func (f *failingStore) List(context.Context) ([]Registration, error) {
	return nil, errors.New("disk is unreadable")
}

// TestStoreFailureDoesNotFailRegistration keeps the protocol working when the
// store does not: the device still registers and the hook sees the failure.
func TestStoreFailureDoesNotFailRegistration(t *testing.T) {
	var mu sync.Mutex
	var seen []error
	writeOnly := &writeFailingStore{}
	server := newStoreServer(t, writeOnly, Config{StoreError: func(err error) {
		mu.Lock()
		seen = append(seen, err)
		mu.Unlock()
	}})
	_, id := startRegisteredDevice(t, server, map[string]string{"/3303/0/5700": "1"}, "dev-store-fail")
	if id == "" {
		t.Fatal("registration did not succeed with a failing write")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("the store failure was not reported")
	}
}

// writeFailingStore lists an empty store and fails every write.
type writeFailingStore struct{ failingStore }

func (w *writeFailingStore) List(context.Context) ([]Registration, error) { return nil, nil }
