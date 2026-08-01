"""LLMClient: the one seam between RAG's mechanics (retrieval, prompt
construction, citation extraction) and an actual model call.

Same Strategy pattern as `ingestion.Extractor`/`search.Searcher` on the Go
side and `VectorStore` here: a `Protocol` plus swappable implementations, so
`RAGServicer` never depends on a concrete provider SDK.

Three implementations:
- `FakeLLMClient` — deterministic, no network call. Used whenever
  `ML_SERVICE_LLM_PROVIDER=fake` (the default), which is the case in this
  dev environment since no OpenAI/Anthropic API key is configured. It still
  exercises the full pipeline (streaming token-by-token, numbered-citation
  markers in its output) so retrieval/prompt/citation-extraction mechanics
  are genuinely verified end-to-end, not skipped.
- `LangChainLLMClient` — wraps `ChatOpenAI`/`ChatAnthropic` via LangChain's
  async streaming (`.astream()`), accumulating `AIMessageChunk`s (which
  support `+`-merging, confirmed against the real library) to recover
  `usage_metadata` from the final merged chunk. Fully wired, just unverified
  against a live provider until an API key is supplied — dropping one into
  `ML_SERVICE_LLM_API_KEY` and setting `ML_SERVICE_LLM_PROVIDER` requires no
  code change.
- `build_llm_client(settings)` — the factory that picks between them.
"""

from __future__ import annotations

import asyncio
import re
from collections.abc import AsyncIterator
from dataclasses import dataclass
from typing import Protocol

from pydantic import SecretStr

from app.config import Settings

_CITATION_TOKEN_RE = re.compile(r"\[(\d+)\]")


@dataclass(frozen=True)
class LLMMessage:
    role: str  # "system" | "user" | "assistant"
    content: str


@dataclass(frozen=True)
class LLMUsage:
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int


@dataclass(frozen=True)
class LLMToken:
    text: str


@dataclass(frozen=True)
class LLMFinal:
    usage: LLMUsage


LLMStreamEvent = LLMToken | LLMFinal


class LLMClient(Protocol):
    def stream(
        self,
        messages: list[LLMMessage],
        *,
        temperature: float,
        max_tokens: int,
    ) -> AsyncIterator[LLMStreamEvent]:
        """Streams tokens as they're generated, followed by exactly one
        `LLMFinal` carrying usage. An `AsyncIterator`, not a coroutine
        returning a list, so `RAGServicer.Query` can forward tokens to the
        gRPC stream as they arrive rather than buffering the whole answer."""
        ...


class FakeLLMClient:
    """Deterministic stand-in for a real provider: no network call, no API
    key required. Synthesizes an answer that cites every context chunk it
    was given (`[1]`, `[2]`, ...) by pulling the "Context:" section back out
    of the user message, so `app/rag/citations.py`'s extraction logic has
    real `[N]` markers to parse in tests — this exercises the full
    retrieval -> prompt -> stream -> cite pipeline, not just the plumbing
    around an LLM call.
    """

    async def stream(
        self,
        messages: list[LLMMessage],
        *,
        temperature: float,
        max_tokens: int,
    ) -> AsyncIterator[LLMStreamEvent]:
        del temperature, max_tokens  # unused by the fake — no real sampling
        user_message = next((m.content for m in reversed(messages) if m.role == "user"), "")
        chunk_indices = sorted({int(n) for n in _CITATION_TOKEN_RE.findall(user_message)})

        if chunk_indices:
            citations = " ".join(f"[{i}]" for i in chunk_indices)
            answer = f"Based on the provided context {citations}, here is a synthesized answer."
        else:
            answer = "I don't have enough context to answer that question."

        words = answer.split(" ")
        completion_tokens = 0
        for i, word in enumerate(words):
            text = word if i == len(words) - 1 else word + " "
            yield LLMToken(text=text)
            completion_tokens += 1
            await asyncio.sleep(0)  # yield control, mirrors a real async stream

        prompt_tokens = sum(len(m.content.split()) for m in messages)
        yield LLMFinal(
            usage=LLMUsage(
                prompt_tokens=prompt_tokens,
                completion_tokens=completion_tokens,
                total_tokens=prompt_tokens + completion_tokens,
            )
        )


class LangChainLLMClient:
    """Provider-switchable real LLM call via LangChain's async streaming.
    Lazy-imports the provider SDK (`langchain_openai`/`langchain_anthropic`)
    so the fake-client path (the only one exercised in this environment)
    never pays for importing either."""

    def __init__(self, provider: str, model_name: str, api_key: str) -> None:
        self._provider = provider
        self._model_name = model_name
        self._api_key = api_key

    def _build_chat_model(self, temperature: float, max_tokens: int):
        # Both SDKs' typed __init__ overloads are narrower than what their
        # underlying pydantic models actually accept (max_tokens works at
        # runtime; SecretStr avoids the api_key type mismatch) — this path
        # is unverified against a live provider in this environment (no API
        # key configured), so the ignores are scoped to exactly the two
        # known typing gaps rather than silencing the whole call.
        if self._provider == "openai":
            from langchain_openai import ChatOpenAI

            return ChatOpenAI(
                model=self._model_name,
                api_key=SecretStr(self._api_key),
                temperature=temperature,
                max_tokens=max_tokens,  # type: ignore[call-arg]
            )
        if self._provider == "anthropic":
            from langchain_anthropic import ChatAnthropic

            return ChatAnthropic(
                model_name=self._model_name,
                api_key=SecretStr(self._api_key),
                temperature=temperature,
                max_tokens_to_sample=max_tokens,
                timeout=None,
                stop=None,
            )
        raise ValueError(f"unknown LLM provider: {self._provider!r}")

    async def stream(
        self,
        messages: list[LLMMessage],
        *,
        temperature: float,
        max_tokens: int,
    ) -> AsyncIterator[LLMStreamEvent]:
        from langchain_core.messages import AIMessageChunk, HumanMessage, SystemMessage

        chat_model = self._build_chat_model(temperature, max_tokens)
        lc_messages = [
            SystemMessage(content=m.content)
            if m.role == "system"
            else HumanMessage(content=m.content)
            for m in messages
        ]

        merged: AIMessageChunk | None = None
        async for chunk in chat_model.astream(lc_messages):
            if chunk.content:
                yield LLMToken(text=str(chunk.content))
            merged = chunk if merged is None else merged + chunk

        usage = getattr(merged, "usage_metadata", None) if merged is not None else None
        yield LLMFinal(
            usage=LLMUsage(
                prompt_tokens=usage["input_tokens"] if usage else 0,
                completion_tokens=usage["output_tokens"] if usage else 0,
                total_tokens=usage["total_tokens"] if usage else 0,
            )
        )


def build_llm_client(settings: Settings) -> LLMClient:
    if settings.llm_provider == "fake":
        return FakeLLMClient()
    return LangChainLLMClient(
        provider=settings.llm_provider,
        model_name=settings.llm_model_name,
        api_key=settings.llm_api_key,
    )
