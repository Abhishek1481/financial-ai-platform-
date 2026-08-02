"""Tests for app/observability.py's gRPC interceptor — real requests
against a live in-process server, then real Prometheus exposition output,
not just calling the interceptor's internals directly.
"""

from __future__ import annotations

import re
from collections.abc import AsyncIterator

import grpc
import pytest
from app.config import Settings
from app.observability import REQUEST_ID_METADATA_KEY
from app.server import build_server
from prometheus_client import generate_latest
from search.v1 import search_pb2, search_pb2_grpc


@pytest.fixture
async def server_port(tmp_path) -> AsyncIterator[int]:
    settings = Settings(
        grpc_port=0,
        reflection_enabled=True,
        vector_store_dir=str(tmp_path / "vector-store"),
    )
    server, port = await build_server(settings)
    await server.start()
    try:
        yield port
    finally:
        await server.stop(grace=0)


def _count_for(method_path: str, status: str) -> float:
    text = generate_latest().decode()
    pattern = rf'ml_service_grpc_requests_total\{{method="{re.escape(method_path)}",status="{status}"\}} ([\d.]+)'
    match = re.search(pattern, text)
    return float(match.group(1)) if match else 0.0


async def test_a_unary_rpc_increments_the_requests_counter(server_port: int):
    method = "/financial.ai.platform.search.v1.SearchService/Search"
    before = _count_for(method, "OK")

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = search_pb2_grpc.SearchServiceStub(channel)
        await stub.Search(search_pb2.SearchRequest(query="anything"))

    assert _count_for(method, "OK") == before + 1


async def test_request_id_metadata_is_accepted_without_error(server_port: int):
    # The interceptor extracts x-request-id if present (see
    # app/tracing.py) — this just proves supplying one doesn't break the
    # call; log-correlation itself is observed by reading logs, not
    # asserted here.
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = search_pb2_grpc.SearchServiceStub(channel)
        response = await stub.Search(
            search_pb2.SearchRequest(query="anything"),
            metadata=((REQUEST_ID_METADATA_KEY, "test-request-id-123"),),
        )

    assert response.search_latency_ms >= 0
