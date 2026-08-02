# Design tradeoffs — an interview walkthrough

This project's actual goal (see the top-level `README.md`): demonstrate
how a real ML platform team splits responsibility, wires the split
together with a typed contract, and operates the result — not "call an
LLM with a prompt." This document is the condensed version of that
argument: the decisions an interviewer would actually probe, why each one
was made, and what the honest tradeoff was. Every claim here traces to a
specific file — cross-reference `git log` for when and why, and each
service's own README for the full detail.

## 1. Why split Go and Python at all, and why this way

**The split**: Go (`gateway-go`) owns everything about *serving traffic
safely* — auth, rate limiting, caching, streaming, request routing.
Python (`ml-service`) owns everything about *ML/NLP* — embeddings,
retrieval, generation, evaluation — behind a gRPC-only boundary with no
public HTTP surface at all.

**Why not one language?** Two different concurrency problems live in this
system. Edge traffic is I/O-bound and bursty (many simultaneous
uploads/queries, each cheap per-request) — Go's goroutines handle that
with less memory and simpler code than an equivalent async-Python or
threaded-Java service. The ML layer is either CPU-bound (embedding,
FAISS search) or bound on an external LLM call — Python isn't there for
performance, it's there because the ecosystem (Sentence-Transformers,
LangChain, FAISS) simply doesn't meaningfully exist elsewhere.

