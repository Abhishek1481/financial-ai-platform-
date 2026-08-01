"""Reciprocal Rank Fusion: combines two independently-ranked result lists
(vector similarity, BM25) into one ranking without needing to normalize or
weight two scores that live on entirely different scales (cosine
similarity in [-1,1] vs. an unbounded BM25 score) — RRF only looks at
*rank position* in each list, which sidesteps that problem entirely and is
the standard, well-understood technique for this rather than inventing a
weighting scheme.
"""

from __future__ import annotations

from app.embeddings.vector_store import ChunkRecord, ScoredChunk

_RRF_K = 60  # standard default from the original RRF paper; dampens the
# impact of any single list's top rank so one list can't dominate purely
# by having the target near position 1.


def reciprocal_rank_fusion(*rankings: list[ScoredChunk]) -> list[ScoredChunk]:
    fused_scores: dict[str, float] = {}
    chunks: dict[str, ChunkRecord] = {}

    for ranking in rankings:
        for rank, hit in enumerate(ranking):
            chunk_id = hit.chunk.chunk_id
            fused_scores[chunk_id] = fused_scores.get(chunk_id, 0.0) + 1.0 / (_RRF_K + rank + 1)
            chunks[chunk_id] = hit.chunk

    ranked_ids = sorted(fused_scores, key=lambda cid: fused_scores[cid], reverse=True)
    return [ScoredChunk(chunk=chunks[cid], score=fused_scores[cid]) for cid in ranked_ids]
