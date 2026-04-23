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

	delivery, _, err := buildWebhookDelivery(deliveryID, event, headers, payload, now)
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

		repositoryID, err := a.projectImmediateEvent(ctx, tx, delivery)
		if err != nil {
			return err
		}
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

func (a *Acceptor) projectImmediateEvent(ctx context.Context, tx *gorm.DB, delivery database.WebhookDelivery) (uint, error) {
	if !supportsImmediateProjection(delivery.Event) || a.immediateWebhookProjectorFactory == nil {
		return 0, nil
	}

	projector := a.immediateWebhookProjectorFactory(tx)
	if projector == nil {
		return 0, nil
	}

	return projectWebhookEvent(ctx, eventProjectionDependencies{
		db:        tx,
		projector: projector,
		recorder:  projector,
	}, delivery.Event, delivery.Action, delivery.PayloadJSON, nil)
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

func buildWebhookDelivery(deliveryID, event string, headers http.Header, payload []byte, receivedAt time.Time) (database.WebhookDelivery, *repositoryRef, error) {
	var payloadEnvelope envelope
	if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
		return database.WebhookDelivery{}, nil, err
	}

	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return database.WebhookDelivery{}, nil, err
	}

	delivery := database.WebhookDelivery{
		DeliveryID:  deliveryID,
		Event:       event,
		Action:      payloadEnvelope.Action,
		HeadersJSON: datatypes.JSON(headersJSON),
		PayloadJSON: datatypes.JSON(payload),
		ReceivedAt:  receivedAt,
	}

	repoRef, err := extractRepository(payloadEnvelope.Repository)
	if err != nil {
		return database.WebhookDelivery{}, nil, err
	}

	return delivery, repoRef, nil
}
