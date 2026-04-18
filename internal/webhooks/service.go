package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WebhookProjector interface {
	UpsertRepository(ctx context.Context, repo gh.RepositoryResponse) (database.Repository, error)
	UpsertIssue(ctx context.Context, repositoryID uint, issue gh.IssueResponse) (database.Issue, error)
	UpsertPullRequest(ctx context.Context, repositoryID uint, pull gh.PullRequestResponse) error
	UpsertIssueComment(ctx context.Context, repositoryID uint, comment gh.IssueCommentResponse) error
	UpsertPullRequestReview(ctx context.Context, repositoryID uint, pullNumber int, review gh.PullRequestReviewResponse) error
	UpsertPullRequestReviewComment(ctx context.Context, repositoryID uint, pullNumber int, comment gh.PullRequestReviewCommentResponse) error
}

type pullRequestIndexer interface {
	SyncPullRequestIndex(ctx context.Context, owner, repo string, repositoryID uint, pull gh.PullRequestResponse) error
}

type baseRefStaler interface {
	MarkBaseRefStale(ctx context.Context, repositoryID uint, ref string) error
}

type repoChangeWebhookRecorder interface {
	NoteRepositoryWebhook(ctx context.Context, repositoryID uint, seenAt time.Time) error
	EnqueuePullRequestRefresh(ctx context.Context, repositoryID uint, number int, seenAt time.Time) error
	MarkInventoryNeedsRefresh(ctx context.Context, repositoryID uint, seenAt time.Time) error
}

