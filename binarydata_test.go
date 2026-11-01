package glwm2m

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety/codec/coap"
	"github.com/maxid/gnety/codec/tlv"
)

func TestBinaryAppDataObjectDefinition(t *testing.T) {
	registry, err := NewObjectRegistry(BinaryAppDataObject())
	if err != nil {
		t.Fatalf("BinaryAppDataObject: %v", err)
	}
	def, ok := registry.Object(BinaryAppDataObjectID)
	if !ok || !def.Multiple {
		t.Fatalf("object 19 = %+v, %v", def, ok)
	}
	data, _, ok := registry.Resource(BinaryAppDataObjectID, BinaryAppDataResourceID)
	if !ok || !data.Multiple || data.Type != ValueOpaque {
		t.Fatalf("Data resource = %+v, %v", data, ok)
	}
	if !data.Operations.Has(OpRead | OpWrite) {
		t.Fatalf("Data operations = %v", data.Operations)
	}
	priority, _, ok := registry.Resource(BinaryAppDataObjectID, 1)
	if !ok || priority.Type != ValueInteger {
		t.Fatalf("Data Priority = %+v, %v", priority, ok)
	}
}

func TestBinaryDataDownlink(t *testing.T) {
	srv := startServerWith(t, Config{})
	_, id := startRegisteredDevice(t, srv, nil, "dev-binary")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	large := bytes.Repeat([]byte{0xAB, 0xCD}, 2048) // 4 KiB
	instanceID, err := srv.CreateBinaryDataInstance(ctx, id, []BinaryData{{ID: 7, Value: large}})
	if err != nil {
		t.Fatalf("CreateBinaryDataInstance: %v", err)
	}
	if instanceID == 0 {
		t.Fatal("Create returned instance id 0")
	}
	values, err := srv.ReadBinaryData(ctx, id, instanceID)
	if err != nil {
		t.Fatalf("ReadBinaryData: %v", err)
	}
	if len(values) != 1 || values[0].ID != 7 || !bytes.Equal(values[0].Value, large) {
		t.Fatalf("read %d instances, first id %d, %d bytes", len(values), values[0].ID, len(values[0].Value))
	}

	updated := bytes.Repeat([]byte{0x01}, 3072)
	if err := srv.WriteBinaryDataInstance(ctx, id, instanceID, BinaryData{ID: 7, Value: updated}); err != nil {
		t.Fatalf("WriteBinaryDataInstance: %v", err)
	}
	values, err = srv.ReadBinaryData(ctx, id, instanceID)
	if err != nil || !bytes.Equal(values[0].Value, updated) {
		t.Fatalf("read after write = %d bytes, %v", len(values[0].Value), err)
	}

	// Writing a set of instances replaces the Data resource on this device.
	set := []BinaryData{{ID: 1, Value: []byte("one")}, {ID: 2, Value: []byte("two")}}
	if err := srv.WriteBinaryData(ctx, id, instanceID, set); err != nil {
		t.Fatalf("WriteBinaryData: %v", err)
	}
	values, err = srv.ReadBinaryData(ctx, id, instanceID)
	if err != nil || len(values) != 2 {
		t.Fatalf("read set = %+v, %v", values, err)
	}
	if values[0].ID != 1 || string(values[0].Value) != "one" || values[1].ID != 2 || string(values[1].Value) != "two" {
		t.Fatalf("set values = %+v", values)
	}

	if err := srv.DeleteBinaryDataInstance(ctx, id, instanceID); err != nil {
		t.Fatalf("DeleteBinaryDataInstance: %v", err)
	}
	if _, err := srv.ReadBinaryData(ctx, id, instanceID); err == nil {
		t.Fatal("read after delete succeeded")
	} else {
		var rejected *OperationError
		if !errors.As(err, &rejected) || rejected.Code != coap.CodeNotFound {
			t.Fatalf("read after delete = %v, want a 4.04 OperationError", err)
		}
	}
}

func TestBinaryDataDownlinkRequestLimit(t *testing.T) {
	srv := startServerWith(t, Config{DownlinkMaxRequestSize: 512})
	_, id := startRegisteredDevice(t, srv, nil, "dev-binary-limit")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.WriteBinaryDataInstance(ctx, id, 1, BinaryData{ID: 0, Value: bytes.Repeat([]byte{1}, 1024)})
	if !errors.Is(err, gcoap.ErrRequestTooLarge) {
		t.Fatalf("oversized write = %v, want the client request-size error", err)
	}
}

func TestBinaryDataLocalObject(t *testing.T) {
	registry, err := NewObjectRegistry(BinaryAppDataObject())
	if err != nil {
		t.Fatal(err)
	}
	srv := startServerWith(t, Config{Objects: registry})
	data := []byte("local binary payload")
	container := tlv.Entry{Type: tlv.ObjectInstance, ID: 0, Children: []tlv.Entry{
		{Type: tlv.MultipleResource, ID: 0, Children: []tlv.Entry{
			{Type: tlv.ResourceInstance, ID: 3, Value: data},
		}},
	}}
	payload, err := tlv.Marshal([]tlv.Entry{container})
	if err != nil {
		t.Fatal(err)
	}
	put := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodePUT, MessageID: 1,
		Options: options("objects", "19"), Payload: payload,
	})
	if put.Code != coap.CodeCreated {
		t.Fatalf("local Object 19 create = %v", put.Code)
	}
	get := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodeGET, MessageID: 2,
		Options: options("objects", "19", "0", "0"),
	})
	if get.Code != coap.CodeContent {
		t.Fatalf("local Object 19 read = %v", get.Code)
	}
	entries, err := tlv.Unmarshal(get.Payload)
	if err != nil || len(entries) != 1 || entries[0].Type != tlv.MultipleResource {
		t.Fatalf("local Object 19 TLV = %+v, %v", entries, err)
	}
	if len(entries[0].Children) != 1 || entries[0].Children[0].ID != 3 || string(entries[0].Children[0].Value) != string(data) {
		t.Fatalf("local Object 19 data = %+v", entries[0].Children)
	}
	if err := srv.SetResource(19, 0, 0, []byte("plain")); err == nil {
		t.Fatal("a plain SetResource on a multiple resource succeeded")
	}
}
