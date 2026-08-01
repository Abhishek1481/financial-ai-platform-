# ml-service

The Python gRPC service that owns every piece of ML/NLP in the platform:
document extraction, chunking, embeddings, vector search, RAG, financial
summarization, and model evaluation. It has **no public HTTP port** —
`gateway-go` is its only client, and it speaks gRPC exclusively (see
[`/proto`](../proto) for the contracts and [`/docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)
for why the boundary is drawn this way).

## Status (Phase 3)

This is the service **skeleton**: every RPC from every service in `/proto`
is registered and reachable, the standard gRPC health-checking and
reflection services are wired up, and the process starts/stops cleanly on
signals. No RPC does real work yet — each one `context.abort()`s with
`UNIMPLEMENTED` and a message naming the phase that fills it in. This is
intentional: the *set* of RPCs, and the health/reflection surface around
them, is stable from day one, so later phases are filling in method bodies
rather than growing the service's shape.

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
│   └── servicers/          one file per gRPC service, implementing its stubs
├── rag/                    real RAG logic lands in Phase 9
├── embeddings/              real embedding pipeline lands in Phase 7
├── evaluation/              real evaluation harness lands in Phase 12
└── pyproject.toml
```

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
