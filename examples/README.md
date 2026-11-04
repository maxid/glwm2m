English | [简体中文](README.zh-CN.md)

# Examples

Two runnable programs, each answering one question. Both are self-contained `main`
packages, so `go run ./examples/<name>` from the repository root is enough — no
LwM2M device, no Leshan instance, no configuration file. Each one starts the
server it talks to, and the simulated device where it needs one, on loopback
ports.

- [registration-demo](#registration-demo) — what the registration lifecycle looks
  like, what the server parses out of it, and how the local object tree answers a
  write.
- [observe-telemetry](#observe-telemetry) — the whole LwM2M circle: a
  server-initiated Read and Observe against a registered device, then a report that
  arrives without any request.

## Common conventions

- Each example prints one line per step — `[n/N] action … result` — and `[done] …`
  when it finishes.
- Failures go to stderr as `<example>: <action>: <error>` with a non-zero exit code.
- Ctrl-C stops either example. The scripted run prints `[stop] interrupted`, the
  long-running `-serve` mode prints `[stop] server stopped`; both exit 0 — an
  example is not a service.
- `-h` prints what the example answers plus commands you can copy verbatim.
- The scripted runs listen on ephemeral loopback ports and print the addresses they
  chose. `-serve` uses the CoAP port `127.0.0.1:5683` and moves to the next free UDP
  port when it is taken, so the address it prints is always the one to use.

## registration-demo

**Answers:** which requests a device sends to be registered, what the server keeps
from them, and how a value the device wrote over CoAP is available to the server
without another request.

```sh
go run ./examples/registration-demo
go run ./examples/registration-demo -endpoint sensor-01 -timeout 2s
go run ./examples/registration-demo -serve   # then: coap-client -m post "coap://127.0.0.1:5683/rd?ep=dev&lt=60&b=U&lwm2m=1.0"
```

Expected:

```
[1/6] server listening on 127.0.0.1:51175
[2/6] device "demo-device" registered as a826c9247143 (2.01)
[3/6] server parsed it: endpoint=demo-device lifetime=60s binding=U version=1.0 links=</3303/0> (live: 1)
[4/6] device PUT /objects/3303/0/5700 -> 2.04
[5/6] device GET /objects/3303/0/5700 -> 2.05 "21.5"
[6/6] server.Resource(3303, 0, 5700) = "21.5" (no request needed)
[done] registration lifecycle demo complete; server and device closed
```

The registration id is server-generated and comes back in the `Location-Path`
options of the `2.01`. `-serve` holds the server open instead and prints every
lifecycle event (`registered`, `updated`, `deregistered`, `expired`) through
`Config.Events` as an external LwM2M client registers, updates and leaves.

## observe-telemetry

**Answers:** what LwM2M adds over CoAP request/response — a server-initiated Read
and Observe against a device, and a device report that arrives as a notification
without the server asking again.

```sh
go run ./examples/observe-telemetry
go run ./examples/observe-telemetry -endpoint sensor-01
go run ./examples/observe-telemetry -serve   # then: coap-client -m post "coap://127.0.0.1:5683/rd?ep=dev&lt=60&b=U&lwm2m=1.0"
```

Expected:

```
[1/7] device serving GET /3303/0/5700 on 127.0.0.1:52429
[2/7] LwM2M server listening on 127.0.0.1:62577, downlink dials the device
[3/7] device "telemetry-device" registered as a92f3464eec5 (2.01 Created)
[4/7] server Read /3303/0/5700 -> "21.5"
[5/7] server Observe /3303/0/5700 -> initial "21.5"
[6/7] device reported "22.0" -> server received "22.0" (no request)
[7/7] device deregistered a92f3464eec5 (2.02)
[done] registration, read, observe and deregistration complete
```

Two implementation notes the output hides:

- A real LwM2M client uses **one socket for both roles**: it registers and it
  serves its object tree on the same address. gcoap's client and server own
  separate sockets, so the demo points `Config.DownlinkDialer` at the device
  listener — the hook the library documents for an address the registration socket
  does not describe. `-serve` does not need it: there the server calls back the
  address a real client registered from.
- Downlinks use the LwM2M client form `/{oid}/{iid}/{rid}` — the path a real
  device serves. The server's own `/objects/...` endpoints (used by
  `registration-demo`) are a different surface and never appear on the wire to a
  device.

[`docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) covers the registration and
observation state machines; [`docs/DEPLOYMENT.md`](../docs/DEPLOYMENT.md) covers
bootstrap, the DTLS/PSK trade-off and the registration-store choice.
