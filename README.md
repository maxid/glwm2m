# glwm2m

A LwM2M server engine in Go (registration, observe, read/write/execute, DTLS), intended as a clean-room alternative to Leshan for Go services.

> Status: **experimental scaffold** (v0.0.0-dev). This is a **clean-room** implementation based on the OMA LwM2M specification; no EPL-licensed source code is copied.

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

## License

MIT. See [LICENSE](LICENSE).
