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
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return RepoBackfillResult{}, fmt.Errorf("repository must be in owner/repo form")
	}

	repository, err := findRepositoryByName(ctx, s.db, owner, repo)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RepoBackfillResult{}, fmt.Errorf("repository %s/%s is not tracked", owner, repo)
		}
		return RepoBackfillResult{}, err
	}

	var state database.RepoChangeSyncState

	if leaseTTL <= 0 {
		leaseTTL = 15 * time.Minute
	}
	leases := newRepoLeaseManager(s.db, leaseTTL)
	worker := &ChangeSyncWorker{
		db:      s.db,
		service: s,
		leases:  leases,
	}

	now := time.Now().UTC()
	leasedUntil := now.Add(leaseTTL)
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("repository_id = ?", repository.ID).
			First(&state).Error
		if lockErr != nil {
			if !errors.Is(lockErr, gorm.ErrRecordNotFound) {
				return lockErr
			}
			state = database.RepoChangeSyncState{
				RepositoryID:             repository.ID,
				BackfillMode:             changeBackfillModeOff,
				RecentPRRepairCursorPage: 1,
				FullHistoryCursorPage:    1,
			}
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
		}

		if phase := inventoryRefreshBlockingPhaseFromState(now, state); phase != "" {
			return fmt.Errorf("cannot refresh inventory while %s is running for %s/%s", phase, owner, repo)
		}

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
			return err
		}

		state.FetchLeaseOwnerID = leases.owner()
		state.FetchLeaseStartedAt = &now
		state.FetchLeaseHeartbeatAt = &now
		state.FetchLeaseUntil = &leasedUntil
		state.InventoryGenerationBuilding = intPtr(nextGeneration)
		return nil
	}); err != nil {
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
