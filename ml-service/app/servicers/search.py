"""SearchService: semantic (vector), keyword (BM25), and hybrid (Reciprocal
Rank Fusion of both) retrieval over embedded chunks.

Unary and on gateway-go's hot path — called directly from an HTTP request
a user is waiting on, unlike ingestion/embedding which run on a background
worker (see proto/README.md's "Why these RPC shapes").
"""

from __future__ import annotations

import time

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.model import EmbeddingModel
from app.embeddings.vector_store import ChunkRecord, VectorStore
from app.search.keyword_index import KeywordIndex
from app.search.retrieval import DEFAULT_TOP_K, retrieve
from common.v1 import common_pb2
from search.v1 import search_pb2, search_pb2_grpc


class SearchServicer(search_pb2_grpc.SearchServiceServicer):
    def __init__(
        self, model: EmbeddingModel, store: VectorStore, keyword_index: KeywordIndex
    ) -> None:
        self._model = model
        self._store = store
        self._keyword_index = keyword_index

    async def Search(
        self,
        request: search_pb2.SearchRequest,
        context: grpc.aio.ServicerContext,
    ) -> search_pb2.SearchResponse:
        start = time.monotonic()

        top = await retrieve(
            model=self._model,
            store=self._store,
            keyword_index=self._keyword_index,
            query=request.query,
            top_k=request.top_k or DEFAULT_TOP_K,
            mode=request.mode,
            metadata_filter=request.filter if request.HasField("filter") else None,
        )
        latency_ms = (time.monotonic() - start) * 1000

        return search_pb2.SearchResponse(
            results=[
                search_pb2.ScoredChunk(chunk=_to_proto_chunk(hit.chunk), score=hit.score)
                for hit in top
            ],
            search_latency_ms=latency_ms,
        )


def _to_proto_chunk(record: ChunkRecord) -> common_pb2.Chunk:
    return common_pb2.Chunk(
        chunk_id=record.chunk_id,
        document_id=record.document_id,
        text=record.text,
        chunk_index=record.chunk_index,
        content_hash=record.content_hash,
        metadata=common_pb2.FinancialMetadata(
            ticker=record.metadata.get("ticker", ""),
            company_name=record.metadata.get("company_name", ""),
            filing_type=record.metadata.get("filing_type", ""),
            fiscal_period=record.metadata.get("fiscal_period", ""),
        ),
    )
