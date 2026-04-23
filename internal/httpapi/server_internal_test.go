package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type recordingWebhookIngestor struct {
	deliveryID string
	event      string
	payload    []byte
}

func (r *recordingWebhookIngestor) HandleWebhook(_ context.Context, deliveryID, event string, _ http.Header, payload []byte) error {
	r.deliveryID = deliveryID
	r.event = event
	r.payload = append([]byte(nil), payload...)
	return nil
}

type metricsChangeStatusStub struct{}

func (metricsChangeStatusStub) GetRepoChangeStatus(context.Context, string, string) (gitindex.RepoStatus, error) {
	return gitindex.RepoStatus{}, nil
}

func (metricsChangeStatusStub) GetPullRequestChangeStatus(context.Context, string, string, int) (gitindex.PullRequestStatus, error) {
	return gitindex.PullRequestStatus{}, nil
}

func (metricsChangeStatusStub) GetChangeSyncMetrics(context.Context) map[string]any {
	return map[string]any{"queues": map[string]any{"targeted_refresh": 3}}
}

func TestServerStartReturnsOnCanceledContext(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	server := NewServer(db, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, server.Start(ctx, "127.0.0.1:0"))
}

func TestServerStartReturnsListenError(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	server := NewServer(db, Options{})

	err := server.Start(context.Background(), "bad::addr")
	require.Error(t, err)
}

