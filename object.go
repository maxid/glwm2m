package glwm2m

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/maxid/gnety/codec/tlv"
)

// OperationMask is the set of operations a resource supports.
type OperationMask uint8

// Resource operations.
const (
	OpRead OperationMask = 1 << iota
	OpWrite
	OpExecute
)

// Has reports whether every operation in want is allowed.
func (m OperationMask) Has(want OperationMask) bool { return m&want == want }

// String renders the mask as "RW" style letters for diagnostics.
func (m OperationMask) String() string {
	out := ""
	if m.Has(OpRead) {
		out += "R"
	}
	if m.Has(OpWrite) {
		out += "W"
	}
	if m.Has(OpExecute) {
		out += "E"
	}
	return out
}

// ValueType is the LwM2M data type of a resource.
type ValueType uint8

// Resource value types.
const (
	ValueString ValueType = iota
	ValueOpaque
	ValueInteger
	ValueFloat
	ValueBoolean
	ValueTime
	ValueObjectLink
)

// String names the value type.
func (t ValueType) String() string {
	switch t {
	case ValueString:
		return "string"
	case ValueOpaque:
		return "opaque"
	case ValueInteger:
		return "integer"
	case ValueFloat:
		return "float"
	case ValueBoolean:
		return "boolean"
	case ValueTime:
		return "time"
	case ValueObjectLink:
		return "objlink"
	default:
		return "unknown"
	}
}

// ResourceDef defines one resource of an object.
type ResourceDef struct {
	// ID is the resource identifier.
	ID uint16
	// Name is a human-readable name, used in errors and diagnostics.
	Name string
	// Operations is the set of operations the resource supports.
	Operations OperationMask
	// Multiple marks a resource that holds several instances addressed by an
	// instance identifier.
	Multiple bool
	// Mandatory marks a resource every instance should carry.
	Mandatory bool
	// Type is the value type used to validate writes.
	Type ValueType
	// Units is the unit of the value, for diagnostics.
	Units string
}

// ObjectDef defines one LwM2M object and its resources.
type ObjectDef struct {
	ID        uint16
	Version   string
	Multiple  bool
	Mandatory bool
	Resources map[uint16]ResourceDef
}

// ObjectRegistry is the validated set of object definitions a server enforces.
//
// A nil registry keeps the permissive behavior: any object, instance or
// resource can be written and read. A configured registry rejects unknown
// objects and resources and enforces the declared operations, multiplicity and
// value types.
type ObjectRegistry struct {
	defs map[uint16]ObjectDef
}

// NewObjectRegistry validates defs and returns the registry.
func NewObjectRegistry(defs ...ObjectDef) (*ObjectRegistry, error) {
	out := &ObjectRegistry{defs: make(map[uint16]ObjectDef, len(defs))}
	for _, def := range defs {
		if _, exists := out.defs[def.ID]; exists {
			return nil, fmt.Errorf("glwm2m: object %d is defined twice", def.ID)
		}
		if len(def.Resources) == 0 {
			return nil, fmt.Errorf("glwm2m: object %d has no resources", def.ID)
		}
		copied := def
		copied.Resources = make(map[uint16]ResourceDef, len(def.Resources))
		for id, res := range def.Resources {
			if id != res.ID {
				return nil, fmt.Errorf("glwm2m: object %d resource map key %d does not match resource id %d", def.ID, id, res.ID)
			}
			if res.Operations == 0 {
				return nil, fmt.Errorf("glwm2m: object %d resource %d has no operations", def.ID, id)
			}
			if res.Operations&^(OpRead|OpWrite|OpExecute) != 0 {
				return nil, fmt.Errorf("glwm2m: object %d resource %d has unknown operations", def.ID, id)
			}
			if res.Multiple && res.Operations.Has(OpExecute) {
				return nil, fmt.Errorf("glwm2m: object %d resource %d is multiple and executable", def.ID, id)
			}
			if res.Type > ValueObjectLink {
				return nil, fmt.Errorf("glwm2m: object %d resource %d has unknown value type %d", def.ID, id, res.Type)
			}
			copied.Resources[id] = res
		}
		out.defs[def.ID] = copied
	}
	return out, nil
}

// Object returns one object definition.
func (r *ObjectRegistry) Object(id uint16) (ObjectDef, bool) {
	if r == nil {
		return ObjectDef{}, false
	}
	def, ok := r.defs[id]
	return def, ok
}

