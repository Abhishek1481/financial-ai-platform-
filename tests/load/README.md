# load

`main.go` — a minimal, stdlib-only HTTP load-test tool (its own Go module,
`go.mod` in this directory — deliberately outside `/go.work`, since it has
no dependency on `proto/gen/go` or any workspace module). No k6/Locust
required to run it, which matters here specifically: neither is installed
in this project's own development environment, so a load test that needed
one could never actually run — this one does.

## Run

```bash
cd tests/load
go run . -url http://localhost:8080/healthz -concurrency 20 -duration 10s
go run . -url "http://localhost:8080/api/v1/search?q=tesla" -header "Authorization: Bearer <token>" -concurrency 10 -duration 10s
```

## Results (this development environment, single machine, both services and the load generator sharing one CPU)

Not a substitute for a real staging-environment load test (no isolation
between the load generator and the services under test, single-machine —
see the caveat every other infra phase in this project already states),
but real numbers from a real live `gateway-go` + `ml-service` pair, not
fabricated:

**`GET /healthz`** (20 concurrent workers, 8s):
```
total requests: 25057 (3132.1 req/s)
successes:     25057 · non-2xx: 0 · errors: 0
latency p50:   1.2ms   p95: 33.3ms   p99: 47.9ms   max: 106.1ms
```

**`GET /api/v1/search?q=tesla+revenue&mode=hybrid`** (10 concurrent workers, 8s, one document pre-embedded):
```
total requests: 23338 (2917.2 req/s)
successes:     23338 · non-2xx: 0 · errors: 0
latency p50:   1.2ms   p95: 12.9ms   p99: 32.5ms   max: 417.0ms
```

The search numbers are close to `/healthz`'s specifically *because* of
`internal/cache` (Phase 13): the first request is a real ml-service round
trip (hybrid vector+keyword search), every identical request afterward
within the 30s TTL is a cache hit — this load test is incidentally a live
demonstration of that cache actually working under concurrent load, not
just in the unit tests (`internal/cache/cache_test.go`,
`internal/httpserver/server_test.go`'s `TestSearch_CachesIdenticalQueries`).
