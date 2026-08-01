"""Puts the generated gRPC stubs (proto/gen/python) on sys.path.

The Docker image sets PYTHONPATH explicitly (see docker/ml-service.Dockerfile,
Phase 16), so this only matters for local development: it lets
`python -m app.server` work from a fresh checkout without every developer
having to export PYTHONPATH by hand. Import this before importing any
generated `*_pb2` / `*_pb2_grpc` module.
"""

from __future__ import annotations

import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parents[2]
_GEN_PYTHON = _REPO_ROOT / "proto" / "gen" / "python"

if _GEN_PYTHON.is_dir() and str(_GEN_PYTHON) not in sys.path:
    sys.path.insert(0, str(_GEN_PYTHON))
