package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"gorm.io/gorm"
)

type Server struct {
	db               *gorm.DB
	echo             *echo.Echo
	webhookSecret    string
	webhookIngestor  webhookIngestor
	changeStatus     changeStatusProvider
	search           *searchindex.Service
	structuralSearch structuralSearchProvider
}

type webhookIngestor interface {
	HandleWebhook(ctx context.Context, deliveryID, event string, headers http.Header, payload []byte) error
}

type Options struct {
	GitHubWebhookSecret string
	WebhookIngestor     webhookIngestor
	ChangeStatus        changeStatusProvider
	StructuralSearch    structuralSearchProvider
}

type changeStatusProvider interface {
	GetRepoChangeStatus(ctx context.Context, owner, repo string) (gitindex.RepoStatus, error)
	GetPullRequestChangeStatus(ctx context.Context, owner, repo string, number int) (gitindex.PullRequestStatus, error)
}

type structuralSearchProvider interface {
	SearchStructural(ctx context.Context, owner, repo string, request gitindex.StructuralSearchRequest) (gitindex.StructuralSearchResponse, error)
}

func NewServer(db *gorm.DB, options Options) *Server {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus: true,
		LogURI:    true,
		LogMethod: true,
		LogValuesFunc: func(_ echo.Context, v middleware.RequestLoggerValues) error {
			return nil
		},
	}))

	server := &Server{
		db:               db,
		echo:             e,
		webhookSecret:    strings.TrimSpace(options.GitHubWebhookSecret),
		webhookIngestor:  options.WebhookIngestor,
		changeStatus:     options.ChangeStatus,
		search:           searchindex.NewService(db),
		structuralSearch: options.StructuralSearch,
	}
	server.registerRoutes()
	return server
}

func (s *Server) Echo() *echo.Echo {
	return s.echo
}

func (s *Server) Start(ctx context.Context, addr string) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.echo.Start(addr)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.echo.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) registerRoutes() {
	s.echo.GET("/healthz", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	s.echo.GET("/readyz", s.handleReadiness)
	s.echo.GET("/metrics", s.handleMetrics)
	s.echo.POST("/webhooks/github", s.handleGitHubWebhook)
	s.echo.GET("/v1/github/repos/:owner/:repo", s.handleGetRepository)
	s.echo.GET("/v1/github/repos/:owner/:repo/issues", s.handleListIssues)
	s.echo.GET("/v1/github/repos/:owner/:repo/issues/:number", s.handleGetIssue)
	s.echo.GET("/v1/github/repos/:owner/:repo/issues/:number/comments", s.handleListIssueComments)
	s.echo.GET("/v1/github/repos/:owner/:repo/pulls", s.handleListPullRequests)
	s.echo.GET("/v1/github/repos/:owner/:repo/pulls/:number", s.handleGetPullRequest)
	s.echo.GET("/v1/github/repos/:owner/:repo/pulls/:number/reviews", s.handleListPullRequestReviews)
	s.echo.GET("/v1/github/repos/:owner/:repo/pulls/:number/comments", s.handleListPullRequestReviewComments)
	s.echo.POST("/v1/github-ext/repos/:owner/:repo/objects/batch", s.handleBatchReadObjects)
	s.echo.GET("/v1/mirror/repos", s.handleListMirrorRepositories)
	s.echo.GET("/v1/mirror/repos/:owner/:repo", s.handleGetMirrorRepository)
	s.echo.GET("/v1/mirror/repos/:owner/:repo/status", s.handleGetMirrorRepositoryStatus)
	s.echo.GET("/v1/changes/repos/:owner/:repo/mirror-status", s.handleGetMirrorStatus)
	s.echo.GET("/v1/changes/repos/:owner/:repo/pulls/:number", s.handleGetPullRequestChangeSnapshot)
	s.echo.GET("/v1/changes/repos/:owner/:repo/pulls/:number/files", s.handleListPullRequestChangeFiles)
	s.echo.GET("/v1/changes/repos/:owner/:repo/pulls/:number/status", s.handleGetPullRequestChangeStatus)
	s.echo.GET("/v1/changes/repos/:owner/:repo/status", s.handleGetRepoChangeStatus)
	s.echo.GET("/v1/changes/repos/:owner/:repo/commits/:sha", s.handleGetCommit)
	s.echo.GET("/v1/changes/repos/:owner/:repo/commits/:sha/files", s.handleListCommitFiles)
	s.echo.GET("/v1/changes/repos/:owner/:repo/compare/:spec", s.handleCompareChanges)
	s.echo.GET("/v1/search/repos/:owner/:repo/pulls/:number/related", s.handleSearchRelatedPullRequests)
	s.echo.POST("/v1/search/repos/:owner/:repo/pulls/by-paths", s.handleSearchPullRequestsByPaths)
	s.echo.POST("/v1/search/repos/:owner/:repo/pulls/by-ranges", s.handleSearchPullRequestsByRanges)
	s.echo.GET("/v1/search/repos/:owner/:repo/status", s.handleGetRepoSearchStatus)
	s.echo.POST("/v1/search/repos/:owner/:repo/mentions", s.handleSearchMentions)
	s.echo.POST("/v1/search/repos/:owner/:repo/ast-grep", s.handleSearchASTGrep)
}

func (s *Server) handleGitHubWebhook(c echo.Context) error {
	if s.webhookIngestor == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "GitHub webhook handling is not configured"})
	}
	if s.webhookSecret == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "GitHub webhook secret is not configured"})
	}

	payload, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return err
	}

	signature := c.Request().Header.Get("X-Hub-Signature-256")
	if !validateGitHubSignature(s.webhookSecret, payload, signature) {
		return c.JSON(http.StatusUnauthorized, map[string]string{"message": "Invalid webhook signature"})
	}

	event := strings.TrimSpace(c.Request().Header.Get("X-GitHub-Event"))
	if event == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Missing X-GitHub-Event header"})
	}

	deliveryID := strings.TrimSpace(c.Request().Header.Get("X-GitHub-Delivery"))
	if deliveryID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Missing X-GitHub-Delivery header"})
	}

	if err := s.webhookIngestor.HandleWebhook(c.Request().Context(), deliveryID, event, c.Request().Header.Clone(), payload); err != nil {
		return err
	}

	return c.JSON(http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (s *Server) handleGetRepository(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	payload, err := decodeStoredJSON(repo.RawJSON)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, payload)
}

