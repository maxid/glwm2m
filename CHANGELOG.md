# Changelog

> Development snapshots are untagged; entries below are chronological.

## [Unreleased]

- Server-initiated Read, Write and Execute now go over the session the registration
  arrived on (`gcoap.Server.DoTo`), so the request leaves from the server's own
  socket — the only source a compliant LwM2M client answers. A registration without
  a live session, or a body that would need Block1, falls back to the dialed client.
  `Observe` still uses the dialed client: `interop/` records the resulting rejection
  from Leshan, and `ROADMAP.md` tracks moving it onto the session.

- `interop/`, an interoperability module against an independent LwM2M
  implementation: the Eclipse Leshan client demo (Java/Californium) registers with
  this server and is parsed like our own client, links and lifetime included. The
  module also records a real limitation instead of hiding it — a client only answers
  its registered server address, and a downlink leaves from its own socket, so
  Leshan rejects server-initiated operations with `5.00 "unknown server"`.
- The CI workflow gains an `integration` job that fetches the Leshan client jar and
  runs `go test -tags=integration ./...`; the tests skip when a JRE or the jar is
  missing.

- **Breaking:** a server-initiated operation now addresses the device with the
  LwM2M client path `/{object}[/{instance}[/{resource}]]`. It used to send the
  server's own `/objects/...` form, which a real LwM2M client answers with 4.04 —
  so `Read`, `Write`, `Execute`, `Observe` and the Object 19 helpers could not
  reach anything but a device that mimicked this library's local endpoints.
  `Path.String()` renders the client form, and `CreateBinaryDataInstance` /
  `DeleteBinaryDataInstance` use it for their requests. Code that goes through
  the exported API needs no change; only a device-side handler that served
  `/objects/...` has to move to the LwM2M path. The local `/objects/...` endpoints
  of this server are unchanged.

- Two runnable examples with a bilingual `examples/README`: `registration-demo`
  (the registration lifecycle, the parsed registration, a local-object answer and
  a `-serve` mode an external LwM2M client can register with) and
  `observe-telemetry` (a server-initiated Read and Observe, then a device report
  that arrives as a notification). Both print `[n/N]` steps and `[done]`, stop on
  Ctrl-C with exit code 0, and list copyable commands in `-h`.
- `docs/ARCHITECTURE.md` and `docs/DEPLOYMENT.md` (each with a Chinese
  counterpart): the registration and observation state machines, the object tree
  and its TLV endpoints, the downlink and its client cache, the limits table, and
  the deployment decisions a fleet forces — bootstrap, the certificate-only DTLS
  trade-off, the in-memory registration store and the restart behaviour.
- `fuzz_test.go`: `FuzzDecodeTLV` drives the TLV codec and the write path that
  turns entries into object values (no panic, canonical re-encode, no value larger
  than the input, the store cannot grow faster than the body);
  `FuzzDecodeRegistration` drives every registration field and pins the contract
  around it (a defined answer, the endpoint kept exactly, the lifetime cap, no
  state left by a rejection).
- The CI workflow gains `race` and a short run of both fuzz targets; the `test`
  matrix stays as it was.

- Object 19 (Binary App Data Container): `BinaryAppDataObject()` is the standard
  registry definition (Data Opaque/multiple R/W; Data Priority, Data Creation
  Time, Data Description, Data Format and App ID). Server-side helpers
  `CreateBinaryDataInstance`, `ReadBinaryData`, `WriteBinaryData`,
  `WriteBinaryDataInstance` and `DeleteBinaryDataInstance` speak the real
  protocol to a registered device; Data travels as a TLV `MultipleResource` so
  resource instance identifiers survive, and payloads above the CoAP block size
  are uploaded with Block1. The local object tree accepts an Object Instance TLV
  at `PUT /objects/19` and answers `2.01 Created`. `Config.DownlinkMaxRequestSize`
  bounds a downlink request body.
- Registration lifecycle: `ExpiryInterval` scans and expires registrations once
  each (`EventExpired`); a duplicate endpoint replaces the previous registration
  with an incremented epoch, or is rejected with 4.09 when
  `RejectEndpointConflict` is set; update and delete are bound to the registering
  peer (4.04 otherwise); `MaxRegistrations` bounds the registry (5.03); an
  invalid lifetime is 4.00 and one above `MaxLifetime` is clamped. `Registration`
  now carries `ObjectLinks`, `Peer`, `RegisteredAt`, `LastSeen`, `ExpiresAt` and
  `Epoch`, and `EventSink` reports registered/updated/deregistered/expired
  outside the server lock.
- Server-initiated device management: `Read`, `Write`, `Execute` and `Observe`
  send real CoAP requests to the registered peer with a per-operation timeout and
  one cached client per registration. UDP is the default, DTLS uses
  `Config.DownlinkTLS`, and `Config.DownlinkDialer` replaces the dialer entirely.
  An error response is returned as `*OperationError` without being retried.
  Closing, expiring or replacing a registration drops its client and
  observations.
- Object model: `ObjectDef`/`ResourceDef` declare operations, multiplicity and
  value types, and `NewObjectRegistry` validates them. A configured registry
  rejects unknown objects or resources (4.04), non-writable writes and
  non-executable executes (4.05), and invalid values (4.00). Multiple resources
  keep their instance identifiers in TLV, and a TLV write is validated in full
  before it is applied atomically.
- **Breaking:** `Server.SetResource` now returns an error so a registry-managed
  write can be rejected; existing call sites that ignore the result keep
  compiling. `SetResourceInstance` and `ResourceInstances` expose multiple
  resource instances.
- The LwM2M TLV wire format moved to `gnety/codec/tlv`, where the protocol
  codecs share one bounded, classified implementation. `glwm2m/tlv` is now a
  documented compatibility shim — type aliases plus delegating
  `Marshal`/`Unmarshal`, no wire logic — so existing importers keep compiling;
  the server engine itself imports the gnety package. Encoding stays
  byte-identical: the codec emits each scope in identifier order, which is the
  order this library already produced. The codec is stricter on input
  (duplicate identifiers in one scope, nesting deeper than 16 scopes and a
  container carrying a value are rejected), which makes `PUT` of an ambiguous
  payload a `4.00 Bad Request` instead of a silent partial write. Multiple
  resource instances are stored per instance identifier, so the canonical order
  no longer collapses them.

- Minimal LwM2M registration state machine on gcoap: register, update,
  deregister, in-memory registry.
- Single-resource read/write (text/plain) via `/objects/{oid}/{iid}/{rid}`.
- LwM2M TLV codec and object/instance read/write; execute endpoint.
- Observe/Notify: resource/instance/object changes notify registered observers.
- DTLS transport configuration and registration-over-DTLS test.
