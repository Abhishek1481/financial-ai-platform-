"""Cross-cutting gRPC server observability — Prometheus metrics and
request-ID log correlation (see app/tracing.py) — applied uniformly to
every RPC via a single grpc.aio.ServerInterceptor, rather than duplicated
by hand inside each servicer method. Handles all four RPC shapes
(unary-unary, unary-stream, stream-unary, stream-stream) since this
service's own RPCs span all of them (Query is unary-stream, BatchEvaluate
is stream-unary, everything else is unary-unary).
"""

from __future__ import annotations

import time
import uuid
from collections.abc import AsyncIterator
from typing import Any

import grpc
from prometheus_client import Counter, Histogram

from app.tracing import reset_request_id, set_request_id

REQUEST_ID_METADATA_KEY = "x-request-id"

requests_total = Counter(
    "ml_service_grpc_requests_total",
    "Total gRPC requests handled, labeled by method and status code.",
    ["method", "status"],
)
request_duration = Histogram(
    "ml_service_grpc_request_duration_seconds",
    "gRPC request latency in seconds, labeled by method.",
    ["method"],
)


def _extract_or_generate_request_id(context: grpc.aio.ServicerContext) -> str:
    for key, value in context.invocation_metadata() or ():
        if key == REQUEST_ID_METADATA_KEY:
            return value
    return str(uuid.uuid4())  # this service was called directly, not via gateway-go


def _status_name(context: grpc.aio.ServicerContext) -> str:
    code = context.code()
    return code.name if code is not None else "OK"


class ObservabilityInterceptor(grpc.aio.ServerInterceptor):
    async def intercept_service(self, continuation, handler_call_details):
        handler = await continuation(handler_call_details)
        if handler is None:
            return None

        method = handler_call_details.method

        if handler.unary_unary is not None:
            inner = handler.unary_unary

            async def unary_unary(request: Any, context: grpc.aio.ServicerContext) -> Any:
                return await _instrument_unary(method, context, inner(request, context))

            return handler._replace(unary_unary=unary_unary)

        if handler.stream_unary is not None:
            inner = handler.stream_unary

            async def stream_unary(
                request_iterator: AsyncIterator[Any], context: grpc.aio.ServicerContext
            ) -> Any:
                return await _instrument_unary(method, context, inner(request_iterator, context))

            return handler._replace(stream_unary=stream_unary)

        if handler.unary_stream is not None:
            inner = handler.unary_stream

            async def unary_stream(
                request: Any, context: grpc.aio.ServicerContext
            ) -> AsyncIterator[Any]:
                async for response in _instrument_stream(method, context, inner(request, context)):
                    yield response

            return handler._replace(unary_stream=unary_stream)

        if handler.stream_stream is not None:
            inner = handler.stream_stream

            async def stream_stream(
                request_iterator: AsyncIterator[Any], context: grpc.aio.ServicerContext
            ) -> AsyncIterator[Any]:
                async for response in _instrument_stream(
                    method, context, inner(request_iterator, context)
                ):
                    yield response

            return handler._replace(stream_stream=stream_stream)

        return handler


async def _instrument_unary(method: str, context: grpc.aio.ServicerContext, awaitable: Any) -> Any:
    token = set_request_id(_extract_or_generate_request_id(context))
    start = time.monotonic()
    try:
        return await awaitable
    finally:
        request_duration.labels(method=method).observe(time.monotonic() - start)
        requests_total.labels(method=method, status=_status_name(context)).inc()
        reset_request_id(token)


async def _instrument_stream(
    method: str, context: grpc.aio.ServicerContext, stream: AsyncIterator[Any]
) -> AsyncIterator[Any]:
    token = set_request_id(_extract_or_generate_request_id(context))
    start = time.monotonic()
    try:
        async for item in stream:
            yield item
    finally:
        request_duration.labels(method=method).observe(time.monotonic() - start)
        requests_total.labels(method=method, status=_status_name(context)).inc()
        reset_request_id(token)
