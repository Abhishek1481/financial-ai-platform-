# gateway-go

The platform's only public-facing service. Everything a request touches
before it reaches ML logic lives here: auth, rate limiting, routing,
streaming, caching (later phases) — never ML/NLP itself, which is
`ml-service`'s job exclusively (see [`/docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)
for why the boundary is drawn this way).

## Status (Phase 16)

Phase 4 built the skeleton (HTTP lifecycle, logging, health/metrics).
Phase 5 added JWT auth and RBAC. Phase 6 added document ingestion: upload,
dedup, and a bounded worker pool that calls `ml-service` over gRPC to
extract text/tables/metadata. Phase 7 chained a second ml-service call onto
the same job: once extraction succeeds, the worker calls
`EmbeddingService.ChunkAndEmbed`, and `Job` gained an `embedding` status
plus `chunk_count`/`chunks_skipped_duplicate` fields. Phase 8 adds
`GET /api/v1/search`, a thin query-parsing layer over
`SearchService.Search` (`internal/search.Searcher` — same fake-for-tests
interface pattern as `Extractor`/`Embedder`). Phase 9 adds
`POST /api/v1/rag/query`, a Server-Sent Events endpoint over
`RAGService.Query` (`internal/rag.Answerer`, same pattern again):
`mlclient.Client.Query` wraps the gRPC server-streaming call in a Go
channel (`<-chan mlclient.QueryEvent`) so `RAGHandlers.Query` can relay
generated tokens to the HTTP client live via Gin's `c.Stream()`, rather
than draining the whole answer first the way `ChunkAndEmbed` does. Phase
10 adds `GET /api/v1/documents/:id/summary`, a unary call onto
`RAGService.Summarize` — no streaming here, since a summary has no
"watch it stream" requirement; `ml-service` returning `NOT_FOUND` for an
unknown document maps onto a 404 via `google.golang.org/grpc/status`.
Phase 11 adds server-side conversation memory to `POST /api/v1/rag/query`:
`internal/conversation.Store` (same in-memory-for-now Repository pattern
as `auth.UserRepository`) is keyed by `session_id` — an omitted one is
minted here and returned in the `final` SSE event, prior turns are loaded
automatically so a caller doesn't have to resend the whole transcript on
every follow-up, and the question/answer are appended back once the
answer finishes. Explicitly supplying `history` in the request body still
overrides this (the stateless mode Phase 9 shipped), for callers that
prefer to manage their own transcript. Phase 12 adds
`POST /api/v1/admin/evaluate`, an admin-only spot-check onto
`EvaluationService.EvaluateAnswer` through a new `internal/evaluation.Evaluator`
interface — the same thin-wrapper pattern as `search.Searcher`/`rag.Answerer`.
`EvaluationService.BatchEvaluate` (client-streaming, for CI eval-regression
gates) has no gateway-go caller — it's driven directly over gRPC by the CI
pipeline landing in Phase 19, not something an HTTP client calls. Phase 13
adds three cross-cutting pieces: `internal/cache.Cache` (in-memory, TTL-based)
caches `GET /api/v1/search` results; `internal/ratelimit.Limiter`
(token-bucket, per client IP) gates all of `/api/v1`, returning 429 once a
caller's burst is exhausted; `internal/scheduler.Run` drives a background job
that prunes abandoned `conversation.MemoryStore` sessions
(`GATEWAY_CONVERSATION_MAX_AGE`, default 24h) so a long-running process
doesn't leak memory for sessions no one returns to. All three are
in-memory/local, same "temporary but real" tradeoff as the repositories
above — Redis lands in Phase 16.

Phase 14 adds monitoring: `internal/mlclient`'s client interceptor
instruments every ml-service call (`gateway_mlclient_requests_total`,
`gateway_mlclient_request_duration_seconds`); `internal/cache`/`ratelimit`/
`conversation` self-instrument (cache hit/miss, rate-limit rejections,
active/pruned session counts); `internal/middleware.RequestID` mints or
forwards a correlation ID per HTTP request, echoed as `X-Request-ID` and
carried to ml-service as gRPC metadata so both services' structured logs
for one request share an ID (see `docs/monitoring/README.md` for why this
is a deliberately scoped-down alternative to full OpenTelemetry tracing —
no collector is deployed until Phase 16). `docs/monitoring/` also has a
Prometheus scrape config and an importable Grafana dashboard.

Phase 15 adds four read-only admin endpoints — `GET /api/v1/admin/{users,
documents,jobs,stats}` — built on new `ListAll` methods on
`auth.UserRepository`/`ingestion.{Document,Job}Repository` rather than a
separate admin-only data path, so an admin sees the same data regular
requests already produce, just across every user. `users` never
serializes `PasswordHash` (the view type has no field for it — not a
json tag hiding it). No pagination yet, an accepted limitation at these
in-memory stores' dev-scale data volumes, same as everywhere else that
tradeoff already exists (Postgres in Phase 16 is what actually needs
pagination to matter). A dashboard *UI* (something a browser renders) is
out of scope — this repo has no frontend framework in play; what this
phase delivers is the API surface a UI would consume.

