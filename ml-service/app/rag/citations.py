"""Citation extraction: pulls `[N]` markers back out of generated text and
resolves them against the numbered context chunks the prompt handed the
model (see app/rag/prompt.py), producing the Citation list the wire
protocol expects.
"""

from __future__ import annotations

import re

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.vector_store import ChunkRecord
from common.v1 import common_pb2

_CITATION_MARKER_RE = re.compile(r"\[(\d+)\]")
_QUOTE_MAX_CHARS = 240


def extract_cited_indices(text: str) -> set[int]:
    return {int(n) for n in _CITATION_MARKER_RE.findall(text)}


def build_citations(
    generated_text: str, context_chunks: list[ChunkRecord]
) -> list[common_pb2.Citation]:
    cited = sorted(extract_cited_indices(generated_text))
    citations = []
    for index in cited:
        if not 1 <= index <= len(context_chunks):
            continue  # model hallucinated a marker with no matching chunk
        chunk = context_chunks[index - 1]
        quote = chunk.text[:_QUOTE_MAX_CHARS]
        citations.append(
            common_pb2.Citation(
                chunk_id=chunk.chunk_id,
                document_id=chunk.document_id,
                quote=quote,
                page_number=0,
                source_url="",
            )
        )
    return citations
