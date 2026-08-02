# docker

Dockerfiles per service (`gateway-go.Dockerfile`, `ml-service.Dockerfile`)
and the Grafana provisioning (`grafana/provisioning/`) the Compose stack
mounts in — see `/docker-compose.yml` at the repo root for the actual
stack definition, and its own header comment for the honest caveat: this
was authored and reviewed carefully but not build/run-verified in this
development environment, since no Docker daemon is available here.

## Run

```bash
docker compose up --build
```

- `gateway-go` — http://localhost:8080 (public API), http://localhost:9090/metrics
- `ml-service` — grpc://localhost:50051, http://localhost:9091/metrics
- `redis` — genuinely used by `gateway-go` (search cache + conversation
  memory switch to Redis-backed implementations automatically once
  `GATEWAY_REDIS_ADDR` is set — see `internal/cache/redis_cache.go` and
  `internal/conversation/redis_store.go`)
- `postgres` — provisioned, not yet consumed. The in-memory repositories
  (`auth.MemoryUserRepository`, `ingestion.Memory{Document,Job}Repository`)
  are already written against `Repository` interfaces specifically so a
  Postgres-backed implementation is a self-contained follow-up, not a
  rewrite — that swap just hasn't happened yet.
- `prometheus` — http://localhost:9092 (scrapes both services; see
  `docs/monitoring/prometheus.compose.yml`)
- `grafana` — http://localhost:3000 (admin/admin), pre-provisioned with
  the Prometheus datasource and `docs/monitoring/grafana-dashboard.json`

## Why each Dockerfile has a `proto-gen` stage

`proto/gen/go` and `proto/gen/python` are gitignored — generated from
`/proto/*.proto`, never committed (see `/proto/README.md`). A Docker build
starts from a clean checkout of the repo, so it has to regenerate them
itself rather than assuming a local `make proto` already ran. Both
Dockerfiles' `proto-gen` stage does exactly what `make proto-go`/
`make proto-python` do locally, then a later stage copies the generated
output in — see each Dockerfile's own comments for why even *Go* stub
generation needs a Python toolchain (grpcio-tools' embedded `protoc` is
what both Makefile targets actually shell out to).

## No LLM API key baked in

`ml-service`'s Compose environment sets `ML_SERVICE_LLM_PROVIDER=fake` —
the same reason as everywhere else in this project (see
`ml-service/app/rag/llm_client.py`): no OpenAI/Anthropic key is available
in this environment. Supply a real one via a `.env` file
(`GATEWAY_...`/`ML_SERVICE_...` — Compose auto-loads a `.env` in the repo
root) and switch the provider to activate live generation; no code change
needed.