Phase 16 adds the full local Docker Compose stack (`/docker-compose.yml`,
`docker/gateway-go.Dockerfile`) — `redis` is genuinely wired: setting
`GATEWAY_REDIS_ADDR` (Compose sets it automatically) switches
`internal/cache.Cache` and `internal/conversation.Store` from their
in-memory implementations to `RedisCache`/`RedisStore` with no other code
change, both tested against `miniredis` (a pure-Go in-process Redis
server — genuine client/server wire testing without needing a live Redis
or Docker for `go test` itself to pass). `postgres` is provisioned in the
stack but not yet consumed — the `Repository` interfaces
(`auth.UserRepository`, `ingestion.{Document,Job}Repository`) are already
the seam a Postgres-backed implementation slots into, that swap just
hasn't happened yet. **Caveat**: this Dockerfile/Compose setup was
authored and reviewed carefully but is unverified by an actual
`docker compose up` — no Docker daemon is available in this development
environment (see `docker/README.md`).

```
POST /api/v1/auth/register   {email, password} -> 201 {id, email, role}   (always role "user")
POST /api/v1/auth/login      {email, password} -> 200 {access_token, token_type, expires_in}
GET  /api/v1/me               Bearer token      -> 200 {id, email, role}
GET  /api/v1/admin/ping       Bearer token, admin role only -> 200 {message}
POST /api/v1/admin/evaluate   Bearer token, admin role only, body: {question, answer, context?, ground_truth_answer?}
                               -> 200 {faithfulness, context_precision, context_recall,
                                        hallucination_score, answer_relevancy}
GET  /api/v1/admin/users      Bearer token, admin role only -> 200 {users: [{id, email, role, created_at}]}
GET  /api/v1/admin/documents  Bearer token, admin role only -> 200 {documents: [...]}  (every user's documents + job status)
GET  /api/v1/admin/jobs       Bearer token, admin role only -> 200 {jobs: [...]}        (every processing attempt, not just latest)
GET  /api/v1/admin/stats      Bearer token, admin role only -> 200 {total_users, total_documents, total_jobs, jobs_by_status}

POST /api/v1/documents        multipart: file, category (optional: "sec_filing")
                               -> 202 {document_id, job_id, status: "pending"}
                               -> 200 + reused:true if identical content was already uploaded
GET  /api/v1/documents/:id    Bearer token -> 200 {..., job: {status, extracted_text_preview, table_count, metadata, ...}}
GET  /api/v1/documents/:id/summary   Bearer token, ?type=executive|risk|revenue|sentiment (default executive)
                               -> 200 {summary, citations, usage, latency_ms}
                               -> 404 if the document has no embedded chunks

GET  /api/v1/search           Bearer token, ?q=...&mode=semantic|keyword|hybrid&top_k=10
                               &tickers=AAPL,TSLA&filing_types=10-K&fiscal_period=FY2025-Q1
                               -> 200 {results: [{chunk_id, document_id, text, score, metadata}], search_latency_ms}

POST /api/v1/rag/query        Bearer token, body: {question, session_id?, history?, top_k?,
                               tickers?, filing_types?, fiscal_period?}
                               -> 200 text/event-stream: "token" events {token}, then one "final"
                               event {session_id, citations, usage, latency_ms}, or an "error" event
                               (session_id omitted in the request is server-generated and returned
                               in "final" for reuse; conversation history is remembered server-side
                               per session unless "history" is explicitly supplied)
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
│   ├── mlclient/                 gRPC client to ml-service (Ingestion/Embedding/Search/RAG)
│   ├── worker/                   generic bounded worker pool
│   ├── ingestion/                 document/job domain: dedup, Upload, worker-pool wiring
│   ├── search/                   Searcher interface over SearchService
│   ├── rag/                       Answerer interface over RAGService (streaming)
│   ├── conversation/               session_id-keyed conversation memory (in-memory Store)
│   ├── evaluation/                 Evaluator interface over EvaluationService
│   ├── cache/                       in-memory TTL cache (GET /api/v1/search)
│   ├── ratelimit/                   per-client token-bucket rate limiter
│   ├── scheduler/                   generic "run this every N minutes" background job
│   ├── reqid/                        per-request correlation ID, carried into mlclient's gRPC metadata
│   ├── handlers/                /healthz, /api/v1/auth/*, /api/v1/me, /api/v1/admin/*, /api/v1/documents*, /api/v1/search, /api/v1/rag/query
│   ├── metrics/                 Prometheus middleware + /metrics handler
│   ├── middleware/              structured request logging, panic recovery
│   └── httpserver/              wires it all together; owns listener/server lifecycle + routes
└── go.mod                     its own module — see /go.work
```

## Design decisions

**`mlclient.Client.Query` returns a channel, not a drained result.**
`ChunkAndEmbed` drains its server-streaming RPC fully and returns only the
final message — fine there, since gateway-go's `Job` model only tracks
coarse stages. RAG is different: the entire point of a streaming RPC here
is that a user is watching tokens arrive, so `Query` instead spawns a
goroutine that forwards each `stream.Recv()` onto a `<-chan QueryEvent`,
closing it when the stream ends or errors. `RAGHandlers.Query`
(`internal/handlers/rag.go`) reads that channel inside Gin's `c.Stream()`
and re-emits each token as an SSE event immediately — no buffering the
whole answer, which would defeat the reason the proto uses server-streaming
at all (see `proto/README.md`'s "Why these RPC shapes").

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
