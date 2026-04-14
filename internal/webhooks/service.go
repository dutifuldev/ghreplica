package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RefreshScheduler interface {
	EnqueueRepositoryRefresh(ctx context.Context, request refresh.Request) error
}

type Service struct {
	db        *gorm.DB
	scheduler RefreshScheduler
}

func NewService(db *gorm.DB, scheduler RefreshScheduler) *Service {
	return &Service{db: db, scheduler: scheduler}
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
		tracked, err := refresh.UpsertTrackedRepositoryForWebhook(ctx, s.db, repoRef.Owner, repoRef.Name, repoRef.FullName, now)
		if err != nil {
			return err
		}

		if err := s.scheduler.EnqueueRepositoryRefresh(ctx, refresh.Request{
			Owner:      repoRef.Owner,
			Name:       repoRef.Name,
			FullName:   repoRef.FullName,
			Source:     "webhook",
			DeliveryID: deliveryID,
		}); err != nil {
			return err
		}

		updates := map[string]any{
			"processed_at": now,
		}
		if tracked.RepositoryID != nil {
			updates["repository_id"] = *tracked.RepositoryID
		} else {
			var repository database.Repository
			err := s.db.WithContext(ctx).Where("full_name = ?", repoRef.FullName).First(&repository).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				updates["repository_id"] = repository.ID
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

type repositoryRef struct {
	Owner    string
	Name     string
	FullName string
}

type envelope struct {
	Action     string `json:"action"`
	Repository *struct {
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
		return &repositoryRef{Owner: owner, Name: name, FullName: fullName}, nil
	}

	if repository.Owner == nil || strings.TrimSpace(repository.Owner.Login) == "" || strings.TrimSpace(repository.Name) == "" {
		return nil, nil
	}

	return &repositoryRef{
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
