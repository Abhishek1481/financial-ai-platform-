"""Benchmarks for ml-service's CPU-bound hot paths — the pure-Python ones
that don't need a loaded ML model or a live gRPC server (chunking,
keyword search, evaluation scoring). Run with:

    cd ml-service && .venv/Scripts/python.exe -m pytest ../tests/benchmark -v --benchmark-only

Not part of the default `pytest` invocation ci.yml runs (that's scoped to
tests/unit/ml_service explicitly) — these measure performance, not
correctness, and belong in their own reporting pass rather than
interleaved with pass/fail unit test output.
"""

from __future__ import annotations

from app.embeddings.chunking import Chunker
from app.evaluation.metrics import score_answer
from app.search.keyword_index import KeywordIndex

_SAMPLE_PARAGRAPH = (
    "Tesla automotive revenue grew eighteen percent year over year in the first "
    "quarter, driven primarily by strong Model Y deliveries across North America "
    "and Europe. Management noted that battery cell supply chain constraints "
    "remain the primary headwind for production targets in the coming quarter. "
)
_LONG_DOCUMENT = (
    _SAMPLE_PARAGRAPH * 50
)  # ~10,000 chars, a realistic filing-section size


def test_chunking_a_realistic_document(benchmark):
    chunker = Chunker(chunk_size_chars=1000, overlap_chars=150)
    result = benchmark(chunker.chunk, _LONG_DOCUMENT)
    assert len(result) > 1


def test_keyword_index_search_over_a_moderate_corpus(benchmark):
    index = KeywordIndex()
    for i in range(500):
        index.upsert(
            f"chunk-{i}",
            f"doc-{i % 50}",
            f"Revenue report number {i} about quarterly earnings.",
        )
    # First search pays the lazy-rebuild cost (see keyword_index.py's
    # module docstring) — pay it once outside the benchmark loop so the
    # benchmark measures steady-state search, not one-time index
    # construction.
    index.search("revenue", 10)

    result = benchmark(index.search, "revenue quarterly earnings", 10)
    assert len(result) > 0


def test_scoring_a_realistic_answer(benchmark):
    context = [
        _SAMPLE_PARAGRAPH,
        "An unrelated paragraph about a completely different topic.",
    ]
    answer = (
        "Tesla revenue grew eighteen percent year over year, driven by Model Y "
        "deliveries [1]. Battery supply chain risk remains a headwind [1]."
    )

    result = benchmark(score_answer, "How did Tesla perform?", answer, context)
    assert result.faithfulness > 0
