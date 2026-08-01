from __future__ import annotations

import re

from bs4 import BeautifulSoup, Tag

from app.extraction.models import ExtractedCell, ExtractedTable, ExtractionResult
from app.extraction.util import is_numeric_cell

# "html.parser" is the stdlib backend, not lxml — no compiled dependency,
# and forgiving enough for the real-world messy HTML (unclosed tags, etc.)
# that both hand-authored and EDGAR-generated filings tend to contain.
_PARSER = "html.parser"


class HtmlExtractor:
    def extract(self, raw_bytes: bytes) -> ExtractionResult:
        soup = BeautifulSoup(raw_bytes, _PARSER)

        # Script/style content isn't document text — leaving it in would
        # pollute both the plain-text output and (once Phase 7 chunks this
        # text for embedding) retrieval quality.
        for tag in soup(["script", "style"]):
            tag.decompose()

        tables = [self._extract_table(table) for table in soup.find_all("table")]

        # Table contents are pulled from the tree above before
        # get_text() so they aren't double-counted in the flat text, then
        # removed so the remaining prose doesn't repeat every cell inline.
        for table in soup.find_all("table"):
            table.decompose()

        text = soup.get_text(separator="\n")
        text = re.sub(r"\n{3,}", "\n\n", text).strip()

        return ExtractionResult(raw_text=text, tables=tables, page_count=1)

    def _extract_table(self, table: Tag) -> ExtractedTable:
        rows: list[list[ExtractedCell]] = []
        headers: list[str] = []

        for tr_index, tr in enumerate(table.find_all("tr")):
            cells = tr.find_all(["td", "th"])
            texts = [c.get_text(strip=True) for c in cells]
            if tr_index == 0 and tr.find("th") is not None:
                headers = texts
                continue
            rows.append([ExtractedCell(value=t, is_numeric=is_numeric_cell(t)) for t in texts])

        caption_tag = table.find("caption")
        caption = caption_tag.get_text(strip=True) if caption_tag else ""

        return ExtractedTable(headers=headers, rows=rows, caption=caption)
