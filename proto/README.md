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

Two separate tools, deliberately not one:

- **Codegen** (`make proto-python`, `make proto-go`) routes through
  `grpcio-tools`' embedded `protoc` for *both* languages — it isn't just a
  Python code generator, `protoc` is a generic compiler that shells out to
  `protoc-gen-<lang>` plugins found on `PATH`, and `grpcio-tools` happens to
  bundle a real `protoc` binary. So Go codegen needs no separate system
  `protoc` install, only the two Go plugin binaries on `PATH`:
  `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` and
  `.../grpc/cmd/protoc-gen-go-grpc@latest`. No network access required
  beyond those one-time installs, so it works the same on a laptop with no
  internet as it does in CI.
- **Contract safety** (`make proto-lint`, `make proto-breaking`) uses
  [buf](https://buf.build) — it's a better linter and breaking-change
  detector than anything the native toolchains ship with, but it's not
  required just to generate code.

```bash
# requires the ml-service venv active (bundles the protoc binary both
# targets below actually use)
cd ml-service && python -m venv .venv && .venv/Scripts/pip install -e ".[dev]"
cd ..

# Python stubs
make proto-python

# Go stubs — requires the two plugin installs above
make proto-go

# lint + breaking-change check (optional, requires buf)
go install github.com/bufbuild/buf/cmd/buf@latest
make proto-lint
make proto-breaking
```

Generated code is written to `gen/go/` and `gen/python/`. The `.go`/`.py`
files themselves are gitignored — never committed, every service
regenerates them as part of its own build — but `gen/go/go.mod`/`go.sum`
*are* committed: `gen/go` is its own Go module (see `/go.work`) that
`gateway-go`, and later `scheduler`/`worker`, depend on, and that module
boundary needs to exist even before its first `make proto-go` has run on a
fresh checkout. `proto-breaking` runs in CI (Phase 19) to fail a PR that
breaks the wire contract without bumping the package version.
