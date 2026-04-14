package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func (s *Server) handleGetMirrorStatus(c echo.Context) error {
	ctx := c.Request().Context()
	fullName := c.Param("owner") + "/" + c.Param("repo")

	var tracked database.TrackedRepository
	err := s.db.WithContext(ctx).Where("full_name = ?", fullName).First(&tracked).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	var repo database.Repository
	repoErr := s.db.WithContext(ctx).Preload("Owner").Where("full_name = ?", fullName).First(&repo).Error
	if repoErr != nil && !errors.Is(repoErr, gorm.ErrRecordNotFound) {
		return repoErr
	}

	if errors.Is(err, gorm.ErrRecordNotFound) && errors.Is(repoErr, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
	}

	var repositoryID *uint
	if repoErr == nil {
		repositoryID = &repo.ID
	} else if tracked.RepositoryID != nil {
		repositoryID = tracked.RepositoryID
	}

	status := mirrorStatusResponse{
		FullName:                 fullName,
		RepositoryPresent:        repoErr == nil,
		TrackedRepositoryPresent: !errors.Is(err, gorm.ErrRecordNotFound),
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
	}

	if repositoryID != nil {
		counts, err := s.loadMirrorCounts(ctx, *repositoryID)
		if err != nil {
			return err
		}
		status.Counts = counts
	}

	if repoErr == nil {
		repository := newRepositoryResponse(repo)
		status.Repository = &repository
	}

	return c.JSON(http.StatusOK, status)
}

func (s *Server) loadMirrorCounts(ctx context.Context, repositoryID uint) (mirrorCountsResponse, error) {
	var counts mirrorCountsResponse
	for _, query := range []struct {
		model any
		dest  *int64
	}{
		{&database.Issue{}, &counts.Issues},
		{&database.PullRequest{}, &counts.Pulls},
		{&database.IssueComment{}, &counts.IssueComments},
		{&database.PullRequestReview{}, &counts.PullRequestReviews},
		{&database.PullRequestReviewComment{}, &counts.PullRequestReviewComments},
	} {
		if err := s.db.WithContext(ctx).Model(query.model).Where("repository_id = ?", repositoryID).Count(query.dest).Error; err != nil {
			return mirrorCountsResponse{}, err
		}
	}
	return counts, nil
}
