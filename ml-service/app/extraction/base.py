"""The Extractor protocol every format implementation satisfies.

A Protocol rather than an ABC: extractors need no shared state or base-class
behavior, only a matching method signature, so structural typing is the
whole contract — nothing to inherit, nothing a new extractor can get wrong
by forgetting to call super().__init__().
"""

from __future__ import annotations

from typing import Protocol

from app.extraction.models import ExtractionResult


class Extractor(Protocol):
    def extract(self, raw_bytes: bytes) -> ExtractionResult: ...


class ExtractionError(Exception):
    """Raised when a document can't be parsed as its declared format —
    e.g. bytes claiming to be a PDF that aren't. Caught once at the
    servicer boundary and mapped to a gRPC INVALID_ARGUMENT/INTERNAL
    status; extractors don't need to know anything about gRPC."""