func pullRequestWebhookNeedsInventoryRefresh(action string, payload []byte) bool {
	switch strings.TrimSpace(action) {
	case "opened", "closed", "reopened":
		return true
	case "edited":
		var envelope struct {
			Changes struct {
				Base *struct {
					Ref *struct {
						From string `json:"from"`
					} `json:"ref"`
				} `json:"base"`
			} `json:"changes"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return false
		}
		return envelope.Changes.Base != nil && envelope.Changes.Base.Ref != nil && strings.TrimSpace(envelope.Changes.Base.Ref.From) != ""
	default:
		return false
	}
}

type Service struct {
	db        *gorm.DB
	projector WebhookProjector
	search    *searchindex.Service
}

var supportedWebhookEvents = map[string]struct{}{
	"ping":                        {},
	"issues":                      {},
	"issue_comment":               {},
	"pull_request":                {},
	"pull_request_review":         {},
	"pull_request_review_comment": {},
	"push":                        {},
	"repository":                  {},
}

func NewService(db *gorm.DB, projector WebhookProjector) *Service {
	return &Service{db: db, projector: projector, search: searchindex.NewService(db)}
}

func (s *Service) HandleWebhook(ctx context.Context, deliveryID, event string, headers http.Header, payload []byte) error {
	now := time.Now().UTC()

	delivery, repoRef, err := s.buildWebhookDelivery(deliveryID, event, headers, payload, now)
	if err != nil {
		return err
	}

	tx := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "delivery_id"}},
		DoNothing: true,
	}).Create(&delivery)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil
	}

	if repoRef != nil {
		existingRepositoryID, err := repositoryIDByRef(ctx, s.db, repoRef)
		if err != nil {
			return err
		}

		var trackedRepositoryID *uint
		if existingRepositoryID != 0 {
			trackedRepositoryID = &existingRepositoryID
		}

		tracked, err := refresh.UpsertTrackedRepositoryForWebhook(ctx, s.db, repoRef.Owner, repoRef.Name, repoRef.FullName, trackedRepositoryID, now)
		if err != nil {
			return err
		}

		updates := map[string]any{
			"processed_at": now,
		}
		if tracked.Enabled && tracked.WebhookProjectionEnabled {
			if _, ok := supportedWebhookEvents[event]; ok {
				repositoryID, err := s.projectEvent(ctx, event, delivery.Action, payload, repoRef)
				if err != nil {
					return err
				}
				if repositoryID != 0 {
					updates["repository_id"] = repositoryID
				}
				if err := s.updateTrackedRepositoryProjectionState(ctx, tracked, repoRef, repositoryID, event, now); err != nil {
					return err
				}
			}
		} else if tracked.RepositoryID != nil {
			updates["repository_id"] = *tracked.RepositoryID
		} else {
			repositoryID, err := repositoryIDByRef(ctx, s.db, repoRef)
			if err != nil {
				return err
			}
			if repositoryID != 0 {
				updates["repository_id"] = repositoryID
			}
		}

		return s.db.WithContext(ctx).Model(&database.WebhookDelivery{}).
			Where("delivery_id = ?", deliveryID).
			Updates(updates).Error
	}

	return s.db.WithContext(ctx).Model(&database.WebhookDelivery{}).
		Where("delivery_id = ?", deliveryID).
		Updates(map[string]any{"processed_at": now}).Error
}

func (s *Service) updateTrackedRepositoryProjectionState(ctx context.Context, tracked database.TrackedRepository, repoRef *repositoryRef, repositoryID uint, event string, seenAt time.Time) error {
	updates := map[string]any{
		"owner":           repoRef.Owner,
		"name":            repoRef.Name,
		"full_name":       repoRef.FullName,
		"last_webhook_at": seenAt,
		"updated_at":      seenAt,
	}
	if repositoryID != 0 && (tracked.RepositoryID == nil || *tracked.RepositoryID != repositoryID) {
		updates["repository_id"] = repositoryID
	}

	if current := strings.TrimSpace(tracked.IssuesCompleteness); current == "" || current == "empty" {
		if _, ok := refresh.CompletenessUpdatesForEvent(event)["issues_completeness"]; ok {
			updates["issues_completeness"] = "sparse"
		}
	}
	if current := strings.TrimSpace(tracked.PullsCompleteness); current == "" || current == "empty" {
		if _, ok := refresh.CompletenessUpdatesForEvent(event)["pulls_completeness"]; ok {
			updates["pulls_completeness"] = "sparse"
		}
	}
	if current := strings.TrimSpace(tracked.CommentsCompleteness); current == "" || current == "empty" {
		if _, ok := refresh.CompletenessUpdatesForEvent(event)["comments_completeness"]; ok {
			updates["comments_completeness"] = "sparse"
		}
	}
	if current := strings.TrimSpace(tracked.ReviewsCompleteness); current == "" || current == "empty" {
		if _, ok := refresh.CompletenessUpdatesForEvent(event)["reviews_completeness"]; ok {
			updates["reviews_completeness"] = "sparse"
		}
	}

	return s.db.WithContext(ctx).Model(&database.TrackedRepository{}).
		Where("id = ?", tracked.ID).
		Updates(updates).Error
}

func (s *Service) projectEvent(ctx context.Context, event, action string, payload []byte, repoRef *repositoryRef) (uint, error) {
	if s.projector == nil {
		return 0, nil
	}

	switch event {
	case "ping", "push":
		if event == "push" {
			if staler, ok := s.projector.(baseRefStaler); ok {
				var envelope struct {
					Repository gh.RepositoryResponse `json:"repository"`
					Ref        string                `json:"ref"`
				}
				if err := json.Unmarshal(payload, &envelope); err == nil {
					repo, err := s.projector.UpsertRepository(ctx, envelope.Repository)
					if err == nil {
						seenAt := time.Now().UTC()
						_ = staler.MarkBaseRefStale(ctx, repo.ID, envelope.Ref)
						if recorder, ok := s.projector.(repoChangeWebhookRecorder); ok {
							_ = recorder.MarkInventoryNeedsRefresh(ctx, repo.ID, seenAt)
						}
						return repo.ID, nil
					}
				}
			}
		}
		repositoryID, err := repositoryIDByRef(ctx, s.db, repoRef)
		if err == nil && repositoryID != 0 {
			if recorder, ok := s.projector.(repoChangeWebhookRecorder); ok {
				_ = recorder.NoteRepositoryWebhook(ctx, repositoryID, time.Now().UTC())
			}
		}
		return repositoryID, err
	case "repository":
		var envelope struct {
			Repository gh.RepositoryResponse `json:"repository"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return 0, err
		}
		repo, err := s.projector.UpsertRepository(ctx, envelope.Repository)
		if err != nil {
			return 0, err
		}
		if recorder, ok := s.projector.(repoChangeWebhookRecorder); ok {
			_ = recorder.NoteRepositoryWebhook(ctx, repo.ID, time.Now().UTC())
		}
		return repo.ID, nil
	case "issues":
		var envelope struct {
			Repository gh.RepositoryResponse `json:"repository"`
			Issue      gh.IssueResponse      `json:"issue"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return 0, err
		}
		repo, err := s.projector.UpsertRepository(ctx, envelope.Repository)
		if err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := s.db.WithContext(ctx).
				Where("repository_id = ? AND number = ?", repo.ID, envelope.Issue.Number).
				Delete(&database.Issue{}).Error; err != nil {
				return 0, err
			}
			if s.search != nil {
				if err := s.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypeIssue, envelope.Issue.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if _, err := s.projector.UpsertIssue(ctx, repo.ID, envelope.Issue); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "issue_comment":
		var envelope struct {
			Repository gh.RepositoryResponse   `json:"repository"`
			Issue      gh.IssueResponse        `json:"issue"`
			Comment    gh.IssueCommentResponse `json:"comment"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return 0, err
		}
		repo, err := s.projector.UpsertRepository(ctx, envelope.Repository)
		if err != nil {
			return 0, err
		}
		if _, err := s.projector.UpsertIssue(ctx, repo.ID, envelope.Issue); err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := s.db.WithContext(ctx).
				Where("github_id = ?", envelope.Comment.ID).
				Delete(&database.IssueComment{}).Error; err != nil {
				return 0, err
			}
			if s.search != nil {
				if err := s.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypeIssueComment, envelope.Comment.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if err := s.projector.UpsertIssueComment(ctx, repo.ID, envelope.Comment); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "pull_request":
		var envelope struct {
			Repository  gh.RepositoryResponse  `json:"repository"`
			PullRequest gh.PullRequestResponse `json:"pull_request"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return 0, err
		}
		repo, err := s.projector.UpsertRepository(ctx, envelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := s.projector.UpsertPullRequest(ctx, repo.ID, envelope.PullRequest); err != nil {
			return 0, err
		}
		if recorder, ok := s.projector.(repoChangeWebhookRecorder); ok {
			seenAt := time.Now().UTC()
			_ = recorder.NoteRepositoryWebhook(ctx, repo.ID, seenAt)
			_ = recorder.EnqueuePullRequestRefresh(ctx, repo.ID, envelope.PullRequest.Number, seenAt)
			if pullRequestWebhookNeedsInventoryRefresh(action, payload) {
				_ = recorder.MarkInventoryNeedsRefresh(ctx, repo.ID, seenAt)
			}
		}
		return repo.ID, nil
	case "pull_request_review":
		var envelope struct {
			Repository  gh.RepositoryResponse        `json:"repository"`
			PullRequest gh.PullRequestResponse       `json:"pull_request"`
			Review      gh.PullRequestReviewResponse `json:"review"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return 0, err
		}
		repo, err := s.projector.UpsertRepository(ctx, envelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := s.projector.UpsertPullRequest(ctx, repo.ID, envelope.PullRequest); err != nil {
			return 0, err
		}
		if err := s.projector.UpsertPullRequestReview(ctx, repo.ID, envelope.PullRequest.Number, envelope.Review); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "pull_request_review_comment":
		var envelope struct {
			Repository  gh.RepositoryResponse               `json:"repository"`
			PullRequest gh.PullRequestResponse              `json:"pull_request"`
			Comment     gh.PullRequestReviewCommentResponse `json:"comment"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return 0, err
		}
		repo, err := s.projector.UpsertRepository(ctx, envelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := s.projector.UpsertPullRequest(ctx, repo.ID, envelope.PullRequest); err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := s.db.WithContext(ctx).
				Where("github_id = ?", envelope.Comment.ID).
				Delete(&database.PullRequestReviewComment{}).Error; err != nil {
				return 0, err
			}
			if s.search != nil {
				if err := s.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypePullRequestReviewComment, envelope.Comment.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if err := s.projector.UpsertPullRequestReviewComment(ctx, repo.ID, envelope.PullRequest.Number, envelope.Comment); err != nil {
			return 0, err
		}
		return repo.ID, nil
	default:
		return repositoryIDByRef(ctx, s.db, repoRef)
	}
}

func repositoryIDByRef(ctx context.Context, db *gorm.DB, repoRef *repositoryRef) (uint, error) {
	if repoRef == nil {
		return 0, nil
	}

	var repository database.Repository
	if repoRef.GitHubID != 0 {
		err := db.WithContext(ctx).Where("github_id = ?", repoRef.GitHubID).First(&repository).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		if err == nil {
			return repository.ID, nil
		}
	}

	if strings.TrimSpace(repoRef.FullName) == "" {
		return 0, nil
	}

	err := db.WithContext(ctx).Where("full_name = ?", repoRef.FullName).First(&repository).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}

	return repository.ID, nil
}

type repositoryRef struct {
	GitHubID int64
	Owner    string
	Name     string
	FullName string
}

type envelope struct {
	Action     string `json:"action"`
	Repository *struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Owner    *struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

func (s *Service) buildWebhookDelivery(deliveryID, event string, headers http.Header, payload []byte, receivedAt time.Time) (database.WebhookDelivery, *repositoryRef, error) {
	var envelope envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return database.WebhookDelivery{}, nil, err
	}

	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return database.WebhookDelivery{}, nil, err
	}

	delivery := database.WebhookDelivery{
		DeliveryID:  deliveryID,
		Event:       event,
		Action:      envelope.Action,
		HeadersJSON: datatypes.JSON(headersJSON),
		PayloadJSON: datatypes.JSON(payload),
		ReceivedAt:  receivedAt,
	}

	repoRef, err := extractRepository(envelope.Repository)
	if err != nil {
		return database.WebhookDelivery{}, nil, err
	}

	return delivery, repoRef, nil
}

func extractRepository(repository *struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    *struct {
		Login string `json:"login"`
	} `json:"owner"`
}) (*repositoryRef, error) {
	if repository == nil {
		return nil, nil
	}

	fullName := strings.TrimSpace(repository.FullName)
	if fullName != "" {
		owner, name, err := splitFullName(fullName)
		if err != nil {
			return nil, err
		}
		return &repositoryRef{GitHubID: repository.ID, Owner: owner, Name: name, FullName: fullName}, nil
	}

	if repository.Owner == nil || strings.TrimSpace(repository.Owner.Login) == "" || strings.TrimSpace(repository.Name) == "" {
		return nil, nil
	}

	return &repositoryRef{
		GitHubID: repository.ID,
		Owner:    strings.TrimSpace(repository.Owner.Login),
		Name:     strings.TrimSpace(repository.Name),
		FullName: strings.TrimSpace(repository.Owner.Login) + "/" + strings.TrimSpace(repository.Name),
	}, nil
}

func splitFullName(fullName string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(fullName), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("webhook repository.full_name must be in owner/repo form")
	}

	return parts[0], parts[1], nil
}
