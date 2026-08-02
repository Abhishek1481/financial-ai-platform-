"""End-to-end tests of EvaluationService against a live in-process gRPC
server — real scoring (app/evaluation/metrics.py), not mocked. See
test_metrics.py for exhaustive coverage of the scoring algorithm itself;
these tests exercise the gRPC plumbing around it (unary EvaluateAnswer,
client-streaming BatchEvaluate's aggregation).
"""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc
import pytest
from app.config import Settings
from app.server import build_server
from common.v1 import common_pb2
from evaluation.v1 import evaluation_pb2, evaluation_pb2_grpc


@pytest.fixture
async def server_port() -> AsyncIterator[int]:
    settings = Settings(grpc_port=0, reflection_enabled=True)
    server, port = await build_server(settings)
    await server.start()
    try:
        yield port
    finally:
        await server.stop(grace=0)


async def test_evaluate_answer_scores_a_supported_claim_highly(server_port: int):
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = evaluation_pb2_grpc.EvaluationServiceStub(channel)
        response = await stub.EvaluateAnswer(
            evaluation_pb2.EvaluateAnswerRequest(
                question="How did Tesla revenue perform?",
                answer="Tesla revenue grew eighteen percent year over year.",
                retrieved_context=[
                    common_pb2.Chunk(
                        chunk_id="c1",
                        text="Tesla automotive revenue grew eighteen percent year over year.",
                    )
                ],
            )
        )

    assert response.faithfulness == 1.0
    assert response.hallucination_score == 0.0
    assert response.context_precision == 1.0


async def test_evaluate_answer_with_no_ground_truth_has_zero_context_recall(
    server_port: int,
):
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = evaluation_pb2_grpc.EvaluationServiceStub(channel)
        response = await stub.EvaluateAnswer(
            evaluation_pb2.EvaluateAnswerRequest(question="q", answer="a")
        )

    assert response.context_recall == 0.0


async def test_batch_evaluate_returns_aggregate_statistics(server_port: int):
    async def requests() -> AsyncIterator[evaluation_pb2.EvaluateAnswerRequest]:
        for latency in (10.0, 20.0, 30.0):
            yield evaluation_pb2.EvaluateAnswerRequest(
                question="q",
                answer="Tesla revenue grew.",
                retrieved_context=[
                    common_pb2.Chunk(chunk_id="c1", text="Tesla revenue grew.")
                ],
                generation_latency_ms=latency,
            )

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = evaluation_pb2_grpc.EvaluationServiceStub(channel)
        response = await stub.BatchEvaluate(requests())

    assert response.samples_evaluated == 3
    assert response.mean_faithfulness == 1.0
    assert response.p50_latency_ms == 20.0
    assert response.p95_latency_ms == 30.0


async def test_batch_evaluate_on_empty_stream_returns_zero_samples(server_port: int):
    async def no_requests() -> AsyncIterator[evaluation_pb2.EvaluateAnswerRequest]:
        return
        yield  # pragma: no cover — makes this an async generator with no items

    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        stub = evaluation_pb2_grpc.EvaluationServiceStub(channel)
        response = await stub.BatchEvaluate(no_requests())

    assert response.samples_evaluated == 0
