"""RAGService.Query: retrieval -> prompt construction -> streamed LLM call ->
citation extraction. RAGService.Summarize: same LLM/citation machinery, but
over every chunk of one document instead of a top-k retrieval slice, and
unary — a summary has no "watch it stream" UX requirement the way an
interactive answer does, so the whole text is generated before responding.

Query's retrieval always runs hybrid (vector + keyword, fused) — unlike
SearchService.Search, callers here have no reason to pick a narrower mode;
RAG wants the best available context, not a mode toggle.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import AsyncIterator

import grpc

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.model import EmbeddingModel
from app.embeddings.vector_store import ChunkRecord, VectorStore
from app.rag.citations import build_citations
from app.rag.llm_client import LLMClient, LLMFinal, LLMMessage, LLMToken, LLMUsage
from app.rag.prompt import build_messages, build_summarize_messages
from app.search.keyword_index import KeywordIndex
from app.search.retrieval import DEFAULT_TOP_K, retrieve
from common.v1 import common_pb2
from rag.v1 import rag_pb2, rag_pb2_grpc
from search.v1 import search_pb2


class RAGServicer(rag_pb2_grpc.RAGServiceServicer):
    def __init__(
        self,
        model: EmbeddingModel,
        store: VectorStore,
        keyword_index: KeywordIndex,
        llm_client: LLMClient,
        temperature: float,
        max_tokens: int,
    ) -> None:
        self._model = model
        self._store = store
        self._keyword_index = keyword_index
        self._llm_client = llm_client
        self._temperature = temperature
        self._max_tokens = max_tokens

    async def Query(
        self,
        request: rag_pb2.QueryRequest,
        context: grpc.aio.ServicerContext,
    ) -> AsyncIterator[rag_pb2.QueryResponseChunk]:
        start = time.monotonic()

        hits = await retrieve(
            model=self._model,
            store=self._store,
            keyword_index=self._keyword_index,
            query=request.question,
            top_k=request.top_k or DEFAULT_TOP_K,
            mode=search_pb2.SEARCH_MODE_HYBRID,
            metadata_filter=request.filter if request.HasField("filter") else None,
        )
        context_chunks = [hit.chunk for hit in hits]
        messages = build_messages(request.question, list(request.history), context_chunks)

        generated_parts: list[str] = []
        usage = LLMUsage(prompt_tokens=0, completion_tokens=0, total_tokens=0)
        async for event in self._llm_client.stream(
            messages, temperature=self._temperature, max_tokens=self._max_tokens
        ):
            if isinstance(event, LLMToken):
                generated_parts.append(event.text)
                yield rag_pb2.QueryResponseChunk(token=event.text)
            elif isinstance(event, LLMFinal):
                usage = event.usage

        citations = build_citations("".join(generated_parts), context_chunks)
        latency_ms = (time.monotonic() - start) * 1000

        yield rag_pb2.QueryResponseChunk(
            final=rag_pb2.QueryFinal(
                citations=citations,
                usage=common_pb2.TokenUsage(
                    prompt_tokens=usage.prompt_tokens,
                    completion_tokens=usage.completion_tokens,
                    total_tokens=usage.total_tokens,
                ),
                latency_ms=latency_ms,
            )
        )

    async def Summarize(
        self,
        request: rag_pb2.SummarizeRequest,
        context: grpc.aio.ServicerContext,
    ) -> rag_pb2.SummarizeResponse:
        start = time.monotonic()
        loop = asyncio.get_running_loop()

        context_chunks: list[ChunkRecord] = await loop.run_in_executor(
            None, self._store.get_by_document_id, request.document_id
        )
        if not context_chunks:
            await context.abort(
                grpc.StatusCode.NOT_FOUND,
                f"document {request.document_id!r} has no embedded chunks",
            )
            return

        messages = build_summarize_messages(request.type, context_chunks)
        generated_text, usage = await self._run_llm_to_completion(messages)
        citations = build_citations(generated_text, context_chunks)
        latency_ms = (time.monotonic() - start) * 1000

        return rag_pb2.SummarizeResponse(
            summary=generated_text,
            citations=citations,
            usage=common_pb2.TokenUsage(
                prompt_tokens=usage.prompt_tokens,
                completion_tokens=usage.completion_tokens,
                total_tokens=usage.total_tokens,
            ),
            latency_ms=latency_ms,
        )

    async def _run_llm_to_completion(self, messages: list[LLMMessage]) -> tuple[str, LLMUsage]:
        generated_parts: list[str] = []
        usage = LLMUsage(prompt_tokens=0, completion_tokens=0, total_tokens=0)
        async for event in self._llm_client.stream(
            messages, temperature=self._temperature, max_tokens=self._max_tokens
        ):
            if isinstance(event, LLMToken):
                generated_parts.append(event.text)
            elif isinstance(event, LLMFinal):
                usage = event.usage
        return "".join(generated_parts), usage
