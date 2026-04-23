package githubsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) RefreshOpenPullInventoryNow(ctx context.Context, owner, repo string, leaseTTL time.Duration) (RepoBackfillResult, error) {
	owner, repo, err := normalizeRefreshInventoryRepo(owner, repo)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	repository, err := s.lookupRefreshInventoryRepository(ctx, owner, repo)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	worker, leaseTTL := s.newInventoryRefreshWorker(leaseTTL)

	state, err := s.beginInventoryRefresh(ctx, repository.ID, owner, repo, worker.leases, leaseTTL)
	if err != nil {
		return RepoBackfillResult{}, err
	}

	var result RepoBackfillResult
	runErr := worker.runWithLeaseHeartbeat(ctx, state.ID, fetchLeaseKind, func(passCtx context.Context) error {
		var err error
		result, err = s.syncOpenPullInventory(passCtx, owner, repo, state)
		return err
	})
	if runErr != nil {
		return RepoBackfillResult{}, worker.finishFetchStateWithError(ctx, state, runErr)
	}
	if err := worker.completeFetchPass(ctx, state, result); err != nil {
		return RepoBackfillResult{}, err
	}

	return result, nil
}

func normalizeRefreshInventoryRepo(owner, repo string) (string, string, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return "", "", fmt.Errorf("repository must be in owner/repo form")
	}
	return owner, repo, nil
}

func (s *Service) lookupRefreshInventoryRepository(ctx context.Context, owner, repo string) (database.Repository, error) {
	repository, err := findRepositoryByName(ctx, s.db, owner, repo)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return database.Repository{}, fmt.Errorf("repository %s/%s is not tracked", owner, repo)
	}
	return repository, err
}

func (s *Service) newInventoryRefreshWorker(leaseTTL time.Duration) (*ChangeSyncWorker, time.Duration) {
	if leaseTTL <= 0 {
		leaseTTL = 15 * time.Minute
	}
	leases := newRepoLeaseManager(s.db, leaseTTL)
	return &ChangeSyncWorker{
		db:      s.db,
		service: s,
		leases:  leases,
	}, leaseTTL
}

func (s *Service) beginInventoryRefresh(ctx context.Context, repositoryID uint, owner, repo string, leases *repoLeaseManager, leaseTTL time.Duration) (database.RepoChangeSyncState, error) {
	var state database.RepoChangeSyncState
	now := time.Now().UTC()
	leasedUntil := now.Add(leaseTTL)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		state, err = loadOrCreateInventoryRefreshState(tx, repositoryID)
		if err != nil {
			return err
		}
		if phase := inventoryRefreshBlockingPhaseFromState(now, state); phase != "" {
			return fmt.Errorf("cannot refresh inventory while %s is running for %s/%s", phase, owner, repo)
		}
		state, err = updateInventoryRefreshLease(tx, state, leases, now, leasedUntil)
		return err
	})
	return state, err
}

func loadOrCreateInventoryRefreshState(tx *gorm.DB, repositoryID uint) (database.RepoChangeSyncState, error) {
	var state database.RepoChangeSyncState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("repository_id = ?", repositoryID).
		First(&state).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return state, err
	}
	state = database.RepoChangeSyncState{
		RepositoryID:             repositoryID,
		BackfillMode:             changeBackfillModeOff,
		RecentPRRepairCursorPage: 1,
		FullHistoryCursorPage:    1,
	}
	return state, tx.Create(&state).Error
}

func updateInventoryRefreshLease(tx *gorm.DB, state database.RepoChangeSyncState, leases *repoLeaseManager, now, leasedUntil time.Time) (database.RepoChangeSyncState, error) {
	nextGeneration := nextInventoryGeneration(state)
	if state.InventoryGenerationBuilding != nil && *state.InventoryGenerationBuilding > 0 {
		nextGeneration = *state.InventoryGenerationBuilding
	}
	if err := tx.Model(&database.RepoChangeSyncState{}).
		Where("id = ?", state.ID).
		Updates(map[string]any{
			"fetch_lease_owner_id":          leases.owner(),
			"fetch_lease_started_at":        now,
			"fetch_lease_heartbeat_at":      now,
			"fetch_lease_until":             leasedUntil,
			"last_fetch_started_at":         now,
			"inventory_generation_building": nextGeneration,
			"updated_at":                    now,
		}).Error; err != nil {
		return state, err
	}
	state.FetchLeaseOwnerID = leases.owner()
	state.FetchLeaseStartedAt = &now
	state.FetchLeaseHeartbeatAt = &now
	state.FetchLeaseUntil = &leasedUntil
	state.InventoryGenerationBuilding = intPtr(nextGeneration)
	return state, nil
}

func inventoryRefreshBlockingPhaseFromState(now time.Time, state database.RepoChangeSyncState) string {
	switch {
	case leaseIsActive(now, state.FetchLeaseHeartbeatAt, state.FetchLeaseUntil):
		return "inventory_scan"
	case leaseIsActive(now, state.BackfillLeaseHeartbeatAt, state.BackfillLeaseUntil):
		return "backfill"
	default:
		return ""
	}
}
