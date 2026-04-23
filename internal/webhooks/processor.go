package webhooks

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"gorm.io/gorm"
)

type Processor struct {
	db        *gorm.DB
	projector WebhookProjector
	staler    BaseRefStaler
	recorder  RepoChangeWebhookRecorder
}

func NewProcessor(db *gorm.DB, projector WebhookProjector, staler BaseRefStaler, recorder RepoChangeWebhookRecorder) *Processor {
	return &Processor{
		db:        db,
		projector: projector,
		staler:    staler,
		recorder:  recorder,
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

	decoded, err := decodeWebhookEvent(delivery.Event, delivery.PayloadJSON)
	if err != nil {
		return err
	}
	repoRef := decoded.RepoRef

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
		if policy, ok := webhookEventPolicyFor(delivery.Event); ok {
			result, err := p.projectEvent(ctx, policy, decoded)
			if err != nil {
				return err
			}
			if err := applyProjectionFollowUp(ctx, eventFollowUpDependencies{
				staler:   p.staler,
				recorder: p.recorder,
			}, result, time.Now().UTC()); err != nil {
				return err
			}
			repositoryID := result.repositoryID
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

func (p *Processor) projectEvent(ctx context.Context, policy webhookEventPolicy, event decodedWebhookEvent) (eventProjectionResult, error) {
	return policy.project(ctx, eventProjectionDependencies{
		projector: p.projector,
		repositoryIDLookup: func(ctx context.Context, repoRef *repositoryRef) (uint, error) {
			return repositoryIDByRef(ctx, p.db, repoRef)
		},
	}, event)
}
