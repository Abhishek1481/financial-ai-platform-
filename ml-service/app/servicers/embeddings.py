"""EmbeddingService: chunks extracted text, embeds each chunk (reusing
cached vectors for content already seen elsewhere), and upserts into the
vector store.

Called by a gateway-go worker immediately after IngestionService.
ExtractDocument succeeds (see gateway-go/internal/ingestion.Service) — the
two RPCs are chained in Go, not inside ml-service, keeping this service
stateless with respect to the ingestion pipeline's job/status tracking
(that stays Go's job, per proto/README.md's "why these RPC shapes").
"""

from __future__ import annotations

import asyncio
import uuid
from collections.abc import AsyncIterator

import grpc
import numpy as np

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.chunking import DEFAULT_CHUNK_SIZE_CHARS, DEFAULT_OVERLAP_CHARS, Chunker
from app.embeddings.model import EmbeddingModel
from app.embeddings.vector_store import ChunkRecord, VectorStore
from app.logging import get_logger
from app.search.keyword_index import KeywordIndex
from embeddings.v1 import embeddings_pb2, embeddings_pb2_grpc

logger = get_logger(__name__)


class EmbeddingServicer(embeddings_pb2_grpc.EmbeddingServiceServicer):
    def __init__(self, model: EmbeddingModel, store: VectorStore, keyword_index: KeywordIndex) -> None:
        self._model = model
        self._store = store
        self._keyword_index = keyword_index

    async def ChunkAndEmbed(
        self,
        request: embeddings_pb2.ChunkAndEmbedRequest,
        context: grpc.aio.ServicerContext,
    ) -> AsyncIterator[embeddings_pb2.ChunkAndEmbedProgress]:
        # Request fields are named *_tokens for a future token-aware
        # chunker; Chunker is character-based today (see chunking.py's
        # module docstring), so these values are interpreted as character
        # counts until that changes.
        chunk_size = request.chunk_size_tokens or DEFAULT_CHUNK_SIZE_CHARS
        overlap = request.chunk_overlap_tokens or DEFAULT_OVERLAP_CHARS

        chunker = Chunker(chunk_size_chars=chunk_size, overlap_chars=overlap)
        chunks = chunker.chunk(request.raw_text)

        yield embeddings_pb2.ChunkAndEmbedProgress(
            stage=embeddings_pb2.EMBED_STAGE_CHUNKING,
            chunks_total=len(chunks),
        )

        if not chunks:
            yield embeddings_pb2.ChunkAndEmbedProgress(
                stage=embeddings_pb2.EMBED_STAGE_COMPLETE,
                chunks_total=0,
            )
            return

        metadata = {
            "ticker": request.metadata.ticker,
            "company_name": request.metadata.company_name,
            "filing_type": request.metadata.filing_type,
            "fiscal_period": request.metadata.fiscal_period,
        }
        metadata = {k: v for k, v in metadata.items() if v}  # drop blanks

        loop = asyncio.get_running_loop()

        # DEDUPING: identical content (e.g. boilerplate legal text
        # repeated across filings) reuses its already-computed vector
        # instead of paying the embedding model again — a distinct
        # ChunkRecord is still created per chunk, since a Chunk always
        # belongs to exactly one document (see proto's common.v1.Chunk),
        # only the underlying vector is shared.
        yield embeddings_pb2.ChunkAndEmbedProgress(
            stage=embeddings_pb2.EMBED_STAGE_DEDUPING,
            chunks_total=len(chunks),
        )

        cached_vectors: dict[int, np.ndarray] = {}
        texts_to_embed: list[str] = []
        indices_to_embed: list[int] = []
        skipped_duplicate = 0

        for i, chunk in enumerate(chunks):
            existing_vector = await loop.run_in_executor(
                None, self._store.get_vector_by_content_hash, chunk.content_hash
            )
            if existing_vector is not None:
                cached_vectors[i] = existing_vector
                skipped_duplicate += 1
            else:
                texts_to_embed.append(chunk.text)
                indices_to_embed.append(i)

        # EMBEDDING: the actual model call — run off the event loop since
        # it's a synchronous, CPU-bound call into native code (see
        # EmbeddingModel's module docstring).
        yield embeddings_pb2.ChunkAndEmbedProgress(
            stage=embeddings_pb2.EMBED_STAGE_EMBEDDING,
            chunks_total=len(chunks),
            chunks_processed=skipped_duplicate,
            chunks_skipped_duplicate=skipped_duplicate,
        )

        if texts_to_embed:
            fresh_vectors = await loop.run_in_executor(None, self._model.embed, texts_to_embed)
            for idx, vector in zip(indices_to_embed, fresh_vectors, strict=True):
                cached_vectors[idx] = vector

        # UPSERTING
        yield embeddings_pb2.ChunkAndEmbedProgress(
            stage=embeddings_pb2.EMBED_STAGE_UPSERTING,
            chunks_total=len(chunks),
            chunks_processed=len(chunks),
            chunks_skipped_duplicate=skipped_duplicate,
        )

        records = [
            ChunkRecord(
                chunk_id=str(uuid.uuid4()),
                document_id=request.document_id,
                text=chunk.text,
                chunk_index=chunk.index,
                content_hash=chunk.content_hash,
                metadata=metadata,
            )
            for chunk in chunks
        ]
        vectors_array = _stack_vectors(cached_vectors, len(chunks), self._model.dimension)

        await loop.run_in_executor(None, self._store.upsert, records, vectors_array)
        for record in records:
            await loop.run_in_executor(
                None, self._keyword_index.upsert, record.chunk_id, record.document_id, record.text
            )

        logger.info(
            "document embedded",
            extra={
                "document_id": request.document_id,
                "chunks_total": len(chunks),
                "chunks_skipped_duplicate": skipped_duplicate,
            },
        )

        yield embeddings_pb2.ChunkAndEmbedProgress(
            stage=embeddings_pb2.EMBED_STAGE_COMPLETE,
            chunks_total=len(chunks),
            chunks_processed=len(chunks),
            chunks_skipped_duplicate=skipped_duplicate,
            chunk_ids=[r.chunk_id for r in records],
        )

    async def DeleteDocumentEmbeddings(
        self,
        request: embeddings_pb2.DeleteDocumentEmbeddingsRequest,
        context: grpc.aio.ServicerContext,
    ) -> embeddings_pb2.DeleteDocumentEmbeddingsResponse:
        loop = asyncio.get_running_loop()
        deleted = await loop.run_in_executor(
            None, self._store.delete_by_document, request.document_id
        )
        await loop.run_in_executor(
            None, self._keyword_index.delete_by_document, request.document_id
        )
        return embeddings_pb2.DeleteDocumentEmbeddingsResponse(chunks_deleted=deleted)

    async def CheckDuplicateChunk(
        self,
        request: embeddings_pb2.CheckDuplicateChunkRequest,
        context: grpc.aio.ServicerContext,
    ) -> embeddings_pb2.CheckDuplicateChunkResponse:
        loop = asyncio.get_running_loop()
        existing = await loop.run_in_executor(
            None, self._store.get_by_content_hash, request.content_hash
        )
        if existing is None:
            return embeddings_pb2.CheckDuplicateChunkResponse(is_duplicate=False)
        return embeddings_pb2.CheckDuplicateChunkResponse(
            is_duplicate=True, existing_chunk_id=existing.chunk_id
        )


def _stack_vectors(vectors_by_index: dict[int, np.ndarray], count: int, dimension: int) -> np.ndarray:
    stacked = np.stack([vectors_by_index[i] for i in range(count)])
    return stacked.astype(np.float32).reshape(count, dimension)
