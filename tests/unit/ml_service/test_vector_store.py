from __future__ import annotations

from pathlib import Path

import numpy as np
import pytest

from app.embeddings.vector_store import ChunkRecord, FaissVectorStore

DIM = 4


def make_record(chunk_id: str, document_id: str = "doc-1", content_hash: str | None = None) -> ChunkRecord:
    return ChunkRecord(
        chunk_id=chunk_id,
        document_id=document_id,
        text=f"text for {chunk_id}",
        chunk_index=0,
        content_hash=content_hash or f"hash-{chunk_id}",
    )


def unit_vector(*components: float) -> np.ndarray:
    v = np.array(components, dtype=np.float32)
    return v / np.linalg.norm(v)


class TestFaissVectorStore_UpsertAndSearch:
    def test_search_returns_closest_match_first(self):
        store = FaissVectorStore(dimension=DIM)
        store.upsert(
            [make_record("a"), make_record("b"), make_record("c")],
            np.stack([unit_vector(1, 0, 0, 0), unit_vector(0, 1, 0, 0), unit_vector(0, 0, 1, 0)]),
        )

        results = store.search(unit_vector(1, 0, 0, 0), top_k=3)

        assert results[0].chunk.chunk_id == "a"
        assert results[0].score == pytest.approx(1.0, abs=1e-5)

    def test_search_on_empty_store_returns_empty(self):
        store = FaissVectorStore(dimension=DIM)
        assert store.search(unit_vector(1, 0, 0, 0), top_k=5) == []

    def test_search_top_k_larger_than_store_does_not_error(self):
        store = FaissVectorStore(dimension=DIM)
        store.upsert([make_record("a")], np.stack([unit_vector(1, 0, 0, 0)]))

        results = store.search(unit_vector(1, 0, 0, 0), top_k=50)

        assert len(results) == 1

    def test_upsert_rejects_mismatched_lengths(self):
        store = FaissVectorStore(dimension=DIM)
        with pytest.raises(ValueError):
            store.upsert([make_record("a"), make_record("b")], np.stack([unit_vector(1, 0, 0, 0)]))

    def test_reupserting_same_chunk_id_replaces_not_duplicates(self):
        store = FaissVectorStore(dimension=DIM)
        store.upsert([make_record("a")], np.stack([unit_vector(1, 0, 0, 0)]))
        store.upsert([make_record("a")], np.stack([unit_vector(0, 1, 0, 0)]))

        results = store.search(unit_vector(0, 1, 0, 0), top_k=10)

        assert len(results) == 1
        assert results[0].chunk.chunk_id == "a"


class TestFaissVectorStore_DeleteByDocument:
    def test_deletes_only_matching_document(self):
        store = FaissVectorStore(dimension=DIM)
        store.upsert(
            [make_record("a", document_id="doc-1"), make_record("b", document_id="doc-2")],
            np.stack([unit_vector(1, 0, 0, 0), unit_vector(0, 1, 0, 0)]),
        )

        deleted = store.delete_by_document("doc-1")

        assert deleted == 1
        remaining = store.search(unit_vector(0, 1, 0, 0), top_k=10)
        assert [r.chunk.chunk_id for r in remaining] == ["b"]

    def test_deleting_nonexistent_document_returns_zero(self):
        store = FaissVectorStore(dimension=DIM)
        assert store.delete_by_document("does-not-exist") == 0


class TestFaissVectorStore_ContentHashDedup:
    def test_get_by_content_hash_finds_existing_chunk(self):
        store = FaissVectorStore(dimension=DIM)
        store.upsert(
            [make_record("a", content_hash="shared-hash")], np.stack([unit_vector(1, 0, 0, 0)])
        )

        found = store.get_by_content_hash("shared-hash")

        assert found is not None
        assert found.chunk_id == "a"

    def test_get_by_content_hash_returns_none_when_unknown(self):
        store = FaissVectorStore(dimension=DIM)
        assert store.get_by_content_hash("unknown-hash") is None

    def test_get_vector_by_content_hash_returns_the_stored_vector(self):
        store = FaissVectorStore(dimension=DIM)
        vec = unit_vector(1, 0, 0, 0)
        store.upsert([make_record("a", content_hash="shared-hash")], np.stack([vec]))

        found = store.get_vector_by_content_hash("shared-hash")

        assert found is not None
        np.testing.assert_allclose(found, vec, atol=1e-6)

    def test_get_vector_by_content_hash_returns_none_when_unknown(self):
        store = FaissVectorStore(dimension=DIM)
        assert store.get_vector_by_content_hash("unknown-hash") is None


class TestFaissVectorStore_Persistence:
    def test_reloading_from_persist_dir_recovers_data(self, tmp_path: Path):
        persist_dir = str(tmp_path / "vector-index")

        store = FaissVectorStore(dimension=DIM, persist_dir=persist_dir)
        store.upsert(
            [make_record("a", content_hash="hash-a")], np.stack([unit_vector(1, 0, 0, 0)])
        )

        reloaded = FaissVectorStore(dimension=DIM, persist_dir=persist_dir)
        results = reloaded.search(unit_vector(1, 0, 0, 0), top_k=10)

        assert len(results) == 1
        assert results[0].chunk.chunk_id == "a"
        assert reloaded.get_by_content_hash("hash-a") is not None

    def test_fresh_persist_dir_starts_empty(self, tmp_path: Path):
        store = FaissVectorStore(dimension=DIM, persist_dir=str(tmp_path / "does-not-exist-yet"))
        assert store.search(unit_vector(1, 0, 0, 0), top_k=10) == []

    def test_deletes_persist_across_reload(self, tmp_path: Path):
        persist_dir = str(tmp_path / "vector-index")

        store = FaissVectorStore(dimension=DIM, persist_dir=persist_dir)
        store.upsert(
            [make_record("a", document_id="doc-1"), make_record("b", document_id="doc-2")],
            np.stack([unit_vector(1, 0, 0, 0), unit_vector(0, 1, 0, 0)]),
        )
        store.delete_by_document("doc-1")

        reloaded = FaissVectorStore(dimension=DIM, persist_dir=persist_dir)
        results = reloaded.search(unit_vector(1, 0, 0, 0), top_k=10)
        results += reloaded.search(unit_vector(0, 1, 0, 0), top_k=10)

        chunk_ids = {r.chunk.chunk_id for r in results}
        assert chunk_ids == {"b"}
