.PHONY: proto proto-python proto-lint proto-breaking

# Regenerates gRPC stubs from proto/*.proto.
#
# Python codegen uses grpcio-tools directly (bundles its own protoc, no
# network access required) rather than buf's remote plugins — this is what
# actually runs in dev and CI. Requires the ml-service venv active:
#   cd ml-service && python -m venv .venv && .venv/Scripts/pip install -e ".[dev]"
#
# Go codegen (protoc-gen-go / protoc-gen-go-grpc) is added in Phase 4 once
# gateway-go's module exists.
proto: proto-python

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

# buf is used for contract safety (lint + breaking-change detection), not
# for day-to-day codegen. Requires: go install github.com/bufbuild/buf/cmd/buf@latest
proto-lint:
	cd proto && buf lint

proto-breaking:
	cd proto && buf breaking --against '.git#branch=main'