func TestHandleGitHubWebhookValidationAndAcceptance(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	secret := "top-secret"
	payload := []byte(`{"action":"opened"}`)
	signature := signGitHubPayload(secret, payload)

	t.Run("accepts valid webhook", func(t *testing.T) {
		ingestor := &recordingWebhookIngestor{}
		server := NewServer(db, Options{
			GitHubWebhookSecret: secret,
			WebhookIngestor:     ingestor,
		})

		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
		req.Header.Set("X-Hub-Signature-256", signature)
		req.Header.Set("X-GitHub-Event", "pull_request")
		req.Header.Set("X-GitHub-Delivery", "delivery-1")
		rec := httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)

		require.Equal(t, http.StatusAccepted, rec.Code)
		require.Equal(t, "delivery-1", ingestor.deliveryID)
		require.Equal(t, "pull_request", ingestor.event)
		require.JSONEq(t, string(payload), string(ingestor.payload))
	})

	t.Run("rejects invalid signature", func(t *testing.T) {
		server := NewServer(db, Options{
			GitHubWebhookSecret: secret,
			WebhookIngestor:     &recordingWebhookIngestor{},
		})
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
		req.Header.Set("X-Hub-Signature-256", "sha256=bad")
		req.Header.Set("X-GitHub-Event", "pull_request")
		req.Header.Set("X-GitHub-Delivery", "delivery-2")
		rec := httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("requires configuration", func(t *testing.T) {
		server := NewServer(db, Options{})
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
		rec := httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	require.True(t, validateGitHubSignature(secret, payload, signature))
	require.False(t, validateGitHubSignature(secret, payload, "sha1=bad"))
}

func TestHandleMetricsAndMirrorHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openHTTPAPITestDB(t)
	now := time.Now().UTC()

	repo := database.Repository{
		ID:            1,
		GitHubID:      101,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
	}
	require.NoError(t, db.Create(&repo).Error)
	require.NoError(t, db.Create(&database.TrackedRepository{
		ID:            1,
		Owner:         "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		RepositoryID:  &repo.ID,
		Enabled:       true,
		SyncMode:      "webhook_only",
		LastWebhookAt: &now,
	}).Error)
	require.NoError(t, db.Create(&database.RepositoryRefreshJob{FullName: "acme/widgets", Status: "pending", JobType: "bootstrap_repository", Source: "manual", MaxAttempts: 1, RequestedAt: now}).Error)
	require.NoError(t, db.Create(&database.RepositoryRefreshJob{FullName: "acme/widgets", Status: "failed", JobType: "bootstrap_repository", Source: "manual", MaxAttempts: 1, RequestedAt: now}).Error)
	require.NoError(t, db.Create(&database.WebhookDelivery{DeliveryID: "metrics-1", Event: "issues", PayloadJSON: []byte(`{}`), HeadersJSON: []byte(`{}`), ReceivedAt: now}).Error)

	server := NewServer(db, Options{ChangeStatus: metricsChangeStatusStub{}})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.EqualValues(t, 1, payload["webhook_deliveries_total"])
	require.EqualValues(t, 1, payload["refresh_jobs_pending"])
	require.EqualValues(t, 1, payload["refresh_jobs_failed"])

	changeSync := payload["change_sync"].(map[string]any)
	require.EqualValues(t, 3, changeSync["queues"].(map[string]any)["targeted_refresh"])

	foundRepo, err := findRepositoryByID(ctx, db, repo.ID)
	require.NoError(t, err)
	require.Equal(t, "acme/widgets", foundRepo.FullName)

	require.Equal(t, "degraded", mirrorSyncState(gitindex.RepoStatus{LastError: "boom"}))
	require.Equal(t, "running", mirrorSyncState(gitindex.RepoStatus{InventoryScanRunning: true}))
	require.Equal(t, "pending", mirrorSyncState(gitindex.RepoStatus{TargetedRefreshPending: true}))
	missing := 1
	require.Equal(t, "pending", mirrorSyncState(gitindex.RepoStatus{OpenPRMissing: &missing}))
	require.Equal(t, "idle", mirrorSyncState(gitindex.RepoStatus{}))
}

func TestHandleGetMirrorRepositoryStatusPendingRepository(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	require.NoError(t, db.Create(&database.TrackedRepository{
		ID:       7,
		Owner:    "acme",
		Name:     "widgets",
		FullName: "acme/widgets",
		Enabled:  true,
		SyncMode: "webhook_only",
	}).Error)

	server := NewServer(db, Options{ChangeStatus: metricsChangeStatusStub{}})
	req := httptest.NewRequest(http.MethodGet, "/v1/mirror/repos/acme/widgets/status", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "pending_repository", payload["sync"].(map[string]any)["state"])
}

func TestMirrorAndStoredResourceHandlerBranches(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	seedStoredGitHubReadData(t, db)

	server := NewServer(db, Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/mirror/repos/missing/repo", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/missing/repo", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/missing/repo/issues/7", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/missing/repo/pulls/7", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/issues/99", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/pulls/99", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	require.NoError(t, db.Create(&database.TrackedRepository{
		ID:           21,
		Owner:        "pending",
		Name:         "repo",
		FullName:     "pending/repo",
		RepositoryID: uintPtrForTest(9999),
		Enabled:      true,
		SyncMode:     "webhook_only",
	}).Error)

	req = httptest.NewRequest(http.MethodGet, "/v1/mirror/repos/pending/repo", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestMirrorStatusAndStoredArrayValidationBranches(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	server := NewServer(db, Options{})

	require.NoError(t, db.Create(&database.TrackedRepository{
		ID:       22,
		Owner:    "acme",
		Name:     "widgets",
		FullName: "acme/widgets",
		Enabled:  true,
		SyncMode: "webhook_only",
	}).Error)

	req := httptest.NewRequest(http.MethodGet, "/v1/mirror/repos/acme/widgets/status", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	repo := database.Repository{
		ID:         1,
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.Save(&repo).Error)
	require.NoError(t, db.Model(&database.TrackedRepository{}).Where("id = ?", 22).Update("repository_id", repo.ID).Error)

	server = NewServer(db, Options{ChangeStatus: stubChangeStatus{repoErr: gorm.ErrRecordNotFound}})
	req = httptest.NewRequest(http.MethodGet, "/v1/mirror/repos/acme/widgets/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	server = NewServer(db, Options{})
	c, rec := newServerTestContext(server, http.MethodGet, "/v1/github/repos/acme/widgets/issues/abc/comments", nil, []string{"owner", "repo", "number"}, []string{"acme", "widgets", "abc"})
	require.NoError(t, server.handleStoredJSONArray(c, func(context.Context, database.Repository, int) ([]any, error) {
		return []any{map[string]any{"id": 1}}, nil
	}))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	c, rec = newServerTestContext(server, http.MethodGet, "/v1/github/repos/missing/repo/issues/1/comments", nil, []string{"owner", "repo", "number"}, []string{"missing", "repo", "1"})
	require.NoError(t, server.handleStoredJSONArray(c, func(context.Context, database.Repository, int) ([]any, error) {
		return []any{}, nil
	}))
	require.Equal(t, http.StatusNotFound, rec.Code)

	c, rec = newServerTestContext(server, http.MethodGet, "/v1/github/repos/acme/widgets/issues/1/comments", nil, []string{"owner", "repo", "number"}, []string{"acme", "widgets", "1"})
	require.NoError(t, server.handleStoredJSONArray(c, func(context.Context, database.Repository, int) ([]any, error) {
		return nil, gorm.ErrRecordNotFound
	}))
	require.Equal(t, http.StatusNotFound, rec.Code)

	c, rec = newServerTestContext(server, http.MethodGet, "/v1/github/repos/acme/widgets/issues/1/comments", nil, []string{"owner", "repo", "number"}, []string{"acme", "widgets", "1"})
	require.NoError(t, server.handleStoredJSONArray(c, func(context.Context, database.Repository, int) ([]any, error) {
		return []any{map[string]any{"id": 1}}, nil
	}))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"id":1}]`, rec.Body.String())
}

func TestStoredCommentRoutesAndReadinessUnavailable(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	seedStoredGitHubReadData(t, db)
	server := NewServer(db, Options{})

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/v1/github/repos/acme/widgets/issues/7/comments", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/issues/99/comments", want: http.StatusNotFound},
		{path: "/v1/github/repos/acme/widgets/pulls/7/reviews", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/pulls/99/reviews", want: http.StatusNotFound},
		{path: "/v1/github/repos/acme/widgets/pulls/7/comments", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/pulls/99/comments", want: http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)
		require.Equal(t, tc.want, rec.Code, tc.path)
	}

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func openHTTPAPITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "httpapi-test.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))
	return db
}

func signGitHubPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newServerTestContext(server *Server, method, target string, body io.Reader, paramNames, paramValues []string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, target, body)
	rec := httptest.NewRecorder()
	c := server.Echo().NewContext(req, rec)
	c.SetParamNames(paramNames...)
	c.SetParamValues(paramValues...)
	return c, rec
}

func uintPtrForTest(value uint) *uint {
	return &value
}

func httpErrorCode(t *testing.T, err error) int {
	t.Helper()
	var httpErr *echo.HTTPError
	require.True(t, errors.As(err, &httpErr))
	return httpErr.Code
}
