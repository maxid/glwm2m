// Command observe-telemetry runs the full LwM2M loop in one process: a device
// that serves one temperature resource, the glwm2m server it registers with, and
// the server-initiated Read and Observe a LwM2M server exists for.
//
// A real LwM2M client uses one socket for both roles: it registers and it serves
// its object tree on the same address. gcoap's client and server own separate
// sockets, so this demo points the server's downlink at the device listener with
// Config.DownlinkDialer — the hook the library documents for an address the
// registration socket does not describe.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/glwm2m"
	"github.com/maxid/gnety"
	"github.com/maxid/gnety/codec/coap"
)

const (
	steps = 7
	// resource is the device-side path both roles address: what a LwM2M client
	// serves for Object 3303, instance 0, resource 5700. The library's own
	// "/objects/..." endpoints are the server side and are not involved here.
	resource = "/3303/0/5700"
)

func main() {
	addr := flag.String("addr", "", "LwM2M server UDP listen address; empty means 127.0.0.1:0 (demo) or 127.0.0.1:5683, moving to the next free port when it is taken (-serve)")
	deviceAddr := flag.String("device-addr", "127.0.0.1:0", "device UDP listen address; the server's downlink dials this address")
	endpoint := flag.String("endpoint", "telemetry-device", "endpoint name the simulated device registers with")
	serve := flag.Bool("serve", false, "hold the server open for an external LwM2M client instead of running the scripted loop; Ctrl-C stops it")
	timeout := flag.Duration("timeout", 10*time.Second, "overall deadline of the scripted loop; -serve is not affected")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `observe-telemetry shows the whole LwM2M circle.

A simulated device serves one temperature resource (Object 3303, instance 0,
resource 5700) and registers with the server. The server then reads the resource
with a server-initiated Read, subscribes to it with Observe, and the device
reports a new value, which arrives as a notification — no request from the
server after the registration.

Examples:
  go run ./examples/observe-telemetry
  go run ./examples/observe-telemetry -endpoint sensor-01
  go run ./examples/observe-telemetry -serve   # then: coap-client -m post "coap://127.0.0.1:5683/rd?ep=dev&lt=60&b=U&lwm2m=1.0"

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listen := *addr
	if listen == "" {
		listen = "127.0.0.1:0"
		if *serve {
			const preferred = 5683
			port, err := gnety.AvailableUDPPortOn("127.0.0.1", preferred, nil)
			if err != nil {
				log.Fatalf("observe-telemetry: find a free port above %d: %v", preferred, err)
			}
			if port != preferred {
				fmt.Printf("[serve] 127.0.0.1:%d is taken; using 127.0.0.1:%d\n", preferred, port)
			}
			listen = "127.0.0.1:" + strconv.Itoa(port)
		}
	}

	// The device side: a plain gcoap server that answers the object resource. It
	// is the address the LwM2M server's downlink will call. -serve does not need
	// it: there the server talks to whichever real client registers, on the
	// address that client registered from.
	var temperature atomic.Value
	temperature.Store("21.5")
	var device *gcoap.Server
	if !*serve {
		dev, err := gcoap.New(gcoap.Config{Addr: *deviceAddr})
		if err != nil {
			log.Fatalf("observe-telemetry: create device: %v", err)
		}
		device = dev
		device.Handle(coap.CodeGET, resource, func(context.Context, gcoap.Request) gcoap.Response {
			return gcoap.Response{Code: coap.CodeContent, Payload: []byte(temperature.Load().(string))}
		})
		if err := device.Start(sigCtx); err != nil {
			log.Fatalf("observe-telemetry: start device on %s: %v", *deviceAddr, err)
		}
		defer func() { _ = device.Stop(context.Background()) }()
	}

	cfg := glwm2m.Config{Addr: listen}
	if !*serve {
		// The registration arrives from the device's client socket, but the
		// object tree lives on its server socket. The dialer replaces the
		// address the registration carries.
		cfg.DownlinkDialer = func(ctx context.Context, _ glwm2m.Registration) (*gcoap.Client, error) {
			return gcoap.Dial(ctx, device.Addr())
		}
	}
	if *serve {
		cfg.Events = glwm2m.EventSinkFunc(func(_ context.Context, event glwm2m.RegistrationEvent) {
			fmt.Printf("[serve] %s ep=%s id=%s\n", event.Type, event.Registration.Endpoint, event.Registration.ID)
		})
	}
	srv, err := glwm2m.New(cfg)
	if err != nil {
		log.Fatalf("observe-telemetry: create server: %v", err)
	}
	if err := srv.Start(sigCtx); err != nil {
		log.Fatalf("observe-telemetry: start server on %s: %v", listen, err)
	}
	defer func() { _ = srv.Stop(context.Background()) }()

	if *serve {
		fmt.Printf("[serve] LwM2M server listening on %s\n", srv.Addr())
		fmt.Printf("[serve] try: coap-client -m post \"coap://%s/rd?ep=dev&lt=60&b=U&lwm2m=1.0\"\n", srv.Addr())
		fmt.Println("[serve] Ctrl-C stops the server")
		<-sigCtx.Done()
		fmt.Println("[stop] server stopped")
		return
	}

	ctx, cancel := context.WithTimeout(sigCtx, *timeout)
	defer cancel()

	fmt.Printf("[1/%d] device serving GET %s on %s\n", steps, resource, device.Addr())
	fmt.Printf("[2/%d] LwM2M server listening on %s, downlink dials the device\n", steps, srv.Addr())

	// The device registers from its own client socket; everything the server
	// sends to it goes through the downlink dialer to the device listener.
	client, err := gcoap.Dial(ctx, srv.Addr())
	if err != nil {
		interruptedOrFatal(ctx, "dial server: %v", err)
	}
	defer func() { _ = client.Close() }()

	query := url.Values{}
	query.Set("ep", *endpoint)
	query.Set("lt", "60")
	query.Set("b", "U")
	query.Set("lwm2m", "1.0")
	resp, err := client.Do(ctx, gcoap.ClientRequest{
		Code: coap.CodePOST, Path: "/rd", Query: query, Payload: []byte("</3303/0>"),
	})
	if err != nil {
		interruptedOrFatal(ctx, "register %q: %v", *endpoint, err)
	}
	id := registrationID(resp)
	if resp.Code != coap.CodeCreated || id == "" {
		log.Fatalf("observe-telemetry: register %q: got %s without an id", *endpoint, resp.Code)
	}
	fmt.Printf("[3/%d] device %q registered as %s (2.01 Created)\n", steps, *endpoint, id)

	instance, resourceID := uint16(0), uint16(5700)
	path := glwm2m.Path{ObjectID: 3303, InstanceID: &instance, ResourceID: &resourceID}

	// Server-initiated Read: the server sends a GET to the device.
	value, err := srv.Read(ctx, glwm2m.Operation{RegistrationID: id, Path: path})
	if err != nil {
		interruptedOrFatal(ctx, "read %s: %v", path, err)
	}
	fmt.Printf("[4/%d] server Read %s -> %q\n", steps, path, value.Payload)

	// Server-initiated Observe: one GET with Observe=0, then the device pushes.
	// The callback runs on the downlink client's receive goroutine, so it only
	// forwards to a buffered channel.
	notifications := make(chan glwm2m.Value, 4)
	observation, initial, err := srv.Observe(ctx, glwm2m.Operation{RegistrationID: id, Path: path}, func(v glwm2m.Value) {
		select {
		case notifications <- v:
		default:
		}
	})
	if err != nil {
		interruptedOrFatal(ctx, "observe %s: %v", path, err)
	}
	fmt.Printf("[5/%d] server Observe %s -> initial %q\n", steps, path, initial.Payload)

	// The device measures a new value and notifies its observers. This is the
	// "report" half of LwM2M: the server sent no request for it.
	temperature.Store("22.0")
	device.Notify(resource, gcoap.Response{Code: coap.CodeContent, Payload: []byte("22.0")})
	select {
	case v := <-notifications:
		fmt.Printf("[6/%d] device reported %q -> server received %q (no request)\n", steps, "22.0", v.Payload)
	case <-ctx.Done():
		interruptedOrFatal(ctx, "wait for the notification: %v", ctx.Err())
	}

	cancelCtx, cancelTimeout := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelTimeout()
	if err := observation.Cancel(cancelCtx); err != nil {
		log.Fatalf("observe-telemetry: cancel observation: %v", err)
	}

	// Deregistration is the client telling the server it is going away.
	del, err := client.Do(ctx, gcoap.ClientRequest{Code: coap.CodeDELETE, Path: "/rd/" + id})
	if err != nil {
		interruptedOrFatal(ctx, "deregister %s: %v", id, err)
	}
	if del.Code != coap.CodeDeleted {
		log.Fatalf("observe-telemetry: deregister %s: got %s, want 2.02 Deleted", id, del.Code)
	}
	fmt.Printf("[7/%d] device deregistered %s (%s)\n", steps, id, del.Code)

	if err := client.Close(); err != nil {
		log.Fatalf("observe-telemetry: close client: %v", err)
	}
	fmt.Println("[done] registration, read, observe and deregistration complete")
}

// registrationID extracts the id from the Location-Path segments of a 2.01
// response: POST /rd answers with "rd" followed by the id.
func registrationID(resp gcoap.ClientResponse) string {
	id := ""
	for _, opt := range resp.Options {
		if opt.Number != coap.OptionLocationPath {
			continue
		}
		if value := string(opt.Value); value != "rd" {
			id = value
		}
	}
	return id
}

// interruptedOrFatal prints the [stop] line and returns when the failure is a
// Ctrl-C, and exits non-zero for a real error.
func interruptedOrFatal(ctx context.Context, format string, args ...any) {
	if ctx.Err() != nil {
		fmt.Println("[stop] interrupted")
		os.Exit(0)
	}
	log.Fatalf("observe-telemetry: "+format, args...)
}
