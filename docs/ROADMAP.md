# Build Roadmap

This project is built incrementally, one phase per step, each with an
explanation of the design before the code lands. Status is tracked here so
it's clear what's real vs. planned at any point in time.

- [x] **Phase 1 — Repo scaffolding.** Folder structure, root README,
      architecture doc, this roadmap, git init.
- [x] **Phase 2 — Protobuf contracts.** The gRPC interface between
      `gateway-go` and `ml-service`, defined before either service is built
      against it.
- [x] **Phase 3 — `ml-service` skeleton.** gRPC server, config, health
      checks, project layout for `rag/`, `embeddings/`, `evaluation/`.
- [x] **Phase 4 — `gateway-go` skeleton.** Gin router, config, structured
      logging, graceful shutdown, `/healthz`, `/readyz`, `/metrics`.
- [x] **Phase 5 — Auth.** JWT issuance/validation, RBAC (admin/user)
      middleware in Go.
- [x] **Phase 6 — Document ingestion.** Concurrent upload handling in Go,
      job handoff to worker pool, text/table/metadata extraction in Python
      (PDF, DOCX, HTML, TXT, SEC filings).
- [x] **Phase 7 — Embedding pipeline.** Chunking, Sentence-Transformers
      embeddings, vector store (FAISS/OpenSearch), dedup, incremental
      updates.
- [x] **Phase 8 — Semantic + hybrid search.** Cosine similarity, metadata
      filtering, top-k retrieval, hybrid (BM25 + vector).
- [x] **Phase 9 — RAG + citations.** Retrieval, prompt construction, LLM
      call, citation extraction, streaming responses.
- [x] **Phase 10 — Financial summarization.** Executive/risk/revenue/
      sentiment summaries.
- [x] **Phase 11 — Conversational QA.** Context-aware follow-up questions,
      conversation memory.
- [x] **Phase 12 — Model evaluation.** Precision/recall, faithfulness,
      context recall, hallucination detection, latency/token tracking.
- [ ] **Phase 13 — Caching, rate limiting, scheduler.** Redis cache, rate
      limiter, job scheduler, worker pool in Go.
- [ ] **Phase 14 — Monitoring.** Prometheus metrics across all services,
      Grafana dashboards, OpenTelemetry tracing/logging.
- [ ] **Phase 15 — Admin dashboard.** Documents, users, jobs, embedding
      status, metrics, logs.
- [ ] **Phase 16 — Docker Compose.** Full local stack.
- [ ] **Phase 17 — Kubernetes manifests.** Base + environment overlays.
- [ ] **Phase 18 — Terraform.** AWS (ECS/EKS, S3, IAM, Secrets Manager,
      CloudWatch).
- [ ] **Phase 19 — CI/CD.** GitHub Actions: test, lint, security scan,
      build, push, deploy.
- [ ] **Phase 20 — Tests.** Unit, integration, API, load, benchmark.
- [ ] **Phase 21 — Docs polish.** Sequence diagrams, API/Swagger docs,
      deployment guide, interview explanation + design tradeoffs writeup.

Phases can be reordered on request — e.g. pulling Docker Compose earlier so
the stack is runnable sooner, at the cost of building against a moving
service surface.
