package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func (s *Server) handleListMirrorRepositories(c echo.Context) error {
	page := parsePositiveInt(c.QueryParam("page"), 1)
	perPage := clamp(parsePositiveInt(c.QueryParam("per_page"), 30), 1, 100)

	var total int64
	if err := s.db.WithContext(c.Request().Context()).Model(&database.TrackedRepository{}).Count(&total).Error; err != nil {
		return err
	}

	var tracked []database.TrackedRepository
	if err := s.db.WithContext(c.Request().Context()).
		Order("full_name ASC").
		Limit(perPage).
		Offset((page - 1) * perPage).
		Find(&tracked).Error; err != nil {
		return err
	}

	queryMap := map[string]string{}
	if link := database.BuildLinkHeader(c.Request().URL.Path, queryMap, page, perPage, int(total)); link != "" {
		c.Response().Header().Set("Link", link)
	}

	repositories, err := s.loadRepositoriesForTracked(c.Request().Context(), tracked)
	if err != nil {
		return err
	}
	counts, err := s.loadMirrorCountsByRepositoryID(c.Request().Context(), mapsKeys(repositories))
	if err != nil {
		return err
	}

	response := make([]mirrorRepositoryResponse, 0, len(tracked))
	for _, item := range tracked {
		repositoryID := trackedRepositoryRepositoryID(item)
		response = append(response, newMirrorRepositoryResponse(item, repositories[repositoryID], counts[repositoryID]))
	}
	return c.JSON(http.StatusOK, response)
}

func (s *Server) handleGetMirrorRepository(c echo.Context) error {
	repo, tracked, err := s.resolveMirrorRepository(c.Request().Context(), c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	var counts mirrorCountsResponse
	if repo != nil {
		counts, err = s.loadMirrorCounts(c.Request().Context(), repo.ID)
		if err != nil {
			return err
		}
	}

	return c.JSON(http.StatusOK, newMirrorRepositoryResponse(*tracked, repo, counts))
}

func (s *Server) handleGetMirrorRepositoryStatus(c echo.Context) error {
	if s.changeStatus == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"message": "Change status is not configured"})
	}

	repo, tracked, err := s.resolveMirrorRepository(c.Request().Context(), c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	if repo == nil {
		return c.JSON(http.StatusOK, mirrorRepositoryStatusResponse{
			Repository: mirrorRepositoryRefResponse{
				Owner:    tracked.Owner,
				Name:     tracked.Name,
				FullName: tracked.FullName,
			},
			Sync: mirrorSyncResponse{
				State: "pending_repository",
			},
			PullRequestChanges: mirrorPullRequestChangesResponse{},
			Activity:           mirrorActivityResponse{},
			Timestamps:         mirrorStatusTimestampsResponse{},
		})
	}

	status, err := s.changeStatus.GetRepoChangeStatus(c.Request().Context(), repo.OwnerLogin, repo.Name)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	return c.JSON(http.StatusOK, newMirrorRepositoryStatusResponse(status))
}

