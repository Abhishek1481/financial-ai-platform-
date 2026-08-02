# ml-service

The Python gRPC service that owns every piece of ML/NLP in the platform:
document extraction, chunking, embeddings, vector search, RAG, financial
summarization, and model evaluation. It has **no public HTTP port** —
`gateway-go` is its only client, and it speaks gRPC exclusively (see
[`/proto`](../proto) for the contracts and [`/docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)
for why the boundary is drawn this way).

## Status (Phase 20)

Phase 3 built the skeleton: every RPC from every service in `/proto` is
registered and reachable, the standard gRPC health-checking and reflection
services are wired up. Phase 6 filled in `IngestionService.ExtractDocument`
(`app/extraction/` — PDF, DOCX, HTML, TXT, SEC filings). Phase 7 fills in
`EmbeddingService`:

- `app/embeddings/chunking.py` — paragraph/sentence-aware character
  chunking with cross-chunk overlap, no ML dependency.
- `app/embeddings/model.py` — lazy-loaded `sentence-transformers`
  (`all-MiniLM-L6-v2`, 384-dim, L2-normalized output), CPU-bound calls run
  via `run_in_executor` so they never block the event loop.
- `app/embeddings/vector_store.py` — `FaissVectorStore`: an in-process
  `IndexFlatIP` (cosine similarity via inner product on normalized
  vectors) optionally persisted to disk, content-hash dedup so identical
  text (boilerplate repeated across filings) is embedded once and reused.
- `ChunkAndEmbed` streams `EMBED_STAGE_{CHUNKING,DEDUPING,EMBEDDING,
  UPSERTING,COMPLETE}` progress, matching the proto defined back in
  Phase 2. `gateway-go`'s worker calls it immediately after
  `ExtractDocument` succeeds (see `gateway-go/internal/ingestion.Service`)
  — chained in Go, not inside ml-service.

Verified with the real model end-to-end (not mocked): downloads/loads
correctly, produces 384-dim normalized vectors where semantically similar
financial text scores higher cosine similarity than unrelated text, and a
live `gateway-go` + `ml-service` pair correctly extracts, embeds, and
reports `chunk_count` back through the HTTP API over a real gRPC
connection.

Phase 8 fills in `SearchService.Search` — semantic, keyword, and hybrid
retrieval:

- `app/search/keyword_index.py` — BM25+ (not classic Okapi BM25; see the
  module docstring for why: at small document counts the classic IDF
  formula hits exactly zero for most terms, silently breaking keyword
  search until enough documents accumulate). Not persisted on its own
  (rank_bm25 has no incremental/serialization support) — rebuilt from
  `FaissVectorStore.all_records()` at startup instead.
- `app/search/fusion.py` — Reciprocal Rank Fusion combines vector and
  keyword rankings by position, not by trying to normalize two
  incomparable score scales (cosine similarity vs. unbounded BM25).
- `app/search/filter.py` — post-retrieval metadata filtering
  (ticker/filing_type/fiscal_period); `filed_after`/`filed_before` are
  accepted on the wire but not applied — nothing in the pipeline
  populates a per-chunk filing date yet, documented as a known gap rather
  than silently ignored.

Verified end-to-end against a live `gateway-go` + `ml-service` pair:
uploaded three documents on different topics, confirmed semantic search
ranks a topically-related-but-keyword-dissimilar query correctly,
keyword search finds only the exact term match, hybrid search blends
both, and ticker filtering excludes non-matching documents.

Phase 9 fills in `RAGService.Query`: retrieval (the same hybrid
vector+keyword pipeline as `SearchService.Search`, extracted into
`app/search/retrieval.py` so neither servicer duplicates it) feeds a
numbered-context prompt (`app/rag/prompt.py`) to an `LLMClient`
(`app/rag/llm_client.py`), whose streamed tokens are relayed to the gRPC
response stream as they're generated and whose `[N]` citation markers are
resolved back to real chunk IDs (`app/rag/citations.py`) once generation
finishes.

- `LLMClient` is a `Protocol` (same Strategy pattern as `VectorStore`,
  `ingestion.Extractor`, `search.Searcher`): `FakeLLMClient` needs no API
  key and is what actually runs in this environment (no
  OpenAI/Anthropic key configured — `ML_SERVICE_LLM_PROVIDER` defaults to
  `fake`), while `LangChainLLMClient` wraps `ChatOpenAI`/`ChatAnthropic`
  via LangChain's async `.astream()` and is fully wired but unverified
  against a live provider — dropping an API key into
  `ML_SERVICE_LLM_API_KEY` and switching the provider setting activates it
  with no code change.
- `FakeLLMClient` isn't a no-op: it parses the numbered context chunks
  back out of the prompt and synthesizes an answer that actually cites
  them (`[1]`, `[2]`, ...), so retrieval, prompt construction, streaming,
  and citation extraction are all genuinely exercised end-to-end — only
  the model call itself is stubbed.

Verified end-to-end against a live `gateway-go` + `ml-service` pair:
uploaded a real document, embedded it, then streamed a real question
through `POST /api/v1/rag/query` and watched SSE `token` events arrive
live, followed by a `final` event whose citation resolved back to the
uploaded document's actual chunk ID — not a canned response.

Phase 10 fills in `RAGService.Summarize`: one of four fixed summary
types (executive/risk/revenue/sentiment, each with its own system prompt
in `app/rag/prompt.py`'s `build_summarize_messages`) generated over every
chunk of one document — `VectorStore.get_by_document_id` (new this phase)
gathers the full document rather than a top-k retrieval slice, since a
summary needs to represent the whole thing, not just the parts most
similar to a query. Unlike `Query`, `Summarize` is unary: a summary has no
"watch it stream" UX requirement, so the servicer collects the full
generated text (reusing the same `LLMClient`/citation-extraction
machinery as `Query`) before responding. An unknown `document_id` (no
embedded chunks) returns `NOT_FOUND`, not an empty summary.

Verified end-to-end against a live `gateway-go` + `ml-service` pair:
uploaded a real document, requested both an executive and a risk summary
through `GET /api/v1/documents/:id/summary?type=...`, and confirmed each
citation resolved back to the uploaded document's own chunk; also
confirmed an unknown document ID returns 404 through the full gRPC
NOT_FOUND -> HTTP path.

Phase 12 fills in `EvaluationService`: `EvaluateAnswer` (unary, one
question/answer/context triple) and `BatchEvaluate` (client-streaming,
aggregate statistics over many). The standard implementation of these
RAGAS-style metrics (faithfulness, context precision/recall,
hallucination, answer relevancy) uses an LLM as a judge — not available
as a *scoring* dependency here any more than as a *generation* one (no
API key in this environment; see `app/rag/llm_client.py`). Rather than
stub evaluation out, `app/evaluation/metrics.py` computes all five via
deterministic lexical overlap: split into sentences, tokenize, and
measure token-set overlap between an answer's claims and the retrieved
context — the same "token-overlap gate" reasoning
`app/search/keyword_index.py` already uses for BM25+ relevance
filtering, plus trusting a valid `[N]` citation marker outright. This is
a real, independently useful algorithm, not a placeholder for the
LLM-judged version — swapping one in later is adding a second scorer
behind the same interface, not a rewrite.

Verified end-to-end against a live `gateway-go` + `ml-service` pair via
the new admin-only `POST /api/v1/admin/evaluate`: a lexically-grounded
answer scored `faithfulness=1.0, hallucination_score=0.0`, and a
fabricated answer over the same context scored `faithfulness=0.0,
hallucination_score=1.0` — the scoring genuinely discriminates, not just
plumbing that always returns the same numbers.

Phase 14 adds monitoring: `app/observability.py`'s `ObservabilityInterceptor`
(a `grpc.aio.ServerInterceptor`, applied once at server construction rather
than by hand in every servicer) records `ml_service_grpc_requests_total`
and `ml_service_grpc_request_duration_seconds` for every RPC regardless of
shape (unary-unary, unary-stream, stream-unary, stream-stream — this
service uses all four), exposed over HTTP via `prometheus_client.start_http_server`
on `ML_SERVICE_METRICS_PORT` (default 9091). The same interceptor extracts
an `x-request-id` gRPC metadata value (forwarded by `gateway-go`'s
`internal/mlclient`) and makes it available to every log line emitted
while handling that RPC (`app/tracing.py`, a contextvar + logging filter)
— see `docs/monitoring/README.md` for why this is a deliberately
scoped-down alternative to full OpenTelemetry tracing in an environment
with no collector deployed yet.

Verified end-to-end against a live `gateway-go` + `ml-service` pair: after
one search request through the full HTTP -> gRPC path, both services'
`/metrics` endpoints showed the matching call recorded exactly once on
each side (`gateway_mlclient_requests_total` and
`ml_service_grpc_requests_total`, same method, same status).

Phases 15-19 (admin dashboard, Docker Compose, Kubernetes manifests,
Terraform, CI/CD) were gateway-go/infrastructure-focused and didn't
change this service's own code. Phase 20 (Tests) adds
`tests/unit/ml_service/test_eval_gate.py` (the CI eval-regression gate
promised in Phase 12's own status section, above) and
`tests/benchmark/test_ml_service_benchmarks.py` (chunking, BM25 search,
and evaluation scoring) — see `/tests/README.md` for the full picture
across both services.

## Setup

```bash
cd ml-service
python -m venv .venv

# Windows
.venv\Scripts\pip install -e ".[dev]"
# macOS/Linux
.venv/bin/pip install -e ".[dev]"
```

Generate the gRPC stubs this service imports (`common.v1`, `ingestion.v1`,
`embeddings.v1`, `search.v1`, `rag.v1`, `evaluation.v1`) — from the repo
root, with the venv above active:

```bash
make proto-python
```

This writes to `proto/gen/python/` (gitignored, regenerated on demand —
never hand-edited or committed).

## Run

```bash
cd ml-service
.venv/Scripts/python -m app.server   # Windows
.venv/bin/python -m app.server       # macOS/Linux
```

Configuration is environment-driven (`app/config.py`, prefix
`ML_SERVICE_`) — copy `.env.example` to `.env` to override defaults. With
`ML_SERVICE_REFLECTION_ENABLED=true` (the default), the running service can
be introspected with `grpcurl`/`grpcui` without needing the `.proto` files
on hand:

```bash
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
```

## Test

```bash
cd ml-service
.venv/Scripts/python -m pytest ../tests/unit/ml_service -v   # Windows
.venv/bin/python -m pytest ../tests/unit/ml_service -v       # macOS/Linux
```

## Layout

```
ml-service/
├── app/
│   ├── server.py           entrypoint: builds + starts the grpc.aio server
│   ├── config.py           pydantic-settings config (env-driven)
│   ├── logging.py          structured JSON logging
│   ├── _bootstrap.py       puts proto/gen/python on sys.path for local dev
│   ├── storage.py           ObjectReader: file:// today, s3:// in Phase 16
│   ├── extraction/          Extractor per format (txt/html/pdf/docx) + factory
│   └── servicers/          one file per gRPC service, implementing its stubs
├── rag/                    real RAG logic lands in Phase 9
├── embeddings/              real embedding pipeline lands in Phase 7
├── evaluation/              real evaluation harness lands in Phase 12
└── pyproject.toml
```

## Document extraction (Phase 6)

`app/extraction/` is a Strategy + Factory pair: `Extractor` is a `Protocol`
(`extract(raw_bytes) -> ExtractionResult`), one implementation per format,
and `factory.get_extractor(doc_type)` maps the proto `DocumentType` enum to
the right one — adding a format later is one new module plus one new dict
entry, never a change to the servicer.

- **TXT** — UTF-8, falling back to Latin-1 (common in older EDGAR exports;
  every byte sequence is valid Latin-1, so this never fails outright).
- **HTML** — `BeautifulSoup` with the stdlib `html.parser` backend (no
  compiled dependency; `lxml` currently has no prebuilt wheel for this
  environment's Python version and was deliberately not made a hard
  requirement over it). `<script>`/`<style>` content is stripped before
  text extraction; `<table>` elements are pulled out structurally (headers
  from the first `<tr>` if it uses `<th>`, cell values flagged numeric via
  a financial-notation-aware heuristic — `$1,234`, `(56.7)`, `12%`) and
  removed from the flat text so table contents aren't duplicated.
- **DOCX** — `python-docx`; paragraphs and tables extracted separately
  (the library has no simple document-order iterator over both, and
  structured table data is more useful downstream than the same numbers
  flattened into prose). First row of each table is treated as a header by
  convention — DOCX has no `<th>`-equivalent markup.
- **PDF** — `pypdf` for text; **table extraction is not implemented**.
  Detecting table structure in a PDF means inferring a grid from character
  positions (what `camelot`/`pdfplumber` do) — a real computer-vision-
  adjacent problem, not a parsing one. Returning an empty table list here
  is an honest scope boundary, not an oversight.
- **SEC filings** — no distinct byte format; EDGAR filings are HTML or
  plain text, so `SEC_FILING` routes to the HTML extractor. Filing-type
  detection (`FORM 10-K`, `10-Q`, `8-K`, `S-1`, `DEF 14A`) via regex over
  the document's first few KB is reliable enough to ship
  (`sec_metadata.py`); ticker/company-name extraction from free text is
  not — EDGAR formats that inconsistently enough that a regex heuristic
  would be wrong often enough to be worse than returning nothing, so those
  fields are left empty pending either caller-supplied metadata or a
  proper NER pass later.

## Why `grpc.aio`, not the sync `grpc` API

This service is I/O-bound (network calls to the LLM, the vector store,
Postgres) far more than it's CPU-bound, so an asyncio-native server handles
concurrent RPCs on one event loop instead of needing a large thread pool —
the same reasoning that justifies async frameworks on the Python web side,
applied to gRPC.

## Why every service registered before every RPC was implemented

An alternative would have been to add each `ingestion_pb2_grpc.add_..._to_server()`
call only once that service's phase landed. That produces a smaller diff per
phase but means the server's reflection output, health-check surface, and
overall shape change on every phase — which makes it harder to build
`gateway-go`'s client code against a stable target, and harder to write
integration tests that assert "these five services exist" independent of
which RPCs on them are implemented. Registering the full surface in Phase 3
and filling in bodies through Phase 12 traded a slightly bigger Phase 3 for
a much smaller diff on every phase after it — as of Phase 12, every RPC on
every registered service is real; `test_server.py`'s health-check smoke
tests are what's left of that scaffolding-era test surface (see its module
docstring for the history of what used to be tested there).
