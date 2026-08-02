package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	embeddingsv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/embeddings/v1"
	evaluationv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/evaluation/v1"
	ingestionv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/ingestion/v1"
	ragv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/rag/v1"
	searchv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/search/v1"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/auth"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/cache"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/config"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/conversation"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/evaluation"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/health"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/ingestion"
	appmiddleware "github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/middleware"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/mlclient"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/rag"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/ratelimit"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/search"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/storage"
)

const (
	testAdminEmail    = "admin@test.local"
	testAdminPassword = "test-admin-password"
)

// fakeExtractor stands in for a live ml-service gRPC connection —
// ingestion.Extractor is an interface specifically so this package's
// integration tests don't need one running. See
// internal/ingestion/service_test.go for the equivalent at the domain
// layer.
type fakeExtractor struct {
	respFn func(uri string) (*ingestionv1.ExtractDocumentResponse, error)
}

func (f *fakeExtractor) ExtractDocument(
	ctx context.Context,
	documentID, uri string,
	docType commonv1.DocumentType,
) (*ingestionv1.ExtractDocumentResponse, error) {
	if f.respFn != nil {
		return f.respFn(uri)
	}
	return &ingestionv1.ExtractDocumentResponse{RawText: "fake extracted text", PageCount: 1}, nil
}

// fakeEmbedder is fakeExtractor's counterpart for ingestion.Embedder.
type fakeEmbedder struct {
	respFn func(rawText string) (*embeddingsv1.ChunkAndEmbedProgress, error)
}

func (f *fakeEmbedder) ChunkAndEmbed(
	ctx context.Context,
	documentID, rawText string,
	metadata *commonv1.FinancialMetadata,
) (*embeddingsv1.ChunkAndEmbedProgress, error) {
	if f.respFn != nil {
		return f.respFn(rawText)
	}
	return &embeddingsv1.ChunkAndEmbedProgress{
		Stage:       embeddingsv1.EmbedStage_EMBED_STAGE_COMPLETE,
		ChunksTotal: 1,
		ChunkIds:    []string{"fake-chunk-1"},
	}, nil
}

// fakeSearcher is fakeExtractor's counterpart for search.Searcher.
type fakeSearcher struct {
	respFn func(query string) (*searchv1.SearchResponse, error)
}

func (f *fakeSearcher) Search(
	ctx context.Context,
	query string,
	topK int32,
	mode searchv1.SearchMode,
	filter *commonv1.MetadataFilter,
) (*searchv1.SearchResponse, error) {
	if f.respFn != nil {
		return f.respFn(query)
	}
	return &searchv1.SearchResponse{
		Results: []*searchv1.ScoredChunk{
			{
				Chunk: &commonv1.Chunk{ChunkId: "fake-chunk-1", DocumentId: "fake-doc-1", Text: "fake result text"},
				Score: 0.9,
			},
		},
		SearchLatencyMs: 1,
	}, nil
}

// fakeAnswerer is fakeSearcher's counterpart for rag.Answerer: emits two
// fake tokens then a final message over the same channel shape mlclient.Query
// produces, so handlers.RAGHandlers is exercised without a live ml-service.
type fakeAnswerer struct {
	respFn      func(req *ragv1.QueryRequest) []mlclient.QueryEvent
	summarizeFn func(documentID string, summaryType ragv1.SummaryType) (*ragv1.SummarizeResponse, error)
}

