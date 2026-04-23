package githubsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gorm.io/gorm"
)

type repoLeaseKind string

const (
	fetchLeaseKind             repoLeaseKind = "fetch"
	backfillLeaseKind          repoLeaseKind = "backfill"
	recentPRRepairLeaseKind    repoLeaseKind = "recent_pr_repair"
	fullHistoryRepairLeaseKind repoLeaseKind = "full_history_repair"
)

type repoLeaseManager struct {
	db                *gorm.DB
	ownerID           string
	leaseTTL          time.Duration
	heartbeatInterval time.Duration
	staleAfter        time.Duration
}

type leaseRecoveryResult struct {
	FetchCleared             int64
	BackfillCleared          int64
	RecentPRRepairCleared    int64
	FullHistoryRepairCleared int64
}

func newRepoLeaseManager(db *gorm.DB, leaseTTL time.Duration) *repoLeaseManager {
	heartbeatInterval := changeSyncHeartbeatInterval(leaseTTL)
	return &repoLeaseManager{
		db:                db,
		ownerID:           newChangeSyncWorkerID(),
		leaseTTL:          leaseTTL,
		heartbeatInterval: heartbeatInterval,
		staleAfter:        maxDuration(3*heartbeatInterval, time.Second),
	}
}

func (m *repoLeaseManager) owner() string {
	return m.ownerID
}

func (m *repoLeaseManager) heartbeatEvery() time.Duration {
	return m.heartbeatInterval
}

func (m *repoLeaseManager) acquire(ctx context.Context, stateID uint, kind repoLeaseKind, now time.Time) (bool, *time.Time, error) {
	now = now.UTC()
	leaseUntil := now.Add(m.leaseTTL)
	ownerCol, startedCol, heartbeatCol, untilCol := leaseColumns(kind)
	availableSQL, availableArgs := m.reclaimableSQL(kind, now)
	args := append([]any{stateID}, availableArgs...)
	updates := map[string]any{
		ownerCol:     m.ownerID,
		startedCol:   now,
		heartbeatCol: now,
		untilCol:     leaseUntil,
		"updated_at": now,
	}
	result := m.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ?", args[0]).
		Where(availableSQL, args[1:]...).
		Updates(updates)
	if result.Error != nil {
		return false, nil, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil, nil
	}
	return true, &leaseUntil, nil
}

func (m *repoLeaseManager) heartbeat(ctx context.Context, stateID uint, kind repoLeaseKind) error {
	now := time.Now().UTC()
	ownerCol, _, heartbeatCol, untilCol := leaseColumns(kind)
	result := m.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND "+ownerCol+" = ?", stateID, m.ownerID).
		Updates(map[string]any{
			heartbeatCol: now,
			untilCol:     now.Add(m.leaseTTL),
			"updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%s lease is no longer owned by worker %s", kind, m.ownerID)
	}
	slog.Debug("change sync lease heartbeat", "phase", kind, "state_id", stateID, "owner_id", m.ownerID)
	return nil
}

func (m *repoLeaseManager) release(ctx context.Context, stateID uint, kind repoLeaseKind, updates map[string]any) error {
	now := time.Now().UTC()
	ownerCol, startedCol, heartbeatCol, untilCol := leaseColumns(kind)
	if updates == nil {
		updates = map[string]any{}
	}
	updates[ownerCol] = ""
	updates[startedCol] = nil
	updates[heartbeatCol] = nil
	updates[untilCol] = nil
	updates["updated_at"] = now
	result := m.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND "+ownerCol+" = ?", stateID, m.ownerID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		slog.Warn("change sync lease release skipped because ownership changed", "phase", kind, "state_id", stateID, "owner_id", m.ownerID)
	}
	return nil
}

func (m *repoLeaseManager) recoverStale(ctx context.Context) (leaseRecoveryResult, error) {
	now := time.Now().UTC()
	staleBefore := now.Add(-m.staleAfter)
	fetchCleared, err := m.clearStaleKind(ctx, fetchLeaseKind, now, staleBefore)
	if err != nil {
		return leaseRecoveryResult{}, err
	}
	backfillCleared, err := m.clearStaleKind(ctx, backfillLeaseKind, now, staleBefore)
	if err != nil {
		return leaseRecoveryResult{}, err
	}
	recentPRRepairCleared, err := m.clearStaleKind(ctx, recentPRRepairLeaseKind, now, staleBefore)
	if err != nil {
		return leaseRecoveryResult{}, err
	}
	fullHistoryRepairCleared, err := m.clearStaleKind(ctx, fullHistoryRepairLeaseKind, now, staleBefore)
	if err != nil {
		return leaseRecoveryResult{}, err
	}
	return leaseRecoveryResult{
		FetchCleared:             fetchCleared,
		BackfillCleared:          backfillCleared,
		RecentPRRepairCleared:    recentPRRepairCleared,
		FullHistoryRepairCleared: fullHistoryRepairCleared,
	}, nil
}

