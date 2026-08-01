from __future__ import annotations

from app.embeddings.vector_store import ChunkRecord, ScoredChunk
from app.search.fusion import reciprocal_rank_fusion


def make_hit(chunk_id: str, score: float) -> ScoredChunk:
    record = ChunkRecord(
        chunk_id=chunk_id,
        document_id="doc-1",
        text=f"text {chunk_id}",
        chunk_index=0,
        content_hash=chunk_id,
    )
    return ScoredChunk(chunk=record, score=score)


class TestReciprocalRankFusion:
    def test_chunk_ranked_first_in_both_lists_wins(self):
        vector_hits = [make_hit("a", 0.9), make_hit("b", 0.8)]
        keyword_hits = [make_hit("a", 5.0), make_hit("c", 3.0)]

        fused = reciprocal_rank_fusion(vector_hits, keyword_hits)

        assert fused[0].chunk.chunk_id == "a"

    def test_chunk_appearing_in_only_one_list_still_included(self):
        vector_hits = [make_hit("a", 0.9)]
        keyword_hits: list[ScoredChunk] = []

        fused = reciprocal_rank_fusion(vector_hits, keyword_hits)

        assert [h.chunk.chunk_id for h in fused] == ["a"]

    def test_empty_lists_produce_empty_result(self):
        assert reciprocal_rank_fusion([], []) == []

    def test_appearing_in_both_lists_outranks_appearing_in_only_one(self):
        # "b" is #1 in vector but absent from keyword; "a" is #2 in vector
        # and #1 in keyword — RRF should favor "a" for showing up in both.
        vector_hits = [make_hit("b", 0.95), make_hit("a", 0.5)]
        keyword_hits = [make_hit("a", 10.0)]

        fused = reciprocal_rank_fusion(vector_hits, keyword_hits)

        assert fused[0].chunk.chunk_id == "a"
