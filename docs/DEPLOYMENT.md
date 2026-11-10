English | [简体中文](DEPLOYMENT.zh-CN.md)

# Deployment: what to configure, and what to know first

> Applies to the current tree (2026-11-04). This document is about running the
> engine, not about the protocol; `docs/ARCHITECTURE.md` covers the internals.

## Shape

One `glwm2m.Server` owns one CoAP listener (UDP or DTLS). Devices register over
it, and server-initiated Read/Write/Execute/Observe are sent back to the address
the registration arrived from, unless `Config.DownlinkDialer` says otherwise.

Registrations live **in this process's memory**. There is no shared store and no
clustering: run one server per set of devices, and expect that a restart loses
every registration (each device re-registers on its own schedule). If that is not
acceptable for a deployment, registrations have to be terminated in front of this
engine or persisted by an implementation of the pending pluggable-store item.

## Ports and transport

| Knob | Meaning |
|---|---|
| `Config.Addr` | listen address, e.g. `0.0.0.0:5683`; the LwM2M/CoAP port is 5683/UDP |
| `Config.Network` | `"udp"` (default) or `"dtls"` |
| `Config.TLS` | `gnety.TLSConfig`: `CertFile`, `KeyFile`, `CAFile`, `RequireClientCert`, `InsecureSkipVerify` |
| `Config.DownlinkTLS` | DTLS client configuration for calling devices back (client certificate, CA to verify the device) |
| `Config.DownlinkDialer` | replaces the callback dialer entirely: a proxy, a fixed address, a test double |

Devices must be reachable at the address their registration came from — or at
whatever `DownlinkDialer` produces — for any server-initiated operation.

Server-initiated Read, Write and Execute are sent over the session the registration
arrived on, so they leave from the server's own socket — the address a real client
answers, `Observe` registers over the same session, and a body above one CoAP block
is uploaded with Block1. Only a registration without a live session (another
transport, a roamed peer) falls back to the dialer. A device
behind NAT that registers outward but cannot be called back will register, update
and deregister fine, and the downlink will time out.

## Bootstrap

There is **no bootstrap interface** in this engine: nothing serves `/bs`, and a
device cannot be told where to register. Provision the server URI out of band
(factory configuration, DHCP option, a provisioning API next to the server) until
the interface exists. This is the largest functional gap for a device fleet.

## DTLS and PSK

Certificate-based DTLS is supported end to end: the listener with `CertFile`/`KeyFile`,
optional `CAFile` and `RequireClientCert` for mutual authentication, and
`DownlinkTLS` for callbacks. `InsecureSkipVerify` exists for development only and
should never reach production.

**Pre-shared keys are not supported.** gnety's transport has no PSK mode, so a
fleet that ships PSK-only devices (common on NB-IoT and other LPWAN modules) cannot
terminate DTLS here today. The two practical options are a DTLS terminator in front
of the engine that forwards plain CoAP over loopback, or extending the transport
layer first.

## Registration sizing

| Knob | Default | Effect |
|---|---|---|
| `MaxRegistrations` | 1024 | hard cap; beyond it a registration is `5.03` |
| `DefaultLifetime` | 24h | used when a registration carries no `lt` |
| `MaxLifetime` | 7 days | a larger requested lifetime is clamped |
| `ExpiryInterval` | 1s | expiry sweep interval; negative disables the sweep |

Shorter lifetimes recover faster after a restart and cost more update traffic;
longer ones mean fewer updates and a longer window in which a dead device is still
counted as live. `MaxRegistrations` is also the memory bound: one registration
holds its endpoint, object links and peer identity.

## Capacity

The CoAP layer underneath has its own limits — observers, block size, the
deduplication cache, exchange timings — and they come from gcoap's defaults.
`glwm2m.Config` does not expose them yet, so a deployment that needs, say, more
than gcoap's default observer budget has to wait for that to be plumbed through
(or configure gcoap directly if it embeds the engine differently).

`DownlinkTimeout` (10s) bounds one server-initiated operation; a slow device blocks
the caller for at most that long. `MaxDownlinksPerPeer` (8) bounds what one peer
address can have in flight — a running request or a registered observation — so a
device that stops answering cannot accumulate an unbounded pile of downlinks; the
refusal is `ErrTooManyDownlinks` and the peer's existing operations keep working.

## Observability

`Config.Events` is the observable surface: `registered`, `updated`, `deregistered`
(with reason `client` or `replaced`) and `expired` (reason `lifetime`). The sink
runs on a library goroutine outside the locks, so it must not block; a slow sink
slows the request or the expiry sweep.

The engine has no metrics hooks of its own, and gcoap's `Metrics` is not exposed
through `glwm2m.Config`. A deployment that needs counters has to derive them from
the event sink (and accept that they are registration-level, not packet-level).

## Upgrades

Snapshots are untagged and published as commit pseudo-versions; pin `gcoap` and
`gnety` explicitly in `go.mod` and upgrade them together with this module. API
stability is a `ROADMAP.md` item (`v1.0`), so treat exported types as moving until
then. A restart drops registrations — plan upgrades for a moment when devices
re-registering in a burst is acceptable.

## Security notes

- Update and deregistration are bound to the peer that registered the endpoint;
  another peer gets `4.04`. Over plain UDP "peer" is an address, which an off-path
  attacker can spoof — DTLS is what makes the binding meaningful.
- A second registration for a live endpoint **replaces** the first by default.
  Set `RejectEndpointConflict` when a fleet must not be able to displace a device
  by re-registering its endpoint.
- `MaxRegistrations` bounds the memory a registration flood can reach; replacement
  closes the replaced registration's cached downlink.
