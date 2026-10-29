# glwm2m

A LwM2M server engine in Go.

> Status: **development snapshot (untagged)** — registration/update/deregistration
> state machine on CoAP/UDP. Observe, read/write/execute and DTLS are roadmap.
> No tags are published until a stable release; use commit pseudo-versions.

## Implemented

- CoAP/UDP transport via [`gcoap`](https://github.com/maxid/gcoap) (which uses
  [`gnety`](https://github.com/maxid/gnety) and `gnety/codec/coap`)
- Registration: `POST /rd?ep=...&lt=...&b=...` → `2.01 Created` + Location-Path
- Update: `POST /rd/{id}` → `2.04 Changed`
- Deregistration: `DELETE /rd/{id}` → `2.02 Deleted`
- In-memory registration registry with endpoint/lifetime/binding/version

## Planned

- Observe / Notify
- Read / Write / Execute
- DTLS (X.509 / RPK)
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
