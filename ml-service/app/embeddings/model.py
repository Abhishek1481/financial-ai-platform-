"""Wraps a sentence-transformers model for text embedding.

Two things make this safe to call from an async gRPC handler:

1. Loading is lazy — model weights are loaded from disk (or downloaded, on
   first run) on first use, not at import time or server startup, so a
   slow first load doesn't block the whole server from binding its port
   and answering health checks.
2. Both loading and encoding are CPU-bound, synchronous, blocking calls
   into native code — run directly inside an async method, either one
   would freeze the entire event loop (every other in-flight gRPC call,
   including health checks) for its whole duration. Callers must run
   `load()`/`embed()` via `loop.run_in_executor(...)`, not await them
   directly; this class does not do it itself so it stays plain,
   synchronous, and unit-testable without an event loop at all.
"""

from __future__ import annotations

import threading

import numpy as np
from sentence_transformers import SentenceTransformer

DEFAULT_MODEL_NAME = "sentence-transformers/all-MiniLM-L6-v2"


class EmbeddingModel:
    def __init__(self, model_name: str = DEFAULT_MODEL_NAME) -> None:
        self._model_name = model_name
        self._model: SentenceTransformer | None = None
        self._lock = threading.Lock()

    def load(self) -> None:
        """Forces the model to load now rather than on first embed() call.
        Synchronous and blocking — see module docstring."""
        if self._model is not None:
            return
        with self._lock:
            if self._model is None:
                self._model = SentenceTransformer(self._model_name)

    @property
    def dimension(self) -> int:
        self.load()
        assert self._model is not None
        dim = self._model.get_embedding_dimension()
        if dim is None:
            # sentence-transformers' return type allows None for models
            # whose output dimension isn't statically known; every model
            # this platform actually configures has one, so treat it
            # missing as a misconfiguration, not a value to silently pass
            # along.
            raise RuntimeError(f"model {self._model_name!r} does not report an embedding dimension")
        return dim

    def embed(self, texts: list[str]) -> np.ndarray:
        """Returns an (len(texts), dimension) float32 array of L2-normalized
        embeddings — normalized so cosine similarity search can use a plain
        inner product (FAISS IndexFlatIP) instead of a separate distance
        metric. Synchronous and blocking — see module docstring."""
        if not texts:
            return np.empty((0, self.dimension), dtype=np.float32)
        self.load()
        assert self._model is not None
        vectors = self._model.encode(texts, convert_to_numpy=True, normalize_embeddings=True)
        return vectors.astype(np.float32)
