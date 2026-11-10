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
- [x] Downlink over the listener socket: Read/Write/Execute are sent over the
      registration's session (`gcoap.Server.DoTo`), so a real client accepts them
      (`interop/` proves it with Leshan)
- [x] Observe over the listener socket: `Observe` registers on the registration's
      session (`gcoap.Server.ObserveTo`), so a real client accepts it (`interop/`
      proves it with Leshan)
- [x] Block1 from the server side, for a downlink body above one block
- [ ] Interop with real devices and vendor simulators, both directions
- [x] Per-peer downlink quota: requests in flight plus observations per peer address
      (`Config.MaxDownlinksPerPeer`, `ErrTooManyDownlinks`)
- [ ] Pluggable registration storage
- [ ] v1.0 API stabilization
