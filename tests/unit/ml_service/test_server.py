"""Smoke tests for the ml-service gRPC server skeleton (Phase 3).

Run with the ml-service venv active:
    cd ml-service && .venv/Scripts/python.exe -m pytest ../tests/unit/ml_service -v

These exist to prove the skeleton itself is sound — service registration
and health checks — not to test business logic, which each service's own
test_*_servicer.py file covers. This file used to also assert the
UNIMPLEMENTED contract an unbuilt RPC honors (IngestionService.ExtractDocument,
then SearchService.Search, then RAGService.Summarize/Query, then finally
EvaluationService.EvaluateAnswer/BatchEvaluate each served as the "still a
stub" example in turn) — as of Phase 12, every RPC in every service is
implemented, so that test no longer has a target and was removed rather
than kept pointed at nothing.
"""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc
import pytest
from app.config import Settings
from app.server import build_server
from grpc_health.v1 import health_pb2, health_pb2_grpc
from ingestion.v1 import ingestion_pb2


@pytest.fixture
async def server_port() -> AsyncIterator[int]:
    settings = Settings(grpc_port=0, reflection_enabled=True)
    server, port = await build_server(settings)
    await server.start()
    try:
        yield port
    finally:
        await server.stop(grace=0)


async def test_overall_health_check_reports_serving(server_port: int) -> None:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = health_pb2_grpc.HealthStub(channel)
        response = await stub.Check(health_pb2.HealthCheckRequest(service=""))

    assert response.status == health_pb2.HealthCheckResponse.SERVING


async def test_per_service_health_check_reports_serving(server_port: int) -> None:
    full_name = ingestion_pb2.DESCRIPTOR.services_by_name["IngestionService"].full_name

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = health_pb2_grpc.HealthStub(channel)
        response = await stub.Check(health_pb2.HealthCheckRequest(service=full_name))

    assert response.status == health_pb2.HealthCheckResponse.SERVING
