package searchindex

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type repoDocumentStats struct {
	DocumentCount      int64      `gorm:"column:document_count"`
	LastIndexedAt      *time.Time `gorm:"column:last_indexed_at"`
	LastSourceUpdateAt *time.Time `gorm:"column:last_source_update_at"`
}

func (s *Service) GetRepoStatus(ctx context.Context, owner, repo string) (RepoStatus, error) {
	var repository database.Repository
	if err := s.db.WithContext(ctx).
		Where("full_name = ?", strings.TrimSpace(owner)+"/"+strings.TrimSpace(repo)).
		First(&repository).Error; err != nil {
		return RepoStatus{}, err
	}

	stats, err := s.repoDocumentStats(ctx, repository.ID)
	if err != nil {
		return RepoStatus{}, err
	}

	state, err := s.repoTextSearchStateOptional(ctx, repository.ID)
	if err != nil {
		return RepoStatus{}, err
	}

	status := newRepoStatus(repository, stats)
	status = applyRepoTextSearchState(status, state)
	return finalizeRepoStatus(status, state, stats), nil
}

func newRepoStatus(repository database.Repository, stats repoDocumentStats) RepoStatus {
	return RepoStatus{
		Repository: RepoStatusResource{
			Owner:    repository.OwnerLogin,
			Name:     repository.Name,
			FullName: repository.FullName,
		},
		DocumentCount:      stats.DocumentCount,
		LastIndexedAt:      stats.LastIndexedAt,
		LastSourceUpdateAt: stats.LastSourceUpdateAt,
		TextIndexStatus:    TextIndexStatusMissing,
		Freshness:          TextIndexFreshnessUnknown,
		Coverage:           TextIndexCoverageEmpty,
	}
}

func applyRepoTextSearchState(status RepoStatus, state *database.RepoTextSearchState) RepoStatus {
	if state == nil {
		return status
	}
	status.TextIndexStatus = normalizeTextIndexStatus(state.Status)
	status.Freshness = normalizeTextIndexFreshness(state.Freshness)
	status.Coverage = normalizeTextIndexCoverage(state.Coverage)
	status.LastError = strings.TrimSpace(state.LastError)
	if state.LastIndexedAt != nil {
		status.LastIndexedAt = state.LastIndexedAt
	}
	if state.LastSourceUpdateAt != nil {
		status.LastSourceUpdateAt = state.LastSourceUpdateAt
	}
	return status
}

func finalizeRepoStatus(status RepoStatus, state *database.RepoTextSearchState, stats repoDocumentStats) RepoStatus {
	if status.DocumentCount == 0 {
		status.Coverage = TextIndexCoverageEmpty
		if status.TextIndexStatus == "" {
			status.TextIndexStatus = TextIndexStatusMissing
		}
		return status
	}
	if state == nil {
		status.TextIndexStatus = TextIndexStatusReady
		status.Freshness = deriveFreshness(stats.LastIndexedAt, stats.LastSourceUpdateAt)
		status.Coverage = TextIndexCoveragePartial
		return status
	}
	if status.Freshness == TextIndexFreshnessUnknown {
		status.Freshness = deriveFreshness(status.LastIndexedAt, status.LastSourceUpdateAt)
	}
	if status.TextIndexStatus == TextIndexStatusReady && status.Freshness == TextIndexFreshnessStale {
		status.TextIndexStatus = TextIndexStatusStale
	}
	if status.TextIndexStatus == TextIndexStatusStale && status.Freshness == TextIndexFreshnessCurrent {
		status.TextIndexStatus = TextIndexStatusReady
	}
	return status
}

func (s *Service) markRebuildStarted(ctx context.Context, repositoryID uint, startedAt time.Time) error {
	return s.upsertRepoTextSearchState(ctx, repositoryID, func(state database.RepoTextSearchState) database.RepoTextSearchState {
		state.Status = TextIndexStatusBuilding
		state.LastError = ""
		return state
	})
}

