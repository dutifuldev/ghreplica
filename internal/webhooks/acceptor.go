package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Acceptor struct {
	db                               *gorm.DB
	sqlDB                            *sql.DB
	dispatcher                       DeliveryDispatcher
	immediateWebhookProjectorFactory ImmediateWebhookProjectorFactory
}

func NewAcceptor(db *gorm.DB, immediateWebhookProjectorFactory ImmediateWebhookProjectorFactory) *Acceptor {
	sqlDB, err := db.DB()
	if err != nil {
		sqlDB = nil
	}
	return &Acceptor{
		db:                               db,
		sqlDB:                            sqlDB,
		immediateWebhookProjectorFactory: immediateWebhookProjectorFactory,
	}
}

func (a *Acceptor) SetDispatcher(dispatcher DeliveryDispatcher) {
	a.dispatcher = dispatcher
}

func (a *Acceptor) HandleWebhook(ctx context.Context, deliveryID, event string, headers http.Header, payload []byte) error {
	now := time.Now().UTC()

	if a.sqlDB == nil {
		return errors.New("webhook SQL database handle is not configured")
	}
	if a.dispatcher == nil {
		return errors.New("webhook delivery dispatcher is not configured")
	}

	decoded, err := decodeWebhookEvent(event, payload)
	if err != nil {
		return err
	}

	delivery, err := buildWebhookDelivery(deliveryID, headers, decoded, payload, now)
	if err != nil {
		return err
	}

	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sqlTx, ok := tx.Statement.ConnPool.(*sql.Tx)
		if !ok || sqlTx == nil {
			return errors.New("webhook SQL transaction is not available")
		}

		inserted, err := a.insertWebhookDeliveryTx(ctx, sqlTx, delivery)
		if err != nil {
			return err
		}
		if !inserted {
			return nil
		}

		projector := a.immediateProjectorForTx(tx)
		result, err := a.projectImmediateEvent(ctx, projector, decoded)
		if err != nil {
			return err
		}
		if err := applyProjectionFollowUp(ctx, eventFollowUpDependencies{
			recorder: projector,
		}, result, time.Now().UTC()); err != nil {
			return err
		}
		repositoryID := result.repositoryID
		if repositoryID != 0 {
			if err := tx.Model(&database.WebhookDelivery{}).
				Where("delivery_id = ?", delivery.DeliveryID).
				Update("repository_id", repositoryID).Error; err != nil {
				return err
			}
		}

		return a.dispatcher.EnqueueWebhookDeliveryTx(ctx, sqlTx, delivery.DeliveryID)
	})
}

func (a *Acceptor) insertWebhookDeliveryTx(ctx context.Context, tx *sql.Tx, delivery database.WebhookDelivery) (bool, error) {
	headersJSON := string(delivery.HeadersJSON)
	payloadJSON := string(delivery.PayloadJSON)

	var (
		query string
		args  []any
	)
	switch a.db.Dialector.Name() {
	case "sqlite":
		query = `
			INSERT INTO webhook_deliveries (delivery_id, event, action, headers_json, payload_json, received_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (delivery_id) DO NOTHING
		`
		args = []any{delivery.DeliveryID, delivery.Event, delivery.Action, headersJSON, payloadJSON, delivery.ReceivedAt}
	default:
		query = `
			INSERT INTO webhook_deliveries (delivery_id, event, action, headers_json, payload_json, received_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (delivery_id) DO NOTHING
		`
		args = []any{delivery.DeliveryID, delivery.Event, delivery.Action, headersJSON, payloadJSON, delivery.ReceivedAt}
	}

	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func (a *Acceptor) projectImmediateEvent(ctx context.Context, projector ImmediateWebhookProjector, event decodedWebhookEvent) (eventProjectionResult, error) {
	policy, ok := webhookEventPolicyFor(event.Event)
	if !ok || !policy.immediate || projector == nil {
		return eventProjectionResult{}, nil
	}

	return policy.project(ctx, eventProjectionDependencies{
		projector: projector,
	}, event)
}

func buildWebhookDelivery(deliveryID string, headers http.Header, event decodedWebhookEvent, payload []byte, receivedAt time.Time) (database.WebhookDelivery, error) {
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return database.WebhookDelivery{}, err
	}

	delivery := database.WebhookDelivery{
		DeliveryID:  deliveryID,
		Event:       event.Event,
		Action:      event.Action,
		HeadersJSON: datatypes.JSON(headersJSON),
		PayloadJSON: datatypes.JSON(payload),
		ReceivedAt:  receivedAt,
	}

	return delivery, nil
}

func (a *Acceptor) immediateProjectorForTx(tx *gorm.DB) ImmediateWebhookProjector {
	if a.immediateWebhookProjectorFactory == nil {
		return nil
	}
	return a.immediateWebhookProjectorFactory(tx)
}
