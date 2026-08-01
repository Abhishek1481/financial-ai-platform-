.PHONY: proto proto-lint proto-breaking

# Regenerate Go + Python gRPC stubs from proto/*.proto. Requires buf:
#   go install github.com/bufbuild/buf/cmd/buf@latest
proto:
	cd proto && buf generate

proto-lint:
	cd proto && buf lint

proto-breaking:
	cd proto && buf breaking --against '.git#branch=main'
