"""Vector storage for embedded chunks.

VectorStore is a Protocol so FaissVectorStore — an in-process index,
optionally persisted to disk — can be swapped for an OpenSearch-backed
implementation in Phase 16 (hybrid BM25+vector search, horizontal scale)
without any caller changing. Same "temporary but real, not a permanent
gap" pattern as LocalObjectStore and the in-memory repositories elsewhere
in this platform (see ml-service/README.md and gateway-go/README.md's
design-decisions sections).
"""

from __future__ import annotations

import json
import threading
from dataclasses import dataclass, field
from pathlib import Path
from typing import Protocol

import faiss
import numpy as np


@dataclass(frozen=True)
class ChunkRecord:
    chunk_id: str
    document_id: str
    text: str
    chunk_index: int
    content_hash: str
    metadata: dict[str, str] = field(default_factory=dict)


@dataclass(frozen=True)
class ScoredChunk:
    chunk: ChunkRecord
    score: float


class VectorStore(Protocol):
    def upsert(self, records: list[ChunkRecord], vectors: np.ndarray) -> None: ...
    def delete_by_document(self, document_id: str) -> int: ...
    def search(self, query_vector: np.ndarray, top_k: int) -> list[ScoredChunk]: ...
    def get_by_content_hash(self, content_hash: str) -> ChunkRecord | None: ...
    def get_vector_by_content_hash(self, content_hash: str) -> np.ndarray | None:
        """Returns the already-embedded vector for a known content hash,
        letting a caller reuse it (e.g. identical boilerplate text
        appearing in a different document) instead of recomputing the
        embedding for content the model has already seen."""
        ...
    def get_by_chunk_id(self, chunk_id: str) -> ChunkRecord | None: ...
    def all_records(self) -> list[ChunkRecord]:
        """Every stored record — used to rebuild KeywordIndex (BM25 has no
        native persistence of its own; see search/keyword_index.py) from
        whatever FaissVectorStore already persisted, on server startup."""
        ...


