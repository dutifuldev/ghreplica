package githubsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gorm.io/gorm"
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

	state, err := s.repoChangeStateByRepositoryID(ctx, repository.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RepoBackfillResult{}, fmt.Errorf("repository %s/%s is not configured for change sync", owner, repo)
		}
		return RepoBackfillResult{}, err
	}

	statusRow, err := s.repoChangeStatusRow(ctx, owner, repo)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	if phase, _ := currentRepoPhase(time.Now().UTC(), statusRow); phase != "" {
		return RepoBackfillResult{}, fmt.Errorf("cannot refresh inventory while %s is running for %s/%s", phase, owner, repo)
	}

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
	acquired, leasedUntil, err := leases.acquire(ctx, state.ID, fetchLeaseKind, now)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	if !acquired {
		return RepoBackfillResult{}, fmt.Errorf("could not acquire inventory refresh lease for %s/%s", owner, repo)
	}

	nextGeneration := nextInventoryGeneration(state)
	if state.InventoryGenerationBuilding != nil && *state.InventoryGenerationBuilding > 0 {
		nextGeneration = *state.InventoryGenerationBuilding
	}
	if err := s.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND fetch_lease_owner_id = ?", state.ID, leases.owner()).
		Updates(map[string]any{
			"last_fetch_started_at":         now,
			"inventory_generation_building": nextGeneration,
			"updated_at":                    now,
		}).Error; err != nil {
		_ = leases.release(ctx, state.ID, fetchLeaseKind, map[string]any{
			"inventory_generation_building": nil,
		})
		return RepoBackfillResult{}, err
	}

	state.FetchLeaseOwnerID = leases.owner()
	state.FetchLeaseStartedAt = &now
	state.FetchLeaseHeartbeatAt = &now
	state.FetchLeaseUntil = leasedUntil
	state.InventoryGenerationBuilding = intPtr(nextGeneration)

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
