package glwm2m

import (
	"bytes"
	"context"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/gnety/codec/coap"
)

// The benchmarks run a real glwm2m server over loopback UDP — no mocked
// transport — and their numbers are the source of docs/PERFORMANCE.md.

const (
	benchPath     = "/3303/0/5700"
	benchLifetime = "300"
)

// benchRegister registers one endpoint and returns the registration id.
func benchRegister(b *testing.B, cli *gcoap.Client, endpoint string) string {
	b.Helper()
	query := url.Values{}
	query.Set("ep", endpoint)
	query.Set("lt", benchLifetime)
	query.Set("b", "U")
	query.Set("lwm2m", "1.0")
	resp, err := cli.Do(context.Background(), gcoap.ClientRequest{
		Code: coap.CodePOST, Path: "/rd", Query: query, Payload: []byte("</3303/0>"),
	})
	if err != nil {
		b.Fatalf("register %q: %v", endpoint, err)
	}
	if resp.Code != coap.CodeCreated {
		b.Fatalf("register %q answered %s, want 2.01", endpoint, resp.Code)
	}
	id := ""
	for _, opt := range resp.Options {
		if opt.Number == coap.OptionLocationPath && string(opt.Value) != "rd" {
			id = string(opt.Value)
		}
	}
	if id == "" {
		b.Fatalf("register %q: response carries no registration id", endpoint)
	}
	return id
}

// BenchmarkRegister measures one registration round trip: POST /rd with a fresh
// endpoint, answered with 2.01 and the registration id.
//
// Every iteration adds a registration, so the cap is raised well above what a
// deployment would use; a real server expires or replaces them.
func BenchmarkRegister(b *testing.B) {
	srv, err := New(Config{Addr: "127.0.0.1:0", MaxRegistrations: 1 << 20})
	if err != nil {
		b.Fatalf("new server: %v", err)
	}
	if err := srv.Start(context.Background()); err != nil {
		b.Fatalf("start server: %v", err)
	}
	b.Cleanup(func() { _ = srv.Stop(context.Background()) })

	cli, err := gcoap.Dial(context.Background(), srv.Addr())
	if err != nil {
		b.Fatalf("dial %s: %v", srv.Addr(), err)
	}
	b.Cleanup(func() { _ = cli.Close() })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchRegister(b, cli, "bench-"+strconv.Itoa(i))
	}
	b.StopTimer()
}

// BenchmarkObserveNotify measures one LwM2M notification end to end: the device
// reports a new value on its object resource, the server receives it through the
// observation it registered. The device is a gcoap server plus a client that
// registers, and Config.DownlinkDialer points the server at the device listener —
// the same shape examples/observe-telemetry uses.
func BenchmarkObserveNotify(b *testing.B) {
	ctx := context.Background()
	var value atomic.Value
	value.Store("21.5")

	device, err := gcoap.New(gcoap.Config{Addr: "127.0.0.1:0"})
	if err != nil {
		b.Fatalf("new device: %v", err)
	}
	device.Handle(coap.CodeGET, benchPath, func(context.Context, gcoap.Request) gcoap.Response {
		return gcoap.Response{Code: coap.CodeContent, Payload: []byte(value.Load().(string))}
	})
	if err := device.Start(ctx); err != nil {
		b.Fatalf("start device: %v", err)
	}
	b.Cleanup(func() { _ = device.Stop(context.Background()) })

	srv, err := New(Config{
		Addr: "127.0.0.1:0",
		DownlinkDialer: func(ctx context.Context, _ Registration) (*gcoap.Client, error) {
			return gcoap.Dial(ctx, device.Addr())
		},
	})
	if err != nil {
		b.Fatalf("new server: %v", err)
	}
	if err := srv.Start(ctx); err != nil {
		b.Fatalf("start server: %v", err)
	}
	b.Cleanup(func() { _ = srv.Stop(context.Background()) })

	cli, err := gcoap.Dial(ctx, srv.Addr())
	if err != nil {
		b.Fatalf("dial %s: %v", srv.Addr(), err)
	}
	b.Cleanup(func() { _ = cli.Close() })
	id := benchRegister(b, cli, "bench-observe")

	// The server's Observe callback runs on the downlink client's receive
	// goroutine, so it only forwards to a buffered channel.
	delivered := make(chan []byte, 1)
	instance, resource := uint16(0), uint16(5700)
	observation, initial, err := srv.Observe(ctx, Operation{
		RegistrationID: id,
		Path:           Path{ObjectID: 3303, InstanceID: &instance, ResourceID: &resource},
	}, func(v Value) {
		select {
		case delivered <- append([]byte(nil), v.Payload...):
		default:
		}
	})
	if err != nil {
		b.Fatalf("observe: %v", err)
	}
	if string(initial.Payload) != "21.5" {
		b.Fatalf("initial value = %q", initial.Payload)
	}
	b.Cleanup(func() { _ = observation.Cancel(context.Background()) })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		payload := []byte("value-" + strconv.Itoa(i))
		value.Store(string(payload))
		device.Notify(benchPath, gcoap.Response{Code: coap.CodeContent, Payload: payload})
		select {
		case got := <-delivered:
			if !bytes.Equal(got, payload) {
				b.Fatalf("notification %d delivered %q, want %q", i, got, payload)
			}
		case <-time.After(5 * time.Second):
			b.Fatalf("notification %d did not arrive", i)
		}
	}
	b.StopTimer()
}
