"""Best-effort metadata inference for SEC filings, applied to whatever text
an HTML/TXT extractor already produced.

Scope is deliberately narrow: filing-type detection (e.g. "FORM 10-K") is a
handful of well-known, consistently-worded phrases and is reliable enough
to ship. Ticker and company-name extraction from free text is a much
harder problem — EDGAR filings format this information inconsistently
enough that a regex heuristic would be wrong often enough to be worse than
returning nothing. Those fields are left for a caller-supplied override
(the upload request can carry known metadata) or a future proper NER-based
pass, not guessed at here.
"""

from __future__ import annotations

import re

from app.extraction.models import InferredMetadata

_FILING_TYPE_PATTERNS: list[tuple[re.Pattern[str], str]] = [
    (re.compile(r"\bform\s+10-k\b", re.IGNORECASE), "10-K"),
    (re.compile(r"\bform\s+10-q\b", re.IGNORECASE), "10-Q"),
    (re.compile(r"\bform\s+8-k\b", re.IGNORECASE), "8-K"),
    (re.compile(r"\bform\s+s-1\b", re.IGNORECASE), "S-1"),
    (re.compile(r"\bform\s+def\s*14a\b", re.IGNORECASE), "DEF 14A"),
]

# Only searched within the first few KB: filing-type declarations appear on
# an EDGAR cover page, not buried in exhibit text — searching the whole
# document risks matching an incidental mention deep in boilerplate.
_SEARCH_WINDOW_CHARS = 4000


def infer_sec_metadata(raw_text: str) -> InferredMetadata:
    window = raw_text[:_SEARCH_WINDOW_CHARS]

    filing_type = ""
    for pattern, label in _FILING_TYPE_PATTERNS:
        if pattern.search(window):
            filing_type = label
            break

    return InferredMetadata(filing_type=filing_type)
