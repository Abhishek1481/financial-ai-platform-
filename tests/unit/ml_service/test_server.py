"""Smoke tests for the ml-service gRPC server skeleton (Phase 3).

Run with the ml-service venv active:
    cd ml-service && .venv/Scripts/python.exe -m pytest ../tests/unit/ml_service -v

These exist to prove the skeleton itself is sound — service registration,
health checks, graceful start/stop, and the UNIMPLEMENTED contract every
unbuilt RPC honors — not to test business logic that doesn't exist yet.
IngestionService.ExtractDocument was this file's original example of an
unbuilt unary RPC until Phase 6 implemented it, then SearchService.Search
took its place until Phase 8 implemented that too, then RAGService.Summarize
until Phase 10 implemented it (see test_ingestion_servicer.py /
test_search_servicer.py / test_rag_servicer.py for their real tests now) —
EvaluationService.EvaluateAnswer is the current unary stand-in, still a stub
pending Phase 12. RAGService.Query was the streaming stand-in until Phase 9
implemented it; EvaluationService.BatchEvaluate (client-streaming, also
Phase 12) is the current streaming stand-in.
"""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc
import pytest
from app.config import Settings
from app.server import build_server
from evaluation.v1 import evaluation_pb2, evaluation_pb2_grpc
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


async def test_unary_rpc_without_a_backing_implementation_returns_unimplemented(
    server_port: int,
) -> None:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = evaluation_pb2_grpc.EvaluationServiceStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as exc_info:
            await stub.EvaluateAnswer(
                evaluation_pb2.EvaluateAnswerRequest(question="q", answer="a")
            )

    assert exc_info.value.code() == grpc.StatusCode.UNIMPLEMENTED


async def test_streaming_rpc_without_a_backing_implementation_returns_unimplemented(
    server_port: int,
) -> None:
    async def one_request() -> AsyncIterator[evaluation_pb2.EvaluateAnswerRequest]:
        yield evaluation_pb2.EvaluateAnswerRequest(question="q", answer="a")

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = evaluation_pb2_grpc.EvaluationServiceStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as exc_info:
            await stub.BatchEvaluate(one_request())

    assert exc_info.value.code() == grpc.StatusCode.UNIMPLEMENTED
