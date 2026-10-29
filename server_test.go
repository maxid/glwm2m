package glwm2m

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/maxid/gnety/codec/coap"
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
