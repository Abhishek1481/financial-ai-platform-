from __future__ import annotations

import io
import re

import pypdf

from app.extraction.base import ExtractionError
from app.extraction.models import ExtractionResult

# Table extraction from PDF is deliberately not implemented. PDF has no
# structural concept of a table — camelot/pdfplumber-style detection works
# by inferring grid layout from character positions, which is a real
# computer-vision-adjacent problem, not a parsing one, and pypdf (a pure
# reader/writer library) doesn't attempt it. Returning an empty tables list
# here is honest about that; faking table detection with a brittle
# heuristic would be worse than admitting the gap.


class PdfExtractor:
    def extract(self, raw_bytes: bytes) -> ExtractionResult:
        try:
            reader = pypdf.PdfReader(io.BytesIO(raw_bytes))
        except Exception as exc:
            raise ExtractionError(f"could not parse PDF file: {exc}") from exc

        if reader.is_encrypted:
            raise ExtractionError("PDF is password-protected; cannot extract text")

        pages_text = []
        for page in reader.pages:
            try:
                pages_text.append(page.extract_text() or "")
            except Exception as exc:
                raise ExtractionError(f"failed to extract text from PDF page: {exc}") from exc

        text = "\n\n".join(pages_text)
        text = re.sub(r"\n{3,}", "\n\n", text).strip()

        return ExtractionResult(raw_text=text, tables=[], page_count=len(reader.pages))
