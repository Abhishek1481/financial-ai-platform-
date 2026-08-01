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

	commonv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/common/v1"
	ingestionv1 "github.com/Abhishek1481/financial-ai-platform/proto/gen/go/ingestion/v1"

	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/auth"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/config"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/health"
	"github.com/Abhishek1481/financial-ai-platform/gateway-go/internal/ingestion"
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

func startTestServerWithExtractor(t *testing.T, ml ingestion.Extractor) *Server {
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
		ml,
		2, 10,
	)
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	ingestionService.Start(workerCtx)
	t.Cleanup(cancelWorkers)

	server := New(cfg, logger, Dependencies{
		Readiness:   readiness,
		AuthService: authService,
		Tokens:      tokens,
		Ingestion:   ingestionService,
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
