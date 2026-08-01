"""Prompt construction: numbers each retrieved chunk so the model can cite
it by position (`[1]`, `[2]`, ...) instead of needing to reproduce chunk IDs
verbatim in generated text — app/rag/citations.py then maps those numbers
back to the actual ChunkRecords for the response's Citation list.
"""

from __future__ import annotations

from app.embeddings.vector_store import ChunkRecord
from app.rag.llm_client import LLMMessage
from common.v1 import common_pb2

SYSTEM_PROMPT = (
    "You are a financial research assistant. Answer the user's question using "
    "only the numbered context chunks provided below the question. Cite every "
    "claim you make with the bracketed number of the chunk it came from, e.g. "
    "[1] or [2][3] for a claim supported by two chunks. If the context does not "
    "contain enough information to answer, say so plainly instead of guessing."
)

_ROLE_NAMES = {
    common_pb2.CONVERSATION_ROLE_USER: "user",
    common_pb2.CONVERSATION_ROLE_ASSISTANT: "assistant",
}


def build_messages(
    question: str,
    history: list[common_pb2.ConversationTurn],
    context_chunks: list[ChunkRecord],
) -> list[LLMMessage]:
    messages = [LLMMessage(role="system", content=SYSTEM_PROMPT)]

    for turn in history:
        role = _ROLE_NAMES.get(turn.role)
        if role is not None:
            messages.append(LLMMessage(role=role, content=turn.content))

    context_block = "\n\n".join(
        f"[{i}] {chunk.text}" for i, chunk in enumerate(context_chunks, start=1)
    )
    user_content = (
        f"Context:\n{context_block}\n\nQuestion: {question}"
        if context_block
        else f"Question: {question}"
    )
    messages.append(LLMMessage(role="user", content=user_content))
    return messages
