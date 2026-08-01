# gateway-go

The platform's only public-facing service. Everything a request touches
before it reaches ML logic lives here: auth, rate limiting, routing,
streaming, caching (later phases) — never ML/NLP itself, which is
`ml-service`'s job exclusively (see [`/docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)
for why the boundary is drawn this way).

## Status (Phase 8)

Phase 4 built the skeleton (HTTP lifecycle, logging, health/metrics).
Phase 5 added JWT auth and RBAC. Phase 6 added document ingestion: upload,
dedup, and a bounded worker pool that calls `ml-service` over gRPC to
extract text/tables/metadata. Phase 7 chained a second ml-service call onto
the same job: once extraction succeeds, the worker calls
`EmbeddingService.ChunkAndEmbed`, and `Job` gained an `embedding` status
plus `chunk_count`/`chunks_skipped_duplicate` fields. Phase 8 adds
`GET /api/v1/search`, a thin query-parsing layer over
`SearchService.Search` (`internal/search.Searcher` — same fake-for-tests
interface pattern as `Extractor`/`Embedder`).

```
POST /api/v1/auth/register   {email, password} -> 201 {id, email, role}   (always role "user")
POST /api/v1/auth/login      {email, password} -> 200 {access_token, token_type, expires_in}
GET  /api/v1/me               Bearer token      -> 200 {id, email, role}
GET  /api/v1/admin/ping       Bearer token, admin role only -> 200 {message}

POST /api/v1/documents        multipart: file, category (optional: "sec_filing")
                               -> 202 {document_id, job_id, status: "pending"}
                               -> 200 + reused:true if identical content was already uploaded
GET  /api/v1/documents/:id    Bearer token -> 200 {..., job: {status, extracted_text_preview, table_count, metadata, ...}}

GET  /api/v1/search           Bearer token, ?q=...&mode=semantic|keyword|hybrid&top_k=10
                               &tickers=AAPL,TSLA&filing_types=10-K&fiscal_period=FY2025-Q1
                               -> 200 {results: [{chunk_id, document_id, text, score, metadata}], search_latency_ms}
```

User storage is in-memory (`internal/auth.MemoryUserRepository`); so are
documents and jobs (`internal/ingestion.Memory{Document,Job}Repository`).
Uploaded files themselves land on the local filesystem
(`internal/storage.LocalObjectStore`). None of this is Postgres/S3-backed
yet — see "Design decisions" below for why that's deliberate.

Verified with both real processes running together: `gateway-go` talking to
a live `ml-service` over an actual gRPC connection, uploading real
`.txt`/`.html`/`.docx`/`.pdf` files and polling until extraction completed
— not just unit tests against fakes.

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
├── cmd/gateway/main.go       entrypoint / composition root: builds every dependency, wires the server
├── internal/
│   ├── config/                env-driven config (prefix GATEWAY_)
│   ├── logging/                log/slog JSON setup
│   ├── health/                 readiness-check registry (liveness lives in handlers)
│   ├── auth/                   JWT issuance/validation, RBAC middleware, user repository + service
│   ├── storage/                 ObjectStore: local filesystem today, S3/MinIO in Phase 16
│   ├── mlclient/                 gRPC client to ml-service (IngestionService today)
│   ├── worker/                   generic bounded worker pool
│   ├── ingestion/                 document/job domain: dedup, Upload, worker-pool wiring
│   ├── handlers/                /healthz, /api/v1/auth/*, /api/v1/me, /api/v1/admin/*, /api/v1/documents*
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
`/readyz` (`internal/health.Readiness`) aggregates named checks; Phase 6
registers the first one (`ml-service`, via `mlclient.Client.HealthCheck`
calling ml-service's own standard gRPC health service), Redis joins in
Phase 13, Postgres once it replaces the in-memory stores. Kubernetes treats
a failed liveness probe as "restart the pod" and a failed readiness probe
as "stop routing traffic here" — conflating them means a flaky downstream
dependency causes restart loops on a process that was never actually
broken.

**`ingestion.Extractor` is an interface, not `*mlclient.Client` directly.**
Same Repository-pattern reasoning as `auth.UserRepository`: `ingestion.Service`
depends on the interface, `*mlclient.Client` happens to satisfy it
structurally, and `internal/ingestion/service_test.go` substitutes a fake
that never opens a socket. Caught during development, not after: the first
draft had `Service` depending on the concrete client type, which would have
made every ingestion test require a live ml-service process just to run.

**A bounded worker pool is the actual "concurrent ingestion" story.**
Accepting many simultaneous upload requests is just what `net/http` already
does per-connection — no special engineering required. What needs
deliberate design is preventing a burst of uploads from unboundedly
fanning out into concurrent extraction RPCs that overwhelm `ml-service`.
`internal/worker.Pool[T]` (generic, reusable beyond ingestion) fixes both
the worker count (`GATEWAY_INGESTION_WORKERS`) and the queue depth
(`GATEWAY_INGESTION_QUEUE_SIZE`); `Submit` returns `ErrQueueFull` rather
than blocking when both are saturated, which the HTTP handler turns into a
503 instead of a hung request.

**Storage URIs, not bytes, cross the gRPC boundary.** `gateway-go` writes
an upload to `ObjectStore` and hands ml-service the resulting URI (see
`proto/ingestion/v1/ingestion.proto`'s `s3_uri` field) — the file itself
never rides the RPC. `LocalObjectStore` writes `file://` URIs today;
`ml-service/app/storage.py` reads them back via the same convention
(verified with real files across the actual process boundary, not just
matching string literals in two languages). Both sides dispatch on the URI
scheme, so adding `s3://` support in Phase 16 touches one new
implementation on each side, never the extraction/upload logic itself.

**Content-hash dedup, checked before storage, not after.** `Service.Upload`
buffers the full upload, hashes it, and checks
`DocumentRepository.FindByContentHash` *before* writing to `ObjectStore` or
queuing a job — a duplicate upload reuses the existing document and never
touches ml-service again. The alternative (store first, dedup later) would
mean paying storage and extraction cost for every duplicate before
discovering it was one.

**SEC filing category is caller-asserted, not content-sniffed.** EDGAR
filings are, at the byte level, ordinary HTML or plain text — nothing in
the bytes themselves says "this is a 10-K." `Service.Upload` accepts an
explicit `category` field precisely because inferring it from content would
mean guessing; the caller (which fetched the filing from EDGAR, or knows
why the user is uploading it) already knows.

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
