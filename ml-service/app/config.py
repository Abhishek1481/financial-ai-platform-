"""Typed application configuration, loaded from environment variables.

Every service in this platform reads config the same way: a single
pydantic-settings model, env-prefixed to avoid collisions when multiple
services' env vars end up in the same shell/container, validated at process
start rather than failing on first use deep in a request handler.
"""

from __future__ import annotations

from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="ML_SERVICE_",
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
    )

    environment: str = "development"
    grpc_host: str = "0.0.0.0"
    grpc_port: int = 50051
    log_level: str = "INFO"

    # Reflection lets `grpcurl`/`grpcui` introspect the running service
    # without the .proto files on hand — invaluable in dev, disabled in
    # prod so the wire schema isn't handed out to anyone who can reach the
    # port.
    reflection_enabled: bool = True

    # EmbeddingModel loads lazily (see app/embeddings/model.py) — the name
    # alone is cheap to hold in config without pulling in model weights at
    # startup. embedding_dimension is a separate, static setting rather
    # than something read off the loaded model, specifically so the vector
    # store can be constructed (it needs a fixed dimension up front)
    # without forcing an eager model load — 384 is
    # all-MiniLM-L6-v2's dimension; change both together if the model
    # changes.
    embedding_model_name: str = "sentence-transformers/all-MiniLM-L6-v2"
    embedding_dimension: int = 384

    # Where FaissVectorStore persists its index + metadata sidecar so
    # embeddings survive a restart — the local-dev stand-in for
    # OpenSearch (Phase 16). Empty string keeps everything in-memory only
    # (used by tests).
    vector_store_dir: str = "./data/vector-store"

    # LLMClient (app/rag/llm_client.py) is provider-switchable: "openai" ->
    # ChatOpenAI, "anthropic" -> ChatAnthropic, "fake" -> FakeLLMClient
    # (deterministic, no network call — used when no API key is available,
    # e.g. this dev environment; see app/rag/llm_client.py's module
    # docstring). llm_api_key is read from this service's own env var
    # rather than deferring to OPENAI_API_KEY/ANTHROPIC_API_KEY directly,
    # so ml-service's config surface is self-contained and doesn't depend
    # on which SDK happens to read which ambient env var.
    llm_provider: str = "fake"
    llm_model_name: str = "gpt-4o-mini"
    llm_api_key: str = ""
    llm_temperature: float = 0.1
    # Ceiling on generated tokens per answer — bounds latency/cost and
    # gives the fake client a concrete number to honor in tests.
    llm_max_tokens: int = 1024


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()