// Resource returns one resource definition and its object.
func (r *ObjectRegistry) Resource(objectID, resourceID uint16) (ResourceDef, ObjectDef, bool) {
	if r == nil {
		return ResourceDef{}, ObjectDef{}, false
	}
	def, ok := r.defs[objectID]
	if !ok {
		return ResourceDef{}, ObjectDef{}, false
	}
	res, ok := def.Resources[resourceID]
	return res, def, ok
}

// Objects returns the definitions in object-id order.
func (r *ObjectRegistry) Objects() []ObjectDef {
	if r == nil {
		return nil
	}
	out := make([]ObjectDef, 0, len(r.defs))
	for _, def := range r.defs {
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// validateValue checks value against the resource type.
func validateValue(def ResourceDef, value []byte) error {
	switch def.Type {
	case ValueString:
		if !utf8.Valid(value) {
			return fmt.Errorf("glwm2m: resource %d is not valid UTF-8", def.ID)
		}
	case ValueOpaque:
		// Any byte sequence is a valid opaque value.
	case ValueInteger, ValueTime:
		if _, err := strconv.ParseInt(string(value), 10, 64); err != nil {
			return fmt.Errorf("glwm2m: resource %d is not an integer: %q", def.ID, value)
		}
	case ValueFloat:
		if _, err := strconv.ParseFloat(string(value), 64); err != nil {
			return fmt.Errorf("glwm2m: resource %d is not a float: %q", def.ID, value)
		}
	case ValueBoolean:
		if string(value) != "0" && string(value) != "1" {
			return fmt.Errorf("glwm2m: resource %d is not 0 or 1: %q", def.ID, value)
		}
	case ValueObjectLink:
		if len(value) != 4 && !validObjectLinkText(value) {
			return fmt.Errorf("glwm2m: resource %d is not an object link", def.ID)
		}
	}
	return nil
}

// validObjectLinkText accepts the "oid:iid" text form of an object link.
func validObjectLinkText(value []byte) bool {
	var objectID, instanceID int
	_, err := fmt.Sscanf(string(value), "%d:%d", &objectID, &instanceID)
	return err == nil
}

// Instance is one value of a resource. A single resource uses instance id 0; a
// multiple resource uses the instance identifiers the client assigned.
type Instance struct {
	ID    uint16
	Value []byte
}

// objectStore holds the local object tree: object/instance/resource path to its
// values. It is the "/objects" store, not the device-side object model of a
// remote client.
type objectStore struct {
	mu     sync.RWMutex
	values map[string][]Instance
}

func newObjectStore() *objectStore {
	return &objectStore{values: make(map[string][]Instance)}
}

// set stores instances for one resource. The caller must hold the write lock.
func (o *objectStore) setLocked(key string, instances []Instance) {
	out := make([]Instance, len(instances))
	for i, inst := range instances {
		out[i] = Instance{ID: inst.ID, Value: append([]byte(nil), inst.Value...)}
	}
	o.values[key] = out
}

// get returns copies of the instances of one resource.
func (o *objectStore) get(key string) []Instance {
	o.mu.RLock()
	defer o.mu.RUnlock()
	list := o.values[key]
	out := make([]Instance, len(list))
	for i, inst := range list {
		out[i] = Instance{ID: inst.ID, Value: append([]byte(nil), inst.Value...)}
	}
	return out
}

// snapshot returns a copy of every value, keyed by path.
func (o *objectStore) snapshot() map[string][]Instance {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make(map[string][]Instance, len(o.values))
	for key, list := range o.values {
		copied := make([]Instance, len(list))
		for i, inst := range list {
			copied[i] = Instance{ID: inst.ID, Value: append([]byte(nil), inst.Value...)}
		}
		out[key] = copied
	}
	return out
}

// resourceEntries renders one resource as ResourceValue or MultipleResource.
func resourceEntries(resourceID uint16, instances []Instance) tlv.Entry {
	if len(instances) == 1 && instances[0].ID == 0 {
		return tlv.Entry{Type: tlv.ResourceValue, ID: resourceID, Value: instances[0].Value}
	}
	return multipleResourceEntry(resourceID, instances)
}

// multipleResourceEntry always renders a MultipleResource container, which is
// what a resource declared as multiple must be encoded as even when it holds
// one instance.
func multipleResourceEntry(resourceID uint16, instances []Instance) tlv.Entry {
	children := make([]tlv.Entry, 0, len(instances))
	sorted := append([]Instance(nil), instances...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, inst := range sorted {
		children = append(children, tlv.Entry{Type: tlv.ResourceInstance, ID: inst.ID, Value: inst.Value})
	}
	return tlv.Entry{Type: tlv.MultipleResource, ID: resourceID, Children: children}
}
