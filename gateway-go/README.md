# gateway-go

The platform's only public-facing service. Everything a request touches
before it reaches ML logic lives here: auth, rate limiting, routing,
streaming, caching (later phases) — never ML/NLP itself, which is
`ml-service`'s job exclusively (see [`/docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)
for why the boundary is drawn this way).

## Status (Phase 5)

Phase 4 built the skeleton: HTTP server lifecycle (bind → serve → graceful
shutdown), structured logging, liveness/readiness probes, Prometheus
metrics on a separate internal port. Phase 5 adds the first real feature:
JWT authentication and role-based access control.

```
POST /api/v1/auth/register   {email, password} -> 201 {id, email, role}   (always role "user")
POST /api/v1/auth/login      {email, password} -> 200 {access_token, token_type, expires_in}
GET  /api/v1/me               Bearer token      -> 200 {id, email, role}
GET  /api/v1/admin/ping       Bearer token, admin role only -> 200 {message}
```

User storage is in-memory (`internal/auth.MemoryUserRepository`) — there is
no Postgres connection wired up yet; see "Design decisions" below for why
that's a deliberate, temporary stand-in rather than a gap.

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
├── cmd/gateway/main.go       entrypoint: config → logger → auth wiring → server → signal handling
├── internal/
│   ├── config/                env-driven config (prefix GATEWAY_)
│   ├── logging/                log/slog JSON setup
│   ├── health/                 readiness-check registry (liveness lives in handlers)
│   ├── auth/                   JWT issuance/validation, RBAC middleware, user repository + service
│   ├── handlers/                /healthz, /api/v1/auth/*, /api/v1/me, /api/v1/admin/*
│   ├── metrics/                 Prometheus middleware + /metrics handler
│   ├── middleware/              structured request logging, panic recovery
│   └── httpserver/              wires it all together; owns listener/server lifecycle + routes
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
phases register (ml-service gRPC in Phase 6, Redis in Phase 13, Postgres
once it replaces the in-memory user store). Kubernetes treats a failed
liveness probe as "restart the pod" and a failed readiness probe as "stop
routing traffic here" — conflating them means a flaky downstream dependency
causes restart loops on a process that was never actually broken.

**In-memory user storage, not Postgres, for now.** `auth.UserRepository` is
an interface; `MemoryUserRepository` is one implementation, chosen because
there's no database connection wired up yet (that lands with Docker Compose
in Phase 16) and every layer above it — `Service`, the HTTP handlers, the
middleware — is written against the interface, not the concrete store. A
`PostgresUserRepository` slots in later without any of those layers
changing, which is the actual point of the Repository pattern here: not
"might swap databases someday" in the abstract, but a concrete swap already
scheduled on the roadmap.

**No self-service admin registration.** `Service.Register` always assigns
`RoleUser`; the only admin account in this phase is seeded at startup from
`GATEWAY_ADMIN_EMAIL`/`GATEWAY_ADMIN_PASSWORD`. Letting a request body
choose its own role would make privilege escalation a one-line curl command
— granting admin becomes an authenticated admin action once Phase 15's
admin dashboard exists, not a public API parameter.

**Stateless JWTs, not session lookups.** `Authenticate` verifies a token's
signature and expiry only — it never re-queries the user repository per
request. That means a token can't be instantly revoked before it expires;
the mitigation is a short TTL (`GATEWAY_JWT_TTL`, default 1h), not a
database round-trip on every authenticated request. Refresh-token rotation
(to keep access-token TTLs short without forcing re-login every hour) is a
deliberately deferred concern, not an oversight — it's real added
complexity (rotation, revocation lists) that a skeleton auth phase doesn't
need to prove the JWT/RBAC mechanics work.

**HS256, not RS256.** `gateway-go` is both the only issuer and the only
verifier of user-facing tokens — `ml-service` is never handed a raw JWT, it
only sees requests gateway-go has already authenticated, over gRPC. A
shared symmetric secret is the right amount of complexity for that
topology; asymmetric signing (RS256, a private key for issuing and a public
key any verifier can hold) would earn its keep the moment a second service
needs to verify tokens independently without sharing the signing secret.

**Same error for "no such user" and "wrong password."** `Service.Login`
returns `ErrInvalidCredentials` in both cases (see `service.go` and
`service_test.go`'s `..._SameErrorAsWrongPassword` test). Distinguishing
them in the response would hand an attacker a free email-enumeration
oracle — a security property specific enough that it's tested explicitly,
not just implied by the code.

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
