package glwm2m

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// RegistrationStore persists registrations so a restarted server keeps them.
//
// The server owns the semantics — when a registration expires, how an endpoint
// conflict replaces one, what the epoch means — and a store only keeps the records
// it is handed: an upsert by ID, a delete by ID and a full list. Implementations
// must be safe for concurrent use.
//
// A restored registration carries the peer address it registered from. After a
// restart that address may be stale (a NAT rebind, a new lease); the first update
// from the device rebinds it, and server-initiated operations to the old address
// simply time out.
type RegistrationStore interface {
	// Save stores one registration, replacing the record with the same ID.
	Save(ctx context.Context, reg Registration) error
	// Delete removes one registration. Deleting an unknown ID is not an error.
	Delete(ctx context.Context, id string) error
	// List returns every stored registration, in any order.
	List(ctx context.Context) ([]Registration, error)
}

// ErrStoreClosed is returned by a store that has been closed.
var ErrStoreClosed = errors.New("glwm2m: registration store is closed")

// fileStoreFormat is the on-disk envelope; the version guards a future migration.
type fileStoreFormat struct {
	Version       int            `json:"version"`
	Registrations []Registration `json:"registrations"`
}

// FileStore keeps registrations in one JSON file. Every change rewrites the file
// atomically (a temporary file renamed over it), which is enough for the
// registration counts a single server holds and avoids a dependency. Use
// FileStore when a restart must not lose registrations; leave Config.Store nil
// when it is fine for devices to re-register.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a store backed by path. The file does not have to exist:
// a missing file is an empty store, and the parent directory is created on the
// first write.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("glwm2m: FileStore needs a path")
	}
	return &FileStore{path: path}, nil
}

// Save stores one registration, replacing the record with the same ID.
func (s *FileStore) Save(ctx context.Context, reg Registration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	regs, err := s.read()
	if err != nil {
		return err
	}
	replaced := false
	for i := range regs {
		if regs[i].ID == reg.ID {
			regs[i] = reg
			replaced = true
			break
		}
	}
	if !replaced {
		regs = append(regs, reg)
	}
	return s.write(regs)
}

// Delete removes one registration.
func (s *FileStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	regs, err := s.read()
	if err != nil {
		return err
	}
	out := regs[:0]
	for _, reg := range regs {
		if reg.ID != id {
			out = append(out, reg)
		}
	}
	return s.write(out)
}

// List returns every stored registration.
func (s *FileStore) List(ctx context.Context) ([]Registration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

// read loads the file; a missing file is an empty store.
func (s *FileStore) read() ([]Registration, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("glwm2m: read %s: %w", s.path, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var stored fileStoreFormat
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("glwm2m: parse %s: %w", s.path, err)
	}
	if stored.Version != 1 {
		return nil, fmt.Errorf("glwm2m: %s has unsupported version %d", s.path, stored.Version)
	}
	sort.Slice(stored.Registrations, func(i, j int) bool {
		return stored.Registrations[i].ID < stored.Registrations[j].ID
	})
	return stored.Registrations, nil
}

// write replaces the file atomically.
func (s *FileStore) write(regs []Registration) error {
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("glwm2m: create %s: %w", dir, err)
		}
	}
	raw, err := json.MarshalIndent(fileStoreFormat{Version: 1, Registrations: regs}, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".registrations-*.tmp")
	if err != nil {
		return fmt.Errorf("glwm2m: create temporary file next to %s: %w", s.path, err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }() // best effort when the rename failed
	if _, err := temp.Write(append(raw, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

// storeSave writes one registration through Config.Store. A failure is reported
// through Config.StoreError and never fails the protocol request: a device's
// registration must not depend on a disk.
func (s *Server) storeSave(reg Registration) {
	if s.cfg.Store == nil {
		return
	}
	if err := s.cfg.Store.Save(context.Background(), reg); err != nil {
		s.reportStoreError(err)
	}
}

// storeDelete removes one registration from Config.Store.
func (s *Server) storeDelete(id string) {
	if s.cfg.Store == nil {
		return
	}
	if err := s.cfg.Store.Delete(context.Background(), id); err != nil {
		s.reportStoreError(err)
	}
}

func (s *Server) reportStoreError(err error) {
	if s.cfg.StoreError != nil {
		s.cfg.StoreError(err)
	}
}

// restore loads the registrations Config.Store holds. An expired record, one with
// an unusable endpoint and one whose endpoint another record already claims are
// dropped from the store, and the server's own expiry scan takes over from there.
func (s *Server) restore(ctx context.Context) error {
	if s.cfg.Store == nil {
		return nil
	}
	regs, err := s.cfg.Store.List(ctx)
	if err != nil {
		return fmt.Errorf("glwm2m: load registrations: %w", err)
	}
	now := s.now()
	var stale []string
	s.mu.Lock()
	for _, reg := range regs {
		if reg.ID == "" || !validEndpoint(reg.Endpoint) || !now.Before(reg.ExpiresAt) {
			stale = append(stale, reg.ID)
			continue
		}
		if _, taken := s.byEndpoint[reg.Endpoint]; taken {
			continue
		}
		if len(s.regs) >= s.cfg.MaxRegistrations {
			continue
		}
		copied := reg
		s.regs[copied.ID] = &copied
		s.byEndpoint[copied.Endpoint] = copied.ID
	}
	s.mu.Unlock()
	for _, id := range stale {
		s.storeDelete(id)
	}
	return nil
}
