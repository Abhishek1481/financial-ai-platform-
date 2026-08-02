// Package evaluation is deliberately thin, same reasoning as internal/search
// and internal/rag: no dedup/job-queue/persistence concern, just translating
// an HTTP request into a gRPC call and back — so there's no Service layer,
// only the interface the handler depends on. *mlclient.Client satisfies this
// structurally; tests substitute a fake.
package evaluation

import (
	"context"

	evaluationv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/evaluation/v1"
)

type Evaluator interface {
	EvaluateAnswer(
		ctx context.Context,
		question, answer string,
		contextTexts []string,
		groundTruthAnswer string,
	) (*evaluationv1.EvaluateAnswerResponse, error)
}
