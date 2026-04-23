package githubsync

import (
	"bytes"
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
	db                    *gorm.DB
	github                *gh.Client
	git                   *gitindex.Service
	search                *searchindex.Service
	repairMetrics         *repairMetricsRegistry
	openPRInventoryMaxAge time.Duration
}

func NewService(db *gorm.DB, githubClient *gh.Client, gitIndex ...*gitindex.Service) *Service {
	var indexer *gitindex.Service
	if len(gitIndex) > 0 {
		indexer = gitIndex[0]
	}
	return &Service{
		db:                    db,
		github:                githubClient,
		git:                   indexer,
		search:                searchindex.NewService(db),
		repairMetrics:         newRepairMetricsRegistry(),
		openPRInventoryMaxAge: 6 * time.Hour,
	}
}

func (s *Service) withoutSearch() *Service {
	clone := *s
	clone.search = nil
	return &clone
}

func (s *Service) WithoutSearch() *Service {
	return s.withoutSearch()
}

func (s *Service) WithOpenPRInventoryMaxAge(maxAge time.Duration) *Service {
	clone := *s
	if maxAge > 0 {
		clone.openPRInventoryMaxAge = maxAge
	}
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

func sanitizeRawJSON(raw []byte) datatypes.JSON {
	if !bytes.Contains(raw, []byte("\\u0000")) {
		return datatypes.JSON(raw)
	}

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return datatypes.JSON(raw)
	}

	cleaned, err := json.Marshal(sanitizeJSONValue(payload))
	if err != nil {
		return datatypes.JSON(raw)
	}
	return datatypes.JSON(cleaned)
}

