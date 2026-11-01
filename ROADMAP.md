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
- [ ] Interop tests with real devices/simulators
- [ ] Pluggable registration storage and per-peer downlink quotas
- [ ] v1.0 API stabilization
