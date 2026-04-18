package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"gorm.io/gorm"
)

type Processor struct {
	db        *gorm.DB
	projector WebhookProjector
	staler    BaseRefStaler
	recorder  RepoChangeWebhookRecorder
	search    *searchindex.Service
}

func NewProcessor(db *gorm.DB, projector WebhookProjector, staler BaseRefStaler, recorder RepoChangeWebhookRecorder, search *searchindex.Service) *Processor {
	return &Processor{
		db:        db,
		projector: projector,
		staler:    staler,
		recorder:  recorder,
		search:    search,
	}
}

func (p *Processor) ProcessWebhookDelivery(ctx context.Context, deliveryID string) error {
	now := time.Now().UTC()

	var delivery database.WebhookDelivery
	if err := p.db.WithContext(ctx).Where("delivery_id = ?", deliveryID).First(&delivery).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if delivery.ProcessedAt != nil {
		return nil
	}

	repoRef, err := repositoryRefFromPayload(delivery.PayloadJSON)
	if err != nil {
		return err
	}

	if repoRef == nil {
		return p.markProcessed(ctx, deliveryID, map[string]any{"processed_at": now})
	}

	existingRepositoryID, err := repositoryIDByRef(ctx, p.db, repoRef)
	if err != nil {
		return err
	}

	var trackedRepositoryID *uint
	if existingRepositoryID != 0 {
		trackedRepositoryID = &existingRepositoryID
	}

	tracked, err := refresh.UpsertTrackedRepositoryForWebhook(ctx, p.db, repoRef.Owner, repoRef.Name, repoRef.FullName, trackedRepositoryID, now)
	if err != nil {
		return err
	}

	updates := map[string]any{"processed_at": now}
	if tracked.Enabled && tracked.WebhookProjectionEnabled {
		if _, ok := supportedWebhookEvents[delivery.Event]; ok {
			repositoryID, err := p.projectEvent(ctx, delivery.Event, delivery.Action, delivery.PayloadJSON, repoRef)
			if err != nil {
				return err
			}
			if repositoryID != 0 {
				updates["repository_id"] = repositoryID
			}
			if err := p.updateTrackedRepositoryProjectionState(ctx, tracked, repoRef, repositoryID, delivery.Event, now); err != nil {
				return err
			}
		}
	} else if tracked.RepositoryID != nil {
		updates["repository_id"] = *tracked.RepositoryID
	} else {
		repositoryID, err := repositoryIDByRef(ctx, p.db, repoRef)
		if err != nil {
			return err
		}
		if repositoryID != 0 {
			updates["repository_id"] = repositoryID
		}
	}

	return p.markProcessed(ctx, deliveryID, updates)
}

func (p *Processor) markProcessed(ctx context.Context, deliveryID string, updates map[string]any) error {
	return p.db.WithContext(ctx).Model(&database.WebhookDelivery{}).
		Where("delivery_id = ?", deliveryID).
		Updates(updates).Error
}

func (p *Processor) updateTrackedRepositoryProjectionState(ctx context.Context, tracked database.TrackedRepository, repoRef *repositoryRef, repositoryID uint, event string, seenAt time.Time) error {
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

	return p.db.WithContext(ctx).Model(&database.TrackedRepository{}).
		Where("id = ?", tracked.ID).
		Updates(updates).Error
}

