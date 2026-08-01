"""Splits extracted document text into overlapping chunks for embedding.

Character-based, not token-based: this keeps chunking independent of any
specific embedding model's tokenizer (swapping models never requires
retuning chunk boundaries), and sentence-transformers truncates any chunk
that ends up over its max sequence length anyway rather than erroring, so
an approximate size budget is enough — it doesn't need to be exact.
"""

from __future__ import annotations

import hashlib
import re
from dataclasses import dataclass

# Paragraph breaks (blank lines) are the preferred chunk-packing unit;
# paragraphs that alone exceed the target size fall back to sentence-level
# splitting.
_PARAGRAPH_RE = re.compile(r"\n\s*\n")

# A deliberately simple sentence-boundary heuristic: split after ./!/?
# followed by whitespace. Real sentence segmentation (handling "Mr.",
# "U.S.", decimal numbers, etc.) needs a proper NLP library — not worth
# adding here since a slightly-wrong split only shifts where a chunk
# boundary falls, it doesn't corrupt the extracted text itself.
_SENTENCE_RE = re.compile(r"(?<=[.!?])\s+")

DEFAULT_CHUNK_SIZE_CHARS = 1000
DEFAULT_OVERLAP_CHARS = 150


@dataclass(frozen=True)
class Chunk:
    text: str
    index: int
    content_hash: str  # sha256 of text — the embedding-cache dedup key


class Chunker:
    def __init__(
        self,
        chunk_size_chars: int = DEFAULT_CHUNK_SIZE_CHARS,
        overlap_chars: int = DEFAULT_OVERLAP_CHARS,
    ) -> None:
        if chunk_size_chars <= 0:
            raise ValueError("chunk_size_chars must be positive")
        if overlap_chars < 0 or overlap_chars >= chunk_size_chars:
            raise ValueError("overlap_chars must be non-negative and smaller than chunk_size_chars")
        self._chunk_size = chunk_size_chars
        self._overlap = overlap_chars

    def chunk(self, text: str) -> list[Chunk]:
        text = text.strip()
        if not text:
            return []

        units = self._split_into_units(text)
        packed = self._pack(units)
        return [Chunk(text=t, index=i, content_hash=_hash(t)) for i, t in enumerate(packed)]

    def _split_into_units(self, text: str) -> list[str]:
        """Breaks text into pieces no single one of which exceeds the chunk
        budget, preferring paragraph boundaries, falling back to
        sentences, and hard-splitting only as a last resort (e.g. one
        run-on sentence with no punctuation longer than the whole
        budget)."""
        units: list[str] = []
        for paragraph in _PARAGRAPH_RE.split(text):
            paragraph = paragraph.strip()
            if not paragraph:
                continue
            if len(paragraph) <= self._chunk_size:
                units.append(paragraph)
                continue
            for sentence in _SENTENCE_RE.split(paragraph):
                sentence = sentence.strip()
                if not sentence:
                    continue
                if len(sentence) <= self._chunk_size:
                    units.append(sentence)
                else:
                    units.extend(_hard_split(sentence, self._chunk_size))
        return units

    def _pack(self, units: list[str]) -> list[str]:
        """Greedily packs units into chunks up to the size budget, carrying
        the tail of each finished chunk forward as the start of the next
        one for cross-chunk context overlap."""
        chunks: list[str] = []
        current = ""

        for unit in units:
            candidate = f"{current} {unit}".strip() if current else unit
            if len(candidate) <= self._chunk_size:
                current = candidate
                continue

            if current:
                chunks.append(current)
                current = (self._overlap_tail(current) + " " + unit).strip()
                # The overlap tail plus this one unit can itself still
                # exceed the budget (a large unit right after a chunk
                # boundary) — accepted as a slightly oversized chunk
                # rather than silently truncating real content.
            else:
                current = unit

        if current:
            chunks.append(current)

        return chunks

    def _overlap_tail(self, text: str) -> str:
        if self._overlap == 0:
            return ""
        if len(text) <= self._overlap:
            return text
        tail = text[-self._overlap :]
        # Avoid starting the overlap mid-word: snap forward to the next
        # whitespace boundary within the tail, if there is one.
        space_idx = tail.find(" ")
        if space_idx != -1:
            tail = tail[space_idx + 1 :]
        return tail


def _hard_split(text: str, size: int) -> list[str]:
    return [text[i : i + size] for i in range(0, len(text), size)]


def _hash(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()
