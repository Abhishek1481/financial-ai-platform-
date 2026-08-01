"""End-to-end tests of SearchService against a live in-process gRPC server
— real sentence-transformers model, real FaissVectorStore, real BM25
index, not mocks. Documents are embedded via EmbeddingService first (the
same path a real ingestion pipeline uses), then queried via SearchService.
"""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc
import pytest
from app.config import Settings
from app.server import build_server
from common.v1 import common_pb2
from embeddings.v1 import embeddings_pb2, embeddings_pb2_grpc
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


async def _embed(
    server_port: int, document_id: str, text: str, **metadata_kwargs
) -> None:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = embeddings_pb2_grpc.EmbeddingServiceStub(channel)
        request = embeddings_pb2.ChunkAndEmbedRequest(
            document_id=document_id,
            raw_text=text,
            metadata=common_pb2.FinancialMetadata(**metadata_kwargs)
            if metadata_kwargs
            else None,
        )
        async for _ in stub.ChunkAndEmbed(request):
            pass


async def _search(server_port: int, **kwargs) -> search_pb2.SearchResponse:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = search_pb2_grpc.SearchServiceStub(channel)
        return await stub.Search(search_pb2.SearchRequest(**kwargs))


async def test_semantic_search_finds_relevant_document(server_port: int):
    await _embed(
        server_port,
        "doc-tesla",
        "Tesla automotive revenue grew significantly this quarter.",
    )
    await _embed(
        server_port, "doc-weather", "The weather in Paris was sunny and warm yesterday."
    )

    response = await _search(
        server_port,
        query="How did Tesla's car sales revenue perform?",
        mode=search_pb2.SEARCH_MODE_SEMANTIC,
    )

    assert len(response.results) >= 1
    assert response.results[0].chunk.document_id == "doc-tesla"
    assert response.search_latency_ms >= 0


async def test_keyword_search_finds_exact_term_match(server_port: int):
    await _embed(
        server_port, "doc-a", "Gigafactory production efficiency improved this quarter."
    )
    await _embed(
        server_port, "doc-b", "Unrelated content about something else entirely."
    )

    response = await _search(
        server_port, query="Gigafactory", mode=search_pb2.SEARCH_MODE_KEYWORD
    )

    assert len(response.results) == 1
    assert response.results[0].chunk.document_id == "doc-a"


async def test_hybrid_search_combines_both_signals(server_port: int):
    await _embed(
        server_port,
        "doc-hybrid",
        "Apple iPhone revenue grew due to strong holiday demand.",
    )
    await _embed(
        server_port, "doc-other", "A completely different topic about gardening tips."
    )

    response = await _search(
        server_port, query="iPhone revenue growth", mode=search_pb2.SEARCH_MODE_HYBRID
    )

    assert len(response.results) >= 1
    assert response.results[0].chunk.document_id == "doc-hybrid"


async def test_metadata_filter_excludes_non_matching_ticker(server_port: int):
    await _embed(server_port, "doc-aapl", "Revenue grew this quarter.", ticker="AAPL")
    await _embed(server_port, "doc-tsla", "Revenue grew this quarter.", ticker="TSLA")

    response = await _search(
        server_port,
        query="revenue grew",
        mode=search_pb2.SEARCH_MODE_KEYWORD,
        filter=common_pb2.MetadataFilter(tickers=["AAPL"]),
    )

    assert all(r.chunk.metadata.ticker == "AAPL" for r in response.results)
    assert any(r.chunk.document_id == "doc-aapl" for r in response.results)
    assert not any(r.chunk.document_id == "doc-tsla" for r in response.results)


async def test_top_k_limits_result_count(server_port: int):
    for i in range(5):
        await _embed(
            server_port,
            f"doc-{i}",
            f"Revenue report number {i} about quarterly earnings.",
        )

    response = await _search(
        server_port,
        query="revenue report",
        mode=search_pb2.SEARCH_MODE_KEYWORD,
        top_k=2,
    )

    assert len(response.results) <= 2


async def test_search_on_empty_store_returns_no_results(server_port: int):
    response = await _search(
        server_port, query="anything", mode=search_pb2.SEARCH_MODE_HYBRID
    )
    assert response.results == []
