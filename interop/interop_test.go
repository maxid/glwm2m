//go:build integration

// Package interop holds the interoperability test against an independent LwM2M
// implementation: the Eclipse Leshan client demo, a real LwM2M client built on a
// different CoAP stack (Californium). It registers against this library's server,
// which exercises the registration lifecycle against something this repository
// did not write.
//
// The test needs a JRE and the Leshan client demo jar. Point LESHAN_CLIENT_JAR at
// the jar (or drop it next to this file as leshan-client-demo.jar) and run:
//
//	go test -tags=integration ./...
//
// Without either, the tests skip instead of failing.
package interop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/maxid/glwm2m"
	"github.com/maxid/gnety/codec/coap"
)

// leshanClientJar returns the jar path and the java binary, or skips.
func leshanClientJar(t *testing.T) (jar, java string) {
	t.Helper()
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java is not installed; the Leshan interop test needs a JRE")
	}
	jar = os.Getenv("LESHAN_CLIENT_JAR")
	if jar == "" {
		if _, err := os.Stat("leshan-client-demo.jar"); err == nil {
			jar = "leshan-client-demo.jar"
		}
	}
	if jar == "" {
		t.Skip("set LESHAN_CLIENT_JAR or place leshan-client-demo.jar here to run against Leshan")
	}
	if _, err := os.Stat(jar); err != nil {
		t.Fatalf("LESHAN_CLIENT_JAR=%s: %v", jar, err)
	}
	return jar, java
}

// fixture is one running server plus the registration events it emits.
type fixture struct {
	srv    *glwm2m.Server
	events chan glwm2m.RegistrationEvent
}

// startServer starts a loopback server whose registration events feed the fixture.
func startServer(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{events: make(chan glwm2m.RegistrationEvent, 16)}
	srv, err := glwm2m.New(glwm2m.Config{
		Addr: "127.0.0.1:0",
		Events: glwm2m.EventSinkFunc(func(_ context.Context, event glwm2m.RegistrationEvent) {
			select {
			case f.events <- event:
			default:
			}
		}),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	f.srv = srv
	return f
}

// register starts the Leshan demo client against the server and waits for its
// registration. The client output is returned for diagnostics: when a real client
// cannot register, that output is the only useful evidence.
func (f *fixture) register(t *testing.T, jar, java string) (glwm2m.Registration, *bytes.Buffer) {
	t.Helper()
	endpoint := "glwm2m-interop-" + strconv.Itoa(os.Getpid())
	output := &bytes.Buffer{}
	cmd := exec.Command(java, "-jar", jar,
		"-u", "coap://"+f.srv.Addr(),
		"-n", endpoint,
		"-l", "300",
	)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the Leshan client: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.After(90 * time.Second)
	for {
		select {
		case event := <-f.events:
			if event.Type != glwm2m.EventRegistered {
				continue
			}
			if event.Registration.Endpoint != endpoint {
				t.Fatalf("registered endpoint = %q, want %q", event.Registration.Endpoint, endpoint)
			}
			return event.Registration, output
		case <-deadline:
			t.Fatalf("the Leshan client did not register within 90s; client output:\n%s", output.String())
		}
	}
}

// TestLeshanClientRegistration is the interoperable direction that works today: a
// real LwM2M client on a different stack registers, and the server parses the
// request exactly like it parses its own client's.
func TestLeshanClientRegistration(t *testing.T) {
	jar, java := leshanClientJar(t)

	f := startServer(t)
	registration, _ := f.register(t, jar, java)

	if registration.Lifetime != 300 {
		t.Fatalf("lifetime = %ds, want 300", registration.Lifetime)
	}
	if registration.Version == "" {
		t.Fatal("registration carries no LwM2M version")
	}
	if registration.Peer.Addr == "" || registration.Peer.Transport != "udp" {
		t.Fatalf("peer = %+v, want a udp address", registration.Peer)
	}
	if f.srv.Count() != 1 {
		t.Fatalf("live registrations = %d, want 1", f.srv.Count())
	}
	// Leshan's demo client announces Device (3), Server (1), Location (6) and the
	// IPSO temperature object (3303); the links prove the body was parsed.
	var hasDevice bool
	for _, link := range registration.ObjectLinks {
		if link == "</3/0>" {
			hasDevice = true
		}
	}
	if !hasDevice {
		t.Fatalf("object links %v do not announce /3/0", registration.ObjectLinks)
	}
	t.Logf("Leshan registered %q as %s (version %s, links %v)",
		registration.Endpoint, registration.ID, registration.Version, registration.ObjectLinks)
}

// TestLeshanDownlinkKnownGap records a real limitation instead of hiding it: a
// LwM2M client answers only its registered server, identified by the source
// address of the request, and this engine sends downlinks from a separate socket.
// Leshan therefore answers "unknown server" with 5.00.
//
// This test fails when the limitation is fixed, which is the signal to update the
// gap in ROADMAP.md, docs/ARCHITECTURE.md and this test together.
func TestLeshanDownlinkKnownGap(t *testing.T) {
	jar, java := leshanClientJar(t)

	f := startServer(t)
	registration, _ := f.register(t, jar, java)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	instance, resource := uint16(0), uint16(0)
	value, err := f.srv.Read(ctx, glwm2m.Operation{
		RegistrationID: registration.ID,
		Path:           glwm2m.Path{ObjectID: 3, InstanceID: &instance, ResourceID: &resource},
	})

	var rejected *glwm2m.OperationError
	switch {
	case err == nil:
		t.Fatalf("the downlink reached Leshan (/3/0/0 = %q): the known gap in ROADMAP.md is fixed; update ROADMAP.md, docs/ARCHITECTURE.md and this test", value.Payload)
	case !errors.As(err, &rejected) || rejected.Code != coap.CodeInternalServerError:
		t.Fatalf("Read /3/0/0 = %v, want Leshan's 5.00 rejection", err)
	}
	t.Logf("known gap reproduced: Leshan answered %s for %s (a LwM2M client only accepts requests from its server address, and the downlink uses a separate socket)",
		rejected.Code, rejected.RegistrationID)
}
