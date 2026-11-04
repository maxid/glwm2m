English | [简体中文](README.zh-CN.md)

# interop

Interoperability test against an independent LwM2M implementation: the
[Eclipse Leshan](https://github.com/eclipse-leshan/leshan) client demo, a real
LwM2M client built on a different CoAP stack (Californium).

The tests live in their own Go module so the library's module graph does not carry
a Java runtime or a second CoAP stack. The module replaces
`github.com/maxid/glwm2m` with the parent directory, so it always exercises the
working copy and is meant to be run from a checkout, never published on its own.

## Running it

The tests are tagged `integration` and skip themselves without a JRE and the
Leshan demo jar:

```sh
# once, from this directory (the jar is gitignored):
curl -sSO https://repo1.maven.org/maven2/org/eclipse/leshan/leshan-client-demo/2.0.0-M15/leshan-client-demo-2.0.0-M15-jar-with-dependencies.jar
mv leshan-client-demo-2.0.0-M15-jar-with-dependencies.jar leshan-client-demo.jar

go test -tags=integration ./...
```

`LESHAN_CLIENT_JAR` points the test at a jar somewhere else. CI runs this with
JDK 17, which the GitHub ubuntu image already has.

## What it proves

- **Registration.** Leshan's demo client (LwM2M 1.1/1.2, Californium) registers
  with this server: the request is parsed into the same `Registration` this
  library's own client produces, with the endpoint, lifetime, version, peer and
  object links intact.
- **Read over the server's own socket.** `Server.Read` sends the request over the
  session the registration arrived on (`gcoap.Server.DoTo`), so Leshan accepts it
  and answers the Device object's manufacturer — a request from any other source is
  rejected as "unknown server".
- **A remaining gap, on purpose.** `Observe` still dials a separate client, so
  Leshan answers it `5.00 "unknown server"`; the test records that instead of
  hiding it and fails when the gap is fixed, which is the signal to update
  `ROADMAP.md`, `docs/ARCHITECTURE.md` and the test together.

## Not covered

- The reverse direction (Leshan's *server* demo with this engine as a device) is
  not tested: `glwm2m` is a server engine, and a device needs one socket for both
  registering and serving its object tree, which gcoap's client/server split does
  not provide. The unit suite's fake device covers that shape locally.
- DTLS between Leshan and this engine: the test runs plain CoAP.
