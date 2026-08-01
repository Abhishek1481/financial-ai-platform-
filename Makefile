.PHONY: proto proto-python proto-go proto-lint proto-breaking gateway-build gateway-test gateway-run gateway-check

# Regenerates gRPC stubs from proto/*.proto.
#
# Both proto-python and proto-go route through grpcio-tools' embedded
# protoc (it isn't just a Python-code generator — protoc is a generic
# compiler that shells out to protoc-gen-<lang> plugins on PATH, and
# grpc_tools bundles a real protoc binary under the hood). That means
# neither target needs a separate system-wide protoc install; proto-go
# only additionally needs the Go plugins on PATH:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# Requires the ml-service venv active either way:
#   cd ml-service && python -m venv .venv && .venv/Scripts/pip install -e ".[dev]"
proto: proto-python proto-go

proto-python:
	python -m grpc_tools.protoc \
		-I proto \
		--python_out=proto/gen/python \
		--grpc_python_out=proto/gen/python \
		--pyi_out=proto/gen/python \
		proto/common/v1/common.proto \
		proto/ingestion/v1/ingestion.proto \
		proto/embeddings/v1/embeddings.proto \
		proto/search/v1/search.proto \
		proto/rag/v1/rag.proto \
		proto/evaluation/v1/evaluation.proto

# Output goes to proto/gen/go, its own workspace module (see /go.work) so
# gateway-go — and scheduler/worker once they exist — can all depend on it
# without duplicating codegen per service.
proto-go:
	python -m grpc_tools.protoc \
		-I proto \
		--go_out=proto/gen/go --go_opt=paths=source_relative \
		--go-grpc_out=proto/gen/go --go-grpc_opt=paths=source_relative \
		proto/common/v1/common.proto \
		proto/ingestion/v1/ingestion.proto \
		proto/embeddings/v1/embeddings.proto \
		proto/search/v1/search.proto \
		proto/rag/v1/rag.proto \
		proto/evaluation/v1/evaluation.proto

# buf is used for contract safety (lint + breaking-change detection), not
# for day-to-day codegen. Requires: go install github.com/bufbuild/buf/cmd/buf@latest
proto-lint:
	cd proto && buf lint

proto-breaking:
	cd proto && buf breaking --against '.git#branch=main'

# gateway-go — module-scoped patterns (./gateway-go/...) so these work from
# the repo root even though it's a Go workspace root, not a module itself.
gateway-build:
	go build ./gateway-go/...

gateway-test:
	go test ./gateway-go/...

gateway-run:
	go run ./gateway-go/cmd/gateway

gateway-check:
	go vet ./gateway-go/...
	test -z "$$(gofmt -l gateway-go)"
