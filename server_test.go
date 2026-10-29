package glwm2m

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/maxid/gnety/codec/coap"

	"github.com/maxid/glwm2m/tlv"
)

func startServer(t *testing.T) *Server {
	t.Helper()
	srv, err := New(Config{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv
}

func exchange(t *testing.T, srv *Server, msg coap.Message) coap.Message {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	raw, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(raw); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := coap.Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func options(segs ...string) []coap.Option {
	out := make([]coap.Option, 0, len(segs))
	for _, s := range segs {
		out = append(out, coap.Option{Number: coap.OptionURIPath, Value: []byte(s)})
	}
	return out
}

func TestRegistrationLifecycle(t *testing.T) {
	srv := startServer(t)
	reg := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodePOST, MessageID: 1,
		Options: append(options("rd"),
			coap.Option{Number: coap.OptionURIQuery, Value: []byte("ep=dev-1&lt=60&b=U")}),
	})
	if reg.Code != coap.CodeCreated {
		t.Fatalf("register code = %v", reg.Code)
	}
	var id string
	for _, opt := range reg.Options {
		if opt.Number == coap.OptionURIPath && string(opt.Value) != "rd" {
			id = string(opt.Value)
		}
	}
	if id == "" {
		t.Fatalf("missing registration id: %+v", reg.Options)
	}
	if srv.Count() != 1 {
		t.Fatalf("count = %d", srv.Count())
	}
	if _, ok := srv.Registration(id); !ok {
		t.Fatal("registration not found")
	}
	upd := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodePOST, MessageID: 2,
		Options: append(options("rd", id), coap.Option{Number: coap.OptionURIQuery, Value: []byte("lt=120")}),
	})
	if upd.Code != coap.CodeChanged {
		t.Fatalf("update code = %v", upd.Code)
	}
	del := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodeDELETE, MessageID: 3,
		Options: options("rd", id),
	})
	if del.Code != coap.CodeDeleted {
		t.Fatalf("delete code = %v", del.Code)
	}
	if srv.Count() != 0 {
		t.Fatalf("count after delete = %d", srv.Count())
	}
}

func TestResourceReadWrite(t *testing.T) {
	srv := startServer(t)
	put := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodePUT, MessageID: 11,
		Options: options("objects", "3303", "0", "5700"),
		Payload: []byte("21.5"),
	})
	if put.Code != coap.CodeChanged {
		t.Fatalf("put code = %v", put.Code)
	}
	get := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodeGET, MessageID: 12,
		Options: options("objects", "3303", "0", "5700"),
	})
	if get.Code != coap.CodeContent || string(get.Payload) != "21.5" {
		t.Fatalf("get = %v %q", get.Code, get.Payload)
	}
	missing := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodeGET, MessageID: 13,
		Options: options("objects", "3303", "0", "9999"),
	})
	if missing.Code != coap.CodeNotFound {
		t.Fatalf("missing code = %v", missing.Code)
	}
}

func TestTLVObjectReadWrite(t *testing.T) {
	srv := startServer(t)
	// PUT /objects/3303/0 with two resources.
	entries := []tlv.Entry{
		{Type: tlv.ResourceValue, ID: 5700, Value: []byte("21.5")},
		{Type: tlv.ResourceValue, ID: 5701, Value: []byte("C")},
	}
	payload, err := tlv.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	put := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodePUT, MessageID: 21,
		Options: options("objects", "3303", "0"), Payload: payload,
	})
	if put.Code != coap.CodeChanged {
		t.Fatalf("put code = %v", put.Code)
	}
	get := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodeGET, MessageID: 22,
		Options: options("objects", "3303", "0"),
	})
	if get.Code != coap.CodeContent {
		t.Fatalf("get code = %v", get.Code)
	}
	got, err := tlv.Unmarshal(get.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 5700 || string(got[0].Value) != "21.5" {
		t.Fatalf("tlv resources = %+v", got)
	}
	// Object-level read wraps instances.
	obj := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodeGET, MessageID: 23,
		Options: options("objects", "3303"),
	})
	if obj.Code != coap.CodeContent {
		t.Fatalf("object get code = %v", obj.Code)
	}
	objTLV, err := tlv.Unmarshal(obj.Payload)
	if err != nil || len(objTLV) != 1 || objTLV[0].Type != tlv.ObjectInstance || objTLV[0].ID != 0 {
		t.Fatalf("object tlv = %+v err=%v", objTLV, err)
	}
	// Execute returns 2.04.
	exec := exchange(t, srv, coap.Message{
		Type: coap.Confirmable, Code: coap.CodePOST, MessageID: 24,
		Options: options("objects", "3303", "0", "5700"),
	})
	if exec.Code != coap.CodeChanged {
		t.Fatalf("execute code = %v", exec.Code)
	}
}

func TestObserveResourceChange(t *testing.T) {
	srv := startServer(t)
	srv.SetResource(3303, 0, 5700, []byte("21.5"))
	addr, _ := net.ResolveUDPAddr("udp", srv.Addr())
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	obs := coap.Message{
		Type: coap.Confirmable, Code: coap.CodeGET, MessageID: 31,
		Token: []byte{7, 8},
		Options: append(options("objects", "3303", "0", "5700"),
			coap.Option{Number: coap.OptionObserve, Value: nil}),
	}
	raw, _ := obs.Marshal()
	if _, err := conn.Write(raw); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := coap.Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := observeOption(initial); !ok {
		t.Fatal("initial observe response missing Observe option")
	}
	srv.SetResource(3303, 0, 5700, []byte("22.0"))
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	notif, err := coap.Parse(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if notif.Type != coap.NonConfirmable || string(notif.Payload) != "22.0" {
		t.Fatalf("notification = %v %q", notif.Type, notif.Payload)
	}
}

func observeOption(m coap.Message) (uint32, bool) {
	for _, opt := range m.Options {
		if opt.Number != coap.OptionObserve {
			continue
		}
		var v uint32
		for _, b := range opt.Value {
			v = v<<8 | uint32(b)
		}
		return v, true
	}
	return 0, false
}
