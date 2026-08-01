# proto

Shared protobuf/gRPC contracts between `gateway-go` and `ml-service`. This is
the single source of truth for the internal API — neither service hand-rolls
request/response types, and a breaking change here fails CI (`buf breaking`)
before it fails in production.

## Layout

```
proto/
├── common/v1/common.proto        shared types: Document, Chunk, Citation,
│                                  ConversationTurn, TokenUsage, MetadataFilter
├── ingestion/v1/ingestion.proto  IngestionService — text/table/metadata extraction
├── embeddings/v1/embeddings.proto EmbeddingService — chunk, embed, upsert, dedup
├── search/v1/search.proto        SearchService — semantic/keyword/hybrid retrieval
├── rag/v1/rag.proto              RAGService — streaming Query, Summarize
├── evaluation/v1/evaluation.proto EvaluationService — faithfulness/recall/hallucination
├── buf.yaml                      buf module + lint/breaking-change config
└── buf.gen.yaml                  codegen targets (Go + Python)
```

Every service is versioned (`v1`) from day one — adding `v2` alongside `v1`
later is a non-breaking change; renaming fields in place is not.

## Why these RPC shapes

- **`IngestionService.ExtractDocument`** is unary and called by a `worker`
  job, not by `gateway-go` directly — extraction can take minutes on a large
  filing and has no business sitting on a request a client is blocked on.
- **`EmbeddingService.ChunkAndEmbed`** is server-streaming so a worker can
  report live progress (`chunks_processed` / `chunks_total`) for a job's
  status row instead of blocking until every chunk in a 300-page 10-K is
  embedded.
- **`SearchService.Search`** is unary and on the hot path — called directly
  by `gateway-go` while a user waits.
- **`RAGService.Query`** is server-streaming: this is *the* reason the
  Go↔Python boundary had to support streaming at all. Tokens are relayed to
  the client over SSE as `ml-service` generates them.
- **`EvaluationService.BatchEvaluate`** is client-streaming so a CI eval gate
  can push an entire eval set through one RPC and get back aggregate
  precision/recall/faithfulness stats instead of one round trip per sample.

## Regenerating stubs

Codegen uses [buf](https://buf.build) rather than raw `protoc` invocations,
so linting and breaking-change detection run from the same config used to
generate code.

```bash
# one-time install
go install github.com/bufbuild/buf/cmd/buf@latest

# from proto/
buf lint
buf generate
```

`buf generate` writes to `gen/go/` and `gen/python/`, both gitignored —
generated code is never committed; every service regenerates it as part of
its build (`make proto` at the repo root, wired up in Phase 3/4).

`buf breaking --against '.git#branch=main'` runs in CI (Phase 19) to fail a
PR that breaks the wire contract without bumping the package version.
