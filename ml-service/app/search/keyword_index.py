"""BM25 keyword search over chunk text.

Uses BM25+ (Lv & Zhai, 2011), not classic Okapi BM25: the classic
Robertson-Sparck Jones IDF formula — log((N - n + 0.5) / (n + 0.5)) — hits
zero (not just "low", exactly zero) for any term appearing in precisely
half a corpus, which happens constantly at small document counts (with 2
documents, EVERY term in either one hits this and every score comes back
0, making keyword search silently useless until enough documents
accumulate). BM25+ adds a floor delta so a term match always contributes a
meaningful positive score regardless of corpus size.

That floor delta has a real side effect worth knowing about: it's added
per query term to *every* document's score, including documents that
don't contain the term at all (tf=0), so "score > 0" stops meaning "this
document matched something" once you're on BM25+ — every document in the
corpus gets a nonzero score for every query, found via this package's own
tests, not assumed. search() therefore filters explicitly on token
overlap (does this chunk actually contain at least one query term) rather
than trusting the score's sign, and uses the BM25+ score only to rank the
documents that pass that filter.

Not persisted on its own — rank_bm25 has no incremental-update or
serialization support, it builds a static index from a full corpus. So
this index is rebuilt (cheaply, at dev-scale data volumes) from
FaissVectorStore.all_records() at server startup, and lazily rebuilt after
any mutation rather than on every single upsert during a batch — a dirty
flag plus rebuild-on-next-search, not eager reindexing per chunk.
"""

from __future__ import annotations

import re
import threading

from rank_bm25 import BM25Plus

_TOKEN_RE = re.compile(r"\w+")


def _tokenize(text: str) -> list[str]:
    return _TOKEN_RE.findall(text.lower())


class KeywordIndex:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._texts: dict[str, str] = {}  # chunk_id -> text
        self._document_by_chunk: dict[str, str] = {}
        self._bm25: BM25Plus | None = None
        self._chunk_ids: list[str] = []
        self._tokenized_by_chunk: dict[str, set[str]] = {}
        self._dirty = True

    def upsert(self, chunk_id: str, document_id: str, text: str) -> None:
        with self._lock:
            self._texts[chunk_id] = text
            self._document_by_chunk[chunk_id] = document_id
            self._dirty = True

    def delete_by_document(self, document_id: str) -> int:
        with self._lock:
            to_remove = [
                chunk_id
                for chunk_id, doc_id in self._document_by_chunk.items()
                if doc_id == document_id
            ]
            for chunk_id in to_remove:
                del self._texts[chunk_id]
                del self._document_by_chunk[chunk_id]
            if to_remove:
                self._dirty = True
            return len(to_remove)

    def search(self, query: str, top_k: int) -> list[tuple[str, float]]:
        """Returns (chunk_id, bm25_score) pairs, highest score first,
        restricted to chunks that actually share at least one token with
        the query (see module docstring for why BM25+'s score alone can't
        be used to decide that)."""
        query_tokens = set(_tokenize(query))
        if not query_tokens:
            return []

        with self._lock:
            self._rebuild_if_dirty()
            if self._bm25 is None:
                return []
            scores = self._bm25.get_scores(list(query_tokens))
            candidates = [
                (chunk_id, float(score))
                for chunk_id, score in zip(self._chunk_ids, scores, strict=True)
                if query_tokens & self._tokenized_by_chunk[chunk_id]
            ]

        candidates.sort(key=lambda pair: pair[1], reverse=True)
        return candidates[:top_k]

    def _rebuild_if_dirty(self) -> None:
        if not self._dirty:
            return
        self._chunk_ids = list(self._texts.keys())
        self._tokenized_by_chunk = {
            cid: set(_tokenize(self._texts[cid])) for cid in self._chunk_ids
        }
        if self._chunk_ids:
            tokenized_corpus = [_tokenize(self._texts[cid]) for cid in self._chunk_ids]
            self._bm25 = BM25Plus(tokenized_corpus)
        else:
            self._bm25 = None
        self._dirty = False
