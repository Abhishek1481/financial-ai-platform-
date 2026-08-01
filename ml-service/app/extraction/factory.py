"""Maps a proto DocumentType to the Extractor that handles it.

A plain dict rather than a chain of if/elif: adding a format later is one
new entry here plus one new module, and get_extractor can't accidentally
fall through to a wrong default the way an if/elif missing a final `else`
can.
"""

from __future__ import annotations

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.extraction.base import Extractor
from app.extraction.docx import DocxExtractor
from app.extraction.html import HtmlExtractor
from app.extraction.pdf import PdfExtractor
from app.extraction.txt import TxtExtractor
from common.v1 import common_pb2

# SEC_FILING documents are, at the byte level, either HTML or plain text —
# EDGAR doesn't define a distinct file format for "this is a filing." The
# HTML extractor handles both cases: HtmlExtractor's HTML parser passes
# plain text straight through as ordinary content with no tags to strip.
_EXTRACTORS: dict[int, Extractor] = {
    common_pb2.DOCUMENT_TYPE_PDF: PdfExtractor(),
    common_pb2.DOCUMENT_TYPE_DOCX: DocxExtractor(),
    common_pb2.DOCUMENT_TYPE_HTML: HtmlExtractor(),
    common_pb2.DOCUMENT_TYPE_TXT: TxtExtractor(),
    common_pb2.DOCUMENT_TYPE_SEC_FILING: HtmlExtractor(),
}


def get_extractor(doc_type: int) -> Extractor:
    try:
        return _EXTRACTORS[doc_type]
    except KeyError as exc:
        raise ValueError(f"unsupported document type: {doc_type}") from exc
