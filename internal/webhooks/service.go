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

type Service struct {
	db        *gorm.DB
	projector WebhookProjector
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
	return &Service{db: db, projector: projector}
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

		updates := map[string]any{
			"processed_at": now,
		}
		if _, ok := supportedWebhookEvents[event]; ok {
			repositoryID, err := s.projectEvent(ctx, event, payload, repoRef.FullName)
			if err != nil {
				return err
			}
			if repositoryID != 0 {
				updates["repository_id"] = repositoryID
				if tracked.RepositoryID == nil || *tracked.RepositoryID != repositoryID {
					if err := s.db.WithContext(ctx).Model(&database.TrackedRepository{}).
						Where("id = ?", tracked.ID).
						Updates(map[string]any{
							"repository_id": repositoryID,
							"updated_at":    now,
						}).Error; err != nil {
						return err
					}
				}
			}
		} else if tracked.RepositoryID != nil {
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

func (s *Service) projectEvent(ctx context.Context, event string, payload []byte, fullName string) (uint, error) {
	if s.projector == nil {
		return 0, nil
	}

	switch event {
	case "ping", "push":
		return repositoryIDByFullName(ctx, s.db, fullName)
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
		if err := s.projector.UpsertPullRequestReviewComment(ctx, repo.ID, envelope.PullRequest.Number, envelope.Comment); err != nil {
			return 0, err
		}
		return repo.ID, nil
	default:
		return repositoryIDByFullName(ctx, s.db, fullName)
	}
}

func repositoryIDByFullName(ctx context.Context, db *gorm.DB, fullName string) (uint, error) {
	if strings.TrimSpace(fullName) == "" {
		return 0, nil
	}

	var repository database.Repository
	err := db.WithContext(ctx).Where("full_name = ?", fullName).First(&repository).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}

	return repository.ID, nil
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