func (s *Server) handleGetMirrorStatus(c echo.Context) error {
	repo, tracked, err := s.resolveMirrorRepository(c.Request().Context(), c.Param("owner"), c.Param("repo"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
		return err
	}

	var (
		repositoryID *uint
		counts       mirrorCountsResponse
	)
	if repo != nil {
		repositoryID = &repo.ID
		counts, err = s.loadMirrorCounts(c.Request().Context(), repo.ID)
		if err != nil {
			return err
		}
	}

	status := mirrorStatusResponse{
		FullName:                 tracked.FullName,
		RepositoryPresent:        repo != nil,
		TrackedRepositoryPresent: true,
		TrackedRepositoryID:      uintPtr(tracked.ID),
		RepositoryID:             repositoryID,
		Enabled:                  tracked.Enabled,
		SyncMode:                 tracked.SyncMode,
		WebhookProjectionEnabled: tracked.WebhookProjectionEnabled,
		AllowManualBackfill:      tracked.AllowManualBackfill,
		IssuesCompleteness:       tracked.IssuesCompleteness,
		PullsCompleteness:        tracked.PullsCompleteness,
		CommentsCompleteness:     tracked.CommentsCompleteness,
		ReviewsCompleteness:      tracked.ReviewsCompleteness,
		LastBootstrapAt:          utcTimePtr(tracked.LastBootstrapAt),
		LastCrawlAt:              utcTimePtr(tracked.LastCrawlAt),
		LastWebhookAt:            utcTimePtr(tracked.LastWebhookAt),
		Counts:                   counts,
	}
	if repo != nil {
		repository := newRepositoryResponse(*repo)
		status.Repository = &repository
		status.FullName = repo.FullName
	}

	return c.JSON(http.StatusOK, status)
}

func (s *Server) resolveMirrorRepository(ctx context.Context, owner, name string) (*database.Repository, *database.TrackedRepository, error) {
	fullName := strings.TrimSpace(owner) + "/" + strings.TrimSpace(name)

	repo, repoErr := findRepository(ctx, s.db, owner, name)
	if repoErr != nil && !errors.Is(repoErr, gorm.ErrRecordNotFound) {
		return nil, nil, repoErr
	}

	var repositoryID *uint
	if repoErr == nil {
		repositoryID = &repo.ID
	}

	tracked, err := refresh.ResolveTrackedRepository(ctx, s.db, repositoryID, fullName)
	if err != nil {
		return nil, nil, err
	}
	if tracked == nil {
		return nil, nil, gorm.ErrRecordNotFound
	}

	if repoErr == nil {
		return &repo, tracked, nil
	}

	if tracked.RepositoryID == nil {
		return nil, tracked, nil
	}

	resolvedRepo, err := findRepositoryByID(ctx, s.db, *tracked.RepositoryID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, tracked, nil
		}
		return nil, nil, err
	}
	return &resolvedRepo, tracked, nil
}

func findRepositoryByID(ctx context.Context, db *gorm.DB, repositoryID uint) (database.Repository, error) {
	var out database.Repository
	err := db.WithContext(ctx).Preload("Owner").First(&out, repositoryID).Error
	return out, err
}

func (s *Server) loadRepositoriesForTracked(ctx context.Context, tracked []database.TrackedRepository) (map[uint]*database.Repository, error) {
	repositoryIDSet := map[uint]struct{}{}
	for _, item := range tracked {
		if item.RepositoryID != nil {
			repositoryIDSet[*item.RepositoryID] = struct{}{}
		}
	}
	repositoryIDs := make([]uint, 0, len(repositoryIDSet))
	for repositoryID := range repositoryIDSet {
		repositoryIDs = append(repositoryIDs, repositoryID)
	}
	if len(repositoryIDs) == 0 {
		return map[uint]*database.Repository{}, nil
	}

	var repositories []database.Repository
	if err := s.db.WithContext(ctx).
		Preload("Owner").
		Where("id IN ?", repositoryIDs).
		Find(&repositories).Error; err != nil {
		return nil, err
	}

	out := make(map[uint]*database.Repository, len(repositories))
	for i := range repositories {
		repo := repositories[i]
		out[repo.ID] = &repo
	}
	return out, nil
}

func (s *Server) loadMirrorCounts(ctx context.Context, repositoryID uint) (mirrorCountsResponse, error) {
	countsByRepositoryID, err := s.loadMirrorCountsByRepositoryID(ctx, []uint{repositoryID})
	if err != nil {
		return mirrorCountsResponse{}, err
	}
	return countsByRepositoryID[repositoryID], nil
}

func (s *Server) loadMirrorCountsByRepositoryID(ctx context.Context, repositoryIDs []uint) (map[uint]mirrorCountsResponse, error) {
	out := make(map[uint]mirrorCountsResponse, len(repositoryIDs))
	if len(repositoryIDs) == 0 {
		return out, nil
	}

	type countRow struct {
		RepositoryID uint
		Count        int64
	}

	for _, query := range []struct {
		model any
		apply func(*mirrorCountsResponse, int64)
	}{
		{&database.Issue{}, func(dest *mirrorCountsResponse, count int64) { dest.Issues = count }},
		{&database.PullRequest{}, func(dest *mirrorCountsResponse, count int64) { dest.Pulls = count }},
		{&database.IssueComment{}, func(dest *mirrorCountsResponse, count int64) { dest.IssueComments = count }},
		{&database.PullRequestReview{}, func(dest *mirrorCountsResponse, count int64) { dest.PullRequestReviews = count }},
		{&database.PullRequestReviewComment{}, func(dest *mirrorCountsResponse, count int64) { dest.PullRequestReviewComments = count }},
	} {
		var rows []countRow
		if err := s.db.WithContext(ctx).
			Model(query.model).
			Select("repository_id, COUNT(*) AS count").
			Where("repository_id IN ?", repositoryIDs).
			Group("repository_id").
			Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			counts := out[row.RepositoryID]
			query.apply(&counts, row.Count)
			out[row.RepositoryID] = counts
		}
	}

	return out, nil
}

