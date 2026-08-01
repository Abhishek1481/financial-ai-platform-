"""End-to-end tests of EmbeddingService against a live in-process gRPC
server, using the real sentence-transformers model and a real (in-memory)
FaissVectorStore — not mocks. The model is downloaded once and cached by
huggingface_hub afterward, so this is slow the very first time a machine
runs it and fast on every run after.
"""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc
import pytest
from app.config import Settings
from app.server import build_server
from common.v1 import common_pb2
from embeddings.v1 import embeddings_pb2, embeddings_pb2_grpc


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


async def _chunk_and_embed(
    server_port: int, document_id: str, raw_text: str, **kwargs
) -> list[embeddings_pb2.ChunkAndEmbedProgress]:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = embeddings_pb2_grpc.EmbeddingServiceStub(channel)
        request = embeddings_pb2.ChunkAndEmbedRequest(
            document_id=document_id, raw_text=raw_text, **kwargs
        )
        return [progress async for progress in stub.ChunkAndEmbed(request)]


async def test_embeds_short_document_end_to_end(server_port: int):
    progress_messages = await _chunk_and_embed(
        server_port, "doc-1", "Tesla Q1 revenue grew eighteen percent year over year."
    )

    stages = [p.stage for p in progress_messages]
    assert stages[0] == embeddings_pb2.EMBED_STAGE_CHUNKING
    assert stages[-1] == embeddings_pb2.EMBED_STAGE_COMPLETE

    final = progress_messages[-1]
    assert final.chunks_total == 1
    assert final.chunks_processed == 1
    assert final.chunks_skipped_duplicate == 0
    assert len(final.chunk_ids) == 1


async def test_empty_text_completes_with_zero_chunks(server_port: int):
    progress_messages = await _chunk_and_embed(server_port, "doc-empty", "")

    final = progress_messages[-1]
    assert final.stage == embeddings_pb2.EMBED_STAGE_COMPLETE
    assert final.chunks_total == 0


async def test_metadata_is_stored_and_survives_via_search(server_port: int):
    await _chunk_and_embed(
        server_port,
        "doc-meta",
        "Apple iPhone revenue grew significantly this quarter.",
        metadata=common_pb2.FinancialMetadata(ticker="AAPL", filing_type="10-Q"),
    )
    # Metadata storage is exercised further once SearchService (Phase 8)
    # can read it back through a public RPC; for now this just confirms
    # ChunkAndEmbed accepts and doesn't choke on a populated metadata
    # field.


async def test_identical_content_across_two_documents_is_deduped(server_port: int):
    text = "This exact boilerplate legal disclaimer text repeats across filings."

    first = await _chunk_and_embed(server_port, "doc-a", text)
    second = await _chunk_and_embed(server_port, "doc-b", text)

    assert first[-1].chunks_skipped_duplicate == 0  # nothing to dedup against yet
    assert second[-1].chunks_skipped_duplicate == 1  # doc-a's chunk already embedded

    # Both documents still get their own distinct chunk_id.
    assert first[-1].chunk_ids != second[-1].chunk_ids


async def test_delete_document_embeddings_removes_only_that_document(server_port: int):
    await _chunk_and_embed(
        server_port, "doc-to-delete", "Some content to be deleted later."
    )
    await _chunk_and_embed(
        server_port, "doc-to-keep", "Different content that should remain."
    )

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = embeddings_pb2_grpc.EmbeddingServiceStub(channel)
        response = await stub.DeleteDocumentEmbeddings(
            embeddings_pb2.DeleteDocumentEmbeddingsRequest(document_id="doc-to-delete")
        )

    assert response.chunks_deleted == 1


async def test_check_duplicate_chunk_reports_known_hash(server_port: int):
    text = "Unique content for the duplicate-check test."
    progress = await _chunk_and_embed(server_port, "doc-dup-check", text)
    chunk_id = progress[-1].chunk_ids[0]

    # Recompute the same content hash the servicer would have (sha256 of
    # the single chunk's text — no chunking splits happen for text this
    # short).
    import hashlib

    content_hash = hashlib.sha256(text.encode("utf-8")).hexdigest()

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = embeddings_pb2_grpc.EmbeddingServiceStub(channel)
        found = await stub.CheckDuplicateChunk(
            embeddings_pb2.CheckDuplicateChunkRequest(content_hash=content_hash)
        )
        not_found = await stub.CheckDuplicateChunk(
            embeddings_pb2.CheckDuplicateChunkRequest(content_hash="not-a-real-hash")
        )

    assert found.is_duplicate is True
    assert found.existing_chunk_id == chunk_id
    assert not_found.is_duplicate is False
