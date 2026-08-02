# integration

`test_full_stack.py` — starts a real `ml-service` (`python -m app.server`)
and a real `gateway-go` (`go run ./cmd/gateway`), waits for both to be
ready, and drives the pair over real HTTP/gRPC exactly the way a live
deployment would: register/login, upload a document and poll until
embedding completes, semantic search, a streaming RAG query (parsing the
real SSE response), admin RBAC, and the evaluate endpoint discriminating
a grounded answer from a fabricated one. Automates what was, until this
phase, only ever done by hand via `curl` once per phase — the results
described in each phase's commit message but never captured as a
repeatable test.

Runs as its own `integration` job in `.github/workflows/ci.yml` (after
the faster unit-level `gateway-go`/`ml-service` jobs pass) — see the
module docstring for why it's a separate job rather than folded into
either of those. Run manually:

```bash
cd ml-service && .venv/Scripts/python.exe -m pytest ../tests/integration -v
```
