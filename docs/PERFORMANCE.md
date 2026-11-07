English | [简体中文](PERFORMANCE.zh-CN.md)

# Performance: one machine, loopback UDP

> Measured 2026-11-04 on the machine below. These are a **baseline, not a
> promise**: they say what the current code costs on one host and exist so a later
> change can be compared against something. No number here is extrapolated to a
> real deployment.

## Machine and toolchain

| Item | Value |
|---|---|
| CPU | Apple M1 Max (10 cores: 8 performance + 2 efficiency) |
| OS / kernel | macOS (darwin/arm64) |
| Go | go1.27.1 |
| `GOMAXPROCS` | default (10) |
| Transport | loopback UDP (`127.0.0.1`), server and device in one process |
| Commit | `14f855e` plus the benchmarks in `benchmark_test.go` |

A macOS laptop is not a quiet machine: run-to-run spread is up to roughly ±10 % for
the registration benchmark and up to ±35 % for the notification benchmark (the
shortest operation is the noisiest). Treat the third significant digit as noise.

## What is measured

`benchmark_test.go` runs a real glwm2m server over loopback UDP — no mocked
transport — and times only the steady-state loop. Setup (listening, dialing, the
observation registration) runs outside the timer.

| Benchmark | What it does | Median of 3 runs | Equivalent rate |
|---|---|---|---|
| `BenchmarkRegister` | one `POST /rd?ep=…&lt=300&b=U&lwm2m=1.0` with an object-link body, answered `2.01 Created` | 53.5 µs/op | 18.7k registrations/s |
| `BenchmarkObserveNotify` | the server registers an Observe on `/3303/0/5700`; the device reports a new value and the server's callback receives it | 25.8 µs/op | 38.8k notifications/s |

Allocations per operation:

| Benchmark | Bytes/op | Allocs/op |
|---|---|---|
| `BenchmarkRegister` | 72.1 kB | 133 |
| `BenchmarkObserveNotify` | 66.7 kB | 29 |

## How to read it

- **A registration costs about 54 µs on loopback**, roughly a CoAP round trip plus
  the registration bookkeeping: parsing the query and the object links, the
  lifetime and binding validation, one map entry under the server lock, and the
  `2.01` with its `Location-Path`. At 18.7k/s that is far above what a device fleet
  produces — a fleet of 10k devices with a one-hour lifetime re-registers about 3
  times a second.
- **A notification costs about 26 µs end to end**, from the device's
  `Server.Notify` through the downlink observation to the handler. The callback
  runs on the downlink client's receive goroutine, so a slow handler slows the
  stream instead of growing a queue.
- **Allocations are not optimized.** ~72 kB per registration and ~67 kB per
  notification are dominated by per-datagram buffers on the gnety side and the
  per-exchange state on the gcoap side; nothing is pooled. The numbers document the
  current cost, not a target.
- **The registration benchmark raises `MaxRegistrations`** to 1<<20, because every
  iteration adds a registration and the default cap (1024) would answer `5.03`
  after a thousand. A real server expires or replaces registrations instead.

## Reproduction

```sh
cd libs/glwm2m

# Both benchmarks, median of 3 runs:
go test -run '^$' -bench . -benchtime=2s -count=3
```

`-run '^$'` keeps the unit tests out of the run; benchmarks are not part of
`go test ./...` and not part of CI, per the protocol-library baseline: numbers are
a manual, release-time activity.

## What these numbers do not cover

- **Real networks.** No packet loss, no RTT, no NAT; retransmission and
  deduplication are exercised by the unit tests, not here.
- **DTLS.** Both benchmarks run plain UDP. Certificate DTLS adds a handshake to
  every registration and record protection to every datagram; neither is measured.
- **Registration expiry.** The sweep is one goroutine that runs every
  `ExpiryInterval` (default 1 s); this is not a soak test, and it does not measure
  what a million registrations do to the scan.
- **Block-wise transfers.** A downlink body above one block (Block1) and a sliced
  response (Block2) are not benchmarked.
- **Metrics hooks and event sinks.** Neither is attached; a hook that allocates or
  blocks would change the numbers.
- **Other hardware.** ARM64 laptop figures do not transfer to an ARM32 gateway or a
  server CPU.
