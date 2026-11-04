English | [简体中文](README.zh-CN.md)

# glwm2m

A LwM2M server engine in Go.

> Status: **development snapshot (untagged)** — registration lifecycle with
> expiry, endpoint-conflict policies and peer binding; server-initiated
> Read/Write/Execute/Observe over UDP and DTLS; object-model validation with
> multiple-resource TLV and atomic writes; Object 19 Binary App Data Container
> with block-wise binary transfer; local object tree, Observe/Notify and DTLS on
> CoAP.
> No tags are published until a stable release; use commit pseudo-versions.

## Implemented

- CoAP/UDP transport via [`gcoap`](https://github.com/maxid/gcoap) (which uses
  [`gnety`](https://github.com/maxid/gnety) and `gnety/codec/coap`)
- Registration: `POST /rd?ep=...&lt=...&b=...` → `2.01 Created` + Location-Path
- Update: `POST /rd/{id}` → `2.04 Changed`
- Deregistration: `DELETE /rd/{id}` → `2.02 Deleted`
- In-memory registration registry with endpoint/lifetime/binding/version/object
  links/peer, `RegisteredAt`, `LastSeen`, `ExpiresAt` and an epoch
- Expiry scan (`ExpiryInterval`) with one event per expired registration; a
  lifetime above `MaxLifetime` is clamped, an invalid one is 4.00
- Endpoint conflicts replace the previous registration by default (epoch
  incremented, old one deregistered with reason `replaced`), or are rejected
  with 4.09 when `RejectEndpointConflict` is set
- Update and delete are bound to the peer that registered the endpoint; another
  peer gets 4.04. A moved peer refreshes the binding and drops its cached
  downlink
- `EventSink` receives `registered`, `updated`, `deregistered` (reason `client`
  or `replaced`) and `expired` events, called outside the server lock
- `MaxRegistrations` bounds the registry (5.03 beyond it)
- Single-resource read/write (text/plain): `GET/PUT /objects/{oid}/{iid}/{rid}`
- Object/instance read/write in LwM2M TLV: `GET/PUT /objects/{oid}` and `/objects/{oid}/{iid}`
  (the TLV wire format itself lives in
  [`gnety/codec/tlv`](https://github.com/maxid/gnety); this module's `tlv`
  package is a compatibility shim over it)
- Execute: `POST /objects/{oid}/{iid}/{rid}` → `2.04 Changed`
- Observe/Notify: GET + Observe=0; resource/instance/object writes trigger notifications
- DTLS transport (server side) via gcoap/gnety
- **Server-initiated device management**: `Server.Read`, `Write`, `Execute` and
  `Observe` send real CoAP requests to the registered peer, with a per-operation
  timeout and a cached client per registration. A 4.xx answer is returned as a
  typed `*OperationError`, not as a transport failure
- Downlink transports: UDP by default; DTLS through `Config.DownlinkTLS`; a
  `Config.DownlinkDialer` replaces the dialer entirely (proxy, PSK, tests). A
  downlink leaves from its own socket, and a compliant LwM2M client only answers
  its registered server address, so server-initiated operations do not reach a
  real client yet — `interop/` reproduces that with Leshan and `ROADMAP.md`
  tracks the transport work.
- Object model: `ObjectDef`/`ResourceDef` declare operations, multiplicity and
  value types; `NewObjectRegistry` validates them. A configured registry rejects
  unknown objects/resources (4.04), non-writable writes and non-executable
  executes (4.05), and invalid values (4.00). Multiple resources keep their
  instance identifiers in TLV (`MultipleResource`/`ResourceInstance`), and a
  TLV write is validated completely before any value is applied
- Object 19 (Binary App Data Container): `BinaryAppDataObject()` definition,
  server-side `CreateBinaryDataInstance`/`ReadBinaryData`/`WriteBinaryData`/
  `DeleteBinaryDataInstance` with block-wise binary transfer, and local
  `/objects/19` support

## Server-initiated management

```go
regs := srv.Registrations() // snapshots of every live registration
instance, resource := uint16(0), uint16(5700)

value, err := srv.Read(ctx, glwm2m.Operation{
    RegistrationID: regs[0].ID,
    Path:           glwm2m.Path{ObjectID: 3303, InstanceID: &instance, ResourceID: &resource},
})
if err != nil {
    return err
}

err = srv.Write(ctx, glwm2m.Operation{
    RegistrationID: regs[0].ID,
    Path:           glwm2m.Path{ObjectID: 3303, InstanceID: &instance, ResourceID: &resource},
    Payload:        []byte("22.5"),
})

observation, initial, err := srv.Observe(ctx, op, func(value glwm2m.Value) {
    // later notifications
})
defer observation.Cancel(context.Background())
```

A downlink uses the address the client registered from, which is the standard
LwM2M assumption that a client serves requests on its registration socket. For
DTLS set `Config.DownlinkTLS`; for any other transport (or to override the
address) use `Config.DownlinkDialer`. `DownlinkMaxRequestSize` bounds a
downlink body (Block1 included).

## Object 19: Binary App Data Container

`BinaryAppDataObject()` returns the standard Object 19 definition for the object
registry:

| Resource | Type | Operations |
| --- | --- | --- |
| 0 Data | Opaque, multiple | R/W |
| 1 Data Priority | Integer | R/W |
| 2 Data Creation Time | Time | R |
| 3 Data Description | String | R/W |
| 4 Data Format | String | R/W |
| 5 App ID | Integer | R/W |

The Data resource holds one or more binary values, each addressed by a resource
instance identifier. The server-side helpers speak the real protocol to a
registered device:

```go
// Create /19 instance with 4 KiB of binary data; Block1 carries it.
instanceID, err := srv.CreateBinaryDataInstance(ctx, registrationID, []glwm2m.BinaryData{
    {ID: 7, Value: payload},
})

values, err := srv.ReadBinaryData(ctx, registrationID, instanceID) // GET /19/{iid}/0
err = srv.WriteBinaryDataInstance(ctx, registrationID, instanceID, glwm2m.BinaryData{ID: 7, Value: next})
err = srv.WriteBinaryData(ctx, registrationID, instanceID, values) // replaces the Data set
err = srv.DeleteBinaryDataInstance(ctx, registrationID, instanceID)
```

Payloads larger than the CoAP block size are uploaded with Block1 automatically,
and a large TLV read is assembled from Block2. The Data resource travels as a TLV
`MultipleResource`, so resource instance identifiers are preserved. The local
`/objects` tree supports the same object: a `PUT /objects/19` with an Object
Instance TLV creates an instance and returns `2.01 Created`.

## Extension points

The extension surface is `EventSink` — one method, `OnRegistration`, called outside the
server lock for `registered`, `updated`, `deregistered` and `expired`. `EventSinkFunc`
adapts a plain function, so a consumer rarely needs a named type.

It deliberately stays in the root package instead of a `hook` subpackage: an event carries
a `Registration`, which is this library's core model, so a separate package would have to
either import the root (a cycle) or own a type that is not an extension. Where a contract
references only foreign types — as `Metrics` does in the CoAP and Modbus siblings — it
belongs in its own package; where it is part of the model, it stays beside it.

To make extensions switchable at run time — start, stop, inspect — wrap the server in a
plugin registry: `gplugin.NewRegistry(server)` from
[`gplugin`](https://github.com/maxid/gplugin) gives named plugins, a three-state lifecycle,
an observer and per-plugin start/stop; this package only defines what a plugin can plug
into.

## Configuration

| Field | Default | Meaning |
| --- | --- | --- |
| `DefaultLifetime` | 24h | lifetime when the client omits `lt` |
| `MaxLifetime` | 7d | cap for a requested lifetime |
| `ExpiryInterval` | 1s | expiry scan interval; negative disables it |
| `MaxRegistrations` | 1024 | registry bound (5.03 beyond it) |
| `RejectEndpointConflict` | false | reject a duplicate endpoint with 4.09 instead of replacing |
| `DownlinkTimeout` | 10s | default per-operation timeout |
| `DownlinkMaxRequestSize` | gcoap default (1 MiB) | bound on one downlink request body |
| `DownlinkTLS` | — | DTLS client configuration for callbacks |
| `DownlinkDialer` | — | custom downlink dialer |
| `Events` | nil | registration lifecycle sink |
| `Objects` | nil | object registry; nil keeps the permissive local tree |

## Planned

- Bootstrap / Queue Mode / OSCORE
- Pluggable registration storage and per-peer downlink quotas
- A real-device/simulator interoperability matrix

## Example

```go
srv, _ := glwm2m.New(glwm2m.Config{Addr: ":5683"})
_ = srv.Start(context.Background())
```

## Runnable examples

Two programs under [`examples/`](examples/README.md) run without an external
device. [`registration-demo`](examples/registration-demo/main.go) registers one
simulated device over CoAP and reads its resource back through the server API;
[`observe-telemetry`](examples/observe-telemetry/main.go) runs the whole circle —
a server-initiated Read, an Observe and a device report that arrives as a
notification. glwm2m has no client type, so the device side uses gcoap, the same
library the server is built on; the registration is the real
`POST /rd?ep=...&lt=...&b=...&lwm2m=...` request:

```sh
go run ./examples/registration-demo
go run ./examples/observe-telemetry
```

[`examples/README.md`](examples/README.md) explains what each one answers, how to
run it and what it prints; `-serve` turns either into a server an external LwM2M
client can register with.

## Versioning

See [VERSIONING.md](VERSIONING.md).

## License

MIT. See [LICENSE](LICENSE).
