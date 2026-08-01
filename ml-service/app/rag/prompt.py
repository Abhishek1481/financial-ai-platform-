"""Prompt construction: numbers each retrieved chunk so the model can cite
it by position (`[1]`, `[2]`, ...) instead of needing to reproduce chunk IDs
verbatim in generated text — app/rag/citations.py then maps those numbers
back to the actual ChunkRecords for the response's Citation list.
"""

from __future__ import annotations

from app import _bootstrap  # noqa: F401  (must run before generated-stub imports)
from app.embeddings.vector_store import ChunkRecord
from app.rag.llm_client import LLMMessage
from common.v1 import common_pb2
from rag.v1 import rag_pb2

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

_SUMMARY_INSTRUCTIONS = {
    rag_pb2.SUMMARY_TYPE_EXECUTIVE: (
        "Write a concise executive summary of this document: what it covers, "
        "the most important facts and figures, and the overall takeaway."
    ),
    rag_pb2.SUMMARY_TYPE_RISK: (
        "Identify and summarize the key risks disclosed in this document — "
        "operational, financial, legal, or market risks the document raises."
    ),
    rag_pb2.SUMMARY_TYPE_REVENUE: (
        "Summarize revenue performance discussed in this document: figures, "
        "growth or decline, drivers, and any guidance or trends mentioned."
    ),
    rag_pb2.SUMMARY_TYPE_SENTIMENT: (
        "Assess the overall sentiment this document conveys about the "
        "company's performance and outlook (positive, neutral, or negative) "
        "and summarize the specific language that supports that assessment."
    ),
}
_DEFAULT_SUMMARY_TYPE = rag_pb2.SUMMARY_TYPE_EXECUTIVE


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


def build_summarize_messages(
    summary_type: rag_pb2.SummaryType.ValueType,
    context_chunks: list[ChunkRecord],
) -> list[LLMMessage]:
    instruction = _SUMMARY_INSTRUCTIONS.get(
        summary_type, _SUMMARY_INSTRUCTIONS[_DEFAULT_SUMMARY_TYPE]
    )
    system_prompt = (
        f"You are a financial research assistant. {instruction} Base the summary "
        "only on the numbered document excerpts below, and cite every claim with "
        "the bracketed number of the excerpt it came from, e.g. [1] or [2][3]."
    )
    context_block = "\n\n".join(
        f"[{i}] {chunk.text}" for i, chunk in enumerate(context_chunks, start=1)
    )
    return [
        LLMMessage(role="system", content=system_prompt),
        LLMMessage(role="user", content=f"Document excerpts:\n{context_block}"),
    ]
