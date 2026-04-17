package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type TrackedRepository struct {
	ID                       uint `gorm:"primaryKey"`
	Owner                    string
	Name                     string
	FullName                 string `gorm:"uniqueIndex"`
	RepositoryID             *uint  `gorm:"index"`
	SyncMode                 string
	WebhookProjectionEnabled bool
	AllowManualBackfill      bool
	IssuesCompleteness       string
	PullsCompleteness        string
	CommentsCompleteness     string
	ReviewsCompleteness      string
	Enabled                  bool
	LastBootstrapAt          *time.Time
	LastCrawlAt              *time.Time
	LastWebhookAt            *time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type User struct {
	ID        uint  `gorm:"primaryKey"`
	GitHubID  int64 `gorm:"column:github_id;uniqueIndex"`
	NodeID    string
	Login     string `gorm:"index"`
	Type      string
	SiteAdmin bool
	Name      string
	AvatarURL string
	HTMLURL   string
	APIURL    string
	RawJSON   datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Repository struct {
	ID            uint  `gorm:"primaryKey"`
	GitHubID      int64 `gorm:"column:github_id;uniqueIndex"`
	NodeID        string
	OwnerID       *uint
	Owner         *User
	OwnerLogin    string
	Name          string
	FullName      string `gorm:"uniqueIndex"`
	Private       bool
	Archived      bool
	Disabled      bool
	DefaultBranch string
	Description   string
	HTMLURL       string
	APIURL        string
	Visibility    string
	Fork          bool
	RawJSON       datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Issue struct {
	ID                uint `gorm:"primaryKey"`
	RepositoryID      uint `gorm:"uniqueIndex:idx_repo_issue_number,priority:1"`
	Repository        Repository
	GitHubID          int64 `gorm:"column:github_id"`
	NodeID            string
	Number            int `gorm:"uniqueIndex:idx_repo_issue_number,priority:2"`
	Title             string
	Body              string
	State             string
	StateReason       string
	AuthorID          *uint
	Author            *User
	CommentsCount     int
	Locked            bool
	IsPullRequest     bool
	PullRequestAPIURL string
	HTMLURL           string
	APIURL            string
	GitHubCreatedAt   time.Time `gorm:"column:github_created_at"`
	GitHubUpdatedAt   time.Time `gorm:"column:github_updated_at"`
	ClosedAt          *time.Time
	RawJSON           datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type PullRequest struct {
	IssueID         uint `gorm:"primaryKey"`
	Issue           Issue
	RepositoryID    uint `gorm:"index"`
	Repository      Repository
	GitHubID        int64 `gorm:"column:github_id"`
	NodeID          string
	Number          int `gorm:"index"`
	State           string
	Draft           bool
	HeadRepoID      *uint
	HeadRepo        *Repository `gorm:"foreignKey:HeadRepoID"`
	HeadRef         string
	HeadSHA         string `gorm:"index"`
	BaseRepoID      *uint
	BaseRepo        *Repository `gorm:"foreignKey:BaseRepoID"`
	BaseRef         string
	BaseSHA         string
	Mergeable       *bool
	MergeableState  string
	Merged          bool
	MergedAt        *time.Time
	MergedByID      *uint
	MergedBy        *User `gorm:"foreignKey:MergedByID"`
	MergeCommitSHA  string
	Additions       int
	Deletions       int
	ChangedFiles    int
	CommitsCount    int
	HTMLURL         string
	APIURL          string
	DiffURL         string
	PatchURL        string
	GitHubCreatedAt time.Time      `gorm:"column:github_created_at"`
	GitHubUpdatedAt time.Time      `gorm:"column:github_updated_at"`
	RawJSON         datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type IssueComment struct {
	ID              uint  `gorm:"primaryKey"`
	GitHubID        int64 `gorm:"column:github_id;uniqueIndex"`
	NodeID          string
	RepositoryID    uint `gorm:"index"`
	Repository      Repository
	IssueID         uint `gorm:"index"`
	Issue           Issue
	AuthorID        *uint
	Author          *User
	Body            string
	HTMLURL         string
	APIURL          string
	GitHubCreatedAt time.Time      `gorm:"column:github_created_at"`
	GitHubUpdatedAt time.Time      `gorm:"column:github_updated_at"`
	RawJSON         datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type PullRequestReview struct {
	ID              uint  `gorm:"primaryKey"`
	GitHubID        int64 `gorm:"column:github_id;uniqueIndex"`
	NodeID          string
	RepositoryID    uint `gorm:"index"`
	Repository      Repository
	PullRequestID   uint        `gorm:"index"`
	PullRequest     PullRequest `gorm:"foreignKey:PullRequestID;references:IssueID"`
	AuthorID        *uint
	Author          *User
	State           string
	Body            string
	CommitID        string
	SubmittedAt     *time.Time
	HTMLURL         string
	APIURL          string
	GitHubCreatedAt time.Time      `gorm:"column:github_created_at"`
	GitHubUpdatedAt time.Time      `gorm:"column:github_updated_at"`
	RawJSON         datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type PullRequestReviewComment struct {
	ID                uint  `gorm:"primaryKey"`
	GitHubID          int64 `gorm:"column:github_id;uniqueIndex"`
	NodeID            string
	RepositoryID      uint `gorm:"index"`
	Repository        Repository
	PullRequestID     uint        `gorm:"index"`
	PullRequest       PullRequest `gorm:"foreignKey:PullRequestID;references:IssueID"`
	ReviewID          *uint
	Review            *PullRequestReview
	InReplyToGitHubID *int64 `gorm:"column:in_reply_to_github_id"`
	AuthorID          *uint
	Author            *User
	Path              string
	DiffHunk          string
	Position          *int
	OriginalPosition  *int
	Line              *int
	OriginalLine      *int
	Side              string
	Body              string
	HTMLURL           string
	APIURL            string
	PullRequestURL    string
	GitHubCreatedAt   time.Time      `gorm:"column:github_created_at"`
	GitHubUpdatedAt   time.Time      `gorm:"column:github_updated_at"`
	RawJSON           datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type WebhookDelivery struct {
	ID           uint   `gorm:"primaryKey"`
	DeliveryID   string `gorm:"uniqueIndex"`
	Event        string
	Action       string
	RepositoryID *uint
	HeadersJSON  datatypes.JSON `gorm:"type:jsonb"`
	PayloadJSON  datatypes.JSON `gorm:"type:jsonb"`
	ReceivedAt   time.Time
	ProcessedAt  *time.Time
}

type RepositoryRefreshJob struct {
	ID                  uint `gorm:"primaryKey"`
	TrackedRepositoryID *uint
	TrackedRepository   *TrackedRepository
	RepositoryID        *uint `gorm:"index"`
	Repository          *Repository
	JobType             string
	Owner               string `gorm:"index"`
	Name                string
	FullName            string `gorm:"index"`
	Source              string
	DeliveryID          string
	Status              string `gorm:"index"`
	Attempts            int
	MaxAttempts         int
	LastError           string
	RequestedAt         time.Time
	NextAttemptAt       *time.Time `gorm:"index"`
	LeaseExpiresAt      *time.Time `gorm:"index"`
	StartedAt           *time.Time
	FinishedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func Open(databaseURL string) (*gorm.DB, error) {
	gormConfig := &gorm.Config{
		Logger: logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		}),
	}

	var (
		db  *gorm.DB
		err error
	)

	if IsSQLiteURL(databaseURL) {
		db, err = gorm.Open(sqlite.Open(strings.TrimPrefix(databaseURL, "sqlite://")), gormConfig)
	} else {
		db, err = gorm.Open(postgres.Open(databaseURL), gormConfig)
	}
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Cloud SQL is a shared backend in staging, so keep the pool intentionally small.
	sqlDB.SetMaxOpenConns(5)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	return db, nil
}

func IsSQLiteURL(databaseURL string) bool {
	return strings.HasPrefix(databaseURL, "sqlite://")
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
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
	)
}

func RunMigrations(db *gorm.DB, dir string) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if _, err := sqlDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return err
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, file := range files {
		version := filepath.Base(file)
		applied, err := migrationApplied(ctx, sqlDB, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		body, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", version, err)
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback()
			return err
		}

		if err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = $1`, version).Scan(&count); err != nil {
		return false, err
	}

	return count > 0, nil
}

func BuildLinkHeader(basePath string, rawQuery map[string]string, page, perPage, total int) string {
	if total == 0 {
		return ""
	}

	lastPage := (total + perPage - 1) / perPage
	if lastPage <= 1 {
		return ""
	}

	parts := make([]string, 0, 4)
	if page > 1 {
		parts = append(parts, fmt.Sprintf("<%s>; rel=\"first\"", buildPageURL(basePath, rawQuery, 1, perPage)))
		parts = append(parts, fmt.Sprintf("<%s>; rel=\"prev\"", buildPageURL(basePath, rawQuery, page-1, perPage)))
	}
	if page < lastPage {
		parts = append(parts, fmt.Sprintf("<%s>; rel=\"next\"", buildPageURL(basePath, rawQuery, page+1, perPage)))
		parts = append(parts, fmt.Sprintf("<%s>; rel=\"last\"", buildPageURL(basePath, rawQuery, lastPage, perPage)))
	}

	return strings.Join(parts, ", ")
}

func buildPageURL(basePath string, query map[string]string, page, perPage int) string {
	copyQuery := map[string]string{}
	for key, value := range query {
		copyQuery[key] = value
	}
	copyQuery["page"] = fmt.Sprintf("%d", page)
	copyQuery["per_page"] = fmt.Sprintf("%d", perPage)

	parts := make([]string, 0, len(copyQuery))
	for key, value := range copyQuery {
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	sort.Strings(parts)

	return fmt.Sprintf("%s?%s", basePath, strings.Join(parts, "&"))
}
