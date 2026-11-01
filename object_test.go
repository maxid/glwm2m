package glwm2m

import (
	"errors"
	"strconv"
	"testing"

	"github.com/maxid/gnety/codec/coap"
	"github.com/maxid/gnety/codec/tlv"
)

func testRegistry(t *testing.T) *ObjectRegistry {
	t.Helper()
	registry, err := NewObjectRegistry(
		ObjectDef{
			ID: 3303, Multiple: true,
			Resources: map[uint16]ResourceDef{
				5700: {ID: 5700, Name: "temperature", Operations: OpRead | OpWrite, Type: ValueFloat},
				5605: {ID: 5605, Name: "reset", Operations: OpRead, Type: ValueString},
				5701: {ID: 5701, Name: "reboot", Operations: OpExecute},
				5800: {ID: 5800, Name: "multi", Operations: OpRead | OpWrite, Multiple: true, Type: ValueString},
			},
		},
		ObjectDef{
			ID: 3, Multiple: false,
			Resources: map[uint16]ResourceDef{
				0: {ID: 0, Name: "manufacturer", Operations: OpRead, Type: ValueString},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestObjectRegistryValidation(t *testing.T) {
	cases := map[string][]ObjectDef{
		"duplicate object": {
			{ID: 1, Resources: map[uint16]ResourceDef{0: {ID: 0, Operations: OpRead}}},
			{ID: 1, Resources: map[uint16]ResourceDef{0: {ID: 0, Operations: OpRead}}},
		},
		"no resources": {{ID: 1}},
		"resource key mismatch": {
			{ID: 1, Resources: map[uint16]ResourceDef{1: {ID: 0, Operations: OpRead}}},
		},
		"no operations": {
			{ID: 1, Resources: map[uint16]ResourceDef{0: {ID: 0}}},
		},
		"unknown operations": {
			{ID: 1, Resources: map[uint16]ResourceDef{0: {ID: 0, Operations: 0x80}}},
		},
		"multiple executable": {
			{ID: 1, Resources: map[uint16]ResourceDef{0: {ID: 0, Operations: OpExecute, Multiple: true}}},
		},
		"unknown type": {
			{ID: 1, Resources: map[uint16]ResourceDef{0: {ID: 0, Operations: OpRead, Type: ValueType(99)}}},
		},
	}
	for name, defs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewObjectRegistry(defs...); err == nil {
				t.Fatal("NewObjectRegistry accepted an invalid definition")
			}
		})
	}
}

func TestObjectRegistryEnforcesOperations(t *testing.T) {
	srv := startServerWith(t, Config{Objects: testRegistry(t)})
	put := func(oid, iid, rid uint16, payload string) coap.Message {
		return exchange(t, srv, coap.Message{
			Type: coap.Confirmable, Code: coap.CodePUT, MessageID: 1,
			Options: options("objects", itoa(oid), itoa(iid), itoa(rid)), Payload: []byte(payload),
		})
	}
	get := func(oid, iid, rid uint16) coap.Message {
		return exchange(t, srv, coap.Message{
			Type: coap.Confirmable, Code: coap.CodeGET, MessageID: 2,
			Options: options("objects", itoa(oid), itoa(iid), itoa(rid)),
		})
	}

	if resp := put(3303, 0, 5700, "21.5"); resp.Code != coap.CodeChanged {
		t.Fatalf("valid write = %v", resp.Code)
	}
	if resp := get(3303, 0, 5700); resp.Code != coap.CodeContent || string(resp.Payload) != "21.5" {
		t.Fatalf("valid read = %v %q", resp.Code, resp.Payload)
	}
	if resp := put(3303, 0, 5700, "not-a-float"); resp.Code != coap.CodeBadRequest {
		t.Fatalf("invalid float = %v, want 4.00", resp.Code)
	}
	if resp := put(3303, 0, 5605, "x"); resp.Code != coap.CodeMethodNotAllowed {
		t.Fatalf("write to a read-only resource = %v, want 4.05", resp.Code)
	}
	if resp := put(3303, 0, 9999, "x"); resp.Code != coap.CodeNotFound {
		t.Fatalf("write to an unknown resource = %v, want 4.04", resp.Code)
	}
	if resp := put(9999, 0, 0, "x"); resp.Code != coap.CodeNotFound {
		t.Fatalf("write to an unknown object = %v, want 4.04", resp.Code)
	}
	execute := func(oid, iid, rid uint16) coap.Message {
		return exchange(t, srv, coap.Message{
			Type: coap.Confirmable, Code: coap.CodePOST, MessageID: 3,
			Options: options("objects", itoa(oid), itoa(iid), itoa(rid)),
		})
	}
	if resp := execute(3303, 0, 5700); resp.Code != coap.CodeMethodNotAllowed {
		t.Fatalf("execute on a writable resource = %v, want 4.05", resp.Code)
	}
	if resp := execute(3303, 0, 5701); resp.Code != coap.CodeChanged {
		t.Fatalf("execute on an executable resource = %v", resp.Code)
	}
	if resp := execute(3303, 0, 9999); resp.Code != coap.CodeNotFound {
		t.Fatalf("execute on an unknown resource = %v, want 4.04", resp.Code)
	}
	// A single-instance object rejects a non-zero instance id.
	if resp := get(3, 1, 0); resp.Code != coap.CodeNotFound {
		t.Fatalf("single-instance object with iid 1 = %v, want 4.04", resp.Code)
	}
}

func TestObjectRegistrySetResourceValidation(t *testing.T) {
	srv := startServerWith(t, Config{Objects: testRegistry(t)})
	if err := srv.SetResource(3303, 0, 5700, []byte("21.5")); err != nil {
		t.Fatalf("valid SetResource: %v", err)
	}
	if err := srv.SetResource(3303, 0, 5700, []byte("x")); !errors.Is(err, errBadValue) {
		t.Fatalf("invalid SetResource = %v, want errBadValue", err)
	}
	if err := srv.SetResource(3303, 0, 5605, []byte("x")); !errors.Is(err, errNotWritable) {
		t.Fatalf("read-only SetResource = %v, want errNotWritable", err)
	}
}

func TestMultipleResourceTLV(t *testing.T) {
	srv := startServerWith(t, Config{Objects: testRegistry(t)})
	writeInstance := func(entries []tlv.Entry) coap.Message {
		payload, err := tlv.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		return exchange(t, srv, coap.Message{
			Type: coap.Confirmable, Code: coap.CodePUT, MessageID: 1,
			Options: options("objects", "3303", "0"), Payload: payload,
		})
	}
	multi := tlv.Entry{Type: tlv.MultipleResource, ID: 5800, Children: []tlv.Entry{
		{Type: tlv.ResourceInstance, ID: 1, Value: []byte("a")},
		{Type: tlv.ResourceInstance, ID: 2, Value: []byte("b")},
	}}
	if resp := writeInstance([]tlv.Entry{multi}); resp.Code != coap.CodeChanged {
		t.Fatalf("multiple write = %v", resp.Code)
	}
	readResource := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodeGET, MessageID: 2,
		Options: options("objects", "3303", "0", "5800"),
	})
	if readResource.Code != coap.CodeContent {
		t.Fatalf("multiple read = %v", readResource.Code)
	}
	entries, err := tlv.Unmarshal(readResource.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != tlv.MultipleResource || entries[0].ID != 5800 || len(entries[0].Children) != 2 {
		t.Fatalf("multiple resource TLV = %+v", entries)
	}
	if entries[0].Children[0].ID != 1 || string(entries[0].Children[0].Value) != "a" ||
		entries[0].Children[1].ID != 2 || string(entries[0].Children[1].Value) != "b" {
		t.Fatalf("children = %+v", entries[0].Children)
	}

	// Writing one instance keeps the other one.
	update := tlv.Entry{Type: tlv.MultipleResource, ID: 5800, Children: []tlv.Entry{
		{Type: tlv.ResourceInstance, ID: 2, Value: []byte("B")},
	}}
	if resp := writeInstance([]tlv.Entry{update}); resp.Code != coap.CodeChanged {
		t.Fatalf("multiple update = %v", resp.Code)
	}
	instances := srv.ResourceInstances(3303, 0, 5800)
	if len(instances) != 2 {
		t.Fatalf("instances = %+v", instances)
	}
	if instances[0].ID != 1 || string(instances[0].Value) != "a" ||
		instances[1].ID != 2 || string(instances[1].Value) != "B" {
		t.Fatalf("instances after update = %+v", instances)
	}

	// A multiple resource rejects a plain text write.
	if resp := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodePUT, MessageID: 3,
		Options: options("objects", "3303", "0", "5800"), Payload: []byte("plain"),
	}); resp.Code != coap.CodeBadRequest {
		t.Fatalf("plain write to a multiple resource = %v, want 4.00", resp.Code)
	}
}

func TestAtomicTLVWrite(t *testing.T) {
	srv := startServerWith(t, Config{Objects: testRegistry(t)})
	entries := []tlv.Entry{
		{Type: tlv.ResourceValue, ID: 5700, Value: []byte("21.5")},
		{Type: tlv.ResourceValue, ID: 5605, Value: []byte("read-only")},
	}
	payload, err := tlv.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	resp := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodePUT, MessageID: 1,
		Options: options("objects", "3303", "0"), Payload: payload,
	})
	if resp.Code != coap.CodeMethodNotAllowed {
		t.Fatalf("mixed write = %v, want 4.05", resp.Code)
	}
	if _, ok := srv.Resource(3303, 0, 5700); ok {
		t.Fatal("a rejected write applied part of its payload")
	}
}

func itoa(value uint16) string {
	return strconv.FormatUint(uint64(value), 10)
}
