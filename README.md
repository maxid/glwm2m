English | [简体中文](README.zh-CN.md)

# glwm2m

A LwM2M server engine in Go.

> Status: **development snapshot (untagged)** — registration/update/deregistration,
> local object read/write/execute, Observe/Notify and DTLS on CoAP/UDP.
> Server-initiated device management, registration expiry and full object-model
> validation are not complete.
> No tags are published until a stable release; use commit pseudo-versions.

## Implemented

- CoAP/UDP transport via [`gcoap`](https://github.com/maxid/gcoap) (which uses
  [`gnety`](https://github.com/maxid/gnety) and `gnety/codec/coap`)
- Registration: `POST /rd?ep=...&lt=...&b=...` → `2.01 Created` + Location-Path
- Update: `POST /rd/{id}` → `2.04 Changed`
- Deregistration: `DELETE /rd/{id}` → `2.02 Deleted`
- In-memory registration registry with endpoint/lifetime/binding/version
- Single-resource read/write (text/plain): `GET/PUT /objects/{oid}/{iid}/{rid}`
- Object/instance read/write in LwM2M TLV: `GET/PUT /objects/{oid}` and `/objects/{oid}/{iid}`
  (the TLV wire format itself lives in
  [`gnety/codec/tlv`](https://github.com/maxid/gnety); this module's `tlv`
  package is a compatibility shim over it)
- Execute: `POST /objects/{oid}/{iid}/{rid}` → `2.04 Changed`
- Observe/Notify: GET + Observe=0; resource/instance/object writes trigger notifications
- DTLS transport (server side) via gcoap/gnety

## Planned

- Object 19 and binary code stream support
- Bootstrap / Queue Mode / OSCORE

## Example

```go
srv, _ := glwm2m.New(glwm2m.Config{Addr: ":5683"})
_ = srv.Start(context.Background())
```

## Runnable examples

[`examples/registration-demo`](examples/registration-demo/main.go) starts a
server on an ephemeral loopback port, registers one simulated device over CoAP
and reads a resource back through the server API. glwm2m has no client type, so
the device side uses gcoap, the same library the server is built on; the
registration is the real `POST /rd?ep=...&lt=...&b=...&lwm2m=...` request:

```sh
go run ./examples/registration-demo
go run ./examples/registration-demo -addr 127.0.0.1:5683 -endpoint demo-device
```

## Versioning

See [VERSIONING.md](VERSIONING.md).

## License

MIT. See [LICENSE](LICENSE).
