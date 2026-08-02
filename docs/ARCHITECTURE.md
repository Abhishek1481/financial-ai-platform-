# Architecture

## 1. System overview

```
                                   ┌─────────────────────────────┐
                                   │           Clients            │
                                   │  (web app, CLI, API users)   │
                                   └──────────────┬───────────────┘
                                                   │ HTTPS (REST + SSE streaming)
                                                   ▼
                     ┌─────────────────────────────────────────────────────┐
                     │                    gateway-go (Gin)                   │
                     │  JWT auth · RBAC · rate limiting · request routing    │
                     │  connection pooling · Redis cache · metrics/health    │
                     └───────────┬───────────────────────────┬──────────────┘
                                 │ gRPC                       │ enqueue
                                 ▼                             ▼
                     ┌───────────────────────┐      ┌───────────────────────┐
                     │      ml-service        │      │  scheduler / worker    │
                     │   (Python, FastAPI      │      │  (Go) — concurrent     │
                     │    internals + gRPC     │◄─────┤  ingestion jobs,       │
                     │    server, no public     gRPC  │  embedding jobs,       │
                     │    HTTP surface)        │      │  scheduled refresh     │
                     │                         │      └───────────┬───────────┘
                     │  ┌───────────────────┐  │                  │
                     │  │ document pipeline  │  │                  │
                     │  │ chunking           │  │                  ▼
                     │  │ embeddings (HF/ST) │  │      ┌───────────────────────┐
                     │  │ RAG orchestration   │  │      │   S3-compatible object │
                     │  │ (LangChain)         │  │      │   storage (raw docs)   │
                     │  │ summarization       │  │      └───────────────────────┘
                     │  │ evaluation harness  │  │
                     │  └─────────┬──────────┘  │
                     └────────────┼──────────────┘
                                  │
                  ┌───────────────┼───────────────────┐
                  ▼               ▼                     ▼
        ┌─────────────────┐ ┌──────────┐      ┌─────────────────────┐
        │ Vector store      │ │ Postgres │      │ Redis (cache +       │
        │ (FAISS / OpenSearch)│ (metadata,│      │ session + rate-limit  │
        └─────────────────┘ │ users,   │      │ counters)             │
                             │ jobs,    │      └─────────────────────┘
                             │ eval log)│
                             └──────────┘

        Cross-cutting: Prometheus (metrics) · Grafana (dashboards) ·
                        OpenTelemetry (traces/logs) across every service
```

## 2. Service boundaries and why they exist

### `gateway-go` — the only public entry point
Owns everything that is about *serving traffic safely*, not about ML:
authentication (JWT issuance/validation), role-based access control (admin vs.
user), per-user/per-IP rate limiting, request routing, connection pooling to
downstream services, response streaming (token-by-token RAG answers over
Server-Sent Events), Redis-backed caching of hot queries, structured logging,
Prometheus `/metrics`, `/healthz`/`/readyz`, and graceful shutdown.

This is deliberately never a passthrough. If a request doesn't need auth,
rate limiting, caching, or streaming, it has no reason to go through Go at all —
everything that *does* cross this boundary needs at least one of those
properties, which is what justifies the service existing.

### `scheduler` + `worker` — Go, concurrency-bound ingestion
Document ingestion is bursty and I/O-heavy (users can bulk-upload dozens of
filings at once). Go's goroutines let a small worker pool fan out uploads,
virus/type validation, and S3 writes concurrently with bounded memory, then
hand off a job ID to the scheduler for the embedding pipeline. This is a
job-queue pattern (Postgres- or Redis-backed queue), not a request/response
call — ingestion must survive a client disconnecting mid-upload.

### `ml-service` — Python, the only place ML happens
A gRPC server (internal-only, no public HTTP port) that owns: text/table
extraction from PDF/DOCX/HTML, chunking strategy, embedding generation
(Sentence-Transformers / HuggingFace), vector upsert (FAISS or OpenSearch),
hybrid semantic+keyword search, RAG prompt construction and LLM invocation via
LangChain, citation extraction, financial summarization (executive/risk/
revenue/sentiment), conversational memory, and the model evaluation harness
(faithfulness, context recall, hallucination detection, latency/token
tracking). FastAPI is used internally for local dev ergonomics (OpenAPI docs
while developing the ML logic in isolation) but production traffic reaches it
only through Go via gRPC.

