package mirror

import (
	"time"

	"gorm.io/datatypes"
)

const (
	DefaultSchema = "ghreplica"

	UsersTable        = "users"
	RepositoriesTable = "repositories"
	IssuesTable       = "issues"
	PullRequestsTable = "pull_requests"
)

type TableNames struct {
	Users        string
	Repositories string
	Issues       string
	PullRequests string
}

func DefaultTableNames() TableNames {
	return TableNames{
		Users:        UsersTable,
		Repositories: RepositoriesTable,
		Issues:       IssuesTable,
		PullRequests: PullRequestsTable,
	}
}

func SchemaTableNames(schema string) TableNames {
	return TableNames{
		Users:        schema + "." + UsersTable,
		Repositories: schema + "." + RepositoriesTable,
		Issues:       schema + "." + IssuesTable,
		PullRequests: schema + "." + PullRequestsTable,
	}
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

// TableName returns the deployed ghreplica table name. Consumers that run
// against a schema-qualified mirror should use NewSchemaReader or WithTables.
func (User) TableName() string { return UsersTable }

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

// TableName returns the deployed ghreplica table name. Consumers that run
// against a schema-qualified mirror should use NewSchemaReader or WithTables.
func (Repository) TableName() string { return RepositoriesTable }

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

// TableName returns the deployed ghreplica table name. Consumers that run
// against a schema-qualified mirror should use NewSchemaReader or WithTables.
func (Issue) TableName() string { return IssuesTable }

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

// TableName returns the deployed ghreplica table name. Consumers that run
// against a schema-qualified mirror should use NewSchemaReader or WithTables.
func (PullRequest) TableName() string { return PullRequestsTable }
