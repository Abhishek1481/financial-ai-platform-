"""Reads raw document bytes given a storage URI.

gateway-go writes uploaded files to an ObjectStore (see
gateway-go/internal/storage) and hands ml-service a URI, not the bytes
themselves — this keeps large file payloads off the gRPC call entirely.
Today that store is local-filesystem-backed (file:// URIs); Phase 16 swaps
in S3-compatible storage (MinIO in Docker Compose, real S3 in the AWS
deployment target) behind an s3:// scheme. get_reader dispatches on the URI
scheme so ExtractDocument never needs to change when that swap happens —
only a new ObjectReader implementation gets registered here.
"""

from __future__ import annotations

from pathlib import Path
from typing import Protocol
from urllib.parse import urlparse
from urllib.request import url2pathname


class ObjectReader(Protocol):
    def read(self, uri: str) -> bytes: ...


class ObjectNotFoundError(Exception):
    pass


class LocalFileObjectReader:
    """Reads file:// URIs — the dev/test stand-in until Phase 16 wires up
    real object storage. Mirrors gateway-go's LocalObjectStore, which
    writes to the same convention (file://<absolute-path>)."""

    def read(self, uri: str) -> bytes:
        parsed = urlparse(uri)
        # url2pathname is platform-aware (uses nturl2path on Windows) — it
        # both un-percent-encodes the path (spaces show up as %20 in
        # Path.as_uri() output) and correctly turns "/C:/Users/..." into
        # "C:\Users\...", which naive string slicing gets wrong as soon as
        # a path contains a character requiring escaping.
        path = Path(url2pathname(parsed.path))

        if not path.is_file():
            raise ObjectNotFoundError(f"no such file: {path}")
        return path.read_bytes()


_READERS: dict[str, ObjectReader] = {
    "file": LocalFileObjectReader(),
}


def get_reader(uri: str) -> ObjectReader:
    scheme = urlparse(uri).scheme
    try:
        return _READERS[scheme]
    except KeyError as exc:
        raise NotImplementedError(
            f"no ObjectReader registered for URI scheme {scheme!r} (uri={uri!r})"
        ) from exc
