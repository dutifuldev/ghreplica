package database

import "time"

type SearchDocument struct {
	ID               uint   `gorm:"primaryKey"`
	RepositoryID     uint   `gorm:"uniqueIndex:idx_search_documents_repo_type_github,priority:1;index:idx_search_documents_repo_type;index:idx_search_documents_repo_state;index:idx_search_documents_repo_author;index:idx_search_documents_repo_number"`
	DocumentType     string `gorm:"uniqueIndex:idx_search_documents_repo_type_github,priority:2;index:idx_search_documents_repo_type"`
	DocumentGitHubID int64  `gorm:"column:document_github_id;uniqueIndex:idx_search_documents_repo_type_github,priority:3"`
	Number           int    `gorm:"index:idx_search_documents_repo_number"`
	State            string `gorm:"index:idx_search_documents_repo_state"`
	AuthorID         *uint
	AuthorLogin      string `gorm:"index:idx_search_documents_repo_author"`
	APIURL           string
	HTMLURL          string
	TitleText        string
	BodyText         string
	SearchText       string
	NormalizedText   string
	ObjectCreatedAt  time.Time
	ObjectUpdatedAt  time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
