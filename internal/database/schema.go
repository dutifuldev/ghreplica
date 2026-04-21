package database

import (
	"errors"

	"gorm.io/gorm"
)

var ErrAutoMigrateDisabled = errors.New("database.AutoMigrate is disabled; use SQL migrations in runtime code and ApplyTestSchema for SQLite tests")

func schemaModels() []any {
	return []any{
		&TrackedRepository{},
		&User{},
		&Repository{},
		&Issue{},
		&PullRequest{},
		&IssueComment{},
		&PullRequestReview{},
		&PullRequestReviewComment{},
		&GitRef{},
		&GitCommit{},
		&GitCommitParent{},
		&GitCommitParentFile{},
		&GitCommitParentHunk{},
		&PullRequestChangeSnapshot{},
		&PullRequestChangeFile{},
		&PullRequestChangeHunk{},
		&SearchDocument{},
		&RepoTextSearchState{},
		&RepoChangeSyncState{},
		&RepoOpenPullInventory{},
		&RepoTargetedPullRefresh{},
		&WebhookDelivery{},
		&RepositoryRefreshJob{},
	}
}

func ApplyTestSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("database.ApplyTestSchema requires a database handle")
	}
	if db.Dialector.Name() != "sqlite" {
		return errors.New("database.ApplyTestSchema only supports sqlite test databases")
	}
	return db.AutoMigrate(schemaModels()...)
}

func AutoMigrate(_ *gorm.DB) error {
	return ErrAutoMigrateDisabled
}