func (s *Server) handleGetIssue(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	number := parsePositiveInt(c.Param("number"), 0)
	if number <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid issue number"})
	}

	var issue database.Issue
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND number = ?", repo.ID, number).
		First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	payload, err := decodeStoredJSON(issue.RawJSON)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, payload)
}

func (s *Server) handleListIssues(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	state := c.QueryParam("state")
	if state == "" {
		state = "open"
	}
	page := parsePositiveInt(c.QueryParam("page"), 1)
	perPage := clamp(parsePositiveInt(c.QueryParam("per_page"), 30), 1, 100)

	var total int64
	query := s.db.WithContext(c.Request().Context()).Model(&database.Issue{}).Where("repository_id = ?", repo.ID)
	query = applyStateFilter(query, state)
	if err := query.Count(&total).Error; err != nil {
		return err
	}

	var issues []database.Issue
	if err := query.Preload("Author").Order("github_created_at DESC").Limit(perPage).Offset((page - 1) * perPage).Find(&issues).Error; err != nil {
		return err
	}

	queryMap := map[string]string{}
	if state != "" {
		queryMap["state"] = state
	}

	if link := database.BuildLinkHeader(c.Request().URL.Path, queryMap, page, perPage, int(total)); link != "" {
		c.Response().Header().Set("Link", link)
	}

	response, err := decodeStoredJSONArrayIssues(issues)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (s *Server) handleListPullRequests(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	state := c.QueryParam("state")
	if state == "" {
		state = "open"
	}
	page := parsePositiveInt(c.QueryParam("page"), 1)
	perPage := clamp(parsePositiveInt(c.QueryParam("per_page"), 30), 1, 100)

	var total int64
	query := s.db.WithContext(c.Request().Context()).Model(&database.PullRequest{}).Where("repository_id = ?", repo.ID)
	query = applyStateFilter(query, state)
	if err := query.Count(&total).Error; err != nil {
		return err
	}

	var pulls []database.PullRequest
	if err := query.
		Preload("Issue").
		Preload("Issue.Author").
		Preload("MergedBy").
		Preload("HeadRepo.Owner").
		Preload("BaseRepo.Owner").
		Order("github_created_at DESC").
		Limit(perPage).
		Offset((page - 1) * perPage).
		Find(&pulls).Error; err != nil {
		return err
	}

	queryMap := map[string]string{}
	if state != "" {
		queryMap["state"] = state
	}

	if link := database.BuildLinkHeader(c.Request().URL.Path, queryMap, page, perPage, int(total)); link != "" {
		c.Response().Header().Set("Link", link)
	}

	response, err := decodeStoredJSONArrayPullRequests(pulls)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, response)
}

func (s *Server) handleGetPullRequest(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	number := parsePositiveInt(c.Param("number"), 0)
	if number <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid pull request number"})
	}

	var pull database.PullRequest
	if err := s.db.WithContext(c.Request().Context()).
		Where("repository_id = ? AND number = ?", repo.ID, number).
		First(&pull).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	payload, err := decodeStoredJSON(pull.RawJSON)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, payload)
}

