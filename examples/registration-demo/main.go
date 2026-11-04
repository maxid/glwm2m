// Command registration-demo runs a glwm2m server and one simulated device
// against it: the device registers over CoAP, the server prints the parsed
// registration, the device writes a resource and reads it back, and the same
// value is then available through the server API without a request.
//
// glwm2m has no client type — it is a server engine. The device side therefore
// uses the gcoap client the server is built on, and the registration is the real
// LwM2M request a device sends: POST /rd?ep=...&lt=...&b=...&lwm2m=...
//
// With -serve it skips the scripted device and holds the server open instead, so
// an external LwM2M client can register until Ctrl-C; every lifecycle event is
// printed as it happens.
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
	"strings"
	"syscall"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/glwm2m"
	"github.com/maxid/gnety"
	"github.com/maxid/gnety/codec/coap"
)

const steps = 6

func main() {
	addr := flag.String("addr", "", "server UDP listen address; empty means 127.0.0.1:0 (demo) or 127.0.0.1:5683, moving to the next free port when it is taken (-serve)")
	endpoint := flag.String("endpoint", "demo-device", "endpoint name the simulated device registers with")
	serve := flag.Bool("serve", false, "hold the server open for an external LwM2M client instead of running the scripted device; Ctrl-C stops it")
	timeout := flag.Duration("timeout", 10*time.Second, "overall deadline of the scripted round trip; -serve is not affected")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `registration-demo registers one simulated device with a glwm2m server and
prints what the server parsed. The device then writes a resource over CoAP
(PUT /objects/3303/0/5700), reads it back and the server returns the same value
from its local object tree without a request.

With -serve the server stays up instead, so an external LwM2M client can register
and every registration event is printed until Ctrl-C.

Examples:
  go run ./examples/registration-demo
  go run ./examples/registration-demo -endpoint sensor-01 -timeout 2s
  go run ./examples/registration-demo -serve    # then: coap-client -m post "coap://127.0.0.1:5683/rd?ep=dev&lt=60&b=U&lwm2m=1.0"

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
			// A fixed port is the point of -serve: it goes into another
			// client's command line. 5683 is the CoAP port and may already be
			// taken on this machine; then the next free UDP port is announced.
			const preferred = 5683
			port, err := gnety.AvailableUDPPortOn("127.0.0.1", preferred, nil)
			if err != nil {
				log.Fatalf("registration-demo: find a free port above %d: %v", preferred, err)
			}
			if port != preferred {
				fmt.Printf("[serve] 127.0.0.1:%d is taken; using 127.0.0.1:%d\n", preferred, port)
			}
			listen = "127.0.0.1:" + strconv.Itoa(port)
		}
	}

	cfg := glwm2m.Config{Addr: listen}
	if *serve {
		cfg.Events = glwm2m.EventSinkFunc(logEvent)
	}
	srv, err := glwm2m.New(cfg)
	if err != nil {
		log.Fatalf("registration-demo: create server: %v", err)
	}
	if err := srv.Start(sigCtx); err != nil {
		log.Fatalf("registration-demo: start server on %s: %v", listen, err)
	}
	// Stop takes the cancelled ctx off the table, so Ctrl-C still closes it.
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

	fmt.Printf("[1/%d] server listening on %s\n", steps, srv.Addr())

	device, err := gcoap.Dial(ctx, srv.Addr())
	if err != nil {
		interruptedOrFatal(ctx, "dial server: %v", err)
	}
	defer func() { _ = device.Close() }()

	// Register: the server answers 2.01 Created and carries the registration id
	// in the response's path options (the last segment after "rd"). The body is
	// the object/instance list the device announces as application/link-format.
	resp, err := device.Do(ctx, gcoap.ClientRequest{
		Code: coap.CodePOST, Path: "/rd", Query: registerQuery(*endpoint),
		Payload: []byte("</3303/0>"),
	})
	if err != nil {
		interruptedOrFatal(ctx, "register %q: %v", *endpoint, err)
	}
	if resp.Code != coap.CodeCreated {
		log.Fatalf("registration-demo: register %q: got %s, want 2.01 Created", *endpoint, resp.Code)
	}
	id := registrationID(resp)
	if id == "" {
		log.Fatalf("registration-demo: register %q: response carries no registration id: %+v", *endpoint, resp.Options)
	}
	fmt.Printf("[2/%d] device %q registered as %s (%s)\n", steps, *endpoint, id, resp.Code)

	reg, ok := srv.Registration(id)
	if !ok {
		log.Fatalf("registration-demo: registration %q not found", id)
	}
	fmt.Printf("[3/%d] server parsed it: endpoint=%s lifetime=%ds binding=%s version=%s links=%s (live: %d)\n",
		steps, reg.Endpoint, reg.Lifetime, reg.Binding, reg.Version, strings.Join(reg.ObjectLinks, ","), srv.Count())

	// The device reports object 3303 (temperature), instance 0, resource 5700
	// (sensor value) as text/plain, then reads it back from the server.
	put, err := device.Do(ctx, gcoap.ClientRequest{
		Code: coap.CodePUT, Path: "/objects/3303/0/5700", Payload: []byte("21.5"),
	})
	if err != nil {
		interruptedOrFatal(ctx, "write 3303/0/5700: %v", err)
	}
	fmt.Printf("[4/%d] device PUT /objects/3303/0/5700 -> %s\n", steps, put.Code)

	read, err := device.Do(ctx, gcoap.ClientRequest{Code: coap.CodeGET, Path: "/objects/3303/0/5700"})
	if err != nil {
		interruptedOrFatal(ctx, "read 3303/0/5700: %v", err)
	}
	fmt.Printf("[5/%d] device GET /objects/3303/0/5700 -> %s %q\n", steps, read.Code, read.Payload)

	// The same value is available through the server API without a request: the
	// server keeps the local object tree it was written into.
	value, ok := srv.Resource(3303, 0, 5700)
	if !ok {
		log.Fatalf("registration-demo: server holds no value for 3303/0/5700")
	}
	fmt.Printf("[6/%d] server.Resource(3303, 0, 5700) = %q (no request needed)\n", steps, value)

	if err := device.Close(); err != nil {
		log.Fatalf("registration-demo: close device: %v", err)
	}
	fmt.Println("[done] registration lifecycle demo complete; server and device closed")
}

// registerQuery builds the LwM2M registration query string.
func registerQuery(endpoint string) url.Values {
	query := url.Values{}
	query.Set("ep", endpoint)
	query.Set("lt", "60")
	query.Set("b", "U")
	query.Set("lwm2m", "1.0")
	return query
}

// registrationID extracts the id from the Location-Path segments of a 2.01
// response: POST /rd answers with "rd" followed by the id.
func registrationID(resp gcoap.ClientResponse) string {
	id := ""
	for _, opt := range resp.Options {
		if opt.Number != coap.OptionLocationPath && opt.Number != coap.OptionURIPath {
			continue
		}
		if value := string(opt.Value); value != "rd" {
			id = value
		}
	}
	return id
}

// logEvent prints registration lifecycle events; the sink runs outside the
// server's lock and must not block.
func logEvent(_ context.Context, event glwm2m.RegistrationEvent) {
	reg := event.Registration
	switch event.Type {
	case glwm2m.EventRegistered:
		fmt.Printf("[serve] registered ep=%s id=%s lifetime=%ds binding=%s links=%s\n",
			reg.Endpoint, reg.ID, reg.Lifetime, reg.Binding, strings.Join(reg.ObjectLinks, ","))
	case glwm2m.EventUpdated:
		fmt.Printf("[serve] updated ep=%s id=%s lifetime=%ds\n", reg.Endpoint, reg.ID, reg.Lifetime)
	case glwm2m.EventDeregistered:
		fmt.Printf("[serve] deregistered ep=%s id=%s reason=%s\n", reg.Endpoint, reg.ID, event.Reason)
	case glwm2m.EventExpired:
		fmt.Printf("[serve] expired ep=%s id=%s reason=%s\n", reg.Endpoint, reg.ID, event.Reason)
	}
}

// interruptedOrFatal prints the [stop] line and returns when the failure is a
// Ctrl-C (cancelling the context is how the signal reaches an in-flight request),
// and exits non-zero for a real error.
func interruptedOrFatal(ctx context.Context, format string, args ...any) {
	if ctx.Err() != nil {
		fmt.Println("[stop] interrupted")
		os.Exit(0)
	}
	log.Fatalf("registration-demo: "+format, args...)
}
