// Command registration-demo runs a glwm2m server and one simulated device
// against it: the device registers over CoAP, the server prints the parsed
// registration, the device writes a resource and reads it back through the
// server's LwM2M object endpoints.
//
// glwm2m has no client type, so the device side uses the gcoap client the
// server is built on. The registration is the real LwM2M request a device
// sends: POST /rd?ep=...&lt=...&b=...&lwm2m=...
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/maxid/gcoap"
	"github.com/maxid/glwm2m"
	"github.com/maxid/gnety/codec/coap"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "server UDP listen address; port 0 picks an ephemeral port")
	endpoint := flag.String("endpoint", "demo-device", "endpoint name the simulated device registers with")
	timeout := flag.Duration("timeout", 5*time.Second, "overall deadline for the demo")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	srv, err := glwm2m.New(glwm2m.Config{Addr: *addr})
	if err != nil {
		log.Fatalf("glwm2m: create server: %v", err)
	}
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("glwm2m: start server: %v", err)
	}
	defer func() { _ = srv.Stop(context.Background()) }()
	fmt.Printf("server listening on %s\n", srv.Addr())

	device, err := gcoap.Dial(ctx, srv.Addr())
	if err != nil {
		log.Fatalf("glwm2m: dial server: %v", err)
	}
	defer device.Close()

	// Register: the server answers 2.01 Created and carries the registration
	// id in the response's path options (the last segment after "rd").
	query := url.Values{}
	query.Set("ep", *endpoint)
	query.Set("lt", "60")
	query.Set("b", "U")
	query.Set("lwm2m", "1.0")
	resp, err := device.Do(ctx, gcoap.ClientRequest{Code: coap.CodePOST, Path: "/rd", Query: query})
	if err != nil {
		log.Fatalf("glwm2m: register %q: %v", *endpoint, err)
	}
	if resp.Code != coap.CodeCreated {
		log.Fatalf("glwm2m: register %q: got %s, want 2.01 Created", *endpoint, resp.Code)
	}
	id := ""
	for _, opt := range resp.Options {
		if (opt.Number == coap.OptionLocationPath || opt.Number == coap.OptionURIPath) && string(opt.Value) != "rd" {
			id = string(opt.Value)
		}
	}
	if id == "" {
		log.Fatalf("glwm2m: register %q: response carries no registration id: %+v", *endpoint, resp.Options)
	}
	fmt.Printf("device %q registered as %s (%s)\n", *endpoint, id, resp.Code)

	reg, ok := srv.Registration(id)
	if !ok {
		log.Fatalf("glwm2m: registration %q not found", id)
	}
	fmt.Printf("server parsed registration: endpoint=%s lifetime=%ds binding=%s version=%s\n",
		reg.Endpoint, reg.Lifetime, reg.Binding, reg.Version)
	fmt.Printf("registered clients: %d\n", srv.Count())

	// The device reports object 3303 (temperature), instance 0, resource 5700
	// (sensor value) as text/plain, then reads it back from the server.
	if _, err := device.Do(ctx, gcoap.ClientRequest{
		Code: coap.CodePUT, Path: "/objects/3303/0/5700", Payload: []byte("21.5"),
	}); err != nil {
		log.Fatalf("glwm2m: write resource: %v", err)
	}
	read, err := device.Do(ctx, gcoap.ClientRequest{Code: coap.CodeGET, Path: "/objects/3303/0/5700"})
	if err != nil {
		log.Fatalf("glwm2m: read resource: %v", err)
	}
	fmt.Printf("GET /objects/3303/0/5700 -> %s %q\n", read.Code, read.Payload)

	// The same value is available through the server API without a request.
	if value, ok := srv.Resource(3303, 0, 5700); ok {
		fmt.Printf("server.Resource(3303, 0, 5700) = %q\n", value)
	}
}