class FaissVectorStore:
    """IndexFlatIP (exact, brute-force inner-product search) wrapped in an
    IndexIDMap2 so chunks can be addressed and removed by a stable integer
    ID, plus a companion dict of ChunkRecord metadata — FAISS itself only
    stores vectors against opaque IDs, nothing about what they mean.
    Inner product rather than L2 distance because EmbeddingModel already
    L2-normalizes every vector, at which point inner product IS cosine
    similarity, just cheaper to compute.

    persist_dir, if given, writes the index and metadata to disk after
    every mutation and reloads them on construction, so embeddings survive
    a process restart — brute-force (rewrite everything each time) rather
    than incremental, which is fine at the data volumes a local FAISS
    index is meant for; Phase 16's OpenSearch is what scales past that.
    """

    def __init__(self, dimension: int, persist_dir: str | None = None) -> None:
        self._dimension = dimension
        self._persist_dir = Path(persist_dir) if persist_dir else None
        self._lock = threading.Lock()

        self._index = faiss.IndexIDMap2(faiss.IndexFlatIP(dimension))
        self._records: dict[int, ChunkRecord] = {}  # faiss int id -> record
        self._id_by_chunk_id: dict[str, int] = {}
        self._id_by_content_hash: dict[str, int] = {}
        self._next_id = 0

        if self._persist_dir:
            self._load()

    def upsert(self, records: list[ChunkRecord], vectors: np.ndarray) -> None:
        if len(records) != vectors.shape[0]:
            raise ValueError("records and vectors must have the same length")
        if vectors.shape[0] == 0:
            return

        with self._lock:
            ids = []
            for record in records:
                # Re-upserting an existing chunk_id (re-processing a
                # document) replaces it in place rather than duplicating.
                existing_id = self._id_by_chunk_id.get(record.chunk_id)
                if existing_id is not None:
                    self._index.remove_ids(_id_selector([existing_id]))
                    faiss_id = existing_id
                else:
                    faiss_id = self._next_id
                    self._next_id += 1

                self._records[faiss_id] = record
                self._id_by_chunk_id[record.chunk_id] = faiss_id
                self._id_by_content_hash[record.content_hash] = faiss_id
                ids.append(faiss_id)

            self._index.add_with_ids(vectors.astype(np.float32), np.array(ids, dtype=np.int64))
            self._save()

    def delete_by_document(self, document_id: str) -> int:
        with self._lock:
            ids_to_remove = [
                faiss_id
                for faiss_id, record in self._records.items()
                if record.document_id == document_id
            ]
            if not ids_to_remove:
                return 0

            self._index.remove_ids(_id_selector(ids_to_remove))
            for faiss_id in ids_to_remove:
                record = self._records.pop(faiss_id)
                self._id_by_chunk_id.pop(record.chunk_id, None)
                # Only drop the content-hash dedup entry if it still
                # points at this exact chunk — a different chunk (a
                # different document with identical content) may have
                # taken over that hash's slot since this one was written.
                if self._id_by_content_hash.get(record.content_hash) == faiss_id:
                    self._id_by_content_hash.pop(record.content_hash, None)

            self._save()
            return len(ids_to_remove)

    def search(self, query_vector: np.ndarray, top_k: int) -> list[ScoredChunk]:
        with self._lock:
            if self._index.ntotal == 0:
                return []
            query = query_vector.astype(np.float32).reshape(1, -1)
            k = min(top_k, self._index.ntotal)
            scores, ids = self._index.search(query, k)

        results = []
        for score, faiss_id in zip(scores[0], ids[0], strict=True):
            if faiss_id == -1:  # FAISS pads short result sets with -1
                continue
            record = self._records.get(int(faiss_id))
            if record is not None:
                results.append(ScoredChunk(chunk=record, score=float(score)))
        return results

    def get_by_chunk_id(self, chunk_id: str) -> ChunkRecord | None:
        with self._lock:
            faiss_id = self._id_by_chunk_id.get(chunk_id)
            return self._records.get(faiss_id) if faiss_id is not None else None

    def all_records(self) -> list[ChunkRecord]:
        with self._lock:
            return list(self._records.values())

    def get_by_content_hash(self, content_hash: str) -> ChunkRecord | None:
        with self._lock:
            faiss_id = self._id_by_content_hash.get(content_hash)
            return self._records.get(faiss_id) if faiss_id is not None else None

    def get_vector_by_content_hash(self, content_hash: str) -> np.ndarray | None:
        with self._lock:
            faiss_id = self._id_by_content_hash.get(content_hash)
            if faiss_id is None:
                return None
            return np.array(self._index.reconstruct(faiss_id), dtype=np.float32)

    def _save(self) -> None:
        if not self._persist_dir:
            return
        self._persist_dir.mkdir(parents=True, exist_ok=True)
        faiss.write_index(self._index, str(self._persist_dir / "index.faiss"))
        payload = {
            "next_id": self._next_id,
            "records": {
                str(faiss_id): {
                    "chunk_id": r.chunk_id,
                    "document_id": r.document_id,
                    "text": r.text,
                    "chunk_index": r.chunk_index,
                    "content_hash": r.content_hash,
                    "metadata": r.metadata,
                }
                for faiss_id, r in self._records.items()
            },
        }
        (self._persist_dir / "metadata.json").write_text(json.dumps(payload))

    def _load(self) -> None:
        assert self._persist_dir is not None
        index_path = self._persist_dir / "index.faiss"
        metadata_path = self._persist_dir / "metadata.json"
        if not index_path.is_file() or not metadata_path.is_file():
            return

        loaded = faiss.read_index(str(index_path))
        # read_index's declared return type is the generic base Index —
        # every index this class ever writes is an IndexIDMap2, so loading
        # one back is safe to narrow, but mypy can't infer that from the
        # library's own signature.
        assert isinstance(loaded, faiss.IndexIDMap2)
        self._index = loaded
        payload = json.loads(metadata_path.read_text())
        self._next_id = payload["next_id"]
        for faiss_id_str, r in payload["records"].items():
            faiss_id = int(faiss_id_str)
            record = ChunkRecord(
                chunk_id=r["chunk_id"],
                document_id=r["document_id"],
                text=r["text"],
                chunk_index=r["chunk_index"],
                content_hash=r["content_hash"],
                metadata=r["metadata"],
            )
            self._records[faiss_id] = record
            self._id_by_chunk_id[record.chunk_id] = faiss_id
            self._id_by_content_hash[record.content_hash] = faiss_id


def _id_selector(ids: list[int]) -> faiss.IDSelectorBatch:
    return faiss.IDSelectorBatch(np.array(ids, dtype=np.int64))
