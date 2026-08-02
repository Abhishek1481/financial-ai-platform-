# tests

A shared top-level directory (see `/pytest.ini`'s own comment) rather than
one owned by either service, split by what each category actually needs
to run:

- **`unit/ml_service/`** — the bulk of this project's test coverage:
  ml-service's servicers, scoring, retrieval, extraction, chunking, etc.,
  each exercised against a real in-process gRPC server (never a mocked
  one — see any given test file's own module docstring). Run by
  `ci.yml` on every push/PR.
- **`integration/`** — `test_full_stack.py`: a real `gateway-go` +
  `ml-service` pair, started and torn down by the test itself, driven
  over real HTTP/gRPC. Manual (`pytest ../tests/integration`), not part
  of the default CI run — see its own README for why.
- **`api/`** — folded into `integration/` rather than duplicated; see
  its own README.
- **`load/`** — `main.go`, a dependency-free Go load-test tool (its own
  module) plus real recorded throughput/latency numbers from this
  project's own development environment. See its own README.
- **`benchmark/`** — Go `Benchmark*` functions (alongside the code they
  measure, Go's own convention) plus `test_ml_service_benchmarks.py`
  here for ml-service's CPU-bound hot paths. See its own README.

gateway-go's own unit tests live under `gateway-go/internal/*/`
(idiomatic Go: `_test.go` files alongside the code, not a parallel
directory tree) — `go test ./gateway-go/...` from the repo root, or
`make gateway-test`.