func (f *fakeAnswerer) Query(
	ctx context.Context, req *ragv1.QueryRequest,
) (<-chan mlclient.QueryEvent, error) {
	events := []mlclient.QueryEvent{
		{Token: "fake "},
		{Token: "answer"},
		{Final: &ragv1.QueryFinal{
			Citations: []*commonv1.Citation{{ChunkId: "fake-chunk-1", DocumentId: "fake-doc-1"}},
			Usage:     &commonv1.TokenUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
			LatencyMs: 1,
		}},
	}
	if f.respFn != nil {
		events = f.respFn(req)
	}

	ch := make(chan mlclient.QueryEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (f *fakeAnswerer) Summarize(
	ctx context.Context, documentID string, summaryType ragv1.SummaryType,
) (*ragv1.SummarizeResponse, error) {
	if f.summarizeFn != nil {
		return f.summarizeFn(documentID, summaryType)
	}
	return &ragv1.SummarizeResponse{
		Summary:   "fake summary",
		Citations: []*commonv1.Citation{{ChunkId: "fake-chunk-1", DocumentId: documentID}},
		Usage:     &commonv1.TokenUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
		LatencyMs: 1,
	}, nil
}

// fakeEvaluator is fakeSearcher's counterpart for evaluation.Evaluator.
type fakeEvaluator struct {
	respFn func(question, answer string, contextTexts []string, groundTruthAnswer string) (
		*evaluationv1.EvaluateAnswerResponse, error,
	)
}

func (f *fakeEvaluator) EvaluateAnswer(
	ctx context.Context, question, answer string, contextTexts []string, groundTruthAnswer string,
) (*evaluationv1.EvaluateAnswerResponse, error) {
	if f.respFn != nil {
		return f.respFn(question, answer, contextTexts, groundTruthAnswer)
	}
	return &evaluationv1.EvaluateAnswerResponse{
		Faithfulness:     0.9,
		ContextPrecision: 0.8,
		AnswerRelevancy:  0.7,
	}, nil
}

// startTestServer binds both listeners on ephemeral (":0") ports, starts
// serving in the background, and registers cleanup to shut the server down
// — mirrors the build/start split ml-service's Python test suite uses for
// the same reason: get the real bound port before issuing requests. It
// also seeds a known admin account so auth integration tests can exercise
// RBAC over real HTTP requests rather than bypassing the API.
func startTestServer(t *testing.T) *Server {
	t.Helper()
	return startTestServerWithExtractor(t, &fakeExtractor{})
}

func startTestServerWithExtractor(t *testing.T, extractor ingestion.Extractor) *Server {
	t.Helper()
	return startTestServerWithMLDeps(t, extractor, &fakeEmbedder{}, &fakeSearcher{}, &fakeAnswerer{})
}

func startTestServerWithMLDeps(
	t *testing.T,
	extractor ingestion.Extractor,
	embedder ingestion.Embedder,
	searcher search.Searcher,
	answerer rag.Answerer,
) *Server {
	t.Helper()
	return startTestServerFull(t, extractor, embedder, searcher, answerer, &fakeEvaluator{}, unlimitedRateLimiter())
}

// startTestServerWithEvaluator is startTestServerWithMLDeps's counterpart
// for tests that need to customize evaluation.Evaluator specifically —
// everything else gets the same default fakes as startTestServer.
func startTestServerWithEvaluator(t *testing.T, evaluator evaluation.Evaluator) *Server {
	t.Helper()
	return startTestServerFull(
		t, &fakeExtractor{}, &fakeEmbedder{}, &fakeSearcher{}, &fakeAnswerer{}, evaluator, unlimitedRateLimiter(),
	)
}

// startTestServerWithRateLimit is startTestServerWithMLDeps's counterpart
// for tests that need to actually observe rate limiting kick in —
// everything else gets the same default fakes as startTestServer.
func startTestServerWithRateLimit(t *testing.T, limiter *ratelimit.Limiter) *Server {
	t.Helper()
	return startTestServerFull(
		t, &fakeExtractor{}, &fakeEmbedder{}, &fakeSearcher{}, &fakeAnswerer{}, &fakeEvaluator{}, limiter,
	)
}

// unlimitedRateLimiter is what every test except the rate-limiting tests
// themselves wants — high enough that a test's handful of requests never
// trips it, so rate limiting doesn't have to be a concern in tests that
// aren't about rate limiting.
func unlimitedRateLimiter() *ratelimit.Limiter {
	return ratelimit.New(1000, 1000)
}

func startTestServerFull(
	t *testing.T,
	extractor ingestion.Extractor,
	embedder ingestion.Embedder,
	searcher search.Searcher,
	answerer rag.Answerer,
	evaluator evaluation.Evaluator,
	rateLimiter *ratelimit.Limiter,
) *Server {
	t.Helper()

	cfg := config.Config{
		Environment:        "test",
		HTTPHost:           "127.0.0.1",
		HTTPPort:           0,
		MetricsHost:        "127.0.0.1",
		MetricsPort:        0,
		LogLevel:           "error",
		ShutdownTimeout:    5 * time.Second,
		MaxUploadSizeBytes: 10 << 20,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	readiness := health.NewReadiness()

	tokens := auth.NewTokenService("test-secret", time.Hour)
	authService := auth.NewService(auth.NewMemoryUserRepository(), tokens)
	if err := authService.SeedAdmin(context.Background(), testAdminEmail, testAdminPassword); err != nil {
		t.Fatalf("SeedAdmin() failed: %v", err)
	}

	objectStore, err := storage.NewLocalObjectStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalObjectStore() failed: %v", err)
	}
	ingestionService := ingestion.NewService(
		logger,
		ingestion.NewMemoryDocumentRepository(),
		ingestion.NewMemoryJobRepository(),
		objectStore,
		extractor,
		embedder,
		2, 10,
	)
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	ingestionService.Start(workerCtx)
	t.Cleanup(cancelWorkers)

	server := New(cfg, logger, Dependencies{
		Readiness:      readiness,
		AuthService:    authService,
		Tokens:         tokens,
		Ingestion:      ingestionService,
		Searcher:       searcher,
		Answerer:       answerer,
		Conversations:  conversation.NewMemoryStore(),
		Evaluator:      evaluator,
		Cache:          cache.NewMemoryCache(),
		SearchCacheTTL: time.Minute,
		RateLimiter:    rateLimiter,
	})
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen() failed: %v", err)
	}

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve() }()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() failed: %v", err)
		}
		if err := <-serveErrCh; err != nil {
			t.Errorf("Serve() returned error: %v", err)
		}
	})

	return server
}

