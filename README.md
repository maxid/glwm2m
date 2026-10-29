# glwm2m

A LwM2M server engine in Go: registration, observe, read/write/execute and DTLS.

> Status: **experimental scaffold** (v0.0.0-dev).

## Planned transport

UDP/DTLS via the `gnety` transport library (no protocol semantics in gnety).

## Planned features

- Registration / update / deregistration
- Observe / Notify
- Read / Write / Execute
- DTLS (X.509 / RPK)
- Object 19 and binary code stream support (optional)
- Bootstrap / Queue Mode / OSCORE: out of initial scope

## Install

```bash
go get github.com/maxid/glwm2m
```

## Versioning

See [VERSIONING.md](VERSIONING.md).

## License

MIT. See [LICENSE](LICENSE).