### Data stores
- **Postgres** — system of record: users, roles, documents, jobs, embedding
  status, evaluation results. Relational because this data has real
  relationships and needs transactions (e.g. "mark job complete" and "write
  embedding-status row" together).
- **FAISS / OpenSearch** — vector store for semantic search. FAISS for local/
  dev (in-process, zero infra), OpenSearch for the AWS deployment target
  (managed, supports hybrid BM25+vector search and horizontal scale).
- **Redis** — cache (hot query results, embedding cache to skip recomputation
  on duplicate chunks), rate-limit counters, session/token blocklist.
- **S3-compatible storage** — raw uploaded documents, immutable, referenced by
  Postgres rows.

## 3. Why gRPC, not HTTP, between Go and Python

1. **Typed contract.** `.proto` files in [`proto/`](../proto/) are the single
   source of truth for the Go↔Python interface. A field rename breaks the
   build, not a production request.
2. **Streaming.** RAG answers are generated token-by-token; gRPC server-side
   streaming lets `ml-service` push tokens as they're generated and
   `gateway-go` relay them to the client over SSE without buffering the full
   answer first.
3. **Performance.** Protobuf binary framing over HTTP/2 multiplexing beats
   JSON-over-HTTP/1.1 for the request volume this internal boundary sees
   during bulk ingestion and concurrent query load.

## 4. Design decisions and tradeoffs (interview-ready)

- **Monorepo, not polyrepo.** Shared proto contracts, one CI pipeline, atomic
  cross-service commits during early-stage development. Tradeoff: services
  can't be deployed on fully independent cadences yet — acceptable at this
  stage; the folder boundaries are drawn so a future split into separate
  repos is a `git filter-repo`, not a redesign.
- **FAISS in dev, OpenSearch in prod.** Avoids requiring a managed search
  cluster for local development while keeping the production path on
  infrastructure that supports hybrid search and scales past in-memory
  limits. The vector-store interface in `ml-service` is written against an
  abstraction so swapping backends doesn't touch RAG logic.
- **gRPC internally, REST externally.** Public API consumers get a
  conventional REST/OpenAPI surface (what any external client expects);
  internal service-to-service traffic gets the performance and type-safety
  of gRPC. Different audiences, different tradeoffs.
- **Go does not run any ML code.** Even trivial string/text processing
  related to documents stays in Python. This keeps the "why does Go exist"
  answer sharp: Go is the concurrency/traffic layer, full stop.

## 5. Evaluation and observability as first-class features

A RAG system without evaluation is a demo; one with a faithfulness/context-
recall/hallucination harness and per-request latency and token-usage tracking
is what makes this look like something a team actually operates. Both landed
as core services, not bolted on at the end: `ml-service/app/evaluation/`
(RAGAS-style lexical-overlap scoring — see
[`docs/DESIGN_TRADEOFFS.md`](DESIGN_TRADEOFFS.md#6-observability-request-id-correlation-instead-of-full-opentelemetry)
for why lexical overlap rather than an LLM judge) wired to a CI
eval-regression gate, and Prometheus metrics on both services plus
request-ID log correlation across the gRPC boundary (a deliberately
scoped-down alternative to a full OpenTelemetry collector — same doc,
same section, explains why). See [`docs/ROADMAP.md`](ROADMAP.md) for when
each phase landed, and [`docs/DESIGN_TRADEOFFS.md`](DESIGN_TRADEOFFS.md)
for the reasoning behind each one, condensed for an interview walkthrough.

## See also

- [`docs/SEQUENCE_DIAGRAMS.md`](SEQUENCE_DIAGRAMS.md) — the three core
  flows (document ingestion, streaming RAG query, auth) end-to-end.
- [`docs/openapi.yaml`](openapi.yaml) — the full REST API `gateway-go`
  exposes.
- [`docs/DEPLOYMENT.md`](DEPLOYMENT.md) — how to actually run this: local
  processes, Docker Compose, or Kubernetes on AWS.
- [`docs/DESIGN_TRADEOFFS.md`](DESIGN_TRADEOFFS.md) — the condensed,
  interview-ready version of every major decision on this page and its
  honest tradeoff.
