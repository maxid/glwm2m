# Roadmap

- [x] CoAP transport integration (gcoap/gnety)
- [x] Registration / update / deregistration state machine
- [x] Observe/Notify
- [x] Read/Write (text/plain single resource)
- [x] TLV multi-resource read/write and Execute
- [x] DTLS transport
- [x] Registration lifecycle: expiry scan, endpoint-conflict policy, epoch,
      peer-bound update/delete, lifecycle events, registration limit
- [x] Server-initiated Read/Write/Execute/Observe over UDP and DTLS
- [x] Object registry (operations/multiplicity/types), per-instance multiple
      resources and atomic TLV writes
- [x] Object 19 Binary App Data Container: standard definition, block-wise
      downlink create/read/write/delete and local object-tree support
- [x] Simulated-device end-to-end tests (registration, downlink, observe)
- [x] Interoperability with an independent implementation: the Eclipse Leshan
      client demo registers and is parsed like our own client (`interop/`)
- [ ] Downlink over the listener socket: a real client only accepts requests
      from its registered server address, so server-initiated
      Read/Write/Execute/Observe do not reach it yet (`interop/` records this)
- [ ] Interop with real devices and vendor simulators, both directions
- [ ] Pluggable registration storage and per-peer downlink quotas
- [ ] v1.0 API stabilization
