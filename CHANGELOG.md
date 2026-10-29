# Changelog

> Development snapshots are untagged; entries below are chronological.

## [Unreleased]

- Minimal LwM2M registration state machine on gcoap: register, update,
  deregister, in-memory registry.
- Single-resource read/write (text/plain) via `/objects/{oid}/{iid}/{rid}`.
- LwM2M TLV codec and object/instance read/write; execute endpoint.
- Observe/Notify: resource/instance/object changes notify registered observers.
