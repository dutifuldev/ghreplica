package githubsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db            *gorm.DB
	github        *gh.Client
	git           *gitindex.Service
	search        *searchindex.Service
	repairMetrics *repairMetricsRegistry
}

func NewService(db *gorm.DB, githubClient *gh.Client, gitIndex ...*gitindex.Service) *Service {
	var indexer *gitindex.Service
	if len(gitIndex) > 0 {
		indexer = gitIndex[0]
	}
	return &Service{
		db:            db,
		github:        githubClient,
		git:           indexer,
		search:        searchindex.NewService(db),
		repairMetrics: newRepairMetricsRegistry(),
	}
}

func (s *Service) withoutSearch() *Service {
	clone := *s
	clone.search = nil
	return &clone
}

func (s *Service) GetChangeSyncMetrics(ctx context.Context) map[string]any {
	if s.repairMetrics == nil {
		return map[string]any{}
	}
	return map[string]any{
		"repair": s.repairMetrics.snapshot(ctx),
	}
}

func sanitizeProjectedText(value string) string {
	if !strings.Contains(value, "\x00") {
		return value
	}
	return strings.ReplaceAll(value, "\x00", "")
}

func (s *Service) UpsertRepository(ctx context.Context, repo gh.RepositoryResponse) (database.Repository, error) {
	return s.upsertRepository(ctx, repo)
}

func (s *Service) UpsertIssue(ctx context.Context, repositoryID uint, issue gh.IssueResponse) (database.Issue, error) {
	return s.upsertIssue(ctx, repositoryID, issue)
}

func (s *Service) UpsertPullRequest(ctx context.Context, repositoryID uint, pull gh.PullRequestResponse) error {
	return s.upsertPullRequest(ctx, repositoryID, pull)
}

func (s *Service) UpsertIssueComment(ctx context.Context, repositoryID uint, comment gh.IssueCommentResponse) error {
	return s.upsertIssueComment(ctx, repositoryID, comment)
}

func (s *Service) UpsertPullRequestReview(ctx context.Context, repositoryID uint, pullNumber int, review gh.PullRequestReviewResponse) error {
	return s.upsertPullRequestReview(ctx, repositoryID, pullNumber, review)
}

func (s *Service) UpsertPullRequestReviewComment(ctx context.Context, repositoryID uint, pullNumber int, comment gh.PullRequestReviewCommentResponse) error {
	return s.upsertPullRequestReviewComment(ctx, repositoryID, pullNumber, comment)
}

func (s *Service) loadStoredIssuesByNumber(ctx context.Context, repositoryID uint, numbers []int) (map[int]database.Issue, error) {
	if len(numbers) == 0 {
		return map[int]database.Issue{}, nil
	}

	var issues []database.Issue
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND number IN ?", repositoryID, uniqueInts(numbers)).
		Find(&issues).Error; err != nil {
		return nil, err
	}

	result := make(map[int]database.Issue, len(issues))
	for _, issue := range issues {
		result[issue.Number] = issue
	}
	return result, nil
}

func (s *Service) loadStoredPullRequestsByNumber(ctx context.Context, repositoryID uint, numbers []int) (map[int]database.PullRequest, error) {
	if len(numbers) == 0 {
		return map[int]database.PullRequest{}, nil
	}

	var pulls []database.PullRequest
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND number IN ?", repositoryID, uniqueInts(numbers)).
		Find(&pulls).Error; err != nil {
		return nil, err
	}

	result := make(map[int]database.PullRequest, len(pulls))
	for _, pull := range pulls {
		result[pull.Number] = pull
	}
	return result, nil
}

func uniqueInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *Service) BootstrapRepository(ctx context.Context, owner, repo string) error {
	repoResp, err := s.github.GetRepository(ctx, owner, repo)
	if err != nil {
		return err
	}

	canonicalRepo, err := s.upsertRepository(ctx, repoResp)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	syncMode, err := s.existingSyncMode(ctx, repoResp.FullName, &canonicalRepo.ID)
	if err != nil {
		return err
	}

	tracked := database.TrackedRepository{
		Owner:                    owner,
		Name:                     repo,
		FullName:                 repoResp.FullName,
		RepositoryID:             &canonicalRepo.ID,
		SyncMode:                 syncMode,
		WebhookProjectionEnabled: true,
		AllowManualBackfill:      true,
		IssuesCompleteness:       "backfilled",
		PullsCompleteness:        "backfilled",
		CommentsCompleteness:     "backfilled",
		ReviewsCompleteness:      "backfilled",
		Enabled:                  true,
		LastBootstrapAt:          &now,
		LastCrawlAt:              &now,
	}
	if err := s.upsertTrackedRepository(ctx, tracked); err != nil {
		return err
	}

	issues, err := s.github.ListIssues(ctx, owner, repo, "all")
	if err != nil {
		return err
	}
	for _, issue := range issues {
		detail, err := s.github.GetIssue(ctx, owner, repo, issue.Number)
		if err != nil {
			return err
		}
		if _, err := s.upsertIssue(ctx, canonicalRepo.ID, detail); err != nil {
			return err
		}
	}

	pulls, err := s.github.ListPullRequests(ctx, owner, repo, "all")
	if err != nil {
		return err
	}
	for _, pull := range pulls {
		detail, err := s.github.GetPullRequest(ctx, owner, repo, pull.Number)
		if err != nil {
			return err
		}
		if err := s.upsertPullRequest(ctx, canonicalRepo.ID, detail); err != nil {
			return err
		}
		if err := s.SyncPullRequestIndex(ctx, owner, repo, canonicalRepo.ID, detail); err != nil {
			return err
		}
	}

	issueComments, err := s.github.ListIssueComments(ctx, owner, repo)
	if err != nil {
		return err
	}
	for _, comment := range issueComments {
		if err := s.upsertIssueComment(ctx, canonicalRepo.ID, comment); err != nil {
			return err
		}
	}

	for _, pull := range pulls {
		reviews, err := s.github.ListPullRequestReviews(ctx, owner, repo, pull.Number)
		if err != nil {
			return err
		}
		for _, review := range reviews {
			if err := s.upsertPullRequestReview(ctx, canonicalRepo.ID, pull.Number, review); err != nil {
				return err
			}
		}

		reviewComments, err := s.github.ListPullRequestReviewComments(ctx, owner, repo, pull.Number)
		if err != nil {
			return err
		}
		for _, reviewComment := range reviewComments {
			if err := s.upsertPullRequestReviewComment(ctx, canonicalRepo.ID, pull.Number, reviewComment); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Service) SyncIssue(ctx context.Context, owner, repo string, number int) error {
	repoResp, err := s.github.GetRepository(ctx, owner, repo)
	if err != nil {
		return err
	}

	canonicalRepo, err := s.upsertRepository(ctx, repoResp)
	if err != nil {
		return err
	}

	issue, err := s.github.GetIssue(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if _, err := s.upsertIssue(ctx, canonicalRepo.ID, issue); err != nil {
		return err
	}

	comments, err := s.github.ListIssueCommentsForIssue(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	for _, comment := range comments {
		if err := s.upsertIssueComment(ctx, canonicalRepo.ID, comment); err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	return s.updateTrackedRepositoryAfterTargetedSync(ctx, repoResp.FullName, canonicalRepo.ID, now, map[string]any{
		"issues_completeness":   "sparse",
		"comments_completeness": "sparse",
	})
}

func (s *Service) SyncPullRequest(ctx context.Context, owner, repo string, number int) error {
	repoResp, err := s.github.GetRepository(ctx, owner, repo)
	if err != nil {
		return err
	}

	canonicalRepo, err := s.upsertRepository(ctx, repoResp)
	if err != nil {
		return err
	}

	issue, err := s.github.GetIssue(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if _, err := s.upsertIssue(ctx, canonicalRepo.ID, issue); err != nil {
		return err
	}

	pull, err := s.github.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if err := s.upsertPullRequest(ctx, canonicalRepo.ID, pull); err != nil {
		return err
	}
	if s.git != nil {
		var storedPull database.PullRequest
		if err := s.db.WithContext(ctx).
			Preload("Issue").
			Where("repository_id = ? AND number = ?", canonicalRepo.ID, number).
			First(&storedPull).Error; err != nil {
			return err
		}
		if err := s.git.IndexPullRequest(ctx, owner, repo, canonicalRepo, storedPull); err != nil {
			return err
		}
	}

	issueComments, err := s.github.ListIssueCommentsForIssue(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	for _, comment := range issueComments {
		if err := s.upsertIssueComment(ctx, canonicalRepo.ID, comment); err != nil {
			return err
		}
	}

	reviews, err := s.github.ListPullRequestReviews(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if err := s.upsertPullRequestReview(ctx, canonicalRepo.ID, number, review); err != nil {
			return err
		}
	}

	reviewComments, err := s.github.ListPullRequestReviewComments(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	for _, reviewComment := range reviewComments {
		if err := s.upsertPullRequestReviewComment(ctx, canonicalRepo.ID, number, reviewComment); err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	return s.updateTrackedRepositoryAfterTargetedSync(ctx, repoResp.FullName, canonicalRepo.ID, now, map[string]any{
		"issues_completeness":   "sparse",
		"pulls_completeness":    "sparse",
		"comments_completeness": "sparse",
		"reviews_completeness":  "sparse",
	})
}

func (s *Service) SyncPullRequestIndex(ctx context.Context, owner, repo string, repositoryID uint, pull gh.PullRequestResponse) error {
	if s.git == nil {
		return nil
	}

	var repository database.Repository
	if err := s.db.WithContext(ctx).Where("id = ?", repositoryID).First(&repository).Error; err != nil {
		return err
	}

	var storedPull database.PullRequest
	if err := s.db.WithContext(ctx).
		Preload("Issue").
		Where("repository_id = ? AND number = ?", repositoryID, pull.Number).
		First(&storedPull).Error; err != nil {
		return err
	}

	return s.git.IndexPullRequest(ctx, owner, repo, repository, storedPull)
}

func (s *Service) MarkBaseRefStale(ctx context.Context, repositoryID uint, ref string) error {
	var errs []error
	if s.git != nil {
		errs = append(errs, s.git.MarkBaseRefStale(ctx, repositoryID, ref))
	}
	errs = append(errs, s.markInventoryBaseRefStale(ctx, repositoryID, ref))
	return errors.Join(errs...)
}

func (s *Service) existingSyncMode(ctx context.Context, fullName string, repositoryID *uint) (string, error) {
	tracked, err := refresh.ResolveTrackedRepository(ctx, s.db, repositoryID, fullName)
	if err != nil {
		return "", err
	}
	if tracked == nil {
		return "manual_backfill", nil
	}
	switch strings.TrimSpace(tracked.SyncMode) {
	case "", "poll":
		return "manual_backfill", nil
	case "webhook":
		return "webhook_only", nil
	}
	return tracked.SyncMode, nil
}

func (s *Service) updateTrackedRepositoryAfterTargetedSync(ctx context.Context, fullName string, repositoryID uint, now time.Time, completeness map[string]any) error {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return err
	}

	existing, err := refresh.ResolveTrackedRepository(ctx, s.db, &repositoryID, fullName)
	if err != nil {
		return err
	}

	syncMode := "webhook_only"
	webhookProjectionEnabled := true
	allowManualBackfill := true
	enabled := true
	if existing != nil {
		if strings.TrimSpace(existing.SyncMode) != "" {
			syncMode = existing.SyncMode
		}
		webhookProjectionEnabled = existing.WebhookProjectionEnabled
		allowManualBackfill = existing.AllowManualBackfill
		enabled = existing.Enabled
	}

	model := database.TrackedRepository{
		Owner:                    owner,
		Name:                     name,
		FullName:                 fullName,
		RepositoryID:             &repositoryID,
		SyncMode:                 syncMode,
		WebhookProjectionEnabled: webhookProjectionEnabled,
		AllowManualBackfill:      allowManualBackfill,
		Enabled:                  enabled,
	}
	if value, ok := completeness["issues_completeness"].(string); ok {
		model.IssuesCompleteness = value
	}
	if value, ok := completeness["pulls_completeness"].(string); ok {
		model.PullsCompleteness = value
	}
	if value, ok := completeness["comments_completeness"].(string); ok {
		model.CommentsCompleteness = value
	}
	if value, ok := completeness["reviews_completeness"].(string); ok {
		model.ReviewsCompleteness = value
	}

	updates := map[string]any{
		"owner":                      owner,
		"name":                       name,
		"repository_id":              repositoryID,
		"sync_mode":                  syncMode,
		"webhook_projection_enabled": webhookProjectionEnabled,
		"allow_manual_backfill":      allowManualBackfill,
		"enabled":                    enabled,
		"updated_at":                 now,
	}
	for key, value := range completeness {
		updates[key] = value
	}

	return s.upsertTrackedRepository(ctx, model, updates)
}

func (s *Service) upsertTrackedRepository(ctx context.Context, model database.TrackedRepository, extraUpdates ...map[string]any) error {
	updates := map[string]any{
		"owner":                      model.Owner,
		"name":                       model.Name,
		"full_name":                  model.FullName,
		"repository_id":              model.RepositoryID,
		"sync_mode":                  model.SyncMode,
		"webhook_projection_enabled": model.WebhookProjectionEnabled,
		"allow_manual_backfill":      model.AllowManualBackfill,
		"enabled":                    model.Enabled,
	}
	if model.IssuesCompleteness != "" {
		updates["issues_completeness"] = model.IssuesCompleteness
	}
	if model.PullsCompleteness != "" {
		updates["pulls_completeness"] = model.PullsCompleteness
	}
	if model.CommentsCompleteness != "" {
		updates["comments_completeness"] = model.CommentsCompleteness
	}
	if model.ReviewsCompleteness != "" {
		updates["reviews_completeness"] = model.ReviewsCompleteness
	}
	if model.LastBootstrapAt != nil {
		updates["last_bootstrap_at"] = model.LastBootstrapAt
	}
	if model.LastCrawlAt != nil {
		updates["last_crawl_at"] = model.LastCrawlAt
	}
	if model.LastWebhookAt != nil {
		updates["last_webhook_at"] = model.LastWebhookAt
	}
	for _, extra := range extraUpdates {
		for key, value := range extra {
			updates[key] = value
		}
	}

	existing, err := refresh.ResolveTrackedRepository(ctx, s.db, model.RepositoryID, model.FullName)
	if err != nil {
		return err
	}
	if existing == nil {
		return s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "full_name"}},
			DoUpdates: clause.Assignments(updates),
		}).Create(&model).Error
	}

	return s.db.WithContext(ctx).Model(&database.TrackedRepository{}).
		Where("id = ?", existing.ID).
		Updates(updates).Error
}

func (s *Service) upsertRepository(ctx context.Context, repo gh.RepositoryResponse) (database.Repository, error) {
	var ownerID *uint
	if repo.Owner != nil {
		user, err := s.upsertUser(ctx, *repo.Owner)
		if err != nil {
			return database.Repository{}, err
		}
		ownerID = &user.ID
	}
	ownerLogin := ""
	if repo.Owner != nil {
		ownerLogin = repo.Owner.Login
	}

	raw, err := json.Marshal(repo)
	if err != nil {
		return database.Repository{}, err
	}

	model := database.Repository{
		GitHubID:      repo.ID,
		NodeID:        sanitizeProjectedText(repo.NodeID),
		OwnerID:       ownerID,
		OwnerLogin:    sanitizeProjectedText(ownerLogin),
		Name:          sanitizeProjectedText(repo.Name),
		FullName:      sanitizeProjectedText(repo.FullName),
		Private:       repo.Private,
		Archived:      repo.Archived,
		Disabled:      repo.Disabled,
		DefaultBranch: sanitizeProjectedText(repo.DefaultBranch),
		Description:   sanitizeProjectedText(repo.Description),
		HTMLURL:       sanitizeProjectedText(repo.HTMLURL),
		APIURL:        sanitizeProjectedText(repo.URL),
		Visibility:    sanitizeProjectedText(repo.Visibility),
		Fork:          repo.Fork,
		RawJSON:       datatypes.JSON(raw),
		CreatedAt:     repo.CreatedAt,
		UpdatedAt:     repo.UpdatedAt,
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existingByGitHubID database.Repository
		err := tx.Where("github_id = ?", repo.ID).First(&existingByGitHubID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var existingByFullName database.Repository
		err = tx.Where("full_name = ?", repo.FullName).First(&existingByFullName).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if existingByFullName.ID != 0 && existingByFullName.GitHubID != repo.ID {
			if err := tx.Model(&database.Repository{}).
				Where("id = ?", existingByFullName.ID).
				Updates(map[string]any{
					"full_name":  releasedRepositoryFullName(existingByFullName, repo.FullName),
					"updated_at": time.Now().UTC(),
				}).Error; err != nil {
				return err
			}
		}

		assignments := repositoryAssignments(model)
		if existingByGitHubID.ID != 0 {
			return tx.Model(&database.Repository{}).
				Where("id = ?", existingByGitHubID.ID).
				Updates(assignments).Error
		}

		return tx.Create(&model).Error
	}); err != nil {
		return database.Repository{}, err
	}

	var stored database.Repository
	err = s.db.WithContext(ctx).Preload("Owner").Where("github_id = ?", repo.ID).First(&stored).Error
	return stored, err
}

func repositoryAssignments(model database.Repository) map[string]any {
	return map[string]any{
		"node_id":        model.NodeID,
		"owner_id":       model.OwnerID,
		"owner_login":    model.OwnerLogin,
		"name":           model.Name,
		"full_name":      model.FullName,
		"private":        model.Private,
		"archived":       model.Archived,
		"disabled":       model.Disabled,
		"default_branch": model.DefaultBranch,
		"description":    model.Description,
		"html_url":       model.HTMLURL,
		"api_url":        model.APIURL,
		"visibility":     model.Visibility,
		"fork":           model.Fork,
		"raw_json":       model.RawJSON,
		"created_at":     model.CreatedAt,
		"updated_at":     model.UpdatedAt,
	}
}

func releasedRepositoryFullName(existing database.Repository, claimedFullName string) string {
	sanitizedClaimed := strings.NewReplacer("/", "__", " ", "_").Replace(strings.TrimSpace(claimedFullName))
	return fmt.Sprintf("__ghreplica_released__/%d/%d/%s", existing.GitHubID, time.Now().UTC().UnixNano(), sanitizedClaimed)
}

func (s *Service) upsertUser(ctx context.Context, user gh.UserResponse) (database.User, error) {
	raw, err := json.Marshal(user)
	if err != nil {
		return database.User{}, err
	}

	model := database.User{
		GitHubID:  user.ID,
		NodeID:    sanitizeProjectedText(user.NodeID),
		Login:     sanitizeProjectedText(user.Login),
		Type:      sanitizeProjectedText(user.Type),
		SiteAdmin: user.SiteAdmin,
		Name:      sanitizeProjectedText(user.Name),
		AvatarURL: sanitizeProjectedText(user.AvatarURL),
		HTMLURL:   sanitizeProjectedText(user.HTMLURL),
		APIURL:    sanitizeProjectedText(user.URL),
		RawJSON:   datatypes.JSON(raw),
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "github_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"node_id", "login", "type", "site_admin", "name", "avatar_url", "html_url", "api_url", "raw_json"}),
	}).Create(&model).Error; err != nil {
		return database.User{}, err
	}

	var stored database.User
	err = s.db.WithContext(ctx).Where("github_id = ?", user.ID).First(&stored).Error
	return stored, err
}

func (s *Service) upsertIssue(ctx context.Context, repositoryID uint, issue gh.IssueResponse) (database.Issue, error) {
	var authorID *uint
	if issue.User != nil {
		author, err := s.upsertUser(ctx, *issue.User)
		if err != nil {
			return database.Issue{}, err
		}
		authorID = &author.ID
	}

	raw, err := json.Marshal(issue)
	if err != nil {
		return database.Issue{}, err
	}

	model := database.Issue{
		RepositoryID:      repositoryID,
		GitHubID:          issue.ID,
		NodeID:            sanitizeProjectedText(issue.NodeID),
		Number:            issue.Number,
		Title:             sanitizeProjectedText(issue.Title),
		Body:              sanitizeProjectedText(issue.Body),
		State:             sanitizeProjectedText(issue.State),
		StateReason:       sanitizeProjectedText(issue.StateReason),
		AuthorID:          authorID,
		CommentsCount:     issue.Comments,
		Locked:            issue.Locked,
		IsPullRequest:     issue.PullRequest != nil,
		PullRequestAPIURL: sanitizeProjectedText(pullRequestURL(issue.PullRequest)),
		HTMLURL:           sanitizeProjectedText(issue.HTMLURL),
		APIURL:            sanitizeProjectedText(issue.URL),
		GitHubCreatedAt:   issue.CreatedAt,
		GitHubUpdatedAt:   issue.UpdatedAt,
		RawJSON:           datatypes.JSON(raw),
	}
	if issue.ClosedAt != nil {
		closedAt := *issue.ClosedAt
		model.ClosedAt = &closedAt
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "repository_id"}, {Name: "number"}},
		DoUpdates: clause.AssignmentColumns([]string{"github_id", "node_id", "title", "body", "state", "state_reason", "author_id", "comments_count", "locked", "is_pull_request", "pull_request_api_url", "html_url", "api_url", "github_created_at", "github_updated_at", "closed_at", "raw_json"}),
	}).Create(&model).Error; err != nil {
		return database.Issue{}, err
	}

	var stored database.Issue
	err = s.db.WithContext(ctx).Preload("Author").Where("repository_id = ? AND number = ?", repositoryID, issue.Number).First(&stored).Error
	if err != nil {
		return stored, err
	}
	if s.search != nil {
		if err := s.search.UpsertIssue(ctx, stored); err != nil {
			return database.Issue{}, err
		}
	}
	return stored, nil
}

