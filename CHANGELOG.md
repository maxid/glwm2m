# Changelog

> Development snapshots are untagged; entries below are chronological.

## [Unreleased]

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
