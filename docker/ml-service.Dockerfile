# syntax=docker/dockerfile:1
#
# Build context is the repo root (see docker-compose.yml) — app/_bootstrap.py
# locates proto/gen/python by walking up from its own file path
# (parents[2]), which only resolves correctly if ml-service/ and
# proto/gen/python keep their real repo-relative layout inside the image,
# same reasoning as gateway-go.Dockerfile's proto/gen/go handling.

# ---- proto codegen ----
# proto/gen/python is gitignored (generated, never committed — see
# /proto/README.md).
FROM python:3.14-slim AS proto-gen
WORKDIR /workspace
RUN pip install --no-cache-dir grpcio-tools
COPY proto/ proto/
RUN mkdir -p proto/gen/python
RUN python -m grpc_tools.protoc \
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

# ---- dependencies ----
# Installs ml-service into a venv purely to resolve pyproject.toml's
# [project.dependencies] — the "app" package pip registers here is not
# what actually runs (see runtime stage): a loose copy of app/ on disk,
# not the pip-installed one, is what app/_bootstrap.py's repo-relative
# path walk needs to line up with proto/gen/python's real location.
FROM python:3.14-slim AS build
WORKDIR /workspace/ml-service
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:${PATH}"
COPY ml-service/pyproject.toml .
COPY ml-service/app app
RUN pip install --no-cache-dir .

# ---- runtime ----
FROM python:3.14-slim AS runtime
RUN useradd --create-home --uid 1000 mlservice
COPY --from=build /opt/venv /opt/venv
ENV PATH="/opt/venv/bin:${PATH}"
WORKDIR /workspace
COPY --from=proto-gen /workspace/proto/gen/python proto/gen/python
COPY ml-service/app ml-service/app
RUN chown -R mlservice:mlservice /workspace
USER mlservice
WORKDIR /workspace/ml-service
EXPOSE 50051 9091
CMD ["python", "-m", "app.server"]
