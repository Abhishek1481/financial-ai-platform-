"""IngestionService — real implementation lands in Phase 6.

Registered now (not added later) so the server's set of RPCs, health check
entries, and reflection surface are stable from the first deploy — adding a
new *service* later is a bigger, riskier change than filling in a method
body that already exists.
"""

from __future__ import annotations

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from ingestion.v1 import ingestion_pb2, ingestion_pb2_grpc


class IngestionServicer(ingestion_pb2_grpc.IngestionServiceServicer):
    async def ExtractDocument(
        self,
        request: ingestion_pb2.ExtractDocumentRequest,
        context: grpc.aio.ServicerContext,
    ) -> ingestion_pb2.ExtractDocumentResponse:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "IngestionService.ExtractDocument is not implemented yet (Phase 6: Document ingestion).",
        )