func (s *Server) handleListIssueComments(c echo.Context) error {
	return s.handleStoredJSONArray(c, func(ctx context.Context, repo database.Repository, number int) ([]any, error) {
		var issue database.Issue
		if err := s.db.WithContext(ctx).Where("repository_id = ? AND number = ?", repo.ID, number).First(&issue).Error; err != nil {
			return nil, err
		}

		var comments []database.IssueComment
		if err := s.db.WithContext(ctx).
			Where("issue_id = ?", issue.ID).
			Order("github_created_at ASC").
			Find(&comments).Error; err != nil {
			return nil, err
		}
		return decodeStoredJSONArrayIssueComments(comments)
	})
}

func (s *Server) handleListPullRequestReviews(c echo.Context) error {
	return s.handleStoredJSONArray(c, func(ctx context.Context, repo database.Repository, number int) ([]any, error) {
		var pull database.PullRequest
		if err := s.db.WithContext(ctx).Where("repository_id = ? AND number = ?", repo.ID, number).First(&pull).Error; err != nil {
			return nil, err
		}

		var reviews []database.PullRequestReview
		if err := s.db.WithContext(ctx).
			Where("pull_request_id = ?", pull.IssueID).
			Order("github_created_at ASC").
			Find(&reviews).Error; err != nil {
			return nil, err
		}
		return decodeStoredJSONArrayPullRequestReviews(reviews)
	})
}

func (s *Server) handleListPullRequestReviewComments(c echo.Context) error {
	return s.handleStoredJSONArray(c, func(ctx context.Context, repo database.Repository, number int) ([]any, error) {
		var pull database.PullRequest
		if err := s.db.WithContext(ctx).Where("repository_id = ? AND number = ?", repo.ID, number).First(&pull).Error; err != nil {
			return nil, err
		}

		var comments []database.PullRequestReviewComment
		if err := s.db.WithContext(ctx).
			Where("pull_request_id = ?", pull.IssueID).
			Order("github_created_at ASC").
			Find(&comments).Error; err != nil {
			return nil, err
		}
		return decodeStoredJSONArrayPullRequestReviewComments(comments)
	})
}

func (s *Server) handleReadiness(c echo.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]any{
			"status":   "not_ready",
			"database": "unavailable",
		})
	}

	pingCtx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]any{
			"status":   "not_ready",
			"database": "unavailable",
		})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"status":   "ready",
		"database": "ok",
	})
}

func (s *Server) handleMetrics(c echo.Context) error {
	ctx := c.Request().Context()

	var pending int64
	var processing int64
	var failed int64
	var succeeded int64
	var superseded int64
	var deliveries int64

	for _, query := range []struct {
		model any
		where string
		dest  *int64
	}{
		{&database.RepositoryRefreshJob{}, "status = 'pending'", &pending},
		{&database.RepositoryRefreshJob{}, "status = 'processing'", &processing},
		{&database.RepositoryRefreshJob{}, "status = 'failed'", &failed},
		{&database.RepositoryRefreshJob{}, "status = 'succeeded'", &succeeded},
		{&database.RepositoryRefreshJob{}, "status = 'superseded'", &superseded},
		{&database.WebhookDelivery{}, "", &deliveries},
	} {
		dbq := s.db.WithContext(ctx).Model(query.model)
		if query.where != "" {
			dbq = dbq.Where(query.where)
		}
		if err := dbq.Count(query.dest).Error; err != nil {
			return err
		}
	}

	return c.JSON(http.StatusOK, map[string]any{
		"webhook_deliveries_total": deliveries,
		"refresh_jobs_pending":     pending,
		"refresh_jobs_processing":  processing,
		"refresh_jobs_failed":      failed,
		"refresh_jobs_succeeded":   succeeded,
		"refresh_jobs_superseded":  superseded,
	})
}

func findRepository(ctx context.Context, db *gorm.DB, owner, repo string) (database.Repository, error) {
	var out database.Repository
	err := db.WithContext(ctx).Preload("Owner").Where("owner_login = ? AND name = ?", owner, repo).First(&out).Error
	return out, err
}

func applyStateFilter(query *gorm.DB, state string) *gorm.DB {
	switch state {
	case "open", "closed":
		return query.Where("state = ?", state)
	default:
		return query
	}
}

func (s *Server) handleStoredJSONArray(c echo.Context, loader func(ctx context.Context, repo database.Repository, number int) ([]any, error)) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	number := parsePositiveInt(c.Param("number"), 0)
	if number <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"message": "Invalid number"})
	}

	payload, err := loader(c.Request().Context(), repo, number)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	return c.JSON(http.StatusOK, payload)
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func validateGitHubSignature(secret string, payload []byte, signature string) bool {
	signature = strings.TrimSpace(signature)
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
