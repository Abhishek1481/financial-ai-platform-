"""Builds a minimal, byte-offset-correct single-page PDF containing a line
of text — for testing PdfExtractor without a system PDF-writing tool.
pypdf can read PDFs but has no high-level text-drawing API to create one,
so the fixture is assembled by hand at the PDF-object level instead.
"""

from __future__ import annotations


def _escape(text: str) -> str:
    return text.replace("\\", r"\\").replace("(", r"\(").replace(")", r"\)")


def make_pdf_bytes(text: str = "Hello World") -> bytes:
    content = f"BT /F1 24 Tf 100 700 Td ({_escape(text)}) Tj ET".encode()

    objects = [
        b"<</Type/Catalog/Pages 2 0 R>>",
        b"<</Type/Pages/Kids[3 0 R]/Count 1>>",
        (
            b"<</Type/Page/Parent 2 0 R/Resources<</Font<</F1 5 0 R>>>>"
            b"/MediaBox[0 0 612 792]/Contents 4 0 R>>"
        ),
        b"<</Length "
        + str(len(content)).encode()
        + b">>\nstream\n"
        + content
        + b"\nendstream",
        b"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
    ]

    buf = bytearray(b"%PDF-1.4\n")
    offsets = [0]  # object 0 is the free-list head, unused
    for i, body in enumerate(objects, start=1):
        offsets.append(len(buf))
        buf += f"{i} 0 obj".encode() + body + b"\nendobj\n"

    xref_offset = len(buf)
    buf += f"xref\n0 {len(objects) + 1}\n".encode()
    buf += b"0000000000 65535 f \n"
    for off in offsets[1:]:
        buf += f"{off:010d} 00000 n \n".encode()
    buf += (
        f"trailer<</Size {len(objects) + 1}/Root 1 0 R>>\n"
        f"startxref\n{xref_offset}\n%%EOF"
    ).encode()

    return bytes(buf)
