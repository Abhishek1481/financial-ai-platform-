# Financial Intelligence AI Platform



A production-shaped Retrieval-Augmented Generation (RAG) platform for querying SEC
filings, earnings call transcripts, financial news, and company reports in natural
language — e.g. *"What risks are mentioned in Apple's latest 10-K?"* or *"What
changed between Microsoft's last two quarterly reports?"*

This is a systems-design portfolio project. The goal is not "call an LLM with a
prompt" — it's demonstrating how a real ML platform team splits responsibility
between a high-throughput API/orchestration layer and a Python ML layer, wires
them together with a typed internal contract, and operates the result (auth,
rate limiting, caching, observability, evaluation, CI/CD, and cloud deployment).

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the full system design and
the reasoning behind every major decision, [`docs/ROADMAP.md`](docs/ROADMAP.md)
for build status, [`docs/SEQUENCE_DIAGRAMS.md`](docs/SEQUENCE_DIAGRAMS.md) for
the three core flows end-to-end, [`docs/openapi.yaml`](docs/openapi.yaml) for
the full REST API reference, [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) for how
to actually run it (local, Docker Compose, or Kubernetes on AWS), and
[`docs/DESIGN_TRADEOFFS.md`](docs/DESIGN_TRADEOFFS.md) for the condensed,
interview-ready version of every major decision and its honest tradeoff.

## Why two languages

| Concern | Owner | Why |
|---|---|---|
| Auth, API gateway, rate limiting, streaming, concurrent ingestion, job scheduling, worker pool, caching | **Go** (`gateway-go`, `scheduler`, `worker`) | I/O-bound, high-concurrency edge workloads where Go's goroutines and lack of a GIL outperform Python under load. This is the part of the system that must never fall over under traffic spikes. |
| Embeddings, chunking, vector search, RAG orchestration, summarization, evaluation | **Python** (`ml-service`) | The ML/NLP ecosystem (HuggingFace, Sentence-Transformers, LangChain, FAISS) is Python-native. This service has no public HTTP surface — it is only reachable via gRPC from `gateway-go`. |

Go and Python communicate exclusively over **gRPC** using contracts defined in
[`proto/`](proto/) — never raw HTTP — so the internal API is typed and versioned
like a real service boundary, not a JSON-shaped suggestion.

## Repository layout

```
financial-ai-platform/
├── gateway-go/       # Go: Gin API gateway — auth, routing, rate limiting, streaming
├── scheduler/        # Go: job scheduler for ingestion/embedding pipelines
├── worker/           # Go: concurrent document ingestion workers
├── ml-service/       # Python: gRPC server — embeddings, RAG, summarization, eval
│   ├── rag/
│   ├── embeddings/
│   └── evaluation/
├── proto/            # Shared protobuf contracts (Go ↔ Python)
├── configs/          # Environment configs (dev/staging/prod)
├── docker/           # Dockerfiles + docker-compose
├── k8s/              # Kubernetes manifests (base + overlays)
├── terraform/        # AWS infrastructure as code
├── .github/workflows/# CI/CD pipelines
├── docs/             # Architecture, diagrams, API docs, deployment guide
└── tests/            # Unit, integration, API, load, benchmark tests
```

## Status

Under active, incremental construction — see [`docs/ROADMAP.md`](docs/ROADMAP.md)
for what's built vs. planned. Each phase lands with its own explanation of the
design decisions behind it.

## CI/CD

Five workflows in [`.github/workflows/`](.github/workflows/), each scoped to
what it actually validates:

- **`ci.yml`** — `go build`/`vet`/`gofmt`/`test` for `gateway-go`;
  `ruff`/`mypy`/`pytest` for `ml-service`, including the CI eval-regression
  gate ([`tests/unit/ml_service/test_eval_gate.py`](tests/unit/ml_service/test_eval_gate.py))
  promised back in Phase 12; then a real cross-service `integration` job
  ([`tests/integration/test_full_stack.py`](tests/integration/test_full_stack.py))
  that starts an actual `gateway-go` + `ml-service` pair and drives them
  end-to-end over real HTTP/gRPC.
- **`docker-build.yml`** — builds both Dockerfiles on GitHub's runners, the
  first real verification either one builds at all (no Docker daemon exists
  in this project's own development environment — see
  [`docker/README.md`](docker/README.md)).
- **`terraform-validate.yml`** / **`k8s-validate.yml`** — same story for
  Terraform (`fmt` + `validate`, no AWS credentials needed) and Kubernetes
  manifests (`kubectl kustomize` + schema validation against the real K8s
  API via `kubeconform`, no cluster needed).
- **`security.yml`** — `gosec` (Go) and `pip-audit` (Python dependency CVEs),
  on every push/PR plus a weekly schedule so a newly-disclosed CVE in an
  unchanged dependency still gets caught.

Deliberately **not** wired up: pushing built images to a registry or
deploying anywhere. `docker-build.yml` builds with `push: false` — flipping
that on, adding registry credentials, and pointing `kubectl`/`terraform` at
a real target are the natural next step once there's an actual cluster/AWS
account to deploy to, not something to fake against nothing.

## License

MIT — see [`LICENSE`](LICENSE).
