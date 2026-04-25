package mirror

import (
	"context"

	"gorm.io/gorm"
)

type Reader struct {
	db     *gorm.DB
	tables TableNames
}

type ReaderOption func(*Reader)

func NewReader(db *gorm.DB, opts ...ReaderOption) *Reader {
	reader := &Reader{
		db:     db,
		tables: DefaultTableNames(),
	}
	for _, opt := range opts {
		opt(reader)
	}
	return reader
}

func NewSchemaReader(db *gorm.DB, schema string) *Reader {
	return NewReader(db, WithSchema(schema))
}

func WithSchema(schema string) ReaderOption {
	return WithTables(SchemaTableNames(schema))
}

func WithTables(tables TableNames) ReaderOption {
	return func(reader *Reader) {
		reader.tables = tables
	}
}

func (r *Reader) RepositoryByOwnerName(ctx context.Context, owner, name string) (Repository, error) {
	var repository Repository
	err := r.db.WithContext(ctx).
		Table(r.tables.Repositories).
		Where("owner_login = ? AND name = ?", owner, name).
		First(&repository).Error
	if err != nil {
		return Repository{}, err
	}
	return r.hydrateRepository(ctx, repository)
}

func (r *Reader) RepositoryByGitHubID(ctx context.Context, githubID int64) (Repository, error) {
	var repository Repository
	err := r.db.WithContext(ctx).
		Table(r.tables.Repositories).
		Where("github_id = ?", githubID).
		First(&repository).Error
	if err != nil {
		return Repository{}, err
	}
	return r.hydrateRepository(ctx, repository)
}

func (r *Reader) PullRequestByRepositoryID(ctx context.Context, repositoryID uint, number int) (PullRequest, error) {
	var pull PullRequest
	err := r.db.WithContext(ctx).
		Table(r.tables.PullRequests).
		Where("repository_id = ? AND number = ?", repositoryID, number).
		First(&pull).Error
	if err != nil {
		return PullRequest{}, err
	}
	return r.hydratePullRequest(ctx, pull)
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
		Table(r.tables.PullRequests).
		Where("repository_id = ? AND number IN ?", repositoryID, numbers).
		Order("number ASC").
		Find(&pulls).Error
	if err != nil {
		return nil, err
	}
	return r.hydratePullRequests(ctx, pulls)
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
		Table(r.tables.Issues).
		Where("repository_id = ? AND number = ?", repositoryID, number).
		First(&issue).Error
	if err != nil {
		return Issue{}, err
	}
	return r.hydrateIssue(ctx, issue)
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
		Table(r.tables.Issues).
		Where("repository_id = ? AND number IN ?", repositoryID, numbers).
		Order("number ASC").
		Find(&issues).Error
	if err != nil {
		return nil, err
	}
	return r.hydrateIssues(ctx, issues)
}

func (r *Reader) IssuesByGitHubRepositoryID(ctx context.Context, githubRepositoryID int64, numbers []int) ([]Issue, error) {
	repository, err := r.RepositoryByGitHubID(ctx, githubRepositoryID)
	if err != nil {
		return nil, err
	}
	return r.IssuesByRepositoryID(ctx, repository.ID, numbers)
}

func (r *Reader) hydrateRepository(ctx context.Context, repository Repository) (Repository, error) {
	if repository.OwnerID == nil {
		return repository, nil
	}
	owner, err := r.userByID(ctx, *repository.OwnerID)
	if err != nil {
		return Repository{}, err
	}
	repository.Owner = &owner
	return repository, nil
}

func (r *Reader) hydrateIssue(ctx context.Context, issue Issue) (Issue, error) {
	if issue.AuthorID == nil {
		return issue, nil
	}
	author, err := r.userByID(ctx, *issue.AuthorID)
	if err != nil {
		return Issue{}, err
	}
	issue.Author = &author
	return issue, nil
}

func (r *Reader) hydrateIssues(ctx context.Context, issues []Issue) ([]Issue, error) {
	for i := range issues {
		issue, err := r.hydrateIssue(ctx, issues[i])
		if err != nil {
			return nil, err
		}
		issues[i] = issue
	}
	return issues, nil
}

func (r *Reader) hydratePullRequest(ctx context.Context, pull PullRequest) (PullRequest, error) {
	issue, err := r.issueByID(ctx, pull.IssueID)
	if err != nil {
		return PullRequest{}, err
	}
	pull.Issue = issue
	return pull, nil
}

func (r *Reader) hydratePullRequests(ctx context.Context, pulls []PullRequest) ([]PullRequest, error) {
	for i := range pulls {
		pull, err := r.hydratePullRequest(ctx, pulls[i])
		if err != nil {
			return nil, err
		}
		pulls[i] = pull
	}
	return pulls, nil
}

func (r *Reader) issueByID(ctx context.Context, id uint) (Issue, error) {
	var issue Issue
	err := r.db.WithContext(ctx).
		Table(r.tables.Issues).
		Where("id = ?", id).
		First(&issue).Error
	if err != nil {
		return Issue{}, err
	}
	return r.hydrateIssue(ctx, issue)
}

func (r *Reader) userByID(ctx context.Context, id uint) (User, error) {
	var user User
	err := r.db.WithContext(ctx).
		Table(r.tables.Users).
		Where("id = ?", id).
		First(&user).Error
	return user, err
}
