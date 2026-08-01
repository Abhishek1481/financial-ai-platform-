from __future__ import annotations

import re

_NUMERIC_RE = re.compile(r"^[\(\-\$]?[\d,]+\.?\d*%?\)?$")


def is_numeric_cell(text: str) -> bool:
    """Best-effort check for whether a table cell holds a number — handles
    the financial-document conventions plain float() parsing would choke
    on: thousands separators, parenthesized negatives, leading $/%."""
    return bool(_NUMERIC_RE.match(text.strip()))
