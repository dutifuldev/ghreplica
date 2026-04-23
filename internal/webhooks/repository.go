package webhooks

import (
	"context"
	"errors"
	"strings"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"gorm.io/gorm"
)

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

func repositoryRefFromGitHubRepository(repository *gh.RepositoryResponse) (*repositoryRef, error) {
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
