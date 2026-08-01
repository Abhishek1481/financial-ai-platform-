"""Post-retrieval metadata filtering.

Applied after vector/keyword search, not as a pre-filter into either index
— FAISS's IndexFlatIP is already an exact brute-force scan (no ANN
structure to preserve), so over-fetching a pool and filtering after is
simpler than threading filter predicates through two different retrieval
engines, at dev-scale data volumes.

filed_after/filed_before are accepted on the wire (proto/common/v1's
MetadataFilter) but not applied here: no stage in the pipeline currently
populates a per-chunk filed_at (see ml-service/README.md's extraction
section on what SEC metadata inference does and doesn't attempt) — a
filter with nothing to compare against would either match everything or
nothing, both misleading. Wiring it up is future work once filing dates
are actually extracted.
"""

from __future__ import annotations

from collections.abc import Callable

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.vector_store import ChunkRecord
from common.v1 import common_pb2


def build_filter(
    metadata_filter: common_pb2.MetadataFilter | None,
) -> Callable[[ChunkRecord], bool]:
    if metadata_filter is None:
        return lambda _chunk: True

    tickers = set(metadata_filter.tickers)
    filing_types = set(metadata_filter.filing_types)
    fiscal_period = metadata_filter.fiscal_period

    def matches(chunk: ChunkRecord) -> bool:
        if tickers and chunk.metadata.get("ticker", "") not in tickers:
            return False
        if filing_types and chunk.metadata.get("filing_type", "") not in filing_types:
            return False
        return not (fiscal_period and chunk.metadata.get("fiscal_period", "") != fiscal_period)

    return matches
