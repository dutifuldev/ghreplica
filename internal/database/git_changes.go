package database

import "time"

type GitRef struct {
	ID              uint   `gorm:"primaryKey"`
	RepositoryID    uint   `gorm:"uniqueIndex:idx_git_refs_repo_name,priority:1"`
	RefName         string `gorm:"uniqueIndex:idx_git_refs_repo_name,priority:2"`
	TargetOID       string `gorm:"column:target_oid"`
	TargetType      string
	PeeledCommitSHA string
	IsSymbolic      bool
	SymbolicTarget  string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type GitCommit struct {
	ID                      uint   `gorm:"primaryKey"`
	RepositoryID            uint   `gorm:"uniqueIndex:idx_git_commits_repo_sha,priority:1"`
	SHA                     string `gorm:"uniqueIndex:idx_git_commits_repo_sha,priority:2"`
	TreeSHA                 string
	AuthorName              string
	AuthorEmail             string
	AuthoredAt              time.Time
	AuthoredTimezoneOffset  int
	CommitterName           string
	CommitterEmail          string
	CommittedAt             time.Time
	CommittedTimezoneOffset int
	Message                 string
	MessageEncoding         string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type GitCommitParent struct {
	ID           uint   `gorm:"primaryKey"`
	RepositoryID uint   `gorm:"uniqueIndex:idx_git_commit_parents_repo_commit_parent,priority:1;index:idx_git_commit_parents_repo_parent"`
	CommitSHA    string `gorm:"uniqueIndex:idx_git_commit_parents_repo_commit_parent,priority:2"`
	ParentSHA    string `gorm:"index:idx_git_commit_parents_repo_parent;uniqueIndex:idx_git_commit_parents_repo_commit_parent,priority:3"`
	ParentIndex  int    `gorm:"uniqueIndex:idx_git_commit_parents_repo_commit_parent,priority:4"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type GitCommitParentFile struct {
	ID              uint   `gorm:"primaryKey"`
	RepositoryID    uint   `gorm:"uniqueIndex:idx_git_commit_parent_files_unique,priority:1;index:idx_git_commit_parent_files_repo_path"`
	CommitSHA       string `gorm:"uniqueIndex:idx_git_commit_parent_files_unique,priority:2"`
	ParentSHA       string
	ParentIndex     int    `gorm:"uniqueIndex:idx_git_commit_parent_files_unique,priority:3"`
	Path            string `gorm:"uniqueIndex:idx_git_commit_parent_files_unique,priority:4;index:idx_git_commit_parent_files_repo_path"`
	PreviousPath    string
	Status          string
	FileKind        string
	IndexedAs       string
	OldMode         string
	NewMode         string
	BlobSHA         string
	PreviousBlobSHA string
	Additions       int
	Deletions       int
	Changes         int
	PatchText       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type GitCommitParentHunk struct {
	ID           uint   `gorm:"primaryKey"`
	RepositoryID uint   `gorm:"uniqueIndex:idx_git_commit_parent_hunks_unique,priority:1;index:idx_git_commit_parent_hunks_repo_path"`
	CommitSHA    string `gorm:"uniqueIndex:idx_git_commit_parent_hunks_unique,priority:2"`
	ParentSHA    string
	ParentIndex  int    `gorm:"uniqueIndex:idx_git_commit_parent_hunks_unique,priority:3"`
	Path         string `gorm:"uniqueIndex:idx_git_commit_parent_hunks_unique,priority:4;index:idx_git_commit_parent_hunks_repo_path"`
	HunkIndex    int    `gorm:"uniqueIndex:idx_git_commit_parent_hunks_unique,priority:5"`
	DiffHunk     string
	OldStart     int
	OldCount     int
	OldEnd       int
	NewStart     int
	NewCount     int
	NewEnd       int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type PullRequestChangeSnapshot struct {
	ID                uint `gorm:"primaryKey"`
	RepositoryID      uint `gorm:"uniqueIndex:idx_pr_change_snapshots_repo_pr,priority:1"`
	PullRequestID     uint `gorm:"index"`
	PullRequestNumber int  `gorm:"uniqueIndex:idx_pr_change_snapshots_repo_pr,priority:2"`
	HeadSHA           string
	BaseSHA           string
	MergeBaseSHA      string
	BaseRef           string
	State             string `gorm:"index"`
	Draft             bool   `gorm:"index"`
	IndexedAs         string
	IndexFreshness    string
	PathCount         int
	IndexedFileCount  int
	HunkCount         int
	Additions         int
	Deletions         int
	PatchBytes        int
	LastIndexedAt     *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type PullRequestChangeFile struct {
	ID                uint   `gorm:"primaryKey"`
	SnapshotID        uint   `gorm:"index;uniqueIndex:idx_pr_change_files_snapshot_path,priority:1"`
	RepositoryID      uint   `gorm:"index:idx_pr_change_files_repo_path;index:idx_pr_change_files_repo_pr"`
	PullRequestNumber int    `gorm:"index:idx_pr_change_files_repo_pr"`
	HeadSHA           string `gorm:"index:idx_pr_change_files_head"`
	BaseSHA           string
	MergeBaseSHA      string
	Path              string `gorm:"index:idx_pr_change_files_repo_path;uniqueIndex:idx_pr_change_files_snapshot_path,priority:2"`
	PreviousPath      string
	Status            string
	FileKind          string
	IndexedAs         string
	OldMode           string
	NewMode           string
	HeadBlobSHA       string
	BaseBlobSHA       string
	Additions         int
	Deletions         int
	Changes           int
	PatchText         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type PullRequestChangeHunk struct {
	ID                uint   `gorm:"primaryKey"`
	SnapshotID        uint   `gorm:"index;uniqueIndex:idx_pr_change_hunks_unique,priority:1"`
	RepositoryID      uint   `gorm:"index:idx_pr_change_hunks_repo_path;index:idx_pr_change_hunks_repo_pr"`
	PullRequestNumber int    `gorm:"index:idx_pr_change_hunks_repo_pr"`
	HeadSHA           string `gorm:"index:idx_pr_change_hunks_head"`
	BaseSHA           string
	MergeBaseSHA      string
	Path              string `gorm:"index:idx_pr_change_hunks_repo_path;uniqueIndex:idx_pr_change_hunks_unique,priority:2"`
	HunkIndex         int    `gorm:"uniqueIndex:idx_pr_change_hunks_unique,priority:3"`
	DiffHunk          string
	OldStart          int
	OldCount          int
	OldEnd            int
	NewStart          int
	NewCount          int
	NewEnd            int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type RepoChangeSyncState struct {
	ID                          uint `gorm:"primaryKey"`
	RepositoryID                uint `gorm:"uniqueIndex"`
	Repository                  Repository
	Dirty                       bool `gorm:"index"`
	DirtySince                  *time.Time
	LastWebhookAt               *time.Time
	LastRequestedFetchAt        *time.Time
	LastFetchStartedAt          *time.Time
	LastFetchFinishedAt         *time.Time
	LastSuccessfulFetchAt       *time.Time
	LastBackfillStartedAt       *time.Time
	LastBackfillFinishedAt      *time.Time
	LastOpenPRScanAt            *time.Time
	InventoryGenerationCurrent  int
	InventoryGenerationBuilding *int
	InventoryLastCommittedAt    *time.Time
	OpenPRTotal                 int
	OpenPRCurrent               int
	OpenPRStale                 int
	BackfillGeneration          int
	OpenPRCursorNumber          *int
	OpenPRCursorUpdatedAt       *time.Time
	BackfillMode                string `gorm:"index"`
	BackfillPriority            int
	FetchLeaseOwnerID           string `gorm:"index"`
	FetchLeaseStartedAt         *time.Time
	FetchLeaseHeartbeatAt       *time.Time `gorm:"index"`
	FetchLeaseUntil             *time.Time `gorm:"index"`
	BackfillLeaseOwnerID        string     `gorm:"index"`
	BackfillLeaseStartedAt      *time.Time
	BackfillLeaseHeartbeatAt    *time.Time `gorm:"index"`
	BackfillLeaseUntil          *time.Time `gorm:"index"`
	LastError                   string
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

type RepoOpenPullInventory struct {
	ID                uint      `gorm:"primaryKey"`
	RepositoryID      uint      `gorm:"uniqueIndex:idx_repo_open_pull_inventory_repo_gen_pr,priority:1;index:idx_repo_open_pull_inventory_repo_gen_freshness_updated,priority:1"`
	Generation        int       `gorm:"uniqueIndex:idx_repo_open_pull_inventory_repo_gen_pr,priority:2;index:idx_repo_open_pull_inventory_repo_gen_freshness_updated,priority:2"`
	PullRequestNumber int       `gorm:"uniqueIndex:idx_repo_open_pull_inventory_repo_gen_pr,priority:3;index:idx_repo_open_pull_inventory_repo_gen_freshness_updated,priority:4"`
	GitHubUpdatedAt   time.Time `gorm:"column:github_updated_at"`
	HeadSHA           string
	BaseSHA           string
	BaseRef           string
	State             string
	Draft             bool
	FreshnessState    string `gorm:"index:idx_repo_open_pull_inventory_repo_gen_freshness_updated,priority:3"`
	LastSeenAt        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type RepoTargetedPullRefresh struct {
	ID                uint       `gorm:"primaryKey"`
	RepositoryID      uint       `gorm:"uniqueIndex:idx_repo_targeted_pull_refreshes_repo_pr,priority:1;index:idx_repo_targeted_pull_refreshes_repo_requested,priority:1"`
	PullRequestNumber int        `gorm:"uniqueIndex:idx_repo_targeted_pull_refreshes_repo_pr,priority:2;index:idx_repo_targeted_pull_refreshes_repo_requested,priority:3"`
	RequestedAt       *time.Time `gorm:"index:idx_repo_targeted_pull_refreshes_repo_requested,priority:2"`
	LastWebhookAt     *time.Time
	LastAttemptedAt   *time.Time
	LastCompletedAt   *time.Time
	LeaseOwnerID      string `gorm:"index"`
	LeaseStartedAt    *time.Time
	LeaseHeartbeatAt  *time.Time `gorm:"index"`
	LeaseUntil        *time.Time `gorm:"index"`
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
