"""EvaluationService: scores RAG answers against their retrieved context
via app/evaluation/metrics.py's lexical-overlap algorithm — see that
module's docstring for why this isn't LLM-judged in this environment.
"""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.evaluation.metrics import ScoreResult, percentile, score_answer
from evaluation.v1 import evaluation_pb2, evaluation_pb2_grpc


class EvaluationServicer(evaluation_pb2_grpc.EvaluationServiceServicer):
    async def EvaluateAnswer(
        self,
        request: evaluation_pb2.EvaluateAnswerRequest,
        context: grpc.aio.ServicerContext,
    ) -> evaluation_pb2.EvaluateAnswerResponse:
        result = await _score(request)
        return _to_response(result)

    async def BatchEvaluate(
        self,
        request_iterator: AsyncIterator[evaluation_pb2.EvaluateAnswerRequest],
        context: grpc.aio.ServicerContext,
    ) -> evaluation_pb2.BatchEvaluateResponse:
        results: list[ScoreResult] = []
        latencies: list[float] = []
        async for request in request_iterator:
            results.append(await _score(request))
            latencies.append(request.generation_latency_ms)

        if not results:
            return evaluation_pb2.BatchEvaluateResponse(samples_evaluated=0)

        return evaluation_pb2.BatchEvaluateResponse(
            samples_evaluated=len(results),
            mean_faithfulness=_mean(r.faithfulness for r in results),
            mean_context_precision=_mean(r.context_precision for r in results),
            mean_context_recall=_mean(r.context_recall for r in results),
            mean_hallucination_score=_mean(r.hallucination_score for r in results),
            mean_answer_relevancy=_mean(r.answer_relevancy for r in results),
            p50_latency_ms=percentile(latencies, 50),
            p95_latency_ms=percentile(latencies, 95),
        )


async def _score(request: evaluation_pb2.EvaluateAnswerRequest) -> ScoreResult:
    loop = asyncio.get_running_loop()
    context_texts = [chunk.text for chunk in request.retrieved_context]
    return await loop.run_in_executor(
        None,
        score_answer,
        request.question,
        request.answer,
        context_texts,
        request.ground_truth_answer,
    )


def _to_response(result: ScoreResult) -> evaluation_pb2.EvaluateAnswerResponse:
    return evaluation_pb2.EvaluateAnswerResponse(
        faithfulness=result.faithfulness,
        context_precision=result.context_precision,
        context_recall=result.context_recall,
        hallucination_score=result.hallucination_score,
        answer_relevancy=result.answer_relevancy,
    )


def _mean(values) -> float:
    values = list(values)
    return sum(values) / len(values) if values else 0.0
