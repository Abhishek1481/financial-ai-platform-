"""SearchService: semantic (vector), keyword (BM25), and hybrid (Reciprocal
Rank Fusion of both) retrieval over embedded chunks.

Unary and on gateway-go's hot path — called directly from an HTTP request
a user is waiting on, unlike ingestion/embedding which run on a background
worker (see proto/README.md's "Why these RPC shapes").
"""

from __future__ import annotations

import asyncio
import time

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.model import EmbeddingModel
from app.embeddings.vector_store import ChunkRecord, ScoredChunk, VectorStore
from app.search.filter import build_filter
from app.search.fusion import reciprocal_rank_fusion
from app.search.keyword_index import KeywordIndex
from common.v1 import common_pb2
from search.v1 import search_pb2, search_pb2_grpc

DEFAULT_TOP_K = 10
# Retrieval fetches more than top_k before filtering, since metadata
# filtering happens after retrieval (see app/search/filter.py) and could
# otherwise leave fewer than top_k results even when enough matching
# chunks exist further down the ranking.
SEARCH_POOL_MULTIPLIER = 5


class SearchServicer(search_pb2_grpc.SearchServiceServicer):
    def __init__(self, model: EmbeddingModel, store: VectorStore, keyword_index: KeywordIndex) -> None:
        self._model = model
        self._store = store
        self._keyword_index = keyword_index

    async def Search(
        self,
        request: search_pb2.SearchRequest,
        context: grpc.aio.ServicerContext,
    ) -> search_pb2.SearchResponse:
        start = time.monotonic()
        loop = asyncio.get_running_loop()

        top_k = request.top_k or DEFAULT_TOP_K
        pool_k = top_k * SEARCH_POOL_MULTIPLIER
        matches_filter = build_filter(request.filter if request.HasField("filter") else None)
        mode = request.mode or search_pb2.SEARCH_MODE_HYBRID

        vector_hits: list[ScoredChunk] = []
        if mode in (search_pb2.SEARCH_MODE_SEMANTIC, search_pb2.SEARCH_MODE_HYBRID):
            query_vector = await loop.run_in_executor(None, self._model.embed, [request.query])
            raw_hits = await loop.run_in_executor(None, self._store.search, query_vector[0], pool_k)
            vector_hits = [h for h in raw_hits if matches_filter(h.chunk)]

        keyword_hits: list[ScoredChunk] = []
        if mode in (search_pb2.SEARCH_MODE_KEYWORD, search_pb2.SEARCH_MODE_HYBRID):
            keyword_matches = await loop.run_in_executor(
                None, self._keyword_index.search, request.query, pool_k
            )
            keyword_hits = await self._resolve_keyword_hits(loop, keyword_matches, matches_filter)

        if mode == search_pb2.SEARCH_MODE_SEMANTIC:
            merged = vector_hits
        elif mode == search_pb2.SEARCH_MODE_KEYWORD:
            merged = keyword_hits
        else:
            merged = reciprocal_rank_fusion(vector_hits, keyword_hits)

        top = merged[:top_k]
        latency_ms = (time.monotonic() - start) * 1000

        return search_pb2.SearchResponse(
            results=[
                search_pb2.ScoredChunk(chunk=_to_proto_chunk(hit.chunk), score=hit.score) for hit in top
            ],
            search_latency_ms=latency_ms,
        )

    async def _resolve_keyword_hits(
        self, loop, keyword_matches: list[tuple[str, float]], matches_filter
    ) -> list[ScoredChunk]:
        hits = []
        for chunk_id, score in keyword_matches:
            record = await loop.run_in_executor(None, self._store.get_by_chunk_id, chunk_id)
            if record is not None and matches_filter(record):
                hits.append(ScoredChunk(chunk=record, score=score))
        return hits


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