func sanitizeJSONValue(value any) any {
	switch typed := value.(type) {
	case string:
		return sanitizeProjectedText(typed)
	case []any:
		for i := range typed {
			typed[i] = sanitizeJSONValue(typed[i])
		}
		return typed
	case map[string]any:
		sanitized := make(map[string]any, len(typed))
		for key, nested := range typed {
			sanitized[sanitizeProjectedText(key)] = sanitizeJSONValue(nested)
		}
		return sanitized
	default:
		return value
	}
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

func (s *Service) DeleteIssue(ctx context.Context, repositoryID uint, issue gh.IssueResponse) error {
	return s.deleteIssue(ctx, repositoryID, issue)
}

func (s *Service) DeleteIssueComment(ctx context.Context, repositoryID uint, comment gh.IssueCommentResponse) error {
	return s.deleteIssueComment(ctx, repositoryID, comment)
}

func (s *Service) DeletePullRequestReviewComment(ctx context.Context, repositoryID uint, comment gh.PullRequestReviewCommentResponse) error {
	return s.deletePullRequestReviewComment(ctx, repositoryID, comment)
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
	if err := s.bootstrapTrackedRepository(ctx, owner, repo, repoResp.FullName, canonicalRepo.ID); err != nil {
		return err
	}
	issues, err := s.github.ListIssues(ctx, owner, repo, "all")
	if err != nil {
		return err
	}
	pulls, err := s.github.ListPullRequests(ctx, owner, repo, "all")
	if err != nil {
		return err
	}
	if err := s.bootstrapIssues(ctx, owner, repo, canonicalRepo.ID, issues); err != nil {
		return err
	}
	if err := s.bootstrapPullRequests(ctx, owner, repo, canonicalRepo.ID, pulls); err != nil {
		return err
	}
	if err := s.bootstrapIssueComments(ctx, owner, repo, canonicalRepo.ID); err != nil {
		return err
	}
	return s.bootstrapPullRequestReviews(ctx, owner, repo, canonicalRepo.ID, pulls)
}

func (s *Service) bootstrapTrackedRepository(ctx context.Context, owner, repo, fullName string, repositoryID uint) error {
	now := time.Now().UTC()
	syncMode, err := s.existingSyncMode(ctx, fullName, &repositoryID)
	if err != nil {
		return err
	}
	return s.upsertTrackedRepository(ctx, database.TrackedRepository{
		Owner:                    owner,
		Name:                     repo,
		FullName:                 fullName,
		RepositoryID:             &repositoryID,
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
	})
}

func (s *Service) bootstrapIssues(ctx context.Context, owner, repo string, repositoryID uint, issues []gh.IssueResponse) error {
	for _, issue := range issues {
		detail, err := s.github.GetIssue(ctx, owner, repo, issue.Number)
		if err != nil {
			return err
		}
		if _, err := s.upsertIssue(ctx, repositoryID, detail); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) bootstrapPullRequests(ctx context.Context, owner, repo string, repositoryID uint, pulls []gh.PullRequestResponse) error {
	for _, pull := range pulls {
		detail, err := s.github.GetPullRequest(ctx, owner, repo, pull.Number)
		if err != nil {
			return err
		}
		if err := s.upsertPullRequest(ctx, repositoryID, detail); err != nil {
			return err
		}
		if err := s.SyncPullRequestIndex(ctx, owner, repo, repositoryID, detail); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) bootstrapIssueComments(ctx context.Context, owner, repo string, repositoryID uint) error {
	issueComments, err := s.github.ListIssueComments(ctx, owner, repo)
	if err != nil {
		return err
	}
	for _, comment := range issueComments {
		if err := s.upsertIssueComment(ctx, repositoryID, comment); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) bootstrapPullRequestReviews(ctx context.Context, owner, repo string, repositoryID uint, pulls []gh.PullRequestResponse) error {
	for _, pull := range pulls {
		if err := s.bootstrapPullRequestReviewSet(ctx, owner, repo, repositoryID, pull.Number); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) bootstrapPullRequestReviewSet(ctx context.Context, owner, repo string, repositoryID uint, pullNumber int) error {
	reviews, err := s.github.ListPullRequestReviews(ctx, owner, repo, pullNumber)
	if err != nil {
		return err
	}
	for _, review := range reviews {
		if err := s.upsertPullRequestReview(ctx, repositoryID, pullNumber, review); err != nil {
			return err
		}
	}
	reviewComments, err := s.github.ListPullRequestReviewComments(ctx, owner, repo, pullNumber)
	if err != nil {
		return err
	}
	for _, reviewComment := range reviewComments {
		if err := s.upsertPullRequestReviewComment(ctx, repositoryID, pullNumber, reviewComment); err != nil {
			return err
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
	if _, err := s.syncPullRequestCore(ctx, owner, repo, canonicalRepo, number); err != nil {
		return err
	}
	if err := s.indexStoredPullRequestIfEnabled(ctx, owner, repo, canonicalRepo, number); err != nil {
		return err
	}
	if err := s.syncPullRequestRelatedObjects(ctx, owner, repo, canonicalRepo.ID, number); err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.updateTrackedRepositoryAfterTargetedSync(ctx, repoResp.FullName, canonicalRepo.ID, now, map[string]any{
		"issues_completeness":   "sparse",
		"pulls_completeness":    "sparse",
		"comments_completeness": "sparse",
		"reviews_completeness":  "sparse",
	})
}

func (s *Service) indexStoredPullRequestIfEnabled(ctx context.Context, owner, repo string, canonicalRepo database.Repository, number int) error {
	if s.git == nil {
		return nil
	}
	var storedPull database.PullRequest
	if err := s.db.WithContext(ctx).
		Preload("Issue").
		Where("repository_id = ? AND number = ?", canonicalRepo.ID, number).
		First(&storedPull).Error; err != nil {
		return err
	}
	return s.git.IndexPullRequest(ctx, owner, repo, canonicalRepo, storedPull)
}

func (s *Service) syncPullRequestRelatedObjects(ctx context.Context, owner, repo string, repositoryID uint, number int) error {
	issueComments, err := s.github.ListIssueCommentsForIssue(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	for _, comment := range issueComments {
		if err := s.upsertIssueComment(ctx, repositoryID, comment); err != nil {
			return err
		}
	}
	return s.bootstrapPullRequestReviewSet(ctx, owner, repo, repositoryID, number)
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
	updates := trackedRepositoryUpdates(model, extraUpdates...)
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

func trackedRepositoryUpdates(model database.TrackedRepository, extraUpdates ...map[string]any) map[string]any {
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
	addOptionalTrackedRepositoryTextUpdates(updates, model)
	addOptionalTrackedRepositoryTimeUpdates(updates, model)
	for _, extra := range extraUpdates {
		for key, value := range extra {
			updates[key] = value
		}
	}
	return updates
}

func addOptionalTrackedRepositoryTextUpdates(updates map[string]any, model database.TrackedRepository) {
	addNonEmptyUpdate(updates, "issues_completeness", model.IssuesCompleteness)
	addNonEmptyUpdate(updates, "pulls_completeness", model.PullsCompleteness)
	addNonEmptyUpdate(updates, "comments_completeness", model.CommentsCompleteness)
	addNonEmptyUpdate(updates, "reviews_completeness", model.ReviewsCompleteness)
}

func addOptionalTrackedRepositoryTimeUpdates(updates map[string]any, model database.TrackedRepository) {
	addOptionalTimeUpdate(updates, "last_bootstrap_at", model.LastBootstrapAt)
	addOptionalTimeUpdate(updates, "last_crawl_at", model.LastCrawlAt)
	addOptionalTimeUpdate(updates, "last_webhook_at", model.LastWebhookAt)
}

func addNonEmptyUpdate(updates map[string]any, key, value string) {
	if value != "" {
		updates[key] = value
	}
}

func addOptionalTimeUpdate(updates map[string]any, key string, value *time.Time) {
	if value != nil {
		updates[key] = value
	}
}

func (s *Service) upsertRepository(ctx context.Context, repo gh.RepositoryResponse) (database.Repository, error) {
	model, err := s.newRepositoryModel(ctx, repo)
	if err != nil {
		return database.Repository{}, err
	}
	if err := s.upsertRepositoryModel(ctx, repo, model); err != nil {
		return database.Repository{}, err
	}

	var stored database.Repository
	err = s.db.WithContext(ctx).Preload("Owner").Where("github_id = ?", repo.ID).First(&stored).Error
	return stored, err
}

func (s *Service) newRepositoryModel(ctx context.Context, repo gh.RepositoryResponse) (database.Repository, error) {
	ownerID, ownerLogin, err := s.repositoryOwnerRef(ctx, repo.Owner)
	if err != nil {
		return database.Repository{}, err
	}
	raw, err := json.Marshal(repo)
	if err != nil {
		return database.Repository{}, err
	}
	return database.Repository{
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
		RawJSON:       sanitizeRawJSON(raw),
		CreatedAt:     repo.CreatedAt,
		UpdatedAt:     repo.UpdatedAt,
	}, nil
}

func (s *Service) repositoryOwnerRef(ctx context.Context, owner *gh.UserResponse) (*uint, string, error) {
	if owner == nil {
		return nil, "", nil
	}
	user, err := s.upsertUser(ctx, *owner)
	if err != nil {
		return nil, "", err
	}
	return &user.ID, owner.Login, nil
}

func (s *Service) upsertRepositoryModel(ctx context.Context, repo gh.RepositoryResponse, model database.Repository) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingByGitHubID, err := findRepositoryByGitHubID(tx, repo.ID)
		if err != nil {
			return err
		}
		existingByFullName, err := findRepositoryByFullName(tx, repo.FullName)
		if err != nil {
			return err
		}
		if err := releaseRepositoryFullNameClaim(tx, existingByFullName, repo.FullName, repo.ID); err != nil {
			return err
		}
		return writeRepositoryModel(tx, existingByGitHubID, model)
	})
}

func findRepositoryByGitHubID(tx *gorm.DB, githubID int64) (database.Repository, error) {
	return findRepositoryRecord(tx, "github_id = ?", githubID)
}

func findRepositoryByFullName(tx *gorm.DB, fullName string) (database.Repository, error) {
	return findRepositoryRecord(tx, "full_name = ?", fullName)
}

func findRepositoryRecord(tx *gorm.DB, query string, args ...any) (database.Repository, error) {
	var repository database.Repository
	err := tx.Where(query, args...).First(&repository).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return database.Repository{}, nil
	}
	return repository, err
}

func releaseRepositoryFullNameClaim(tx *gorm.DB, existing database.Repository, claimedFullName string, githubID int64) error {
	if existing.ID == 0 || existing.GitHubID == githubID {
		return nil
	}
	return tx.Model(&database.Repository{}).
		Where("id = ?", existing.ID).
		Updates(map[string]any{
			"full_name":  releasedRepositoryFullName(existing, claimedFullName),
			"updated_at": time.Now().UTC(),
		}).Error
}

func writeRepositoryModel(tx *gorm.DB, existing database.Repository, model database.Repository) error {
	assignments := repositoryAssignments(model)
	if existing.ID != 0 {
		return tx.Model(&database.Repository{}).
			Where("id = ?", existing.ID).
			Updates(assignments).Error
	}
	return tx.Create(&model).Error
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
		RawJSON:   sanitizeRawJSON(raw),
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
		RawJSON:           sanitizeRawJSON(raw),
	}
	if issue.ClosedAt != nil {
		closedAt := *issue.ClosedAt
		model.ClosedAt = &closedAt
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}, {Name: "number"}},
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "excluded.github_updated_at > issues.github_updated_at"},
		}},
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

func (s *Service) deleteIssue(ctx context.Context, repositoryID uint, issue gh.IssueResponse) error {
	if err := s.db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", repositoryID, issue.Number).
		Delete(&database.Issue{}).Error; err != nil {
		return err
	}
	if s.search != nil {
		if err := s.search.DeleteByGitHubID(ctx, repositoryID, searchindex.DocumentTypeIssue, issue.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertPullRequest(ctx context.Context, repositoryID uint, pull gh.PullRequestResponse) error {
	issue, err := s.ensureIssueForPullRequest(ctx, repositoryID, pull)
	if err != nil {
		return err
	}
	model, err := s.newPullRequestModel(ctx, repositoryID, issue.ID, pull)
	if err != nil {
		return err
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "issue_id"}},
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "excluded.github_updated_at > pull_requests.github_updated_at"},
		}},
		DoUpdates: clause.AssignmentColumns([]string{"repository_id", "github_id", "node_id", "number", "state", "draft", "head_repo_id", "head_ref", "head_sha", "base_repo_id", "base_ref", "base_sha", "mergeable", "mergeable_state", "merged", "merged_at", "merged_by_id", "merge_commit_sha", "additions", "deletions", "changed_files", "commits_count", "html_url", "api_url", "diff_url", "patch_url", "github_created_at", "github_updated_at", "raw_json"}),
	}).Create(&model).Error; err != nil {
		return err
	}
	return s.indexStoredPullRequest(ctx, repositoryID, pull.Number)
}

