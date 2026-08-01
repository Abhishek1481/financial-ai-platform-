"""EvaluationService — real implementation lands in Phase 12."""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from evaluation.v1 import evaluation_pb2, evaluation_pb2_grpc


class EvaluationServicer(evaluation_pb2_grpc.EvaluationServiceServicer):
    async def EvaluateAnswer(
        self,
        request: evaluation_pb2.EvaluateAnswerRequest,
        context: grpc.aio.ServicerContext,
    ) -> evaluation_pb2.EvaluateAnswerResponse:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "EvaluationService.EvaluateAnswer is not implemented yet (Phase 12: Model evaluation).",
        )

    async def BatchEvaluate(
        self,
        request_iterator: AsyncIterator[evaluation_pb2.EvaluateAnswerRequest],
        context: grpc.aio.ServicerContext,
    ) -> evaluation_pb2.BatchEvaluateResponse:
        await context.abort(
            grpc.StatusCode.UNIMPLEMENTED,
            "EvaluationService.BatchEvaluate is not implemented yet (Phase 12: Model evaluation).",
        )
