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
