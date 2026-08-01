"""RAGService — real implementation lands across Phase 9 (RAG + citations +
streaming) and Phase 10 (financial summarization)."""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from rag.v1 import rag_pb2, rag_pb2_grpc


class RAGServicer(rag_pb2_grpc.RAGServiceServicer):
    async def Query(
        self,
        request: rag_pb2.QueryRequest,
        context: grpc.aio.ServicerContext,
    ) -> AsyncIterator[rag_pb2.QueryResponseChunk]:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "RAGService.Query is not implemented yet (Phase 9: RAG + citations + streaming).",
        )
        yield  # pragma: no cover — see EmbeddingServicer.ChunkAndEmbed for why this is here

    async def Summarize(
        self,
        request: rag_pb2.SummarizeRequest,
        context: grpc.aio.ServicerContext,
    ) -> rag_pb2.SummarizeResponse:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "RAGService.Summarize is not implemented yet (Phase 10: Financial summarization).",
        )
