"""End-to-end test of IngestionService.ExtractDocument against a live
in-process gRPC server — not just the extractor unit tests, to prove the
storage-read -> extractor-dispatch -> proto-response wiring actually works.
"""

from __future__ import annotations

from collections.abc import AsyncIterator
from pathlib import Path

import grpc
import pytest
from app.config import Settings
from app.server import build_server
from common.v1 import common_pb2
from ingestion.v1 import ingestion_pb2, ingestion_pb2_grpc


@pytest.fixture
async def server_port() -> AsyncIterator[int]:
    settings = Settings(grpc_port=0, reflection_enabled=True)
    server, port = await build_server(settings)
    await server.start()
    try:
        yield port
    finally:
        await server.stop(grace=0)


async def _extract(
    server_port: int, uri: str, doc_type: int
) -> ingestion_pb2.ExtractDocumentResponse:
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = ingestion_pb2_grpc.IngestionServiceStub(channel)
        return await stub.ExtractDocument(
            ingestion_pb2.ExtractDocumentRequest(
                document_id="doc-1", s3_uri=uri, doc_type=doc_type
            )
        )


async def test_extracts_txt_file_end_to_end(server_port: int, tmp_path: Path):
    target = tmp_path / "doc.txt"
    target.write_text("Q1 revenue grew 12%.", encoding="utf-8")

    response = await _extract(
        server_port, target.as_uri(), common_pb2.DOCUMENT_TYPE_TXT
    )

    assert response.raw_text == "Q1 revenue grew 12%."
    assert response.page_count == 1


async def test_extracts_sec_filing_and_infers_filing_type(
    server_port: int, tmp_path: Path
):
    target = tmp_path / "filing.html"
    target.write_text(
        "<html><body><h1>FORM 10-K</h1><p>Annual report content.</p></body></html>",
        encoding="utf-8",
    )

    response = await _extract(
        server_port, target.as_uri(), common_pb2.DOCUMENT_TYPE_SEC_FILING
    )

    assert "Annual report content." in response.raw_text
    assert response.inferred_metadata.filing_type == "10-K"


async def test_missing_object_returns_not_found(server_port: int, tmp_path: Path):
    missing = tmp_path / "does-not-exist.txt"

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = ingestion_pb2_grpc.IngestionServiceStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as exc_info:
            await stub.ExtractDocument(
                ingestion_pb2.ExtractDocumentRequest(
                    document_id="doc-1",
                    s3_uri=missing.as_uri(),
                    doc_type=common_pb2.DOCUMENT_TYPE_TXT,
                )
            )
    assert exc_info.value.code() == grpc.StatusCode.NOT_FOUND


async def test_unsupported_storage_scheme_returns_unimplemented(server_port: int):
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = ingestion_pb2_grpc.IngestionServiceStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as exc_info:
            await stub.ExtractDocument(
                ingestion_pb2.ExtractDocumentRequest(
                    document_id="doc-1",
                    s3_uri="s3://bucket/key.txt",
                    doc_type=common_pb2.DOCUMENT_TYPE_TXT,
                )
            )
    assert exc_info.value.code() == grpc.StatusCode.UNIMPLEMENTED


async def test_malformed_document_returns_invalid_argument(
    server_port: int, tmp_path: Path
):
    target = tmp_path / "broken.docx"
    target.write_bytes(b"not actually a docx file")

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = ingestion_pb2_grpc.IngestionServiceStub(channel)
        with pytest.raises(grpc.aio.AioRpcError) as exc_info:
            await stub.ExtractDocument(
                ingestion_pb2.ExtractDocumentRequest(
                    document_id="doc-1",
                    s3_uri=target.as_uri(),
                    doc_type=common_pb2.DOCUMENT_TYPE_DOCX,
                )
            )
    assert exc_info.value.code() == grpc.StatusCode.INVALID_ARGUMENT
