from __future__ import annotations

from app.extraction.models import ExtractionResult


class TxtExtractor:
    def extract(self, raw_bytes: bytes) -> ExtractionResult:
        try:
            text = raw_bytes.decode("utf-8")
        except UnicodeDecodeError:
            # latin-1 maps every byte 0-255 to a character, so this can
            # never itself raise — it's a deliberate lossy fallback for the
            # plenty of real-world .txt exports (older EDGAR filings
            # especially) that are Latin-1, not UTF-8, not a second chance
            # at failure. There is no ExtractionError case for TxtExtractor:
            # any byte sequence is decodable as text.
            text = raw_bytes.decode("latin-1")

        return ExtractionResult(raw_text=text, page_count=1)
