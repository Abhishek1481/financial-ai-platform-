# gateway-go

The platform's only public-facing service. Everything a request touches
before it reaches ML logic lives here: auth, rate limiting, routing,
streaming, caching (later phases) — never ML/NLP itself, which is
`ml-service`'s job exclusively (see [`/docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)
for why the boundary is drawn this way).

## Status (Phase 4)

This is the service **skeleton**: HTTP server lifecycle (bind → serve →
graceful shutdown), structured logging, liveness/readiness probes, and
Prometheus metrics on a separate internal port. No business routes yet —
those start arriving in Phase 5 (auth) and Phase 6 (ingestion).

## Setup

Requires Go 1.23+.

```bash
cd gateway-go
go mod download
```

## Run

```bash
cd gateway-go
go run ./cmd/gateway
```

Or from the repo root: `make gateway-run`. Configuration is
environment-driven (`internal/config/config.go`, prefix `GATEWAY_`) — see
`.env.example` for every setting and its default.

```bash
curl http://localhost:8080/healthz   # liveness — always 200 if the process is up
curl http://localhost:8080/readyz    # readiness — 200 until later phases register checks
curl http://localhost:9090/metrics   # Prometheus exposition — NOT on the public port
```

## Test

```bash
cd gateway-go
go test ./... -v
```

Or from the repo root: `make gateway-test`. `internal/httpserver/server_test.go`
binds real ephemeral ports and drives the server over actual HTTP — not
handler unit tests in isolation — the same bar `ml-service`'s Python suite
holds itself to.

## Layout

```
gateway-go/
├── cmd/gateway/main.go       entrypoint: config → logger → server → signal handling
├── internal/
│   ├── config/                env-driven config (prefix GATEWAY_)
│   ├── logging/                log/slog JSON setup
│   ├── health/                 readiness-check registry (liveness lives in handlers)
│   ├── handlers/                /healthz
│   ├── metrics/                 Prometheus middleware + /metrics handler
│   ├── middleware/              structured request logging, panic recovery
│   └── httpserver/              wires it all together; owns listener/server lifecycle
└── go.mod                     its own module — see /go.work
```

## Design decisions

**Metrics on a separate port from public traffic.** `/metrics` is served by
its own `http.Server` on `GATEWAY_METRICS_PORT` (default 9090), never on the
Gin engine that handles public API traffic. A Kubernetes Service/Ingress
exposes the public port only; Prometheus scrapes the metrics port
in-cluster. Exposing request-rate and latency data on the same port the
internet can reach is a needless information leak this avoids for free.

**Liveness and readiness are different endpoints for a reason.** `/healthz`
never checks a downstream dependency — it only proves the process is up.
`/readyz` (`internal/health.Readiness`) aggregates named checks that later
phases register (Postgres in Phase 5, ml-service gRPC in Phase 6, Redis in
Phase 13). Kubernetes treats a failed liveness probe as "restart the pod"
and a failed readiness probe as "stop routing traffic here" — conflating
them means a flaky downstream dependency causes restart loops on a process
that was never actually broken.

**`Listen()` and `Serve()` are separate methods**, not one blocking call.
`Listen` binds the TCP listeners (fast, synchronous) so the real bound port
is known immediately — required when tests ask for port `0` and need to
learn what the OS actually picked before issuing requests. `Serve` then
blocks running the accept loop. `main.go` calls them back-to-back; tests
call `Listen`, start `Serve` in a goroutine, then hit the now-known address.

**`log/slog` over a third-party logging library.** Stdlib as of Go 1.21,
JSON output out of the box, no dependency to justify for what a handful of
`With`/`Info`/`Error` calls need.

**Hand-rolled env config over viper/envconfig.** Six scalar fields with
defaults don't earn a parsing dependency; `os.LookupEnv` plus a couple of
typed helpers is the whole implementation (`internal/config/config.go`).

**Go workspace (`/go.work`), not a single monolithic module.** `gateway-go`
is its own Go module today; `scheduler` and `worker` will be too once
Phase 6/13 create them. The workspace lets all three be developed and
tested together against each other's local changes without publishing a
version first, while still being independently buildable, versionable, and
(later) deployable — the same "monorepo now, splittable later" tradeoff
`/docs/ARCHITECTURE.md` makes for the repo as a whole, applied one level
down to the Go services specifically.
