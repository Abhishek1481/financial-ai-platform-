# syntax=docker/dockerfile:1
#
# Build context is the repo root (see docker-compose.yml) — this Dockerfile
# needs proto/ and gateway-go/ both, and go.mod's `replace` directive
# resolves proto/gen/go by its position relative to gateway-go/, so the two
# directories have to keep their real repo-relative layout inside the
# image too.

# ---- proto codegen ----
# proto/gen/go is gitignored (generated, never committed — see
# /proto/README.md) and Makefile's `proto-go` target generates it via
# grpcio-tools' embedded protoc, which means even *Go* stub generation
# needs a Python toolchain, not just the Go one — this stage installs
# both rather than reinventing codegen a second way just to avoid that.
FROM golang:1.26-bookworm AS proto-gen
RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 python3-venv \
    && rm -rf /var/lib/apt/lists/*
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
ENV PATH="/root/go/bin:${PATH}"
WORKDIR /workspace
COPY proto/ proto/
RUN mkdir -p proto/gen/go
RUN python3 -m venv /tmp/protoc-venv \
    && /tmp/protoc-venv/bin/pip install --no-cache-dir grpcio-tools \
    && /tmp/protoc-venv/bin/python -m grpc_tools.protoc \
         -I proto \
         --go_out=proto/gen/go --go_opt=paths=source_relative \
         --go-grpc_out=proto/gen/go --go-grpc_opt=paths=source_relative \
         proto/common/v1/common.proto \
         proto/ingestion/v1/ingestion.proto \
         proto/embeddings/v1/embeddings.proto \
         proto/search/v1/search.proto \
         proto/rag/v1/rag.proto \
         proto/evaluation/v1/evaluation.proto

# ---- build ----
FROM golang:1.26-bookworm AS build
WORKDIR /workspace
COPY proto/gen/go/go.mod proto/gen/go/go.sum proto/gen/go/
COPY gateway-go/go.mod gateway-go/go.sum gateway-go/
RUN cd gateway-go && go mod download
COPY --from=proto-gen /workspace/proto/gen/go proto/gen/go
COPY gateway-go/ gateway-go/
RUN cd gateway-go && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# ---- runtime ----
# distroless: no shell, no package manager — nothing to exploit in the
# image beyond the binary itself. static-debian12 (not the Go-specific
# variant) is enough since CGO_ENABLED=0 above produces a static binary.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/gateway /usr/local/bin/gateway
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/gateway"]