func (s *Service) newPullRequestModel(ctx context.Context, repositoryID, issueID uint, pull gh.PullRequestResponse) (database.PullRequest, error) {
	mergedByID, err := s.optionalUserID(ctx, pull.MergedBy)
	if err != nil {
		return database.PullRequest{}, err
	}
	headRepoID, baseRepoID, err := s.pullRequestRepositoryRefs(ctx, pull)
	if err != nil {
		return database.PullRequest{}, err
	}
	raw, err := json.Marshal(pull)
	if err != nil {
		return database.PullRequest{}, err
	}

	model := database.PullRequest{
		IssueID:         issueID,
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
		RawJSON:         sanitizeRawJSON(raw),
	}
	if pull.MergedAt != nil {
		mergedAt := *pull.MergedAt
		model.MergedAt = &mergedAt
	}
	return model, nil
}

func (s *Service) optionalUserID(ctx context.Context, user *gh.UserResponse) (*uint, error) {
	if user == nil {
		return nil, nil
	}
	stored, err := s.upsertUser(ctx, *user)
	if err != nil {
		return nil, err
	}
	return &stored.ID, nil
}

func (s *Service) pullRequestRepositoryRefs(ctx context.Context, pull gh.PullRequestResponse) (*uint, *uint, error) {
	headRepoID, err := s.ensureRepositoryRef(ctx, pull.Head.Repo)
	if err != nil {
		return nil, nil, err
	}
	baseRepoID, err := s.ensureRepositoryRef(ctx, pull.Base.Repo)
	if err != nil {
		return nil, nil, err
	}
	return headRepoID, baseRepoID, nil
}

