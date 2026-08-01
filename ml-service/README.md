# ml-service

The Python gRPC service that owns every piece of ML/NLP in the platform:
document extraction, chunking, embeddings, vector search, RAG, financial
summarization, and model evaluation. It has **no public HTTP port** —
`gateway-go` is its only client, and it speaks gRPC exclusively (see
[`/proto`](../proto) for the contracts and [`/docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)
for why the boundary is drawn this way).

## Status (Phase 7)

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

## Why every service registers even though most RPCs abort

An alternative would be to add each `ingestion_pb2_grpc.add_..._to_server()`
call only once that service's phase lands. That produces a smaller diff per
phase but means the server's reflection output, health-check surface, and
overall shape change on every phase — which makes it harder to build
`gateway-go`'s client code against a stable target, and harder to write
integration tests that assert "these five services exist" independent of
which RPCs on them are implemented. Registering the full surface now and
filling in bodies later trades a slightly bigger Phase 3 for a much smaller
diff on every phase after it.
