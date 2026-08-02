"""RAG answer scoring: faithfulness, context precision/recall,
hallucination, and answer relevancy — the RAGAS-style metric set
`EvaluationService` exposes (see proto/evaluation/v1/evaluation.proto).

The standard implementation of these metrics uses an LLM as a judge (ask a
model "is this claim supported by this context?"). That's not available as
a *scoring* dependency here any more than it is as a *generation* one (see
app/rag/llm_client.py's module docstring) — this environment has no
OpenAI/Anthropic key. Rather than stub evaluation out entirely, these
metrics are computed via deterministic lexical overlap instead: split text
into sentences, tokenize (lowercase, stopwords stripped), and measure
token-set overlap between an answer's claims and the retrieved context —
the same "does this pass a token-overlap gate" reasoning
`app/search/keyword_index.py` already uses for BM25+ relevance filtering.
This is a real, independently useful algorithm (correlates with the
LLM-judged version reasonably well and needs no external call at all,
online or offline), not a placeholder — swapping in an LLM-judge
implementation later is a matter of adding a second `Scorer` behind the
same interface if the fidelity tradeoff turns out to matter, not a
rewrite of `EvaluationServicer`.
"""

from __future__ import annotations

import math
import re
from dataclasses import dataclass

from app.rag.citations import extract_cited_indices

_TOKEN_RE = re.compile(r"\w+")
_SENTENCE_SPLIT_RE = re.compile(r"(?<=[.!?])\s+")

# A deliberately small, hand-picked list — just enough that common function
# words don't inflate overlap scores between unrelated sentences. Not a
# general-purpose NLP stopword list (no external dependency for one).
_STOPWORDS = frozenset(
    ["a", "an", "the", "is", "are", "was", "were", "be", "been", "being", "of", "to", "in", "on", "for", "and", "or", "that", "this", "it", "as", "by", "with", "at", "from", "but", "not", "has", "have", "had", "will", "would", "can", "could", "should", "do", "does", "did", "i", "you", "he", "she", "we", "they", "them", "his", "her", "its", "our", "your", "their"]
)

# A sentence counts as "supported" by a context chunk once this fraction of
# its (non-stopword) tokens also appear in that chunk. Below 1.0 on purpose
# — requiring every token to match would fail on trivial paraphrasing
# (plurals, word order) that isn't actually unfaithful.
_SUPPORT_OVERLAP_THRESHOLD = 0.6


@dataclass(frozen=True)
class ScoreResult:
    faithfulness: float
    context_precision: float
    context_recall: float
    hallucination_score: float
    answer_relevancy: float


def _tokenize(text: str) -> set[str]:
    return {t for t in _TOKEN_RE.findall(text.lower()) if t not in _STOPWORDS and len(t) > 1}


def _split_sentences(text: str) -> list[str]:
    return [s.strip() for s in _SENTENCE_SPLIT_RE.split(text.strip()) if s.strip()]


def _overlap_ratio(claim_tokens: set[str], reference_tokens: set[str]) -> float:
    if not claim_tokens or not reference_tokens:
        return 0.0
    return len(claim_tokens & reference_tokens) / len(claim_tokens)


def _is_supported(sentence: str, context_chunks: list[str], num_context_chunks: int) -> bool:
    cited = extract_cited_indices(sentence)
    if cited and any(1 <= i <= num_context_chunks for i in cited):
        return True
    sentence_tokens = _tokenize(sentence)
    if not sentence_tokens:
        return True  # nothing substantive being claimed (e.g. a bare citation marker)
    return any(
        _overlap_ratio(sentence_tokens, _tokenize(chunk)) >= _SUPPORT_OVERLAP_THRESHOLD
        for chunk in context_chunks
    )


def _faithfulness(answer: str, context_chunks: list[str]) -> float:
    sentences = _split_sentences(answer)
    if not sentences:
        return 0.0
    supported = sum(1 for s in sentences if _is_supported(s, context_chunks, len(context_chunks)))
    return supported / len(sentences)


def _context_precision(question: str, context_chunks: list[str]) -> float:
    if not context_chunks:
        return 0.0
    question_tokens = _tokenize(question)
    if not question_tokens:
        return 0.0
    relevant = sum(1 for chunk in context_chunks if _tokenize(chunk) & question_tokens)
    return relevant / len(context_chunks)


def _context_recall(ground_truth_answer: str, context_chunks: list[str]) -> float:
    if not ground_truth_answer or not context_chunks:
        return 0.0
    sentences = _split_sentences(ground_truth_answer)
    if not sentences:
        return 0.0
    context_token_sets = [_tokenize(c) for c in context_chunks]
    covered = sum(
        1
        for s in sentences
        if any(
            _overlap_ratio(_tokenize(s), ctx_tokens) >= _SUPPORT_OVERLAP_THRESHOLD
            for ctx_tokens in context_token_sets
        )
    )
    return covered / len(sentences)


def _answer_relevancy(question: str, answer: str) -> float:
    question_tokens = _tokenize(question)
    if not question_tokens:
        return 0.0
    return len(question_tokens & _tokenize(answer)) / len(question_tokens)


def percentile(values: list[float], pct: float) -> float:
    """Nearest-rank percentile — used by BatchEvaluate for p50/p95 latency.
    Deliberately the simple nearest-rank definition (not interpolated):
    it's exact for the small batch sizes a CI eval-regression gate runs
    (proto/README.md's stated use case for BatchEvaluate), where
    interpolation would add complexity without changing the answer."""
    if not values:
        return 0.0
    ordered = sorted(values)
    rank = max(1, math.ceil(pct / 100 * len(ordered)))
    return ordered[min(rank, len(ordered)) - 1]


def score_answer(
    question: str,
    answer: str,
    context_chunks: list[str],
    ground_truth_answer: str = "",
) -> ScoreResult:
    faithfulness = _faithfulness(answer, context_chunks)
    return ScoreResult(
        faithfulness=faithfulness,
        context_precision=_context_precision(question, context_chunks),
        context_recall=_context_recall(ground_truth_answer, context_chunks),
        hallucination_score=1.0 - faithfulness,
        answer_relevancy=_answer_relevancy(question, answer),
    )
