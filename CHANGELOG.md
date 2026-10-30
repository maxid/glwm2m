# Changelog

> Development snapshots are untagged; entries below are chronological.

## [Unreleased]


- The LwM2M TLV wire format moved to `gnety/codec/tlv`, where the protocol
  codecs share one bounded, classified implementation. `glwm2m/tlv` is now a
  documented compatibility shim — type aliases plus delegating
  `Marshal`/`Unmarshal`, no wire logic — so existing importers keep compiling;
  the server engine itself imports the gnety package. Encoding stays
  byte-identical: the codec emits each scope in identifier order, which is the
  order this library already produced. The codec is stricter on input
  (duplicate identifiers in one scope, nesting deeper than 16 scopes and a
  container carrying a value are rejected), which makes `PUT` of an ambiguous
  payload a `4.00 Bad Request` instead of a silent partial write. One nuance of
  the canonical order: `applyTLV` still stores one value per resource
  identifier, so for a multiple resource carrying several instances the child
  with the highest instance identifier now wins, where wire order decided
  before. Per-instance storage is the object-model gap on the roadmap.

  **Build note**: until the gnety commit is pushed, `go.mod` needs
  `replace github.com/maxid/gnety => ../gnety` (the sibling checkout).
  Remove it and pin the pushed pseudo-version instead.

- Minimal LwM2M registration state machine on gcoap: register, update,
  deregister, in-memory registry.
- Single-resource read/write (text/plain) via `/objects/{oid}/{iid}/{rid}`.
- LwM2M TLV codec and object/instance read/write; execute endpoint.
- Observe/Notify: resource/instance/object changes notify registered observers.
- DTLS transport configuration and registration-over-DTLS test.
