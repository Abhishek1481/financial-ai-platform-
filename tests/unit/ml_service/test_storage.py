from __future__ import annotations

from pathlib import Path

import pytest

from app.storage import LocalFileObjectReader, ObjectNotFoundError, get_reader


class TestLocalFileObjectReader:
    def test_reads_existing_file(self, tmp_path: Path):
        target = tmp_path / "doc.txt"
        target.write_bytes(b"hello from disk")

        content = LocalFileObjectReader().read(target.as_uri())

        assert content == b"hello from disk"

    def test_missing_file_raises(self, tmp_path: Path):
        missing = tmp_path / "does-not-exist.txt"
        with pytest.raises(ObjectNotFoundError):
            LocalFileObjectReader().read(missing.as_uri())


class TestGetReader:
    def test_file_scheme_returns_local_reader(self):
        reader = get_reader("file:///some/path.txt")
        assert isinstance(reader, LocalFileObjectReader)

    def test_unregistered_scheme_raises_not_implemented(self):
        with pytest.raises(NotImplementedError):
            get_reader("s3://bucket/key.txt")
