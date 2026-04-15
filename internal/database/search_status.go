package database

import "time"

type RepoTextSearchState struct {
	RepositoryID       uint `gorm:"primaryKey"`
	Repository         Repository
	Status             string `gorm:"index"`
	Freshness          string
	Coverage           string
	LastIndexedAt      *time.Time
	LastSourceUpdateAt *time.Time
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
