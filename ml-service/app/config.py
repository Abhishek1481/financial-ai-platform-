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


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()
