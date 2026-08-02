# Sequence diagrams

Companion to [`ARCHITECTURE.md`](ARCHITECTURE.md)'s static service-boundary
diagram — these show the three flows that actually exercise every layer of
the system end-to-end, each verified against real running processes (not
just diagrammed from reading the code — see the relevant phase's commit
message in `git log` for what was actually run).

## 1. Document upload and ingestion

Async by design: the HTTP request returns as soon as the file is durably
stored and a job is queued, not once processing finishes — a client polls
`GET /api/v1/documents/{id}` for status (see `gateway-go/README.md`'s
"A bounded worker pool is the actual 'concurrent ingestion' story").

```mermaid
sequenceDiagram
    actor Client
    participant Gateway as gateway-go
    participant Store as LocalObjectStore
    participant Worker as worker.Pool
    participant ML as ml-service

    Client->>Gateway: POST /api/v1/documents (multipart file)
    Gateway->>Gateway: hash file content (SHA-256)
    alt content hash already seen
        Gateway-->>Client: 200 {document_id, reused: true}
    else new content
        Gateway->>Store: write file, get file:// URI
        Gateway->>Gateway: create Document + Job (status: pending)
        Gateway->>Worker: Submit(job)
        Gateway-->>Client: 202 {document_id, job_id, status: pending}

        Worker->>ML: ExtractDocument(uri, doc_type)  [gRPC]
        ML->>ML: parse PDF/DOCX/HTML/TXT, infer SEC metadata
        ML-->>Worker: text, tables, metadata
        Worker->>Worker: Job.status = extracting -> embedding

        Worker->>ML: ChunkAndEmbed(raw_text, metadata)  [gRPC, server-streaming]
        ML->>ML: chunk, dedup by content hash, embed (Sentence-Transformers)
        ML->>ML: upsert into FaissVectorStore + KeywordIndex
        ML-->>Worker: ChunkAndEmbedProgress (final: chunk_count, ...)
        Worker->>Worker: Job.status = completed
    end

    Client->>Gateway: GET /api/v1/documents/{id}  (polling)
    Gateway-->>Client: 200 {job: {status, chunk_count, ...}}
```

## 2. RAG query — streamed, cited, and remembered

The reason the gateway-go <-> ml-service boundary supports streaming at
all (see `proto/README.md`'s "Why these RPC shapes"): a token appears on
the client the moment ml-service generates it, not after the whole answer
is done.

```mermaid
sequenceDiagram
    actor Client
    participant Gateway as gateway-go
    participant Conv as conversation.Store
    participant ML as ml-service (RAGService)
    participant LLM as LLMClient

    Client->>Gateway: POST /api/v1/rag/query {question, session_id?}
    Gateway->>Gateway: mint session_id if omitted
    Gateway->>Conv: History(session_id)
    Conv-->>Gateway: prior turns (empty for a new session)

    Gateway->>ML: Query(question, history, filter)  [gRPC, server-streaming]
    ML->>ML: hybrid retrieval (vector + BM25, fused)
    ML->>ML: build numbered-context prompt
    ML->>LLM: stream(messages)

    loop each generated token
        LLM-->>ML: token
        ML-->>Gateway: QueryResponseChunk{token}
        Gateway-->>Client: SSE "token" event
    end

    LLM-->>ML: usage (prompt/completion/total tokens)
    ML->>ML: extract [N] citation markers, resolve to chunk IDs
    ML-->>Gateway: QueryResponseChunk{final: citations, usage, latency_ms}
    Gateway->>Conv: AppendTurns(session_id, question, answer)
    Gateway-->>Client: SSE "final" event {session_id, citations, usage, latency_ms}
```

## 3. Auth: register, login, and every authenticated request after

Stateless JWTs — no session lookup on the hot path, at the cost of no
instant revocation before a token's (short) TTL expires (see
`gateway-go/README.md`'s design decisions for why that tradeoff was made
deliberately).

```mermaid
sequenceDiagram
    actor Client
    participant Gateway as gateway-go
    participant Auth as auth.Service
    participant Repo as UserRepository

    Client->>Gateway: POST /api/v1/auth/register {email, password}
    Gateway->>Auth: Register(email, password)
    Auth->>Auth: validate email, enforce min password length
    Auth->>Auth: bcrypt-hash password
    Auth->>Repo: Create(user, role=user)
    Repo-->>Auth: OK
    Auth-->>Gateway: User
    Gateway-->>Client: 201 {id, email, role}

    Client->>Gateway: POST /api/v1/auth/login {email, password}
    Gateway->>Auth: Login(email, password)
    Auth->>Repo: FindByEmail(email)
    Auth->>Auth: verify bcrypt hash
    Auth->>Auth: sign JWT (HS256, GATEWAY_JWT_TTL)
    Auth-->>Gateway: access_token
    Gateway-->>Client: 200 {access_token, token_type, expires_in}

    Note over Client,Gateway: every subsequent request
    Client->>Gateway: GET /api/v1/... (Authorization: Bearer <token>)
    Gateway->>Gateway: verify JWT signature + expiry (no DB round-trip)
    alt valid
        Gateway->>Gateway: handle request as the token's user
    else invalid/expired
        Gateway-->>Client: 401
    end
```
