package mirror

import (
	"context"

	"gorm.io/gorm"
)

type Reader struct {
	db *gorm.DB
}

func NewReader(db *gorm.DB) *Reader {
	return &Reader{db: db}
}

func (r *Reader) RepositoryByOwnerName(ctx context.Context, owner, name string) (Repository, error) {
	var repository Repository
	err := r.db.WithContext(ctx).
		Preload("Owner").
		Where("owner_login = ? AND name = ?", owner, name).
		First(&repository).Error
	return repository, err
}

func (r *Reader) RepositoryByGitHubID(ctx context.Context, githubID int64) (Repository, error) {
	var repository Repository
	err := r.db.WithContext(ctx).
		Preload("Owner").
		Where("github_id = ?", githubID).
		First(&repository).Error
	return repository, err
}

func (r *Reader) PullRequestByRepositoryID(ctx context.Context, repositoryID uint, number int) (PullRequest, error) {
	var pull PullRequest
	err := r.db.WithContext(ctx).
		Preload("Issue").
		Preload("Issue.Author").
		Where("repository_id = ? AND number = ?", repositoryID, number).
		First(&pull).Error
	return pull, err
}

func (r *Reader) PullRequestByGitHubRepositoryID(ctx context.Context, githubRepositoryID int64, number int) (PullRequest, error) {
	repository, err := r.RepositoryByGitHubID(ctx, githubRepositoryID)
	if err != nil {
		return PullRequest{}, err
	}
	return r.PullRequestByRepositoryID(ctx, repository.ID, number)
}

func (r *Reader) PullRequestsByRepositoryID(ctx context.Context, repositoryID uint, numbers []int) ([]PullRequest, error) {
	if len(numbers) == 0 {
		return []PullRequest{}, nil
	}
	var pulls []PullRequest
	err := r.db.WithContext(ctx).
		Preload("Issue").
		Preload("Issue.Author").
		Where("repository_id = ? AND number IN ?", repositoryID, numbers).
		Order("number ASC").
		Find(&pulls).Error
	return pulls, err
}

func (r *Reader) PullRequestsByGitHubRepositoryID(ctx context.Context, githubRepositoryID int64, numbers []int) ([]PullRequest, error) {
	repository, err := r.RepositoryByGitHubID(ctx, githubRepositoryID)
	if err != nil {
		return nil, err
	}
	return r.PullRequestsByRepositoryID(ctx, repository.ID, numbers)
}

func (r *Reader) IssueByRepositoryID(ctx context.Context, repositoryID uint, number int) (Issue, error) {
	var issue Issue
	err := r.db.WithContext(ctx).
		Preload("Author").
		Where("repository_id = ? AND number = ?", repositoryID, number).
		First(&issue).Error
	return issue, err
}

func (r *Reader) IssueByGitHubRepositoryID(ctx context.Context, githubRepositoryID int64, number int) (Issue, error) {
	repository, err := r.RepositoryByGitHubID(ctx, githubRepositoryID)
	if err != nil {
		return Issue{}, err
	}
	return r.IssueByRepositoryID(ctx, repository.ID, number)
}

func (r *Reader) IssuesByRepositoryID(ctx context.Context, repositoryID uint, numbers []int) ([]Issue, error) {
	if len(numbers) == 0 {
		return []Issue{}, nil
	}
	var issues []Issue
	err := r.db.WithContext(ctx).
		Preload("Author").
		Where("repository_id = ? AND number IN ?", repositoryID, numbers).
		Order("number ASC").
		Find(&issues).Error
	return issues, err
}

func (r *Reader) IssuesByGitHubRepositoryID(ctx context.Context, githubRepositoryID int64, numbers []int) ([]Issue, error) {
	repository, err := r.RepositoryByGitHubID(ctx, githubRepositoryID)
	if err != nil {
		return nil, err
	}
	return r.IssuesByRepositoryID(ctx, repository.ID, numbers)
}