// uploadFile builds and sends a multipart/form-data upload — real HTTP,
// same encoding a browser or curl -F would send, not a shortcut around the
// handler's actual parsing path.
func uploadFile(t *testing.T, url, token, filename, content, category string) *http.Response {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if category != "" {
		if err := writer.WriteField("category", category); err != nil {
			t.Fatalf("write category field: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", url, err)
	}
	return resp
}

func TestServer_Healthz(t *testing.T) {
	server := startTestServer(t)

	resp, err := http.Get("http://" + server.HTTPAddr() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`body["status"] = %q, want "ok"`, body["status"])
	}
}

func TestServer_Readyz(t *testing.T) {
	server := startTestServer(t)

	resp, err := http.Get("http://" + server.HTTPAddr() + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestServer_MetricsServedOnlyOnMetricsPort(t *testing.T) {
	server := startTestServer(t)

	metricsResp, err := http.Get("http://" + server.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics on metrics port failed: %v", err)
	}
	defer metricsResp.Body.Close()
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("metrics port /metrics status = %d, want %d", metricsResp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatalf("reading metrics body: %v", err)
	}
	if !strings.Contains(string(body), "gateway_http_requests_total") {
		t.Error("metrics output missing gateway_http_requests_total series")
	}

	publicResp, err := http.Get("http://" + server.HTTPAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics on public port failed: %v", err)
	}
	defer publicResp.Body.Close()
	if publicResp.StatusCode != http.StatusNotFound {
		t.Errorf("public port /metrics status = %d, want %d (metrics must not be reachable publicly)",
			publicResp.StatusCode, http.StatusNotFound)
	}
}

func TestServer_RequestMetricsAreRecorded(t *testing.T) {
	server := startTestServer(t)

	resp, err := http.Get("http://" + server.HTTPAddr() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	resp.Body.Close()

	metricsResp, err := http.Get("http://" + server.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer metricsResp.Body.Close()
	body, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatalf("reading metrics body: %v", err)
	}

	want := `gateway_http_requests_total{method="GET",route="/healthz",status="200"}`
	if !strings.Contains(string(body), want) {
		t.Errorf("metrics output missing counter for /healthz request; want substring %q, got:\n%s", want, body)
	}
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	resp, err := http.Post(url, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("POST %s failed: %v", url, err)
	}
	return resp
}

func postJSONWithToken(t *testing.T, url, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", url, err)
	}
	return resp
}

func getWithToken(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	return resp
}

func login(t *testing.T, baseURL, email, password string) string {
	t.Helper()
	resp := postJSON(t, baseURL+"/api/v1/auth/login", map[string]string{
		"email":    email,
		"password": password,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("invalid login response body: %v", err)
	}
	if body.AccessToken == "" {
		t.Fatal("login response missing access_token")
	}
	return body.AccessToken
}

func TestAuth_RegisterLoginMeFlow(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "new-user@test.local",
		"password": "correct-horse-battery",
	})
	defer registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", registerResp.StatusCode, http.StatusCreated)
	}
	var registered struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(registerResp.Body).Decode(&registered); err != nil {
		t.Fatalf("invalid register response: %v", err)
	}
	if registered.Role != "user" {
		t.Errorf("registered role = %q, want %q (no self-service admin registration)", registered.Role, "user")
	}

	token := login(t, baseURL, "new-user@test.local", "correct-horse-battery")

	meResp := getWithToken(t, baseURL+"/api/v1/me", token)
	defer meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("/me status = %d, want %d", meResp.StatusCode, http.StatusOK)
	}
	var me struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
		t.Fatalf("invalid /me response: %v", err)
	}
	if me.Email != "new-user@test.local" || me.ID != registered.ID {
		t.Errorf("/me = %+v, want email/id matching registered user %+v", me, registered)
	}
}

func TestAuth_DuplicateRegistrationIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	body := map[string]string{"email": "dupe@test.local", "password": "correct-horse-battery"}
	first := postJSON(t, baseURL+"/api/v1/auth/register", body)
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first register status = %d, want %d", first.StatusCode, http.StatusCreated)
	}

	second := postJSON(t, baseURL+"/api/v1/auth/register", body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Errorf("second register status = %d, want %d", second.StatusCode, http.StatusConflict)
	}
}

func TestAuth_LoginWithWrongPasswordIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	resp := postJSON(t, baseURL+"/api/v1/auth/login", map[string]string{
		"email":    testAdminEmail,
		"password": "not-the-right-password",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestAuth_MeWithoutTokenIsRejected(t *testing.T) {
	server := startTestServer(t)

	resp := getWithToken(t, "http://"+server.HTTPAddr()+"/api/v1/me", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestAuth_AdminRouteRequiresAdminRole(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "regular-user@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()

	userToken := login(t, baseURL, "regular-user@test.local", "correct-horse-battery")
	userResp := getWithToken(t, baseURL+"/api/v1/admin/ping", userToken)
	defer userResp.Body.Close()
	if userResp.StatusCode != http.StatusForbidden {
		t.Errorf("regular user /admin/ping status = %d, want %d", userResp.StatusCode, http.StatusForbidden)
	}

	adminToken := login(t, baseURL, testAdminEmail, testAdminPassword)
	adminResp := getWithToken(t, baseURL+"/api/v1/admin/ping", adminToken)
	defer adminResp.Body.Close()
	if adminResp.StatusCode != http.StatusOK {
		t.Errorf("admin /admin/ping status = %d, want %d", adminResp.StatusCode, http.StatusOK)
	}
}

func TestDocuments_UploadRequiresAuth(t *testing.T) {
	server := startTestServer(t)

	resp := uploadFile(t, "http://"+server.HTTPAddr()+"/api/v1/documents", "", "doc.txt", "content", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestDocuments_UploadAndPollUntilCompleted(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "uploader@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "uploader@test.local", "correct-horse-battery")

	uploadResp := uploadFile(t, baseURL+"/api/v1/documents", token, "report.txt", "Q1 revenue grew 12%", "")
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload status = %d, want %d", uploadResp.StatusCode, http.StatusAccepted)
	}
	var uploaded struct {
		DocumentID string `json:"document_id"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(uploadResp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("invalid upload response: %v", err)
	}
	if uploaded.DocumentID == "" {
		t.Fatal("upload response missing document_id")
	}

	deadline := time.Now().Add(2 * time.Second)
	var docResp *http.Response
	var doc struct {
		ID  string `json:"id"`
		Job struct {
			Status               string `json:"status"`
			ExtractedTextPreview string `json:"extracted_text_preview"`
			ChunkCount           int    `json:"chunk_count"`
		} `json:"job"`
	}
	for time.Now().Before(deadline) {
		docResp = getWithToken(t, baseURL+"/api/v1/documents/"+uploaded.DocumentID, token)
		if err := json.NewDecoder(docResp.Body).Decode(&doc); err != nil {
			docResp.Body.Close()
			t.Fatalf("invalid document response: %v", err)
		}
		docResp.Body.Close()
		if doc.Job.Status == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if doc.Job.Status != "completed" {
		t.Fatalf("job status = %q after polling, want %q", doc.Job.Status, "completed")
	}
	// Regression check: jobStatusView's ChunkCount field was added but
	// the GetDocument handler's struct literal wasn't updated to
	// populate it from job.ChunkCount, so this silently stayed 0 despite
	// the domain layer tracking it correctly — caught via live
	// end-to-end testing, not by the unit tests alone.
	if doc.Job.ChunkCount != 1 {
		t.Errorf("ChunkCount = %d, want 1", doc.Job.ChunkCount)
	}
	if doc.Job.ExtractedTextPreview != "fake extracted text" {
		t.Errorf("ExtractedTextPreview = %q, want %q", doc.Job.ExtractedTextPreview, "fake extracted text")
	}
}

func TestDocuments_UploadUnsupportedFileTypeIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "uploader2@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "uploader2@test.local", "correct-horse-battery")

	resp := uploadFile(t, baseURL+"/api/v1/documents", token, "malware.exe", "binary content", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnsupportedMediaType)
	}
}

func TestDocuments_DuplicateUploadReturns200(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "uploader3@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "uploader3@test.local", "correct-horse-battery")

	first := uploadFile(t, baseURL+"/api/v1/documents", token, "a.txt", "identical content", "")
	first.Body.Close()
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first upload status = %d, want %d", first.StatusCode, http.StatusAccepted)
	}

	second := uploadFile(t, baseURL+"/api/v1/documents", token, "b.txt", "identical content", "")
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Errorf("duplicate upload status = %d, want %d", second.StatusCode, http.StatusOK)
	}
}

func TestDocuments_GetNonexistentDocumentReturns404(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "uploader4@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "uploader4@test.local", "correct-horse-battery")

	resp := getWithToken(t, baseURL+"/api/v1/documents/does-not-exist", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestDocuments_InferredMetadataSurfacesInResponse(t *testing.T) {
	ml := &fakeExtractor{
		respFn: func(uri string) (*ingestionv1.ExtractDocumentResponse, error) {
			return &ingestionv1.ExtractDocumentResponse{
				RawText:   "Annual report content",
				PageCount: 1,
				InferredMetadata: &commonv1.FinancialMetadata{
					FilingType: "10-K",
				},
			}, nil
		},
	}
	server := startTestServerWithExtractor(t, ml)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "sec-uploader@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "sec-uploader@test.local", "correct-horse-battery")

	uploadResp := uploadFile(t, baseURL+"/api/v1/documents", token, "filing.html", "<h1>FORM 10-K</h1>", "sec_filing")
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload status = %d, want %d", uploadResp.StatusCode, http.StatusAccepted)
	}
	var uploaded struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.NewDecoder(uploadResp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("invalid upload response: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var doc struct {
		Job struct {
			Status   string `json:"status"`
			Metadata struct {
				FilingType string `json:"filing_type"`
			} `json:"metadata"`
		} `json:"job"`
	}
	for time.Now().Before(deadline) {
		resp := getWithToken(t, baseURL+"/api/v1/documents/"+uploaded.DocumentID, token)
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			resp.Body.Close()
			t.Fatalf("invalid document response: %v", err)
		}
		resp.Body.Close()
		if doc.Job.Status == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if doc.Job.Status != "completed" {
		t.Fatalf("job status = %q after polling, want %q", doc.Job.Status, "completed")
	}
	if doc.Job.Metadata.FilingType != "10-K" {
		t.Errorf("Metadata.FilingType = %q, want %q", doc.Job.Metadata.FilingType, "10-K")
	}
}

func TestSearch_RequiresAuth(t *testing.T) {
	server := startTestServer(t)

	resp := getWithToken(t, "http://"+server.HTTPAddr()+"/api/v1/search?q=tesla", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestSearch_MissingQueryParamIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "searcher@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "searcher@test.local", "correct-horse-battery")

	resp := getWithToken(t, baseURL+"/api/v1/search", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestSearch_InvalidModeIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "searcher2@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "searcher2@test.local", "correct-horse-battery")

	resp := getWithToken(t, baseURL+"/api/v1/search?q=tesla&mode=nonsense", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestSearch_ReturnsResultsFromSearcher(t *testing.T) {
	searcher := &fakeSearcher{
		respFn: func(query string) (*searchv1.SearchResponse, error) {
			return &searchv1.SearchResponse{
				Results: []*searchv1.ScoredChunk{
					{
						Chunk: &commonv1.Chunk{
							ChunkId:    "chunk-1",
							DocumentId: "doc-1",
							Text:       "Tesla revenue grew significantly",
							Metadata:   &commonv1.FinancialMetadata{Ticker: "TSLA"},
						},
						Score: 0.87,
					},
				},
				SearchLatencyMs: 12.5,
			}, nil
		},
	}
	server := startTestServerWithMLDeps(t, &fakeExtractor{}, &fakeEmbedder{}, searcher, &fakeAnswerer{})
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "searcher3@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "searcher3@test.local", "correct-horse-battery")

	resp := getWithToken(t, baseURL+"/api/v1/search?q=tesla+revenue&mode=hybrid&top_k=5", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Results []struct {
			ChunkID    string  `json:"chunk_id"`
			DocumentID string  `json:"document_id"`
			Text       string  `json:"text"`
			Score      float32 `json:"score"`
			Metadata   struct {
				Ticker string `json:"ticker"`
			} `json:"metadata"`
		} `json:"results"`
		SearchLatencyMs float64 `json:"search_latency_ms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}

	if len(body.Results) != 1 {
		t.Fatalf("results count = %d, want 1", len(body.Results))
	}
	if body.Results[0].DocumentID != "doc-1" || body.Results[0].Metadata.Ticker != "TSLA" {
		t.Errorf("unexpected result: %+v", body.Results[0])
	}
}

func TestRAGQuery_RequiresAuth(t *testing.T) {
	server := startTestServer(t)

	resp := postJSONWithToken(t, "http://"+server.HTTPAddr()+"/api/v1/rag/query", "", map[string]string{
		"question": "What are Tesla's Q1 risks?",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestRAGQuery_MissingQuestionIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "rag1@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "rag1@test.local", "correct-horse-battery")

	resp := postJSONWithToken(t, baseURL+"/api/v1/rag/query", token, map[string]string{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestRAGQuery_InvalidHistoryRoleIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "rag2@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "rag2@test.local", "correct-horse-battery")

	resp := postJSONWithToken(t, baseURL+"/api/v1/rag/query", token, map[string]any{
		"question": "What happened?",
		"history":  []map[string]string{{"role": "narrator", "content": "..."}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestRAGQuery_StreamsTokensThenAFinalEventWithCitations(t *testing.T) {
	answerer := &fakeAnswerer{
		respFn: func(req *ragv1.QueryRequest) []mlclient.QueryEvent {
			if req.GetQuestion() != "How did Tesla's revenue perform?" {
				t.Errorf("question = %q, want %q", req.GetQuestion(), "How did Tesla's revenue perform?")
			}
			return []mlclient.QueryEvent{
				{Token: "Revenue "},
				{Token: "grew "},
				{Token: "[1]."},
				{Final: &ragv1.QueryFinal{
					Citations: []*commonv1.Citation{
						{ChunkId: "chunk-1", DocumentId: "doc-tesla", Quote: "Tesla revenue grew significantly."},
					},
					Usage:     &commonv1.TokenUsage{PromptTokens: 42, CompletionTokens: 3, TotalTokens: 45},
					LatencyMs: 7.5,
				}},
			}
		},
	}
	server := startTestServerWithMLDeps(t, &fakeExtractor{}, &fakeEmbedder{}, &fakeSearcher{}, answerer)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "rag3@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "rag3@test.local", "correct-horse-battery")

	resp := postJSONWithToken(t, baseURL+"/api/v1/rag/query", token, map[string]string{
		"question": "How did Tesla's revenue perform?",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(rawBody)

	for _, want := range []string{"event:token", `"token":"Revenue "`, `"token":"[1]."`, "event:final"} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q, got:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `"document_id":"doc-tesla"`) {
		t.Errorf("SSE final event missing citation document_id, got:\n%s", body)
	}
	if !strings.Contains(body, `"total_tokens":45`) {
		t.Errorf("SSE final event missing usage, got:\n%s", body)
	}
}

func TestSummarize_RequiresAuth(t *testing.T) {
	server := startTestServer(t)

	resp := getWithToken(t, "http://"+server.HTTPAddr()+"/api/v1/documents/doc-1/summary", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestSummarize_InvalidTypeIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "summarize1@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "summarize1@test.local", "correct-horse-battery")

	resp := getWithToken(t, baseURL+"/api/v1/documents/doc-1/summary?type=nonsense", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestSummarize_UnknownDocumentReturns404(t *testing.T) {
	answerer := &fakeAnswerer{
		summarizeFn: func(documentID string, summaryType ragv1.SummaryType) (*ragv1.SummarizeResponse, error) {
			return nil, status.Error(codes.NotFound, "document has no embedded chunks")
		},
	}
	server := startTestServerWithMLDeps(t, &fakeExtractor{}, &fakeEmbedder{}, &fakeSearcher{}, answerer)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "summarize2@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "summarize2@test.local", "correct-horse-battery")

	resp := getWithToken(t, baseURL+"/api/v1/documents/doc-missing/summary", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestSummarize_ReturnsSummaryFromAnswerer(t *testing.T) {
	answerer := &fakeAnswerer{
		summarizeFn: func(documentID string, summaryType ragv1.SummaryType) (*ragv1.SummarizeResponse, error) {
			if documentID != "doc-tesla" {
				t.Errorf("documentID = %q, want %q", documentID, "doc-tesla")
			}
			if summaryType != ragv1.SummaryType_SUMMARY_TYPE_RISK {
				t.Errorf("summaryType = %v, want SUMMARY_TYPE_RISK", summaryType)
			}
			return &ragv1.SummarizeResponse{
				Summary: "Tesla faces battery supply chain risk. [1]",
				Citations: []*commonv1.Citation{
					{ChunkId: "chunk-1", DocumentId: "doc-tesla", Quote: "battery cell sourcing risk"},
				},
				Usage:     &commonv1.TokenUsage{PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60},
				LatencyMs: 12.5,
			}, nil
		},
	}
	server := startTestServerWithMLDeps(t, &fakeExtractor{}, &fakeEmbedder{}, &fakeSearcher{}, answerer)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "summarize3@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "summarize3@test.local", "correct-horse-battery")

	resp := getWithToken(t, baseURL+"/api/v1/documents/doc-tesla/summary?type=risk", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Summary   string `json:"summary"`
		Citations []struct {
			DocumentID string `json:"document_id"`
		} `json:"citations"`
		Usage struct {
			TotalTokens int32 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}

	if body.Summary != "Tesla faces battery supply chain risk. [1]" {
		t.Errorf("summary = %q", body.Summary)
	}
	if len(body.Citations) != 1 || body.Citations[0].DocumentID != "doc-tesla" {
		t.Errorf("unexpected citations: %+v", body.Citations)
	}
	if body.Usage.TotalTokens != 60 {
		t.Errorf("total_tokens = %d, want 60", body.Usage.TotalTokens)
	}
}

func TestRAGQuery_ConversationMemoryPersistsAcrossTurns(t *testing.T) {
	var capturedHistories [][]*commonv1.ConversationTurn
	answerer := &fakeAnswerer{
		respFn: func(req *ragv1.QueryRequest) []mlclient.QueryEvent {
			capturedHistories = append(capturedHistories, req.GetHistory())
			return []mlclient.QueryEvent{
				{Token: "answer"},
				{Final: &ragv1.QueryFinal{
					Usage: &commonv1.TokenUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
				}},
			}
		},
	}
	server := startTestServerWithMLDeps(t, &fakeExtractor{}, &fakeEmbedder{}, &fakeSearcher{}, answerer)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "convo1@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "convo1@test.local", "correct-horse-battery")

	resp1 := postJSONWithToken(t, baseURL+"/api/v1/rag/query", token, map[string]string{
		"question": "What did revenue do?",
	})
	body1, err := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if err != nil {
		t.Fatalf("read first response: %v", err)
	}
	sessionID := extractSessionID(t, string(body1))
	if sessionID == "" {
		t.Fatalf("no session_id in first response:\n%s", body1)
	}

	resp2 := postJSONWithToken(t, baseURL+"/api/v1/rag/query", token, map[string]string{
		"question":   "And risks?",
		"session_id": sessionID,
	})
	if _, err := io.ReadAll(resp2.Body); err != nil {
		t.Fatalf("read second response: %v", err)
	}
	resp2.Body.Close()

	if len(capturedHistories) != 2 {
		t.Fatalf("answerer called %d times, want 2", len(capturedHistories))
	}
	if len(capturedHistories[0]) != 0 {
		t.Errorf("first call history = %v, want empty (new session)", capturedHistories[0])
	}
	if len(capturedHistories[1]) != 2 {
		t.Fatalf("second call history = %v, want 2 turns from the first exchange", capturedHistories[1])
	}
	if capturedHistories[1][0].GetContent() != "What did revenue do?" {
		t.Errorf("history[0].content = %q", capturedHistories[1][0].GetContent())
	}
	if capturedHistories[1][0].GetRole() != commonv1.ConversationRole_CONVERSATION_ROLE_USER {
		t.Errorf("history[0].role = %v, want USER", capturedHistories[1][0].GetRole())
	}
	if capturedHistories[1][1].GetContent() != "answer" {
		t.Errorf("history[1].content = %q", capturedHistories[1][1].GetContent())
	}
	if capturedHistories[1][1].GetRole() != commonv1.ConversationRole_CONVERSATION_ROLE_ASSISTANT {
		t.Errorf("history[1].role = %v, want ASSISTANT", capturedHistories[1][1].GetRole())
	}
}

func TestRAGQuery_ExplicitHistoryOverridesStoredSession(t *testing.T) {
	var capturedHistories [][]*commonv1.ConversationTurn
	answerer := &fakeAnswerer{
		respFn: func(req *ragv1.QueryRequest) []mlclient.QueryEvent {
			capturedHistories = append(capturedHistories, req.GetHistory())
			return []mlclient.QueryEvent{
				{Final: &ragv1.QueryFinal{Usage: &commonv1.TokenUsage{}}},
			}
		},
	}
	server := startTestServerWithMLDeps(t, &fakeExtractor{}, &fakeEmbedder{}, &fakeSearcher{}, answerer)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "convo2@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "convo2@test.local", "correct-horse-battery")

	resp := postJSONWithToken(t, baseURL+"/api/v1/rag/query", token, map[string]any{
		"question":   "Follow-up?",
		"session_id": "irrelevant-session",
		"history":    []map[string]string{{"role": "user", "content": "explicit turn"}},
	})
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()

	if len(capturedHistories) != 1 || len(capturedHistories[0]) != 1 {
		t.Fatalf("captured histories = %+v", capturedHistories)
	}
	if capturedHistories[0][0].GetContent() != "explicit turn" {
		t.Errorf("history[0].content = %q, want explicit body history to win", capturedHistories[0][0].GetContent())
	}
}

func TestAdminEvaluate_RequiresAdminRole(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "eval-user@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	userToken := login(t, baseURL, "eval-user@test.local", "correct-horse-battery")

	resp := postJSONWithToken(t, baseURL+"/api/v1/admin/evaluate", userToken, map[string]string{
		"question": "q", "answer": "a",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestAdminEvaluate_MissingFieldsIsRejected(t *testing.T) {
	server := startTestServer(t)
	baseURL := "http://" + server.HTTPAddr()
	adminToken := login(t, baseURL, testAdminEmail, testAdminPassword)

	resp := postJSONWithToken(t, baseURL+"/api/v1/admin/evaluate", adminToken, map[string]string{
		"question": "q",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestAdminEvaluate_ReturnsScoresFromEvaluator(t *testing.T) {
	evaluator := &fakeEvaluator{
		respFn: func(question, answer string, contextTexts []string, groundTruthAnswer string) (
			*evaluationv1.EvaluateAnswerResponse, error,
		) {
			if question != "How did revenue grow?" || answer != "Revenue grew 18%. [1]" {
				t.Errorf("unexpected question/answer: %q / %q", question, answer)
			}
			if len(contextTexts) != 1 || contextTexts[0] != "Revenue grew 18% year over year." {
				t.Errorf("unexpected context: %v", contextTexts)
			}
			return &evaluationv1.EvaluateAnswerResponse{
				Faithfulness:       1.0,
				ContextPrecision:   0.5,
				ContextRecall:      0.0,
				HallucinationScore: 0.0,
				AnswerRelevancy:    0.8,
			}, nil
		},
	}
	server := startTestServerWithEvaluator(t, evaluator)
	baseURL := "http://" + server.HTTPAddr()
	adminToken := login(t, baseURL, testAdminEmail, testAdminPassword)

	resp := postJSONWithToken(t, baseURL+"/api/v1/admin/evaluate", adminToken, map[string]any{
		"question": "How did revenue grow?",
		"answer":   "Revenue grew 18%. [1]",
		"context":  []string{"Revenue grew 18% year over year."},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Faithfulness     float32 `json:"faithfulness"`
		ContextPrecision float32 `json:"context_precision"`
		AnswerRelevancy  float32 `json:"answer_relevancy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if body.Faithfulness != 1.0 || body.ContextPrecision != 0.5 || body.AnswerRelevancy != 0.8 {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestRateLimit_ExceedingBurstReturns429(t *testing.T) {
	server := startTestServerWithRateLimit(t, ratelimit.New(0, 2)) // 0 refill isolates this to burst
	baseURL := "http://" + server.HTTPAddr()

	// /healthz sits outside the /api/v1 group the rate limiter is
	// registered on, so this hits a route under /api/v1 instead —
	// /me without a token still reaches the limiter before Authenticate
	// rejects it, since the limiter is v1-group middleware and runs first.
	var lastStatus int
	for i := 0; i < 3; i++ {
		resp := getWithToken(t, baseURL+"/api/v1/me", "")
		lastStatus = resp.StatusCode
		resp.Body.Close()
	}

	if lastStatus != http.StatusTooManyRequests {
		t.Errorf("status after exhausting burst = %d, want %d", lastStatus, http.StatusTooManyRequests)
	}
}

func TestRateLimit_DifferentClientsHaveIndependentBudgets(t *testing.T) {
	// The limiter itself is keyed by remote IP (see routes.go's comment on
	// why, not user ID) — this test just confirms a request that exhausts
	// one key's budget doesn't affect requests answered under a different
	// key, using the Limiter directly since httptest can't easily vary
	// RemoteAddr through gin's real network listener.
	limiter := ratelimit.New(0, 1)
	if !limiter.Allow("client-a") {
		t.Fatal("client-a's first request should be allowed")
	}
	if !limiter.Allow("client-b") {
		t.Fatal("client-b should have an independent budget from client-a")
	}
}

func TestSearch_CachesIdenticalQueries(t *testing.T) {
	var calls int
	searcher := &fakeSearcher{
		respFn: func(query string) (*searchv1.SearchResponse, error) {
			calls++
			return &searchv1.SearchResponse{
				Results: []*searchv1.ScoredChunk{
					{Chunk: &commonv1.Chunk{ChunkId: "c1", DocumentId: "d1", Text: "result"}, Score: 0.5},
				},
			}, nil
		},
	}
	server := startTestServerWithMLDeps(t, &fakeExtractor{}, &fakeEmbedder{}, searcher, &fakeAnswerer{})
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "cache1@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "cache1@test.local", "correct-horse-battery")

	url := baseURL + "/api/v1/search?q=tesla&mode=hybrid&top_k=5"
	resp1 := getWithToken(t, url, token)
	resp1.Body.Close()
	resp2 := getWithToken(t, url, token)
	resp2.Body.Close()

	if calls != 1 {
		t.Errorf("searcher called %d times for two identical queries, want 1 (second should be a cache hit)", calls)
	}
}

func TestSearch_DoesNotCacheAcrossDifferentQueries(t *testing.T) {
	var calls int
	searcher := &fakeSearcher{
		respFn: func(query string) (*searchv1.SearchResponse, error) {
			calls++
			return &searchv1.SearchResponse{Results: nil}, nil
		},
	}
	server := startTestServerWithMLDeps(t, &fakeExtractor{}, &fakeEmbedder{}, searcher, &fakeAnswerer{})
	baseURL := "http://" + server.HTTPAddr()

	registerResp := postJSON(t, baseURL+"/api/v1/auth/register", map[string]string{
		"email":    "cache2@test.local",
		"password": "correct-horse-battery",
	})
	registerResp.Body.Close()
	token := login(t, baseURL, "cache2@test.local", "correct-horse-battery")

	resp1 := getWithToken(t, baseURL+"/api/v1/search?q=tesla&mode=hybrid", token)
	resp1.Body.Close()
	resp2 := getWithToken(t, baseURL+"/api/v1/search?q=apple&mode=hybrid", token)
	resp2.Body.Close()

	if calls != 2 {
		t.Errorf("searcher called %d times for two different queries, want 2 (no cross-query cache hit)", calls)
	}
}

func extractSessionID(t *testing.T, sseBody string) string {
	t.Helper()
	const marker = `"session_id":"`
	idx := strings.Index(sseBody, marker)
	if idx == -1 {
		return ""
	}
	rest := sseBody[idx+len(marker):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return ""
	}
	return rest[:end]
}

func TestRequestID_IsGeneratedWhenNotSupplied(t *testing.T) {
	server := startTestServer(t)

	resp := getWithToken(t, "http://"+server.HTTPAddr()+"/healthz", "")
	defer resp.Body.Close()

	if resp.Header.Get(appmiddleware.RequestIDHeader) == "" {
		t.Error("expected a generated X-Request-ID response header")
	}
}

func TestRequestID_SuppliedValueIsEchoedBack(t *testing.T) {
	server := startTestServer(t)

	req, err := http.NewRequest(http.MethodGet, "http://"+server.HTTPAddr()+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set(appmiddleware.RequestIDHeader, "caller-supplied-id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get(appmiddleware.RequestIDHeader); got != "caller-supplied-id" {
		t.Errorf("X-Request-ID = %q, want it echoed back unchanged", got)
	}
}