func newMirrorRepositoryResponse(tracked database.TrackedRepository, repo *database.Repository, counts mirrorCountsResponse) mirrorRepositoryResponse {
	response := mirrorRepositoryResponse{
		Owner:    tracked.Owner,
		Name:     tracked.Name,
		FullName: tracked.FullName,
		Enabled:  tracked.Enabled,
		SyncMode: tracked.SyncMode,
		Completeness: mirrorCompletenessResponse{
			Issues:   tracked.IssuesCompleteness,
			Pulls:    tracked.PullsCompleteness,
			Comments: tracked.CommentsCompleteness,
			Reviews:  tracked.ReviewsCompleteness,
		},
		Coverage: counts,
		Timestamps: mirrorMetadataTimestampsResponse{
			LastWebhookAt:   utcTimePtr(tracked.LastWebhookAt),
			LastBootstrapAt: utcTimePtr(tracked.LastBootstrapAt),
			LastCrawlAt:     utcTimePtr(tracked.LastCrawlAt),
		},
	}
	if repo != nil {
		response.Owner = repo.OwnerLogin
		response.Name = repo.Name
		response.FullName = repo.FullName
		response.GitHubID = int64Ptr(repo.GitHubID)
		response.NodeID = repo.NodeID
		response.Fork = boolPtr(repo.Fork)
	}
	return response
}

func newMirrorRepositoryStatusResponse(status gitindex.RepoStatus) mirrorRepositoryStatusResponse {
	return mirrorRepositoryStatusResponse{
		Repository: mirrorRepositoryRefResponse{
			Owner:    ownerFromFullName(status.FullName),
			Name:     nameFromFullName(status.FullName),
			FullName: status.FullName,
		},
		Sync: mirrorSyncResponse{
			State:     mirrorSyncState(status),
			LastError: strings.TrimSpace(status.LastError),
		},
		PullRequestChanges: mirrorPullRequestChangesResponse{
			Total:   status.OpenPRTotal,
			Current: status.OpenPRCurrent,
			Stale:   status.OpenPRStale,
			Missing: status.OpenPRMissing,
		},
		Activity: mirrorActivityResponse{
			InventoryScanRunning:      status.InventoryScanRunning,
			BackfillRunning:           status.BackfillRunning,
			TargetedRefreshPending:    status.TargetedRefreshPending,
			TargetedRefreshRunning:    status.TargetedRefreshRunning,
			InventoryRefreshRequested: status.InventoryNeedsRefresh,
		},
		Timestamps: mirrorStatusTimestampsResponse{
			LastInventoryScanStartedAt:  utcTimePtr(status.LastInventoryScanStartedAt),
			LastInventoryScanFinishedAt: utcTimePtr(status.LastInventoryScanFinishedAt),
			LastBackfillStartedAt:       utcTimePtr(status.LastBackfillStartedAt),
			LastBackfillFinishedAt:      utcTimePtr(status.LastBackfillFinishedAt),
		},
	}
}

func mirrorSyncState(status gitindex.RepoStatus) string {
	if strings.TrimSpace(status.LastError) != "" {
		return "degraded"
	}
	if status.InventoryScanRunning || status.BackfillRunning || status.TargetedRefreshRunning {
		return "running"
	}
	if status.TargetedRefreshPending || status.InventoryNeedsRefresh || status.OpenPRMissing > 0 || status.OpenPRStale > 0 {
		return "pending"
	}
	return "idle"
}

func ownerFromFullName(fullName string) string {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

func nameFromFullName(fullName string) string {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return fullName
	}
	return parts[1]
}

func mapsKeys(input map[uint]*database.Repository) []uint {
	out := make([]uint, 0, len(input))
	for id := range input {
		out = append(out, id)
	}
	return out
}

func int64Ptr(value int64) *int64 {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func trackedRepositoryRepositoryID(tracked database.TrackedRepository) uint {
	if tracked.RepositoryID == nil {
		return 0
	}
	return *tracked.RepositoryID
}