func (p *Processor) projectEvent(ctx context.Context, event, action string, payload []byte, repoRef *repositoryRef) (uint, error) {
	if p.projector == nil {
		return 0, nil
	}

	switch event {
	case "ping", "push":
		if event == "push" && p.staler != nil {
			var payloadEnvelope struct {
				Repository gh.RepositoryResponse `json:"repository"`
				Ref        string                `json:"ref"`
			}
			if err := json.Unmarshal(payload, &payloadEnvelope); err == nil {
				repo, err := p.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
				if err == nil {
					seenAt := time.Now().UTC()
					_ = p.staler.MarkBaseRefStale(ctx, repo.ID, payloadEnvelope.Ref)
					if p.recorder != nil {
						_ = p.recorder.MarkInventoryNeedsRefresh(ctx, repo.ID, seenAt)
					}
					return repo.ID, nil
				}
			}
		}
		repositoryID, err := repositoryIDByRef(ctx, p.db, repoRef)
		if err == nil && repositoryID != 0 && p.recorder != nil {
			_ = p.recorder.NoteRepositoryWebhook(ctx, repositoryID, time.Now().UTC())
		}
		return repositoryID, err
	case "repository":
		var payloadEnvelope struct {
			Repository gh.RepositoryResponse `json:"repository"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := p.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if p.recorder != nil {
			_ = p.recorder.NoteRepositoryWebhook(ctx, repo.ID, time.Now().UTC())
		}
		return repo.ID, nil
	case "issues":
		var payloadEnvelope struct {
			Repository gh.RepositoryResponse `json:"repository"`
			Issue      gh.IssueResponse      `json:"issue"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := p.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := p.db.WithContext(ctx).
				Where("repository_id = ? AND number = ?", repo.ID, payloadEnvelope.Issue.Number).
				Delete(&database.Issue{}).Error; err != nil {
				return 0, err
			}
			if p.search != nil {
				if err := p.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypeIssue, payloadEnvelope.Issue.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if _, err := p.projector.UpsertIssue(ctx, repo.ID, payloadEnvelope.Issue); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "issue_comment":
		var payloadEnvelope struct {
			Repository gh.RepositoryResponse   `json:"repository"`
			Issue      gh.IssueResponse        `json:"issue"`
			Comment    gh.IssueCommentResponse `json:"comment"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := p.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if _, err := p.projector.UpsertIssue(ctx, repo.ID, payloadEnvelope.Issue); err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := p.db.WithContext(ctx).
				Where("github_id = ?", payloadEnvelope.Comment.ID).
				Delete(&database.IssueComment{}).Error; err != nil {
				return 0, err
			}
			if p.search != nil {
				if err := p.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypeIssueComment, payloadEnvelope.Comment.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if err := p.projector.UpsertIssueComment(ctx, repo.ID, payloadEnvelope.Comment); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "pull_request":
		var payloadEnvelope struct {
			Repository  gh.RepositoryResponse  `json:"repository"`
			PullRequest gh.PullRequestResponse `json:"pull_request"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := p.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := p.projector.UpsertPullRequest(ctx, repo.ID, payloadEnvelope.PullRequest); err != nil {
			return 0, err
		}
		if p.recorder != nil {
			seenAt := time.Now().UTC()
			_ = p.recorder.NoteRepositoryWebhook(ctx, repo.ID, seenAt)
			_ = p.recorder.EnqueuePullRequestRefresh(ctx, repo.ID, payloadEnvelope.PullRequest.Number, seenAt)
			if pullRequestWebhookNeedsInventoryRefresh(action, payload) {
				_ = p.recorder.MarkInventoryNeedsRefresh(ctx, repo.ID, seenAt)
			}
		}
		return repo.ID, nil
	case "pull_request_review":
		var payloadEnvelope struct {
			Repository  gh.RepositoryResponse        `json:"repository"`
			PullRequest gh.PullRequestResponse       `json:"pull_request"`
			Review      gh.PullRequestReviewResponse `json:"review"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := p.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := p.projector.UpsertPullRequest(ctx, repo.ID, payloadEnvelope.PullRequest); err != nil {
			return 0, err
		}
		if err := p.projector.UpsertPullRequestReview(ctx, repo.ID, payloadEnvelope.PullRequest.Number, payloadEnvelope.Review); err != nil {
			return 0, err
		}
		return repo.ID, nil
	case "pull_request_review_comment":
		var payloadEnvelope struct {
			Repository  gh.RepositoryResponse               `json:"repository"`
			PullRequest gh.PullRequestResponse              `json:"pull_request"`
			Comment     gh.PullRequestReviewCommentResponse `json:"comment"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return 0, err
		}
		repo, err := p.projector.UpsertRepository(ctx, payloadEnvelope.Repository)
		if err != nil {
			return 0, err
		}
		if err := p.projector.UpsertPullRequest(ctx, repo.ID, payloadEnvelope.PullRequest); err != nil {
			return 0, err
		}
		if action == "deleted" {
			if err := p.db.WithContext(ctx).
				Where("github_id = ?", payloadEnvelope.Comment.ID).
				Delete(&database.PullRequestReviewComment{}).Error; err != nil {
				return 0, err
			}
			if p.search != nil {
				if err := p.search.DeleteByGitHubID(ctx, repo.ID, searchindex.DocumentTypePullRequestReviewComment, payloadEnvelope.Comment.ID); err != nil {
					return 0, err
				}
			}
			return repo.ID, nil
		}
		if err := p.projector.UpsertPullRequestReviewComment(ctx, repo.ID, payloadEnvelope.PullRequest.Number, payloadEnvelope.Comment); err != nil {
			return 0, err
		}
		return repo.ID, nil
	default:
		return repositoryIDByRef(ctx, p.db, repoRef)
	}
}
