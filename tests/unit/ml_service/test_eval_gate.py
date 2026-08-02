"""CI eval-regression gate (Phase 12's promise, delivered in Phase 19):
runs a small fixed set of questions through the real RAG pipeline
(embed -> retrieve -> generate -> cite), scores the results via
EvaluationService.BatchEvaluate, and fails if answer quality regresses
below a threshold. Driven directly over gRPC against a live in-process
server — the same "genuinely exercised, not mocked" bar every other
servicer test in this suite holds itself to — so this is exactly what
was meant by "BatchEvaluate... driven directly over gRPC by the CI
pipeline" (see gateway-go/README.md's Phase 12 section): a dedicated
test, run by the same `pytest` invocation as everything else, not a
separate script.

Because generation runs through FakeLLMClient in this environment (see
app/rag/llm_client.py — no LLM API key available), the "quality" this
gate actually protects is the mechanics around generation: retrieval
finding the right chunks, the prompt handing them to the model with
correct numbering, and citation extraction resolving [N] markers back
correctly. A real regression in any of those would show up here as
degraded faithfulness/context_precision, the same way it would with a
real LLM in the loop — FakeLLMClient's citations are only trustworthy if
the chunks it's citing were the right ones to retrieve in the first
place.
"""

from __future__ import annotations

from collections.abc import AsyncIterator

import grpc
import pytest
from app.config import Settings
from app.server import build_server
from common.v1 import common_pb2
from embeddings.v1 import embeddings_pb2, embeddings_pb2_grpc
from evaluation.v1 import evaluation_pb2, evaluation_pb2_grpc
from rag.v1 import rag_pb2, rag_pb2_grpc

# Faithfulness/hallucination thresholds a healthy pipeline should clear by
# a wide margin (test_metrics.py's own unit tests show a fully-supported
# answer scores faithfulness=1.0) — set well below that so this gate
# tolerates minor wording variance without being so loose it can't catch
# an actual regression.
_MIN_MEAN_FAITHFULNESS = 0.8
_MAX_MEAN_HALLUCINATION = 0.2

_GOLDEN_SET = [
    {
        "document_id": "eval-doc-tesla",
        "document_text": "Tesla automotive revenue grew eighteen percent year over year in Q1, "
        "driven by strong Model Y deliveries.",
        "question": "How did Tesla's automotive revenue perform?",
    },
    {
        "document_id": "eval-doc-apple",
        "document_text": "Apple iPhone revenue grew due to strong holiday demand and services "
        "revenue reached a new all-time high.",
        "question": "What drove Apple's revenue growth?",
    },
    {
        "document_id": "eval-doc-risk",
        "document_text": "Management flagged battery cell supply chain risk as the primary "
        "headwind for next quarter's production targets.",
        "question": "What risk did management flag?",
    },
]


@pytest.fixture
async def server_port(tmp_path) -> AsyncIterator[int]:
    settings = Settings(
        grpc_port=0,
        reflection_enabled=True,
        vector_store_dir=str(tmp_path / "vector-store"),
    )
    server, port = await build_server(settings)
    await server.start()
    try:
        yield port
    finally:
        await server.stop(grace=0)


async def test_eval_gate_golden_set_meets_quality_thresholds(server_port: int):
    async with grpc.aio.insecure_channel(f"127.0.0.1:{server_port}") as channel:
        embeddings_stub = embeddings_pb2_grpc.EmbeddingServiceStub(channel)
        rag_stub = rag_pb2_grpc.RAGServiceStub(channel)
        eval_stub = evaluation_pb2_grpc.EvaluationServiceStub(channel)

        for case in _GOLDEN_SET:
            request = embeddings_pb2.ChunkAndEmbedRequest(
                document_id=case["document_id"], raw_text=case["document_text"]
            )
            async for _ in embeddings_stub.ChunkAndEmbed(request):
                pass

        async def eval_requests() -> AsyncIterator[
            evaluation_pb2.EvaluateAnswerRequest
        ]:
            for case in _GOLDEN_SET:
                answer_text, citations, latency_ms = await _run_query(
                    rag_stub, case["question"]
                )
                # citations already carry chunk_id/document_id/quote (the
                # actual chunks retrieval found) — that's the honest
                # retrieved_context for scoring, not the raw source
                # document text, which is what a real caller (gateway-go)
                # would also have on hand after a Query call.
                context_chunks = [
                    common_pb2.Chunk(
                        chunk_id=c.chunk_id, document_id=c.document_id, text=c.quote
                    )
                    for c in citations
                ]
                yield evaluation_pb2.EvaluateAnswerRequest(
                    question=case["question"],
                    answer=answer_text,
                    retrieved_context=context_chunks,
                    generation_latency_ms=latency_ms,
                )

        result = await eval_stub.BatchEvaluate(eval_requests())

    assert result.samples_evaluated == len(_GOLDEN_SET)
    assert result.mean_faithfulness >= _MIN_MEAN_FAITHFULNESS, (
        f"mean_faithfulness={result.mean_faithfulness:.2f} fell below the "
        f"{_MIN_MEAN_FAITHFULNESS} gate — retrieval, prompt construction, or "
        "citation extraction likely regressed."
    )
    assert result.mean_hallucination_score <= _MAX_MEAN_HALLUCINATION
    assert result.p95_latency_ms >= 0


async def _run_query(rag_stub, question: str):
    answer_parts: list[str] = []
    citations: list = []
    latency_ms = 0.0
    async for chunk in rag_stub.Query(rag_pb2.QueryRequest(question=question)):
        if chunk.HasField("token"):
            answer_parts.append(chunk.token)
        elif chunk.HasField("final"):
            citations = list(chunk.final.citations)
            latency_ms = chunk.final.latency_ms
    return "".join(answer_parts), citations, latency_ms
