"""Shared semantic/keyword/hybrid retrieval, used by both `SearchService.Search`
and `RAGService.Query` — extracted so the two servicers don't duplicate the
embed-query -> vector-search -> keyword-search -> filter -> fuse pipeline
(previously lived only in app/servicers/search.py).
"""

from __future__ import annotations

import asyncio

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.model import EmbeddingModel
from app.embeddings.vector_store import ScoredChunk, VectorStore
from app.search.filter import build_filter
from app.search.fusion import reciprocal_rank_fusion
from app.search.keyword_index import KeywordIndex
from common.v1 import common_pb2
from search.v1 import search_pb2

DEFAULT_TOP_K = 10
# Retrieval fetches more than top_k before filtering, since metadata
# filtering happens after retrieval (see app/search/filter.py) and could
# otherwise leave fewer than top_k results even when enough matching
# chunks exist further down the ranking.
SEARCH_POOL_MULTIPLIER = 5


async def retrieve(
    *,
    model: EmbeddingModel,
    store: VectorStore,
    keyword_index: KeywordIndex,
    query: str,
    top_k: int,
    mode: search_pb2.SearchMode.ValueType,
    metadata_filter: common_pb2.MetadataFilter | None,
) -> list[ScoredChunk]:
    loop = asyncio.get_running_loop()
    pool_k = top_k * SEARCH_POOL_MULTIPLIER
    matches_filter = build_filter(metadata_filter)
    effective_mode = mode or search_pb2.SEARCH_MODE_HYBRID

    vector_hits: list[ScoredChunk] = []
    if effective_mode in (search_pb2.SEARCH_MODE_SEMANTIC, search_pb2.SEARCH_MODE_HYBRID):
        query_vector = await loop.run_in_executor(None, model.embed, [query])
        raw_hits = await loop.run_in_executor(None, store.search, query_vector[0], pool_k)
        vector_hits = [h for h in raw_hits if matches_filter(h.chunk)]

    keyword_hits: list[ScoredChunk] = []
    if effective_mode in (search_pb2.SEARCH_MODE_KEYWORD, search_pb2.SEARCH_MODE_HYBRID):
        keyword_matches = await loop.run_in_executor(None, keyword_index.search, query, pool_k)
        keyword_hits = await _resolve_keyword_hits(loop, store, keyword_matches, matches_filter)

    if effective_mode == search_pb2.SEARCH_MODE_SEMANTIC:
        merged = vector_hits
    elif effective_mode == search_pb2.SEARCH_MODE_KEYWORD:
        merged = keyword_hits
    else:
        merged = reciprocal_rank_fusion(vector_hits, keyword_hits)

    return merged[:top_k]


async def _resolve_keyword_hits(loop, store: VectorStore, keyword_matches, matches_filter):
    hits = []
    for chunk_id, score in keyword_matches:
        record = await loop.run_in_executor(None, store.get_by_chunk_id, chunk_id)
        if record is not None and matches_filter(record):
            hits.append(ScoredChunk(chunk=record, score=score))
    return hits
