package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gorm.io/gorm"
)

func pullRequestWebhookNeedsInventoryRefresh(action string, payload []byte) bool {
	switch strings.TrimSpace(action) {
	case "opened", "closed", "reopened":
		return true
	case "edited":
		var payloadEnvelope struct {
			Changes struct {
				Base *struct {
					Ref *struct {
						From string `json:"from"`
					} `json:"ref"`
				} `json:"base"`
			} `json:"changes"`
		}
		if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
			return false
		}
		return payloadEnvelope.Changes.Base != nil && payloadEnvelope.Changes.Base.Ref != nil && strings.TrimSpace(payloadEnvelope.Changes.Base.Ref.From) != ""
	default:
		return false
	}
}

type repositoryRef struct {
	GitHubID int64
	Owner    string
	Name     string
	FullName string
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

func repositoryRefFromPayload(payload []byte) (*repositoryRef, error) {
	var payloadEnvelope envelope
	if err := json.Unmarshal(payload, &payloadEnvelope); err != nil {
		return nil, err
	}
	return extractRepository(payloadEnvelope.Repository)
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

	owner := strings.TrimSpace(repository.Owner.Login)
	name := strings.TrimSpace(repository.Name)
	return &repositoryRef{
		GitHubID: repository.ID,
		Owner:    owner,
		Name:     name,
		FullName: owner + "/" + name,
	}, nil
}

func splitFullName(fullName string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(fullName), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("webhook repository.full_name must be in owner/repo form")
	}

	return parts[0], parts[1], nil
}
