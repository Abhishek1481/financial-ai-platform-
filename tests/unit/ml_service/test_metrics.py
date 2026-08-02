"""Unit tests for app/evaluation/metrics.py's lexical-overlap scoring —
pure functions, no gRPC server needed (see test_evaluation_servicer.py for
the servicer wrapped around these)."""

from __future__ import annotations

import pytest
from app.evaluation.metrics import percentile, score_answer


class TestFaithfulness:
    def test_lexically_supported_claim_scores_high(self):
        context = ["Tesla automotive revenue grew eighteen percent year over year."]
        result = score_answer(
            "q", "Tesla revenue grew eighteen percent year over year.", context
        )
        assert result.faithfulness == 1.0

    def test_unsupported_claim_with_no_citation_scores_low(self):
        context = ["Tesla automotive revenue grew eighteen percent year over year."]
        result = score_answer(
            "q", "Tesla stock price surged due to strong earnings guidance.", context
        )
        assert result.faithfulness == 0.0

    def test_a_valid_citation_marker_overrides_lexical_mismatch(self):
        context = ["Completely unrelated text about something else entirely."]
        result = score_answer(
            "What happened to revenue?", "Revenue grew significantly [1].", context
        )
        assert result.faithfulness == 1.0

    def test_citation_marker_out_of_range_is_not_trusted(self):
        context = ["Tesla automotive revenue grew this quarter."]
        result = score_answer("q", "Something happened [7].", context)
        assert result.faithfulness == 0.0

    def test_empty_answer_scores_zero(self):
        result = score_answer("q", "", ["some context"])
        assert result.faithfulness == 0.0


def test_hallucination_score_is_the_complement_of_faithfulness():
    context = ["Tesla automotive revenue grew eighteen percent year over year."]
    result = score_answer(
        "q", "Tesla revenue grew eighteen percent year over year.", context
    )
    assert result.hallucination_score == pytest.approx(1.0 - result.faithfulness)


class TestContextPrecision:
    def test_counts_only_chunks_relevant_to_the_question(self):
        context = [
            "Tesla automotive revenue grew this quarter.",
            "Completely unrelated text about gardening.",
        ]
        result = score_answer("How did Tesla revenue perform?", "some answer", context)
        assert result.context_precision == 0.5

    def test_empty_context_scores_zero(self):
        result = score_answer("question", "answer", [])
        assert result.context_precision == 0.0


class TestContextRecall:
    def test_zero_without_a_ground_truth_answer(self):
        context = ["Tesla revenue grew this quarter."]
        result = score_answer("q", "a", context, ground_truth_answer="")
        assert result.context_recall == 0.0

    def test_measures_ground_truth_coverage_by_context(self):
        context = ["Tesla automotive revenue grew eighteen percent year over year."]
        result = score_answer(
            "q",
            "a",
            context,
            ground_truth_answer="Tesla revenue grew eighteen percent.",
        )
        assert result.context_recall == 1.0

    def test_uncovered_ground_truth_scores_zero(self):
        context = ["Tesla automotive revenue grew eighteen percent year over year."]
        result = score_answer(
            "q",
            "a",
            context,
            ground_truth_answer="The stock buyback program was expanded.",
        )
        assert result.context_recall == 0.0


class TestAnswerRelevancy:
    def test_measures_question_token_overlap(self):
        result = score_answer(
            "What happened to Tesla revenue?",
            "Tesla revenue grew significantly this quarter.",
            [],
        )
        assert result.answer_relevancy == 0.5

    def test_unrelated_answer_scores_zero(self):
        result = score_answer(
            "What happened to Tesla revenue?",
            "The weather today is sunny and warm.",
            [],
        )
        assert result.answer_relevancy == 0.0


class TestPercentile:
    def test_nearest_rank_p50_of_three_values(self):
        assert percentile([10.0, 20.0, 30.0], 50) == 20.0

    def test_p95_of_ten_values(self):
        assert percentile([float(i) for i in range(1, 11)], 95) == 10.0

    def test_empty_list_is_zero(self):
        assert percentile([], 50) == 0.0
