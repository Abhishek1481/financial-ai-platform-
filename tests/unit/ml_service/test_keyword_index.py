from __future__ import annotations

from app.search.keyword_index import KeywordIndex


class TestKeywordIndex:
    def test_search_empty_index_returns_empty(self):
        index = KeywordIndex()
        assert index.search("tesla", top_k=5) == []

    def test_finds_matching_document_by_term_overlap(self):
        index = KeywordIndex()
        index.upsert(
            "chunk-1", "doc-1", "Tesla revenue grew significantly this quarter"
        )
        index.upsert("chunk-2", "doc-2", "Apple iPhone sales declined")

        results = index.search("tesla revenue", top_k=5)

        assert len(results) == 1
        assert results[0][0] == "chunk-1"

    def test_excludes_zero_score_matches(self):
        index = KeywordIndex()
        index.upsert("chunk-1", "doc-1", "Tesla revenue grew")

        results = index.search("completely unrelated query about weather", top_k=5)

        assert results == []

    def test_ranks_stronger_term_overlap_higher(self):
        index = KeywordIndex()
        index.upsert("chunk-1", "doc-1", "Tesla Tesla Tesla revenue grew significantly")
        index.upsert("chunk-2", "doc-2", "Tesla mentioned once in passing about cars")

        results = index.search("tesla", top_k=5)

        assert results[0][0] == "chunk-1"

    def test_upsert_of_same_chunk_id_replaces_text(self):
        index = KeywordIndex()
        index.upsert("chunk-1", "doc-1", "original text about apples")
        index.upsert("chunk-1", "doc-1", "updated text about oranges")

        assert index.search("apples", top_k=5) == []
        results = index.search("oranges", top_k=5)
        assert len(results) == 1

    def test_delete_by_document_removes_matching_chunks(self):
        index = KeywordIndex()
        index.upsert("chunk-1", "doc-1", "tesla revenue")
        index.upsert("chunk-2", "doc-2", "tesla margins")

        deleted = index.delete_by_document("doc-1")

        assert deleted == 1
        results = index.search("tesla", top_k=5)
        assert [cid for cid, _ in results] == ["chunk-2"]

    def test_delete_nonexistent_document_returns_zero(self):
        index = KeywordIndex()
        assert index.delete_by_document("does-not-exist") == 0

    def test_top_k_limits_results(self):
        index = KeywordIndex()
        for i in range(10):
            index.upsert(f"chunk-{i}", "doc-1", "tesla revenue growth report")

        results = index.search("tesla", top_k=3)

        assert len(results) == 3