func (s *Service) markRebuildSucceeded(ctx context.Context, repositoryID uint, indexedAt, sourceUpdatedAt time.Time) error {
	indexedAt = indexedAt.UTC()
	sourceUpdatedAt = normalizeSourceUpdatedAt(indexedAt, sourceUpdatedAt)
	return s.upsertRepoTextSearchState(ctx, repositoryID, func(state database.RepoTextSearchState) database.RepoTextSearchState {
		state.Status = TextIndexStatusReady
		state.Freshness = TextIndexFreshnessCurrent
		state.Coverage = coverageOrDefault(state.Coverage, TextIndexCoverageComplete)
		state.Coverage = TextIndexCoverageComplete
		state.LastIndexedAt = maxTimePtr(state.LastIndexedAt, indexedAt)
		state.LastSourceUpdateAt = maxTimePtr(state.LastSourceUpdateAt, sourceUpdatedAt)
		state.LastError = ""
		return state
	})
}

func (s *Service) markRebuildFailed(ctx context.Context, repositoryID uint, failedAt time.Time, failure error) error {
	return s.upsertRepoTextSearchState(ctx, repositoryID, func(state database.RepoTextSearchState) database.RepoTextSearchState {
		state.Status = TextIndexStatusFailed
		state.Freshness = deriveFreshness(state.LastIndexedAt, state.LastSourceUpdateAt)
		state.Coverage = normalizeTextIndexCoverage(state.Coverage)
		if state.Coverage == "" {
			state.Coverage = TextIndexCoveragePartial
		}
		if failure != nil {
			state.LastError = failure.Error()
		}
		return state
	})
}

func (s *Service) touchIndexedDocument(ctx context.Context, repositoryID uint, sourceUpdatedAt time.Time) error {
	indexedAt := time.Now().UTC()
	sourceUpdatedAt = normalizeSourceUpdatedAt(indexedAt, sourceUpdatedAt)
	return s.upsertRepoTextSearchState(ctx, repositoryID, func(state database.RepoTextSearchState) database.RepoTextSearchState {
		state.Status = TextIndexStatusReady
		state.Freshness = TextIndexFreshnessCurrent
		state.Coverage = coverageOrDefault(state.Coverage, TextIndexCoveragePartial)
		state.LastIndexedAt = maxTimePtr(state.LastIndexedAt, indexedAt)
		state.LastSourceUpdateAt = maxTimePtr(state.LastSourceUpdateAt, sourceUpdatedAt)
		state.LastError = ""
		return state
	})
}

func (s *Service) touchDeletedDocument(ctx context.Context, repositoryID uint) error {
	now := time.Now().UTC()
	return s.upsertRepoTextSearchState(ctx, repositoryID, func(state database.RepoTextSearchState) database.RepoTextSearchState {
		state.Status = TextIndexStatusReady
		state.Freshness = TextIndexFreshnessCurrent
		state.Coverage = coverageOrDefault(state.Coverage, TextIndexCoveragePartial)
		state.LastIndexedAt = maxTimePtr(state.LastIndexedAt, now)
		state.LastSourceUpdateAt = maxTimePtr(state.LastSourceUpdateAt, now)
		state.LastError = ""
		return state
	})
}

func (s *Service) upsertRepoTextSearchState(ctx context.Context, repositoryID uint, mutate func(database.RepoTextSearchState) database.RepoTextSearchState) error {
	state, err := s.repoTextSearchStateOptional(ctx, repositoryID)
	if err != nil {
		return err
	}
	if state == nil {
		state = &database.RepoTextSearchState{
			RepositoryID: repositoryID,
		}
	}
	next := mutate(*state)
	next.RepositoryID = repositoryID
	next.Status = normalizeTextIndexStatus(next.Status)
	next.Freshness = normalizeTextIndexFreshness(next.Freshness)
	next.Coverage = normalizeTextIndexCoverage(next.Coverage)
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status",
			"freshness",
			"coverage",
			"last_indexed_at",
			"last_source_update_at",
			"last_error",
			"updated_at",
		}),
	}).Create(&next).Error
}

