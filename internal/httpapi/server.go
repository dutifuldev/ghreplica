package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"gorm.io/gorm"
)

type Server struct {
	db   *gorm.DB
	echo *echo.Echo
}

func NewServer(db *gorm.DB) *Server {
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
		db:   db,
		echo: e,
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
	s.echo.GET("/repos/:owner/:repo", s.handleGetRepository)
	s.echo.GET("/repos/:owner/:repo/issues", s.handleListIssues)
	s.echo.GET("/repos/:owner/:repo/pulls", s.handleListPullRequests)
}

func (s *Server) handleGetRepository(c echo.Context) error {
	repo, err := findRepository(c.Request().Context(), s.db, c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	return c.JSON(http.StatusOK, newRepositoryResponse(repo))
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

	pullMap, err := loadPullRequestRefs(c.Request().Context(), s.db, issues)
	if err != nil {
		return err
	}

	queryMap := map[string]string{}
	if state != "" {
		queryMap["state"] = state
	}

	if link := database.BuildLinkHeader(c.Request().URL.Path, queryMap, page, perPage, int(total)); link != "" {
		c.Response().Header().Set("Link", link)
	}

	response := make([]issueResponse, 0, len(issues))
	for _, issue := range issues {
		response = append(response, newIssueResponse(issue, pullMap[issue.ID]))
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

	response := make([]pullRequestResponse, 0, len(pulls))
	for _, pull := range pulls {
		response = append(response, newPullRequestResponse(pull))
	}

	return c.JSON(http.StatusOK, response)
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

func loadPullRequestRefs(ctx context.Context, db *gorm.DB, issues []database.Issue) (map[uint]issuePullRequestRef, error) {
	issueIDs := make([]uint, 0)
	for _, issue := range issues {
		if issue.IsPullRequest {
			issueIDs = append(issueIDs, issue.ID)
		}
	}
	if len(issueIDs) == 0 {
		return map[uint]issuePullRequestRef{}, nil
	}

	var pulls []database.PullRequest
	if err := db.WithContext(ctx).Where("issue_id IN ?", issueIDs).Find(&pulls).Error; err != nil {
		return nil, err
	}

	out := make(map[uint]issuePullRequestRef, len(pulls))
	for _, pull := range pulls {
		out[pull.IssueID] = issuePullRequestRef{
			URL:      pull.APIURL,
			HTMLURL:  pull.HTMLURL,
			DiffURL:  pull.DiffURL,
			PatchURL: pull.PatchURL,
		}
	}

	return out, nil
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
