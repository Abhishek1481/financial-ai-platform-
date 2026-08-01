"""EmbeddingService — real implementation lands in Phase 7."""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from embeddings.v1 import embeddings_pb2, embeddings_pb2_grpc


class EmbeddingServicer(embeddings_pb2_grpc.EmbeddingServiceServicer):
    async def ChunkAndEmbed(
        self,
        request: embeddings_pb2.ChunkAndEmbedRequest,
        context: grpc.aio.ServicerContext,
    ) -> AsyncIterator[embeddings_pb2.ChunkAndEmbedProgress]:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "EmbeddingService.ChunkAndEmbed is not implemented yet (Phase 7: Embedding pipeline).",
        )
        # `context.abort` always raises; this unreachable yield makes the
        # method an async generator, which is what grpc.aio's dispatcher
        # expects for a server-streaming RPC.
        yield  # pragma: no cover

    async def DeleteDocumentEmbeddings(
        self,
        request: embeddings_pb2.DeleteDocumentEmbeddingsRequest,
        context: grpc.aio.ServicerContext,
    ) -> embeddings_pb2.DeleteDocumentEmbeddingsResponse:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "EmbeddingService.DeleteDocumentEmbeddings is not implemented yet (Phase 7: Embedding pipeline).",
        )

    async def CheckDuplicateChunk(
        self,
        request: embeddings_pb2.CheckDuplicateChunkRequest,
        context: grpc.aio.ServicerContext,
    ) -> embeddings_pb2.CheckDuplicateChunkResponse:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "EmbeddingService.CheckDuplicateChunk is not implemented yet (Phase 7: Embedding pipeline).",
        )
