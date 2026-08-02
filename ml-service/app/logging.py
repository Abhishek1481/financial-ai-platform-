"""Structured (JSON) logging configuration.

Every log line is a JSON object so it can be ingested by a log aggregator
(CloudWatch, Loki, etc.) without a parsing layer in front of it — plain-text
log lines are fine on a laptop and a liability in production. Kept on the
stdlib logging module (no extra dependency) — Phase 14 adds request-ID log
correlation (see app/tracing.py) on top of this rather than pulling in a
full OpenTelemetry SDK.
"""

from __future__ import annotations

import json
import logging
import sys
from datetime import UTC, datetime
from typing import Any

from app.tracing import RequestIDLogFilter

_RESERVED_LOG_RECORD_ATTRS = frozenset(logging.LogRecord("", 0, "", 0, "", (), None).__dict__)


class JSONFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        payload: dict[str, Any] = {
            "timestamp": datetime.fromtimestamp(record.created, tz=UTC).isoformat(),
            "level": record.levelname,
            "logger": record.name,
            "message": record.getMessage(),
        }
        # Anything passed via `extra={...}` rides along as top-level fields.
        for key, value in record.__dict__.items():
            if key not in _RESERVED_LOG_RECORD_ATTRS:
                payload[key] = value
        if record.exc_info:
            payload["exception"] = self.formatException(record.exc_info)
        return json.dumps(payload, default=str)


def configure_logging(level: str = "INFO") -> None:
    handler = logging.StreamHandler(stream=sys.stdout)
    handler.setFormatter(JSONFormatter())
    handler.addFilter(RequestIDLogFilter())

    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(level.upper())


def get_logger(name: str) -> logging.Logger:
    return logging.getLogger(name)