func (s *Service) indexStoredPullRequest(ctx context.Context, repositoryID uint, number int) error {
	if s.search == nil {
		return nil
	}
	var stored database.PullRequest
	if err := s.db.WithContext(ctx).
		Preload("Issue").
		Preload("Issue.Author").
		Where("repository_id = ? AND number = ?", repositoryID, number).
		First(&stored).Error; err != nil {
		return err
	}
	return s.search.UpsertPullRequest(ctx, stored)
}

func (s *Service) deleteIssueComment(ctx context.Context, repositoryID uint, comment gh.IssueCommentResponse) error {
	if err := s.db.WithContext(ctx).
		Where("github_id = ?", comment.ID).
		Delete(&database.IssueComment{}).Error; err != nil {
		return err
	}
	if s.search != nil {
		if err := s.search.DeleteByGitHubID(ctx, repositoryID, searchindex.DocumentTypeIssueComment, comment.ID); err != nil {
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
		RawJSON:         sanitizeRawJSON(raw),
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
		RawJSON:         sanitizeRawJSON(raw),
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

func (s *Service) deletePullRequestReviewComment(ctx context.Context, repositoryID uint, comment gh.PullRequestReviewCommentResponse) error {
	if err := s.db.WithContext(ctx).
		Where("github_id = ?", comment.ID).
		Delete(&database.PullRequestReviewComment{}).Error; err != nil {
		return err
	}
	if s.search != nil {
		if err := s.search.DeleteByGitHubID(ctx, repositoryID, searchindex.DocumentTypePullRequestReviewComment, comment.ID); err != nil {
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
	model, err := s.newPullRequestReviewCommentModel(ctx, repositoryID, pullRequestID, comment)
	if err != nil {
		return err
	}

	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "github_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"repository_id", "pull_request_id", "review_id", "in_reply_to_github_id", "author_id", "path", "diff_hunk", "position", "original_position", "line", "original_line", "side", "body", "html_url", "api_url", "pull_request_url", "github_created_at", "github_updated_at", "raw_json"}),
	}).Create(&model).Error; err != nil {
		return err
	}
	return s.indexStoredPullRequestReviewComment(ctx, comment.ID)
}

func (s *Service) newPullRequestReviewCommentModel(ctx context.Context, repositoryID, pullRequestID uint, comment gh.PullRequestReviewCommentResponse) (database.PullRequestReviewComment, error) {
	authorID, err := s.optionalUserID(ctx, comment.User)
	if err != nil {
		return database.PullRequestReviewComment{}, err
	}
	reviewID, err := s.optionalPullRequestReviewID(ctx, comment.PullRequestReviewID)
	if err != nil {
		return database.PullRequestReviewComment{}, err
	}
	raw, err := json.Marshal(comment)
	if err != nil {
		return database.PullRequestReviewComment{}, err
	}
	return database.PullRequestReviewComment{
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
		RawJSON:           sanitizeRawJSON(raw),
	}, nil
}

func (s *Service) optionalPullRequestReviewID(ctx context.Context, reviewGitHubID *int64) (*uint, error) {
	if reviewGitHubID == nil {
		return nil, nil
	}
	var review database.PullRequestReview
	err := s.db.WithContext(ctx).Where("github_id = ?", *reviewGitHubID).First(&review).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &review.ID, nil
}

func (s *Service) indexStoredPullRequestReviewComment(ctx context.Context, githubID int64) error {
	if s.search == nil {
		return nil
	}
	var stored database.PullRequestReviewComment
	if err := s.db.WithContext(ctx).
		Preload("Author").
		Preload("PullRequest").
		Preload("PullRequest.Issue").
		Where("github_id = ?", githubID).
		First(&stored).Error; err != nil {
		return err
	}
	return s.search.UpsertPullRequestReviewComment(ctx, stored)
}

func (s *Service) ensureIssueForPullRequest(ctx context.Context, repositoryID uint, pull gh.PullRequestResponse) (database.Issue, error) {
	var existing database.Issue
	err := s.db.WithContext(ctx).Where("repository_id = ? AND number = ?", repositoryID, pull.Number).First(&existing).Error
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
	if err == nil {
		issueLike.StateReason = existing.StateReason
		issueLike.Locked = existing.Locked
		issueLike.Comments = existing.CommentsCount
		switch {
		case pull.State == "open":
			issueLike.ClosedAt = nil
		case pull.MergedAt != nil:
			closedAt := pull.MergedAt.UTC()
			issueLike.ClosedAt = &closedAt
		default:
			issueLike.ClosedAt = existing.ClosedAt
		}
	} else if pull.MergedAt != nil {
		closedAt := pull.MergedAt.UTC()
		issueLike.ClosedAt = &closedAt
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
