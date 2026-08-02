# benchmark

**Go** — `Benchmark*` functions live alongside the code they measure
(Go's own convention: `go test -bench` discovers them via `*_bench_test.go`
files in the package under test, not a separate directory), not here:

```bash
cd gateway-go
go test ./internal/cache/... -bench=. -benchmem -run=^$
go test ./internal/ratelimit/... -bench=. -benchmem -run=^$
go test ./internal/conversation/... -bench=. -benchmem -run=^$
```

**Python** — `test_ml_service_benchmarks.py` (this directory), covering
the CPU-bound hot paths that don't need a loaded ML model or a live gRPC
server: chunking, BM25 keyword search, and evaluation-metric scoring.

```bash
cd ml-service && .venv/Scripts/python.exe -m pytest ../tests/benchmark -v --benchmark-only
```
