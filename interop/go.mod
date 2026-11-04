// This module holds the interoperability tests against an independent LwM2M
// implementation. It is deliberately separate so the library's module graph does
// not carry a Java runtime, a container runtime or a second CoAP stack.
module github.com/maxid/glwm2m/interop

go 1.27

require github.com/maxid/glwm2m v0.0.0

require (
	github.com/maxid/gcoap v0.0.0-20261104133055-c3d8a75ea893 // indirect
	github.com/maxid/gnety v0.0.0-20261101095123-3eff0a20f62c // indirect
	github.com/pion/dtls/v2 v2.2.12 // indirect
	github.com/pion/logging v0.2.2 // indirect
	github.com/pion/transport/v2 v2.2.4 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

replace github.com/maxid/glwm2m => ../
