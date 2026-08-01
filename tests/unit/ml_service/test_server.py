"""Smoke tests for the ml-service gRPC server skeleton (Phase 3).

Run with the ml-service venv active:
    cd ml-service && .venv/Scripts/python.exe -m pytest ../tests/unit/ml_service -v

These exist to prove the skeleton itself is sound — service registration,
health checks, graceful start/stop, and the UNIMPLEMENTED contract every
unbuilt RPC honors — not to test business logic that doesn't exist yet.
"""

from __future__ import annotations

from typing import AsyncIterator

import grpc
import pytest
from grpc_health.v1 import health_pb2, health_pb2_grpc

from app.config import Settings
from app.server import build_server
from ingestion.v1 import ingestion_pb2, ingestion_pb2_grpc
from rag.v1 import rag_pb2, rag_pb2_grpc


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


async def test_unary_rpc_without_a_backing_implementation_returns_unimplemented(
    server_port: int,
) -> None:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = ingestion_pb2_grpc.IngestionServiceStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as exc_info:
            await stub.ExtractDocument(ingestion_pb2.ExtractDocumentRequest(document_id="doc-1"))

    assert exc_info.value.code() == grpc.StatusCode.UNIMPLEMENTED


async def test_streaming_rpc_without_a_backing_implementation_returns_unimplemented(
    server_port: int,
) -> None:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = rag_pb2_grpc.RAGServiceStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as exc_info:
            async for _ in stub.Query(rag_pb2.QueryRequest(question="What are Tesla's Q1 risks?")):
                pass

    assert exc_info.value.code() == grpc.StatusCode.UNIMPLEMENTED
