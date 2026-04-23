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
	delivery, ok, err := p.loadPendingDelivery(ctx, deliveryID)
	if err != nil || !ok {
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
	tracked, existingRepositoryID, err := p.upsertTrackedRepositoryForWebhook(ctx, repoRef, now)
	if err != nil {
		return err
	}

	updates, err := p.processTrackedWebhookDelivery(ctx, tracked, repoRef, delivery.Event, decoded, now, existingRepositoryID)
	if err != nil {
		return err
	}
	return p.markProcessed(ctx, deliveryID, updates)
}

func (p *Processor) loadPendingDelivery(ctx context.Context, deliveryID string) (database.WebhookDelivery, bool, error) {
	var delivery database.WebhookDelivery
	if err := p.db.WithContext(ctx).Where("delivery_id = ?", deliveryID).First(&delivery).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.WebhookDelivery{}, false, nil
		}
		return database.WebhookDelivery{}, false, err
	}
	return delivery, true, nil
}

func (p *Processor) upsertTrackedRepositoryForWebhook(ctx context.Context, repoRef *repositoryRef, seenAt time.Time) (database.TrackedRepository, uint, error) {
	existingRepositoryID, err := repositoryIDByRef(ctx, p.db, repoRef)
	if err != nil {
		return database.TrackedRepository{}, 0, err
	}
	var trackedRepositoryID *uint
	if existingRepositoryID != 0 {
		trackedRepositoryID = &existingRepositoryID
	}
	tracked, err := refresh.UpsertTrackedRepositoryForWebhook(ctx, p.db, repoRef.Owner, repoRef.Name, repoRef.FullName, trackedRepositoryID, seenAt)
	return tracked, existingRepositoryID, err
}

func (p *Processor) processTrackedWebhookDelivery(ctx context.Context, tracked database.TrackedRepository, repoRef *repositoryRef, event string, decoded decodedWebhookEvent, now time.Time, existingRepositoryID uint) (map[string]any, error) {
	updates := map[string]any{"processed_at": now}
	if !tracked.Enabled || !tracked.WebhookProjectionEnabled {
		return p.nonProjectedWebhookUpdates(ctx, tracked, repoRef, updates, existingRepositoryID)
	}
	return p.projectedWebhookUpdates(ctx, tracked, repoRef, event, decoded, updates, now)
}

func (p *Processor) projectedWebhookUpdates(ctx context.Context, tracked database.TrackedRepository, repoRef *repositoryRef, event string, decoded decodedWebhookEvent, updates map[string]any, now time.Time) (map[string]any, error) {
	policy, ok := webhookEventPolicyFor(event)
	if !ok {
		return updates, nil
	}
	result, err := p.projectEvent(ctx, policy, decoded)
	if err != nil {
		return nil, err
	}
	if err := applyProjectionFollowUp(ctx, eventFollowUpDependencies{
		staler:   p.staler,
		recorder: p.recorder,
	}, result, time.Now().UTC()); err != nil {
		return nil, err
	}
	if result.repositoryID != 0 {
		updates["repository_id"] = result.repositoryID
	}
	if err := p.updateTrackedRepositoryProjectionState(ctx, tracked, repoRef, result.repositoryID, event, now); err != nil {
		return nil, err
	}
	return updates, nil
}

func (p *Processor) nonProjectedWebhookUpdates(ctx context.Context, tracked database.TrackedRepository, repoRef *repositoryRef, updates map[string]any, existingRepositoryID uint) (map[string]any, error) {
	if tracked.RepositoryID != nil {
		updates["repository_id"] = *tracked.RepositoryID
		return updates, nil
	}
	if existingRepositoryID != 0 {
		updates["repository_id"] = existingRepositoryID
		return updates, nil
	}
	repositoryID, err := repositoryIDByRef(ctx, p.db, repoRef)
	if err != nil {
		return nil, err
	}
	if repositoryID != 0 {
		updates["repository_id"] = repositoryID
	}
	return updates, nil
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
	applyTrackedRepositoryCompletenessUpdates(updates, tracked, event)

	return p.db.WithContext(ctx).Model(&database.TrackedRepository{}).
		Where("id = ?", tracked.ID).
		Updates(updates).Error
}

func applyTrackedRepositoryCompletenessUpdates(updates map[string]any, tracked database.TrackedRepository, event string) {
	completenessUpdates := refresh.CompletenessUpdatesForEvent(event)
	applyCompletenessUpdate(updates, completenessUpdates, "issues_completeness", tracked.IssuesCompleteness)
	applyCompletenessUpdate(updates, completenessUpdates, "pulls_completeness", tracked.PullsCompleteness)
	applyCompletenessUpdate(updates, completenessUpdates, "comments_completeness", tracked.CommentsCompleteness)
	applyCompletenessUpdate(updates, completenessUpdates, "reviews_completeness", tracked.ReviewsCompleteness)
}

func applyCompletenessUpdate(updates map[string]any, completenessUpdates map[string]any, key, current string) {
	current = strings.TrimSpace(current)
	if current != "" && current != "empty" {
		return
	}
	if _, ok := completenessUpdates[key]; ok {
		updates[key] = "sparse"
	}
}

func (p *Processor) projectEvent(ctx context.Context, policy webhookEventPolicy, event decodedWebhookEvent) (eventProjectionResult, error) {
	return policy.project(ctx, eventProjectionDependencies{
		projector: p.projector,
		repositoryIDLookup: func(ctx context.Context, repoRef *repositoryRef) (uint, error) {
			return repositoryIDByRef(ctx, p.db, repoRef)
		},
	}, event)
}
