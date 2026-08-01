from __future__ import annotations

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.vector_store import ChunkRecord
from app.search.filter import build_filter
from common.v1 import common_pb2


def make_chunk(**metadata: str) -> ChunkRecord:
    return ChunkRecord(
        chunk_id="c1", document_id="doc-1", text="text", chunk_index=0, content_hash="h1", metadata=metadata
    )


class TestBuildFilter:
    def test_no_filter_matches_everything(self):
        matches = build_filter(None)
        assert matches(make_chunk()) is True
        assert matches(make_chunk(ticker="AAPL")) is True

    def test_ticker_filter_matches_only_listed_tickers(self):
        matches = build_filter(common_pb2.MetadataFilter(tickers=["AAPL", "TSLA"]))
        assert matches(make_chunk(ticker="AAPL")) is True
        assert matches(make_chunk(ticker="MSFT")) is False
        assert matches(make_chunk()) is False  # no ticker at all

    def test_filing_type_filter(self):
        matches = build_filter(common_pb2.MetadataFilter(filing_types=["10-K"]))
        assert matches(make_chunk(filing_type="10-K")) is True
        assert matches(make_chunk(filing_type="10-Q")) is False

    def test_fiscal_period_filter(self):
        matches = build_filter(common_pb2.MetadataFilter(fiscal_period="FY2025-Q1"))
        assert matches(make_chunk(fiscal_period="FY2025-Q1")) is True
        assert matches(make_chunk(fiscal_period="FY2025-Q2")) is False

    def test_combined_filters_are_all_required(self):
        matches = build_filter(common_pb2.MetadataFilter(tickers=["AAPL"], filing_types=["10-K"]))
        assert matches(make_chunk(ticker="AAPL", filing_type="10-K")) is True
        assert matches(make_chunk(ticker="AAPL", filing_type="10-Q")) is False

    def test_empty_filter_message_matches_everything(self):
        matches = build_filter(common_pb2.MetadataFilter())
        assert matches(make_chunk(ticker="AAPL")) is True
