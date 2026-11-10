package glwm2m

import (
	"errors"
	"fmt"
	"sync"
)

// ErrTooManyDownlinks is returned when one peer already has the maximum number of
// downlinks in flight (Config.MaxDownlinksPerPeer): a Read/Write/Execute running
// right now or an observation registered on it. The peer's existing operations
// keep working; another peer has its own budget.
var ErrTooManyDownlinks = errors.New("glwm2m: too many downlinks in flight for one peer")

// downlinkQuota counts the downlinks in flight per peer address. The address is the
// peer's transport identity — a reconnect from the same address does not reset the
// budget — and a zero max disables the bound.
type downlinkQuota struct {
	mu   sync.Mutex
	max  int
	used map[string]int
}

func newDownlinkQuota(max int) *downlinkQuota {
	return &downlinkQuota{max: max, used: make(map[string]int)}
}

// acquire takes one slot for addr, or reports that the peer is at its limit.
func (q *downlinkQuota) acquire(addr string) error {
	if q.max <= 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.used[addr] >= q.max {
		return fmt.Errorf("%w: %s holds %d", ErrTooManyDownlinks, addr, q.used[addr])
	}
	q.used[addr]++
	return nil
}

// release returns the slot taken by acquire. A release without an acquire (the
// bound disabled) is a no-op.
func (q *downlinkQuota) release(addr string) {
	if q.max <= 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.used[addr] <= 1 {
		delete(q.used, addr)
		return
	}
	q.used[addr]--
}

// count reports the slots one address holds; it is used by tests.
func (q *downlinkQuota) count(addr string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.used[addr]
}
