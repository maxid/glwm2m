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

## Versioning

See [VERSIONING.md](VERSIONING.md).

## License

MIT. See [LICENSE](LICENSE).
