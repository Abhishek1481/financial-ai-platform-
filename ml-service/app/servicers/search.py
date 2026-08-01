"""SearchService — real implementation lands in Phase 8."""

from __future__ import annotations

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from search.v1 import search_pb2, search_pb2_grpc


class SearchServicer(search_pb2_grpc.SearchServiceServicer):
    async def Search(
        self,
        request: search_pb2.SearchRequest,
        context: grpc.aio.ServicerContext,
    ) -> search_pb2.SearchResponse:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "SearchService.Search is not implemented yet (Phase 8: Semantic + hybrid search).",
        )
