package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func (s *Server) handleGetMirrorStatus(c echo.Context) error {
	ctx := c.Request().Context()
	fullName := c.Param("owner") + "/" + c.Param("repo")

	repo, repoErr := findRepository(ctx, s.db, c.Param("owner"), c.Param("repo"))
	if repoErr != nil && !errors.Is(repoErr, gorm.ErrRecordNotFound) {
		return repoErr
	}

	var repositoryID *uint
	if repoErr == nil {
		repositoryID = &repo.ID
	}

	tracked, err := refresh.ResolveTrackedRepository(ctx, s.db, repositoryID, fullName)
	if err != nil {
		return err
	}
	if tracked == nil && errors.Is(repoErr, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
	if repositoryID == nil && tracked != nil && tracked.RepositoryID != nil {
		repositoryID = tracked.RepositoryID
	}

	status := mirrorStatusResponse{
		FullName:                 fullName,
		RepositoryPresent:        repoErr == nil,
		TrackedRepositoryPresent: tracked != nil,
		TrackedRepositoryID:      trackedRepositoryIDPtr(tracked),
		RepositoryID:             repositoryID,
	}
	if tracked != nil {
		status.Enabled = tracked.Enabled
		status.SyncMode = tracked.SyncMode
		status.WebhookProjectionEnabled = tracked.WebhookProjectionEnabled
		status.AllowManualBackfill = tracked.AllowManualBackfill
		status.IssuesCompleteness = tracked.IssuesCompleteness
		status.PullsCompleteness = tracked.PullsCompleteness
		status.CommentsCompleteness = tracked.CommentsCompleteness
		status.ReviewsCompleteness = tracked.ReviewsCompleteness
		status.LastBootstrapAt = utcTimePtr(tracked.LastBootstrapAt)
		status.LastCrawlAt = utcTimePtr(tracked.LastCrawlAt)
		status.LastWebhookAt = utcTimePtr(tracked.LastWebhookAt)
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

func trackedRepositoryIDPtr(tracked *database.TrackedRepository) *uint {
	if tracked == nil {
		return nil
	}
	return uintPtr(tracked.ID)
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
