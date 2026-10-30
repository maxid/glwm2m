// Package tlv is a compatibility shim over github.com/maxid/gnety/codec/tlv.
//
// The LwM2M TLV wire format used to be implemented here. It now lives in
// gnety's codec layer, where every protocol codec in the stack shares one
// bounded, classified implementation. This package keeps the import path
// working for existing callers and carries no wire knowledge of its own: the
// types are aliases of gnety's and both functions delegate.
//
// New code should import github.com/maxid/gnety/codec/tlv directly; the LwM2M
// server engine in this module does. Expect this package to be dropped in a
// future major version.
package tlv

import (
	"github.com/maxid/gnety/codec/tlv"
	"github.com/maxid/gnety/codec/wire"
)

// Type is the LwM2M TLV type.
type Type = tlv.Type

// TLV types, aliases of the gnety constants.
const (
	// ObjectInstance is type 00: an object instance container.
	ObjectInstance = tlv.ObjectInstance
	// ResourceInstance is type 01: a resource instance leaf.
	ResourceInstance = tlv.ResourceInstance
	// MultipleResource is type 10: a multiple resource container.
	MultipleResource = tlv.MultipleResource
	// ResourceValue is type 11: a resource with a value.
	ResourceValue = tlv.ResourceValue
)

// Entry is one TLV entry. It is an alias, so an Entry built here can be passed
// to gnety's functions and the other way round.
type Entry = tlv.Entry

// Limits the codec enforces. See gnety's tlv package for what each one bounds.
const (
	// DefaultMaxSize bounds one payload when the caller sets no limit.
	DefaultMaxSize = tlv.DefaultMaxSize
	// MaxDepth is the deepest nesting the codec accepts.
	MaxDepth = tlv.MaxDepth
	// MaxValueLen is the largest payload one entry can carry.
	MaxValueLen = tlv.MaxValueLen
	// MaxID is the largest identifier an entry can carry.
	MaxID = tlv.MaxID
)

// ErrMalformed is the sentinel of the codec's malformed errors. It is kept for
// callers that classified the error this package returned before the move; the
// codec now classifies every error through codec/wire, so prefer
// wire.KindOf with wire.ErrMalformed, wire.ErrTooLarge or wire.ErrUnsupported.
var ErrMalformed = wire.ErrMalformed

// Marshal encodes entries as one TLV payload. The entries of one scope are
// emitted in identifier order and the caller's slices are copied, never
// retained or reordered: see gnety's tlv.Marshal.
func Marshal(entries []Entry) ([]byte, error) { return tlv.Marshal(entries) }

// Unmarshal decodes one TLV payload, bounded by DefaultMaxSize. The decoded
// values never alias data.
func Unmarshal(data []byte) ([]Entry, error) { return tlv.Unmarshal(data) }

// ParseLimit decodes one TLV payload that is at most maxSize bytes. A
// non-positive maxSize means DefaultMaxSize.
func ParseLimit(data []byte, maxSize int) ([]Entry, error) { return tlv.ParseLimit(data, maxSize) }
