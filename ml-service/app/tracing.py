"""Request-ID log correlation across the gateway-go <-> ml-service gRPC
boundary — a deliberately scoped-down alternative to full OpenTelemetry
distributed tracing (spans, exporters, a collector) for this environment:
no tracing backend (Jaeger/Tempo) is deployed until Docker Compose lands
(Phase 16), and threading one shared ID into every structured log line on
both sides gets most of the practical debugging value (grep one request's
logs across both services) without that infrastructure dependency. Wiring
in a real OTel SDK later is additive, not a rewrite — this ID slots
directly into an OTel trace_id field.

gateway-go generates the ID per HTTP request (internal/reqid) and sends it
as the "x-request-id" gRPC metadata key (see internal/mlclient); this
module is ml-service's side of that: extracting it (app/observability.py)
and making it available to every log line emitted while handling that RPC.
"""

from __future__ import annotations

import logging
from contextvars import ContextVar, Token

_request_id: ContextVar[str | None] = ContextVar("request_id", default=None)


def get_request_id() -> str | None:
    return _request_id.get()


def set_request_id(value: str) -> Token[str | None]:
    return _request_id.set(value)


def reset_request_id(token: Token[str | None]) -> None:
    _request_id.reset(token)


class RequestIDLogFilter(logging.Filter):
    """Stamps every log record with the current request ID (or "-" outside
    any RPC's scope, e.g. startup/shutdown logs) — attach once to the root
    handler (see app/logging.py) so no call site needs `extra={...}`."""

    def filter(self, record: logging.LogRecord) -> bool:
        record.request_id = get_request_id() or "-"
        return True