func (m *repoLeaseManager) clearStaleKind(ctx context.Context, kind repoLeaseKind, now, staleBefore time.Time) (int64, error) {
	ownerCol, startedCol, heartbeatCol, untilCol := leaseColumns(kind)
	where := untilCol + " IS NOT NULL AND (" + untilCol + " <= ? OR " + heartbeatCol + " IS NULL OR " + heartbeatCol + " <= ?)"
	result := m.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where(where, now.UTC(), staleBefore.UTC()).
		Updates(map[string]any{
			ownerCol:     "",
			startedCol:   nil,
			heartbeatCol: nil,
			untilCol:     nil,
			"updated_at": now.UTC(),
		})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected > 0 {
		slog.Info("recovered stale change sync leases", "phase", kind, "count", result.RowsAffected, "owner_id", m.ownerID)
	}
	return result.RowsAffected, nil
}

func (m *repoLeaseManager) reclaimableSQL(kind repoLeaseKind, now time.Time) (string, []any) {
	_, _, heartbeatCol, untilCol := leaseColumns(kind)
	staleBefore := now.UTC().Add(-m.staleAfter)
	return "(" + untilCol + " IS NULL OR " + untilCol + " <= ? OR " + heartbeatCol + " IS NULL OR " + heartbeatCol + " <= ?)", []any{now.UTC(), staleBefore}
}

func leaseColumns(kind repoLeaseKind) (ownerCol, startedCol, heartbeatCol, untilCol string) {
	switch kind {
	case fetchLeaseKind:
		return "fetch_lease_owner_id", "fetch_lease_started_at", "fetch_lease_heartbeat_at", "fetch_lease_until"
	case backfillLeaseKind:
		return "backfill_lease_owner_id", "backfill_lease_started_at", "backfill_lease_heartbeat_at", "backfill_lease_until"
	case recentPRRepairLeaseKind:
		return "recent_pr_repair_lease_owner_id", "recent_pr_repair_lease_started_at", "recent_pr_repair_lease_heartbeat_at", "recent_pr_repair_lease_until"
	case fullHistoryRepairLeaseKind:
		return "full_history_repair_lease_owner_id", "full_history_repair_lease_started_at", "full_history_repair_lease_heartbeat_at", "full_history_repair_lease_until"
	default:
		panic("unknown repo lease kind")
	}
}

func newChangeSyncWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	return fmt.Sprintf("%s:%d:%d", hostname, os.Getpid(), time.Now().UTC().UnixNano())
}

func changeSyncHeartbeatInterval(leaseTTL time.Duration) time.Duration {
	if leaseTTL <= 0 {
		return 10 * time.Second
	}
	interval := leaseTTL / 6
	interval = maxDuration(interval, 200*time.Millisecond)
	interval = minDuration(interval, 10*time.Second)
	return interval
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func leaseIsActive(now time.Time, heartbeatAt, until *time.Time) bool {
	if until == nil || !until.After(now.UTC()) {
		return false
	}
	if heartbeatAt == nil {
		return false
	}
	impliedTTL := until.UTC().Sub(heartbeatAt.UTC())
	if impliedTTL <= 0 {
		return false
	}
	staleAfter := maxDuration(3*changeSyncHeartbeatInterval(impliedTTL), time.Second)
	return heartbeatAt.UTC().Add(staleAfter).After(now.UTC())
}

func (w *ChangeSyncWorker) recoverLeases(ctx context.Context) error {
	result, err := w.leases.recoverStale(ctx)
	if err != nil {
		return err
	}
	if result.FetchCleared == 0 && result.BackfillCleared == 0 && result.RecentPRRepairCleared == 0 && result.FullHistoryRepairCleared == 0 {
		slog.Info("change sync lease recovery complete", "owner_id", w.leases.owner())
		return nil
	}
	slog.Info(
		"change sync lease recovery complete",
		"owner_id", w.leases.owner(),
		"fetch_cleared", result.FetchCleared,
		"backfill_cleared", result.BackfillCleared,
		"recent_pr_repair_cleared", result.RecentPRRepairCleared,
		"full_history_repair_cleared", result.FullHistoryRepairCleared,
	)
	return nil
}

func (w *ChangeSyncWorker) runWithLeaseHeartbeat(ctx context.Context, stateID uint, kind repoLeaseKind, fn func(context.Context) error) error {
	if w.leases == nil {
		return errors.New("repo lease manager is not configured")
	}
	if w.leases.heartbeatEvery() <= 0 {
		return fn(ctx)
	}

	passCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done, errCh := w.startLeaseHeartbeat(passCtx, cancel, stateID, kind)
	runErr := fn(passCtx)
	close(done)
	return w.finalizeLeaseHeartbeat(stateID, kind, runErr, errCh)
}

func (w *ChangeSyncWorker) startLeaseHeartbeat(passCtx context.Context, cancel context.CancelFunc, stateID uint, kind repoLeaseKind) (chan struct{}, chan error) {
	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(w.leases.heartbeatEvery())
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-passCtx.Done():
				return
			case <-ticker.C:
				if err := w.leases.heartbeat(passCtx, stateID, kind); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	return done, errCh
}

func (w *ChangeSyncWorker) finalizeLeaseHeartbeat(stateID uint, kind repoLeaseKind, runErr error, errCh <-chan error) error {
	select {
	case hbErr := <-errCh:
		if runErr == nil {
			return hbErr
		}
		slog.Warn("change sync pass ended after heartbeat failure", "phase", kind, "state_id", stateID, "owner_id", w.leases.owner(), "run_error", runErr, "heartbeat_error", hbErr)
	default:
	}
	return runErr
}