func (s *Service) upsertPullRequest(ctx context.Context, repositoryID uint, pull gh.PullRequestResponse) error {
	issue, err := s.ensureIssueForPullRequest(ctx, repositoryID, pull)
	if err != nil {
		return err
	}

	var mergedByID *uint
	if pull.MergedBy != nil {
		mergedBy, err := s.upsertUser(ctx, *pull.MergedBy)
		if err != nil {
			return err
		}
		mergedByID = &mergedBy.ID
	}

	headRepoID, err := s.ensureRepositoryRef(ctx, pull.Head.Repo)
	if err != nil {
		return err
	}
	baseRepoID, err := s.ensureRepositoryRef(ctx, pull.Base.Repo)
	if err != nil {
		return err
	}

	raw, err := json.Marshal(pull)
	if err != nil {
		return err
	}

	model := database.PullRequest{
		IssueID:         issue.ID,
		RepositoryID:    repositoryID,
		GitHubID:        pull.ID,
		NodeID:          sanitizeProjectedText(pull.NodeID),
		Number:          pull.Number,
		State:           sanitizeProjectedText(pull.State),
		Draft:           pull.Draft,
		HeadRepoID:      headRepoID,
		HeadRef:         sanitizeProjectedText(pull.Head.Ref),
		HeadSHA:         sanitizeProjectedText(pull.Head.SHA),
		BaseRepoID:      baseRepoID,
		BaseRef:         sanitizeProjectedText(pull.Base.Ref),
		BaseSHA:         sanitizeProjectedText(pull.Base.SHA),
		Mergeable:       pull.Mergeable,
		MergeableState:  sanitizeProjectedText(pull.MergeableState),
		Merged:          pull.Merged,
		MergedByID:      mergedByID,
		MergeCommitSHA:  sanitizeProjectedText(pull.MergeCommitSHA),
		Additions:       pull.Additions,
		Deletions:       pull.Deletions,
		ChangedFiles:    pull.ChangedFiles,
		CommitsCount:    pull.Commits,
		HTMLURL:         sanitizeProjectedText(pull.HTMLURL),
		APIURL:          sanitizeProjectedText(pull.URL),
		DiffURL:         sanitizeProjectedText(pull.DiffURL),
		PatchURL:        sanitizeProjectedText(pull.PatchURL),
		GitHubCreatedAt: pull.CreatedAt,
		GitHubUpdatedAt: pull.UpdatedAt,
		RawJSON:         datatypes.JSON(raw),
	}
	if pull.MergedAt != nil {
		mergedAt := *pull.MergedAt
		model.MergedAt = &mergedAt
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "issue_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"repository_id", "github_id", "node_id", "number", "state", "draft", "head_repo_id", "head_ref", "head_sha", "base_repo_id", "base_ref", "base_sha", "mergeable", "mergeable_state", "merged", "merged_at", "merged_by_id", "merge_commit_sha", "additions", "deletions", "changed_files", "commits_count", "html_url", "api_url", "diff_url", "patch_url", "github_created_at", "github_updated_at", "raw_json"}),
	}).Create(&model).Error; err != nil {
		return err
	}

	if s.search != nil {
		var stored database.PullRequest
		if err := s.db.WithContext(ctx).
			Preload("Issue").
			Preload("Issue.Author").
			Where("repository_id = ? AND number = ?", repositoryID, pull.Number).
			First(&stored).Error; err != nil {
			return err
		}
		if err := s.search.UpsertPullRequest(ctx, stored); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertIssueComment(ctx context.Context, repositoryID uint, comment gh.IssueCommentResponse) error {
	issueNumber, err := issueNumberFromURL(comment.IssueURL)
	if err != nil {
		return err
	}

	var issue database.Issue
	if err := s.db.WithContext(ctx).Where("repository_id = ? AND number = ?", repositoryID, issueNumber).First(&issue).Error; err != nil {
		return err
	}

	var authorID *uint
	if comment.User != nil {
		author, err := s.upsertUser(ctx, *comment.User)
		if err != nil {
			return err
		}
		authorID = &author.ID
	}

	raw, err := json.Marshal(comment)
	if err != nil {
		return err
	}

	model := database.IssueComment{
		GitHubID:        comment.ID,
		NodeID:          sanitizeProjectedText(comment.NodeID),
		RepositoryID:    repositoryID,
		IssueID:         issue.ID,
		AuthorID:        authorID,
		Body:            sanitizeProjectedText(comment.Body),
		HTMLURL:         sanitizeProjectedText(comment.HTMLURL),
		APIURL:          sanitizeProjectedText(comment.URL),
		GitHubCreatedAt: comment.CreatedAt,
		GitHubUpdatedAt: comment.UpdatedAt,
		RawJSON:         datatypes.JSON(raw),
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "github_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"repository_id", "issue_id", "author_id", "body", "html_url", "api_url", "github_created_at", "github_updated_at", "raw_json"}),
	}).Create(&model).Error; err != nil {
		return err
	}

	if s.search != nil {
		var stored database.IssueComment
		if err := s.db.WithContext(ctx).
			Preload("Author").
			Preload("Issue").
			Where("github_id = ?", comment.ID).
			First(&stored).Error; err != nil {
			return err
		}
		if err := s.search.UpsertIssueComment(ctx, stored); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertPullRequestReview(ctx context.Context, repositoryID uint, pullNumber int, review gh.PullRequestReviewResponse) error {
	pullRequestID, err := s.pullRequestIssueID(ctx, repositoryID, pullNumber)
	if err != nil {
		return err
	}

	var authorID *uint
	if review.User != nil {
		author, err := s.upsertUser(ctx, *review.User)
		if err != nil {
			return err
		}
		authorID = &author.ID
	}

	raw, err := json.Marshal(review)
	if err != nil {
		return err
	}

	model := database.PullRequestReview{
		GitHubID:        review.ID,
		NodeID:          sanitizeProjectedText(review.NodeID),
		RepositoryID:    repositoryID,
		PullRequestID:   pullRequestID,
		AuthorID:        authorID,
		State:           sanitizeProjectedText(review.State),
		Body:            sanitizeProjectedText(review.Body),
		CommitID:        sanitizeProjectedText(review.CommitID),
		SubmittedAt:     review.SubmittedAt,
		HTMLURL:         sanitizeProjectedText(review.HTMLURL),
		APIURL:          sanitizeProjectedText(review.URL),
		GitHubCreatedAt: review.CreatedAt,
		GitHubUpdatedAt: review.UpdatedAt,
		RawJSON:         datatypes.JSON(raw),
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "github_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"repository_id", "pull_request_id", "author_id", "state", "body", "commit_id", "submitted_at", "html_url", "api_url", "github_created_at", "github_updated_at", "raw_json"}),
	}).Create(&model).Error; err != nil {
		return err
	}

	if s.search != nil {
		var stored database.PullRequestReview
		if err := s.db.WithContext(ctx).
			Preload("Author").
			Preload("PullRequest").
			Preload("PullRequest.Issue").
			Where("github_id = ?", review.ID).
			First(&stored).Error; err != nil {
			return err
		}
		if err := s.search.UpsertPullRequestReview(ctx, stored); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertPullRequestReviewComment(ctx context.Context, repositoryID uint, pullNumber int, comment gh.PullRequestReviewCommentResponse) error {
	pullRequestID, err := s.pullRequestIssueID(ctx, repositoryID, pullNumber)
	if err != nil {
		return err
	}

	var authorID *uint
	if comment.User != nil {
		author, err := s.upsertUser(ctx, *comment.User)
		if err != nil {
			return err
		}
		authorID = &author.ID
	}

	var reviewID *uint
	if comment.PullRequestReviewID != nil {
		var review database.PullRequestReview
		if err := s.db.WithContext(ctx).Where("github_id = ?", *comment.PullRequestReviewID).First(&review).Error; err == nil {
			reviewID = &review.ID
		}
	}

	raw, err := json.Marshal(comment)
	if err != nil {
		return err
	}

	model := database.PullRequestReviewComment{
		GitHubID:          comment.ID,
		NodeID:            sanitizeProjectedText(comment.NodeID),
		RepositoryID:      repositoryID,
		PullRequestID:     pullRequestID,
		ReviewID:          reviewID,
		InReplyToGitHubID: comment.InReplyToID,
		AuthorID:          authorID,
		Path:              sanitizeProjectedText(comment.Path),
		DiffHunk:          sanitizeProjectedText(comment.DiffHunk),
		Position:          comment.Position,
		OriginalPosition:  comment.OriginalPosition,
		Line:              comment.Line,
		OriginalLine:      comment.OriginalLine,
		Side:              sanitizeProjectedText(comment.Side),
		Body:              sanitizeProjectedText(comment.Body),
		HTMLURL:           sanitizeProjectedText(comment.HTMLURL),
		APIURL:            sanitizeProjectedText(comment.URL),
		PullRequestURL:    sanitizeProjectedText(comment.PullRequestURL),
		GitHubCreatedAt:   comment.CreatedAt,
		GitHubUpdatedAt:   comment.UpdatedAt,
		RawJSON:           datatypes.JSON(raw),
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "github_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"repository_id", "pull_request_id", "review_id", "in_reply_to_github_id", "author_id", "path", "diff_hunk", "position", "original_position", "line", "original_line", "side", "body", "html_url", "api_url", "pull_request_url", "github_created_at", "github_updated_at", "raw_json"}),
	}).Create(&model).Error; err != nil {
		return err
	}

	if s.search != nil {
		var stored database.PullRequestReviewComment
		if err := s.db.WithContext(ctx).
			Preload("Author").
			Preload("PullRequest").
			Preload("PullRequest.Issue").
			Where("github_id = ?", comment.ID).
			First(&stored).Error; err != nil {
			return err
		}
		if err := s.search.UpsertPullRequestReviewComment(ctx, stored); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ensureIssueForPullRequest(ctx context.Context, repositoryID uint, pull gh.PullRequestResponse) (database.Issue, error) {
	var existing database.Issue
	err := s.db.WithContext(ctx).Where("repository_id = ? AND number = ?", repositoryID, pull.Number).First(&existing).Error
	if err == nil {
		return existing, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return database.Issue{}, err
	}

	issueLike := gh.IssueResponse{
		ID:          pull.ID,
		NodeID:      pull.NodeID,
		Number:      pull.Number,
		Title:       pull.Title,
		Body:        pull.Body,
		State:       pull.State,
		User:        pull.User,
		PullRequest: &gh.IssuePullRequestRef{URL: pull.URL},
		HTMLURL:     pull.HTMLURL,
		URL:         pull.URL,
		CreatedAt:   pull.CreatedAt,
		UpdatedAt:   pull.UpdatedAt,
	}
	return s.upsertIssue(ctx, repositoryID, issueLike)
}

func (s *Service) ensureRepositoryRef(ctx context.Context, repo *gh.PullBranchRepository) (*uint, error) {
	if repo == nil {
		return nil, nil
	}

	stored, err := s.upsertRepository(ctx, gh.RepositoryResponse{
		ID:            repo.ID,
		NodeID:        repo.NodeID,
		Name:          repo.Name,
		FullName:      repo.FullName,
		Private:       repo.Private,
		Owner:         repo.Owner,
		HTMLURL:       repo.HTMLURL,
		Description:   repo.Description,
		Fork:          repo.Fork,
		URL:           repo.URL,
		DefaultBranch: repo.DefaultBranch,
		Visibility:    repo.Visibility,
		Archived:      repo.Archived,
		Disabled:      repo.Disabled,
		CreatedAt:     repo.CreatedAt,
		UpdatedAt:     repo.UpdatedAt,
	})
	if err != nil {
		return nil, err
	}

	return &stored.ID, nil
}

func pullRequestURL(ref *gh.IssuePullRequestRef) string {
	if ref == nil {
		return ""
	}
	return ref.URL
}

func (s *Service) pullRequestIssueID(ctx context.Context, repositoryID uint, pullNumber int) (uint, error) {
	var pull database.PullRequest
	if err := s.db.WithContext(ctx).Where("repository_id = ? AND number = ?", repositoryID, pullNumber).First(&pull).Error; err != nil {
		return 0, err
	}
	return pull.IssueID, nil
}

func issueNumberFromURL(issueURL string) (int, error) {
	parts := strings.Split(strings.TrimRight(issueURL, "/"), "/")
	if len(parts) == 0 {
		return 0, fmt.Errorf("issue_url is invalid")
	}
	number, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0, err
	}
	return number, nil
}

func splitFullName(fullName string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(fullName), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repository full name is invalid")
	}
	return parts[0], parts[1], nil
}
