"""End-to-end tests of RAGService.Query against a live in-process gRPC
server — real retrieval (sentence-transformers + FaissVectorStore + BM25,
same path as SearchService), real prompt construction, real citation
extraction. The LLM call itself uses FakeLLMClient (ML_SERVICE_LLM_PROVIDER
defaults to "fake" — see app/rag/llm_client.py's module docstring for why:
no API key is available in this environment, and the fake client still
synthesizes `[N]`-cited text so the pipeline mechanics around it are
genuinely exercised, not skipped).
"""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc
import pytest
from app.config import Settings
from app.server import build_server
from common.v1 import common_pb2
from embeddings.v1 import embeddings_pb2, embeddings_pb2_grpc
from rag.v1 import rag_pb2, rag_pb2_grpc


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


async def _query(server_port: int, **kwargs) -> list[rag_pb2.QueryResponseChunk]:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = rag_pb2_grpc.RAGServiceStub(channel)
        return [chunk async for chunk in stub.Query(rag_pb2.QueryRequest(**kwargs))]


async def _summarize(server_port: int, **kwargs) -> rag_pb2.SummarizeResponse:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = rag_pb2_grpc.RAGServiceStub(channel)
        return await stub.Summarize(rag_pb2.SummarizeRequest(**kwargs))


async def test_query_streams_tokens_then_a_final_chunk_with_citations(server_port: int):
    await _embed(
        server_port,
        "doc-tesla",
        "Tesla automotive revenue grew significantly this quarter.",
    )

    chunks = await _query(server_port, question="How did Tesla's revenue perform?")

    token_chunks = [c for c in chunks if c.HasField("token")]
    final_chunks = [c for c in chunks if c.HasField("final")]
    assert len(token_chunks) >= 1
    assert len(final_chunks) == 1

    final = final_chunks[0].final
    assert len(final.citations) >= 1
    assert final.citations[0].document_id == "doc-tesla"
    assert final.citations[0].quote != ""
    assert final.usage.completion_tokens > 0
    assert (
        final.usage.total_tokens
        == final.usage.prompt_tokens + final.usage.completion_tokens
    )
    assert final.latency_ms >= 0

    answer = "".join(c.token for c in token_chunks)
    assert "[1]" in answer


async def test_query_against_empty_store_returns_no_citations(server_port: int):
    chunks = await _query(server_port, question="Anything at all?")

    final_chunks = [c for c in chunks if c.HasField("final")]
    assert len(final_chunks) == 1
    assert final_chunks[0].final.citations == []


async def test_query_respects_metadata_filter(server_port: int):
    await _embed(server_port, "doc-aapl", "Revenue grew this quarter.", ticker="AAPL")
    await _embed(server_port, "doc-tsla", "Revenue grew this quarter.", ticker="TSLA")

    chunks = await _query(
        server_port,
        question="How did revenue grow?",
        filter=common_pb2.MetadataFilter(tickers=["AAPL"]),
    )

    final = next(c for c in chunks if c.HasField("final")).final
    assert all(c.document_id == "doc-aapl" for c in final.citations)


async def test_summarize_returns_a_summary_citing_the_documents_own_chunks(
    server_port: int,
):
    await _embed(
        server_port,
        "doc-tesla",
        "Tesla automotive revenue grew significantly this quarter.",
    )
    await _embed(server_port, "doc-other", "An unrelated document about gardening.")

    response = await _summarize(
        server_port, document_id="doc-tesla", type=rag_pb2.SUMMARY_TYPE_EXECUTIVE
    )

    assert response.summary != ""
    assert len(response.citations) >= 1
    assert all(c.document_id == "doc-tesla" for c in response.citations)
    assert response.usage.completion_tokens > 0
    assert response.latency_ms >= 0


async def test_summarize_unknown_document_returns_not_found(server_port: int):
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = rag_pb2_grpc.RAGServiceStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as exc_info:
            await stub.Summarize(rag_pb2.SummarizeRequest(document_id="does-not-exist"))

    assert exc_info.value.code() == grpc.StatusCode.NOT_FOUND


async def test_summarize_defaults_to_executive_when_type_unspecified(server_port: int):
    await _embed(
        server_port,
        "doc-tesla",
        "Tesla automotive revenue grew significantly this quarter.",
    )

    response = await _summarize(server_port, document_id="doc-tesla")

    assert response.summary != ""