**Why gRPC, not REST, between them?** Two reasons that actually matter in
practice: (1) protobuf schemas are the source of truth and break the
build on both sides when they drift — a REST+JSON boundary would only
catch that at runtime, in production, on whichever field someone forgot
to update; (2) `RAGService.Query` needs server-streaming (tokens arrive
as they're generated, not after the full answer) — gRPC has that natively,
REST would need SSE-over-HTTP hand-rolled on top of whatever the
gateway<->ml-service transport already is, which is strictly more moving
parts for the same result.

**What this costs**: every new field crossing the boundary is a
proto-then-regenerate-then-implement cycle, not "just add a JSON key." A
looser system evolves faster short-term and drifts silently long-term —
this bet is that the discipline pays for itself past a certain team size,
which is exactly what a systems-design interview should hear as the
actual justification, not "gRPC is faster" (it barely matters here; the
LLM call dominates latency by orders of magnitude).

## 2. The Repository pattern, applied consistently, not just in one place

Every piece of state (`auth.UserRepository`, `ingestion.{Document,Job}Repository`,
`conversation.Store`, `cache.Cache`) is an interface with an in-memory
implementation today. This is not "we didn't get to the database yet" —
it's a deliberate sequencing choice: prove every layer above the
persistence boundary (handlers, services, RBAC, tests) against a fast,
dependency-free implementation first, then swap the implementation
without touching any of those layers.

**Evidence this actually paid off, not just a nice story**: `internal/cache`
went from `MemoryCache` to also having `RedisCache` (Phase 16) with zero
changes to any handler — the constructor call in `main.go` is the only
diff. Same for `conversation.Store` → `RedisStore`. `auth.UserRepository`
and `ingestion.{Document,Job}Repository` haven't made that jump yet
(Postgres is provisioned in both the Docker Compose stack and the
Terraform/EKS setup, but nothing reads from it) — stated as an open item,
not hidden, in `k8s/README.md` and `terraform/README.md`.

**The honest tradeoff**: in-memory state means horizontal scaling of
`gateway-go` doesn't actually work correctly yet for anything
stateful — two replicas would each have their own user list. This is
explicitly why `k8s/base/gateway-go-deployment.yaml` still runs multiple
replicas behind a shared filesystem PVC for *documents* specifically
(that part works across replicas) while user/job state implicitly assumes
a single logical backing store that hasn't been made real yet. An
interviewer probing "so does this actually scale" should get exactly that
answer, not a deflection.

## 3. Streaming, end to end, not just at the edge

A RAG answer's tokens travel: LLM provider → `LangChainLLMClient`
(`.astream()`) → `RAGServicer.Query` (gRPC server-streaming) →
`mlclient.Query`'s channel-based Go client → Gin's `c.Stream()` → SSE →
browser. Every hop is a real stream, not "buffer, then fake a stream on
the way out" — which is the actual value of choosing gRPC streaming at
step 2: `ChunkAndEmbed` (Phase 7) deliberately does *not* do this — it
drains its own streaming RPC fully and returns the final message, because
gateway-go's ingestion job model only tracks coarse status and there was
nothing meaningful to do with intermediate progress yet. Two different
choices for two different actual requirements, not a blanket "always
stream" or "never stream" policy — the kind of nuance worth calling out
explicitly, because the lazy version of this system would have picked one
pattern and used it everywhere regardless of fit.

## 4. Building against a fake LLM, honestly

No LLM API key exists in this development environment. The response to
that wasn't "skip RAG" or "hardcode a canned response" — it was: build
the *real* pipeline (retrieval, prompt construction with numbered
citations, streaming, citation extraction) behind an `LLMClient`
Protocol, and make the fake implementation (`FakeLLMClient`) do
real work — it parses the numbered context back out of the prompt it was
handed and synthesizes an answer that actually cites those chunks, so
retrieval and citation-extraction bugs still show up as real test
failures, not silently passed-through nothing.

**What this proves, and what it doesn't**: every mechanical piece of RAG
is genuinely exercised — verified live, with real processes, across every
phase's commit message. What it *can't* prove is generation quality
itself, since there's no real model in the loop. `LangChainLLMClient` is
fully implemented and switches on with one environment variable
(`ML_SERVICE_LLM_PROVIDER` + `ML_SERVICE_LLM_API_KEY`) and zero code
changes — the honest state is "wired, unverified," stated as such
everywhere it's relevant, not implied to be more than it is.

## 5. Caching and rate limiting: what's real vs. what's deferred

`internal/cache.Cache` caches `GET /api/v1/search` results — a genuinely
measurable win under load (see `tests/load/README.md`'s real numbers:
near-`/healthz`-level throughput on repeated identical search queries,
specifically because of the cache). `internal/ratelimit.Limiter` is a
real per-client token bucket, not a stub — `internal/httpserver`'s tests
prove a client actually gets throttled after its burst, and
`tests/load`'s numbers were captured with rate limiting *raised* for that
run specifically so it wasn't confounding the throughput measurement
(documented in the test file, not silently worked around).

**A deliberate scope cut worth naming**: the rate limiter is keyed by
client IP, not authenticated user ID — not because that's the ideal
design, but because it's registered as middleware *before* any route's
`Authenticate()` runs (so it can also cover the pre-auth register/login
routes), meaning JWT claims genuinely aren't available yet at that point
in the middleware chain. The alternative (per-route rate limiting, with
different keys pre- and post-auth) was considered and rejected as
unnecessary complexity for what this system actually needs — stated as a
tradeoff in `internal/httpserver/routes.go`'s own comment, not hidden.

## 6. Observability: request-ID correlation instead of full OpenTelemetry

Both services expose Prometheus metrics (`gateway_mlclient_requests_total`
et al. — see `docs/monitoring/`), verified live: after one real request,
both services' `/metrics` showed the same call recorded on each side. For
tracing specifically, the choice was **not** to pull in a full OTel
SDK — no collector (Jaeger/Tempo) is deployed anywhere in this project's
infrastructure yet, and a tracing SDK with nothing collecting its output
is dead weight. Instead: one correlation ID per HTTP request, forwarded to
ml-service as gRPC metadata, available to every log line on both sides via
a `contextvar` + logging filter. `grep request_id=<id>` across both
services' logs reconstructs one request's full path — most of the
practical debugging value, at a fraction of the infrastructure
dependency, and designed to slot directly into an OTel `trace_id` field
later rather than be thrown away when a real collector does get deployed.

## 7. Testing philosophy: real components, not mocks, until it would be dishonest not to

Every servicer test spins up a real in-process gRPC server (`build_server`)
and drives it with a real client — never a servicer method called
directly, never a mocked gRPC channel. Every `httpserver` test binds a
real TCP listener and issues real HTTP requests. The two places this
philosophy meets a hard limit, both handled the same way — build the real
thing, verify what's verifiable, state the gap plainly:

- **`FakeLLMClient`** (section 4) — the seam is a `Protocol`, not a
  conditional inside business logic, so the fake is a real implementation
  of a real interface, not a bypass.
- **`RedisCache`/`RedisStore`** (section 2) — tested against `miniredis`,
  a pure-Go in-process Redis server, not a mock of the Redis client
  library. Real wire protocol, real client code, just no actual Redis
  binary or Docker required to run `go test`.

`tests/integration/test_full_stack.py` (Phase 20) goes one step further:
it starts *actual separate OS processes* for both services and drives
them exactly like a real deployment would, automating what had previously
only ever been done by hand via `curl` once per development phase — the
gap between "I tested this manually and it worked" and "there's a test
that proves it" is exactly what that file closes.

## 8. Infrastructure-as-code, layered, with verification gaps stated instead of hidden

Docker Compose → Kubernetes/Terraform, each genuinely more capable than
the last (see `docs/DEPLOYMENT.md`), but none of the three could be
build/run-verified in this specific development environment — no Docker
daemon, no `kubectl`, no `terraform` binary exists here. The response
wasn't to skip writing them, or to claim verification that didn't happen:
every one of them was authored carefully, checked with whatever *was*
available (a real YAML parser, a real HCL2 parser, a programmatic
cross-check that every Terraform module call supplies every variable its
module actually declares), and then wired into GitHub Actions — which
*does* have Docker, `kubectl`, and `terraform` — as the actual first real
verification (`docker-build.yml`, `k8s-validate.yml`,
`terraform-validate.yml`). Every one of those workflows exists
specifically to close a gap this development environment couldn't close
on its own, not as generic CI boilerplate.

**A gap in that story, found and fixed rather than left implicit**:
`docker-build.yml` runs on every push, and it turned out `ml-service.Dockerfile`
had the identical missing-output-directory bug as `make proto-python` (see
above) — `proto/gen/python` doesn't exist in the build context either,
for the same reason. It was masked for the same reason: never actually
checked after being written. `terraform-validate.yml` and
`k8s-validate.yml` are narrower still — both are path-filtered to their
own directory (`terraform/**`, `k8s/**`), and since no commit has touched
either path since Phase 19 added the workflows, **neither has executed
even once**. Both now carry a `workflow_dispatch` trigger so they can be
run on demand without waiting for an unrelated Terraform/K8s change —
worth running once before trusting either validation claim at face
value.

## If I were extending this next

In roughly the order they'd matter for a team actually running this in
production, not the order they're numbered in `docs/ROADMAP.md`:

1. **Postgres-backed `UserRepository`/`Document/JobRepository`** — the
   actual blocker to `gateway-go` scaling horizontally with correct
   behavior. The interfaces are ready; this is "just" the implementation.
2. **S3-backed `ObjectStore`** — removes the `ReadWriteMany` PVC
   requirement in Kubernetes entirely, and is a prerequisite for
   `ml-service` and `gateway-go` not needing to share a filesystem at all.
3. **An OpenSearch-backed `VectorStore`** — the actual precondition for
   running more than one `ml-service` replica.
4. **A real LLM provider** — flip one environment variable once there's a
   budget for it; the code has been ready since Phase 9.