func (s *Service) repoTextSearchStateOptional(ctx context.Context, repositoryID uint) (*database.RepoTextSearchState, error) {
	var state database.RepoTextSearchState
	err := s.db.WithContext(ctx).Where("repository_id = ?", repositoryID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *Service) repoDocumentStats(ctx context.Context, repositoryID uint) (repoDocumentStats, error) {
	var stats repoDocumentStats
	if err := s.db.WithContext(ctx).
		Model(&database.SearchDocument{}).
		Where("repository_id = ?", repositoryID).
		Count(&stats.DocumentCount).Error; err != nil {
		return stats, err
	}
	if stats.DocumentCount == 0 {
		return stats, nil
	}

	var latestIndexed database.SearchDocument
	if err := s.db.WithContext(ctx).
		Model(&database.SearchDocument{}).
		Select("updated_at").
		Where("repository_id = ?", repositoryID).
		Order("updated_at DESC").
		Limit(1).
		Take(&latestIndexed).Error; err != nil {
		return stats, err
	}
	stats.LastIndexedAt = &latestIndexed.UpdatedAt

	var latestSource database.SearchDocument
	if err := s.db.WithContext(ctx).
		Model(&database.SearchDocument{}).
		Select("object_updated_at").
		Where("repository_id = ?", repositoryID).
		Order("object_updated_at DESC").
		Limit(1).
		Take(&latestSource).Error; err != nil {
		return stats, err
	}
	stats.LastSourceUpdateAt = &latestSource.ObjectUpdatedAt
	return stats, nil
}

func normalizeTextIndexStatus(status string) string {
	switch strings.TrimSpace(status) {
	case TextIndexStatusMissing:
		return TextIndexStatusMissing
	case TextIndexStatusBuilding:
		return TextIndexStatusBuilding
	case TextIndexStatusReady:
		return TextIndexStatusReady
	case TextIndexStatusStale:
		return TextIndexStatusStale
	case TextIndexStatusFailed:
		return TextIndexStatusFailed
	default:
		return TextIndexStatusMissing
	}
}

func normalizeTextIndexFreshness(freshness string) string {
	switch strings.TrimSpace(freshness) {
	case TextIndexFreshnessCurrent:
		return TextIndexFreshnessCurrent
	case TextIndexFreshnessStale:
		return TextIndexFreshnessStale
	case TextIndexFreshnessUnknown:
		return TextIndexFreshnessUnknown
	default:
		return TextIndexFreshnessUnknown
	}
}

func normalizeTextIndexCoverage(coverage string) string {
	switch strings.TrimSpace(coverage) {
	case TextIndexCoverageEmpty:
		return TextIndexCoverageEmpty
	case TextIndexCoveragePartial:
		return TextIndexCoveragePartial
	case TextIndexCoverageComplete:
		return TextIndexCoverageComplete
	default:
		return TextIndexCoverageEmpty
	}
}

func coverageOrDefault(current, fallback string) string {
	current = normalizeTextIndexCoverage(current)
	if current == TextIndexCoverageEmpty {
		return fallback
	}
	return current
}

func deriveFreshness(lastIndexedAt, lastSourceUpdateAt *time.Time) string {
	if lastIndexedAt == nil || lastSourceUpdateAt == nil {
		return TextIndexFreshnessUnknown
	}
	if !lastIndexedAt.Before(*lastSourceUpdateAt) {
		return TextIndexFreshnessCurrent
	}
	return TextIndexFreshnessStale
}

func normalizeSourceUpdatedAt(indexedAt, sourceUpdatedAt time.Time) time.Time {
	if sourceUpdatedAt.IsZero() {
		return indexedAt.UTC()
	}
	return sourceUpdatedAt.UTC()
}

func maxTimePtr(existing *time.Time, candidate time.Time) *time.Time {
	candidate = candidate.UTC()
	if existing == nil || existing.Before(candidate) {
		next := candidate
		return &next
	}
	next := existing.UTC()
	return &next
}
