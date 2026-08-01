"""Format-agnostic extraction result types.

Every extractor (txt/html/pdf/docx) returns an ExtractionResult — the
IngestionServicer maps this once to the proto response, so a new format
only ever has to implement Extractor.extract, never touch the gRPC layer.
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class ExtractedCell:
    value: str
    is_numeric: bool = False


@dataclass(frozen=True)
class ExtractedTable:
    headers: list[str]
    rows: list[list[ExtractedCell]]
    caption: str = ""
    page_number: int = 0


@dataclass(frozen=True)
class InferredMetadata:
    """Best-effort metadata pulled from the document's own content — never
    authoritative, always overridable by metadata the caller already has
    (e.g. from the upload request). Fields left empty simply weren't
    confidently inferable; see extraction/sec_metadata.py for what's
    actually implemented versus deliberately left as future work."""

    ticker: str = ""
    company_name: str = ""
    filing_type: str = ""
    fiscal_period: str = ""


@dataclass(frozen=True)
class ExtractionResult:
    raw_text: str
    tables: list[ExtractedTable] = field(default_factory=list)
    metadata: InferredMetadata = field(default_factory=InferredMetadata)
    page_count: int = 0
