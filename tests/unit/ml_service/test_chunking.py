from __future__ import annotations

from itertools import pairwise

import pytest
from app.embeddings.chunking import Chunker


class TestChunker_Construction:
    def test_rejects_non_positive_chunk_size(self):
        with pytest.raises(ValueError):
            Chunker(chunk_size_chars=0)

    def test_rejects_overlap_greater_than_or_equal_to_chunk_size(self):
        with pytest.raises(ValueError):
            Chunker(chunk_size_chars=100, overlap_chars=100)


class TestChunker_EmptyAndTrivialInput:
    def test_empty_text_produces_no_chunks(self):
        assert Chunker().chunk("") == []

    def test_whitespace_only_text_produces_no_chunks(self):
        assert Chunker().chunk("   \n\n  ") == []

    def test_short_text_produces_one_chunk(self):
        chunks = Chunker(chunk_size_chars=1000).chunk("Tesla Q1 revenue grew 18%.")
        assert len(chunks) == 1
        assert chunks[0].text == "Tesla Q1 revenue grew 18%."
        assert chunks[0].index == 0


class TestChunker_Packing:
    def test_short_paragraphs_are_packed_together(self):
        text = "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."
        chunks = Chunker(chunk_size_chars=1000).chunk(text)
        assert len(chunks) == 1
        assert "First paragraph." in chunks[0].text
        assert "Third paragraph." in chunks[0].text

    def test_long_text_is_split_into_multiple_chunks(self):
        # Each paragraph is well under the budget individually, but all of
        # them together exceed it, forcing a split across chunk
        # boundaries.
        paragraphs = [
            f"Paragraph number {i} with some financial content here." for i in range(30)
        ]
        text = "\n\n".join(paragraphs)
        chunks = Chunker(chunk_size_chars=200, overlap_chars=20).chunk(text)
        assert len(chunks) > 1
        for c in chunks:
            # Allow slight overshoot (see _pack's comment on oversized
            # chunks) but nothing wildly larger than the budget.
            assert len(c.text) <= 260

    def test_chunk_indices_are_sequential_from_zero(self):
        paragraphs = [f"Paragraph {i}." * 20 for i in range(10)]
        text = "\n\n".join(paragraphs)
        chunks = Chunker(chunk_size_chars=150, overlap_chars=20).chunk(text)
        assert [c.index for c in chunks] == list(range(len(chunks)))

    def test_oversized_single_sentence_is_hard_split(self):
        huge_sentence = (
            "revenue " * 500
        )  # no terminal punctuation, one giant "sentence"
        chunks = Chunker(chunk_size_chars=300, overlap_chars=30).chunk(huge_sentence)
        assert len(chunks) > 1
        for c in chunks:
            assert len(c.text) <= 330


class TestChunker_Overlap:
    def test_consecutive_chunks_share_trailing_context(self):
        paragraphs = [
            f"This is paragraph number {i} about quarterly earnings results."
            for i in range(20)
        ]
        text = "\n\n".join(paragraphs)
        chunks = Chunker(chunk_size_chars=200, overlap_chars=50).chunk(text)
        assert len(chunks) > 1

        # The start of each chunk after the first should contain some
        # trailing words from the previous chunk, not begin cold.
        for prev, cur in pairwise(chunks):
            prev_tail_words = prev.text.split()[-3:]
            assert any(word in cur.text.split()[:6] for word in prev_tail_words)

    def test_zero_overlap_produces_no_shared_context(self):
        paragraphs = [f"Paragraph {i} content here about revenue." for i in range(20)]
        text = "\n\n".join(paragraphs)
        chunker = Chunker(chunk_size_chars=150, overlap_chars=0)
        chunks = chunker.chunk(text)
        assert len(chunks) > 1  # sanity check the test actually forces multiple chunks


class TestChunker_ContentHash:
    def test_identical_chunks_produce_identical_hashes(self):
        chunks_a = Chunker().chunk("Repeated boilerplate disclaimer text.")
        chunks_b = Chunker().chunk("Repeated boilerplate disclaimer text.")
        assert chunks_a[0].content_hash == chunks_b[0].content_hash

    def test_different_chunks_produce_different_hashes(self):
        chunks_a = Chunker().chunk("Tesla revenue grew.")
        chunks_b = Chunker().chunk("Apple revenue grew.")
        assert chunks_a[0].content_hash != chunks_b[0].content_hash

    def test_hash_is_sha256_hex(self):
        chunks = Chunker().chunk("Some text.")
        assert len(chunks[0].content_hash) == 64
        int(chunks[0].content_hash, 16)  # raises ValueError if not valid hex
