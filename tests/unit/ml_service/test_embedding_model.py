"""Unit tests for EmbeddingModel using the real sentence-transformers model
(downloaded once, cached by huggingface_hub afterward) — not a mock, for
the same reason test_extraction.py exercises real pypdf/python-docx rather
than stubbing them out.
"""

from __future__ import annotations

import numpy as np
import pytest

from app.embeddings.model import EmbeddingModel


class TestEmbeddingModel:
    def test_dimension_matches_known_model_output(self):
        model = EmbeddingModel()
        assert model.dimension == 384  # all-MiniLM-L6-v2's known dimension

    def test_embed_returns_correct_shape(self):
        model = EmbeddingModel()
        vectors = model.embed(["Tesla revenue grew.", "Apple revenue declined."])
        assert vectors.shape == (2, 384)
        assert vectors.dtype == np.float32

    def test_embed_vectors_are_l2_normalized(self):
        model = EmbeddingModel()
        vectors = model.embed(["Some arbitrary financial text about revenue growth."])
        norm = np.linalg.norm(vectors[0])
        assert norm == pytest.approx(1.0, abs=1e-4)

    def test_embed_empty_list_returns_empty_array_with_correct_dimension(self):
        model = EmbeddingModel()
        vectors = model.embed([])
        assert vectors.shape == (0, 384)

    def test_similar_texts_have_higher_cosine_similarity_than_dissimilar(self):
        model = EmbeddingModel()
        vectors = model.embed(
            [
                "Tesla's automotive revenue grew significantly this quarter.",
                "Tesla's car sales revenue increased substantially in Q1.",
                "The weather in Paris was sunny and warm yesterday.",
            ]
        )
        # Vectors are normalized, so dot product IS cosine similarity.
        sim_related = float(np.dot(vectors[0], vectors[1]))
        sim_unrelated = float(np.dot(vectors[0], vectors[2]))
        assert sim_related > sim_unrelated

    def test_model_loads_lazily_not_at_construction(self):
        model = EmbeddingModel()
        assert model._model is None  # not loaded yet
        model.load()
        assert model._model is not None
