package githubsync

import (
	"context"
	"errors"
	"sync"
	"time"
)

type repairPhase string

const (
	recentRepairPhase      repairPhase = "recent_pr_repair"
	fullHistoryRepairPhase repairPhase = "full_history_repair"
)

type repairPassMetrics struct {
	PullsScanned    int
	IssuesScanned   int
	PullsStale      int
	IssuesStale     int
	PullsUnchanged  int
	IssuesUnchanged int
	PullFetches     int
	IssueFetches    int
	PullsRepaired   int
	IssuesRepaired  int
	ApplyWrites     int
	Completed       bool
	NextPage        int
}

type repairRepoMetrics struct {
	RepositoryID     uint       `json:"repository_id"`
	FullName         string     `json:"full_name"`
	Passes           int64      `json:"passes"`
	Failures         int64      `json:"failures"`
	PullsScanned     int64      `json:"pulls_scanned"`
	IssuesScanned    int64      `json:"issues_scanned"`
	PullsStale       int64      `json:"pulls_stale"`
	IssuesStale      int64      `json:"issues_stale"`
	PullsUnchanged   int64      `json:"pulls_unchanged"`
	IssuesUnchanged  int64      `json:"issues_unchanged"`
	PullFetches      int64      `json:"pull_fetches"`
	IssueFetches     int64      `json:"issue_fetches"`
	PullsRepaired    int64      `json:"pulls_repaired"`
	IssuesRepaired   int64      `json:"issues_repaired"`
	ApplyWrites      int64      `json:"apply_writes"`
	Timeouts         int64      `json:"timeouts"`
	LastLeaseWaitMS  int64      `json:"last_lease_wait_ms"`
	TotalLeaseWaitMS int64      `json:"total_lease_wait_ms"`
	LastDurationMS   int64      `json:"last_duration_ms"`
	TotalDurationMS  int64      `json:"total_duration_ms"`
	LastCompletedAt  *time.Time `json:"last_completed_at,omitempty"`
	LastFailedAt     *time.Time `json:"last_failed_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	LastCompleted    bool       `json:"last_completed"`
	LastNextPage     int        `json:"last_next_page"`
}

type repairMetricsRegistry struct {
	mu      sync.RWMutex
	byPhase map[repairPhase]map[uint]repairRepoMetrics
}

func newRepairMetricsRegistry() *repairMetricsRegistry {
	return &repairMetricsRegistry{
		byPhase: map[repairPhase]map[uint]repairRepoMetrics{
			recentRepairPhase:      {},
			fullHistoryRepairPhase: {},
		},
	}
}

func (m *repairMetricsRegistry) recordSuccess(phase repairPhase, repositoryID uint, fullName string, result repairPassMetrics, leaseWait time.Duration, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repoMetrics := m.repoMetricsLocked(phase, repositoryID, fullName)
	repoMetrics.Passes++
	repoMetrics.PullsScanned += int64(result.PullsScanned)
	repoMetrics.IssuesScanned += int64(result.IssuesScanned)
	repoMetrics.PullsStale += int64(result.PullsStale)
	repoMetrics.IssuesStale += int64(result.IssuesStale)
	repoMetrics.PullsUnchanged += int64(result.PullsUnchanged)
	repoMetrics.IssuesUnchanged += int64(result.IssuesUnchanged)
	repoMetrics.PullFetches += int64(result.PullFetches)
	repoMetrics.IssueFetches += int64(result.IssueFetches)
	repoMetrics.PullsRepaired += int64(result.PullsRepaired)
	repoMetrics.IssuesRepaired += int64(result.IssuesRepaired)
	repoMetrics.ApplyWrites += int64(result.ApplyWrites)
	repoMetrics.LastLeaseWaitMS = leaseWait.Milliseconds()
	repoMetrics.TotalLeaseWaitMS += leaseWait.Milliseconds()
	repoMetrics.LastDurationMS = duration.Milliseconds()
	repoMetrics.TotalDurationMS += duration.Milliseconds()
	now := time.Now().UTC()
	repoMetrics.LastCompletedAt = &now
	repoMetrics.LastError = ""
	repoMetrics.LastCompleted = result.Completed
	repoMetrics.LastNextPage = result.NextPage
	m.byPhase[phase][repositoryID] = repoMetrics
}

func (m *repairMetricsRegistry) recordFailure(phase repairPhase, repositoryID uint, fullName string, leaseWait time.Duration, duration time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repoMetrics := m.repoMetricsLocked(phase, repositoryID, fullName)
	repoMetrics.Failures++
	repoMetrics.LastLeaseWaitMS = leaseWait.Milliseconds()
	repoMetrics.TotalLeaseWaitMS += leaseWait.Milliseconds()
	repoMetrics.LastDurationMS = duration.Milliseconds()
	repoMetrics.TotalDurationMS += duration.Milliseconds()
	now := time.Now().UTC()
	repoMetrics.LastFailedAt = &now
	if err != nil {
		repoMetrics.LastError = err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			repoMetrics.Timeouts++
		}
	}
	m.byPhase[phase][repositoryID] = repoMetrics
}

func (m *repairMetricsRegistry) repoMetricsLocked(phase repairPhase, repositoryID uint, fullName string) repairRepoMetrics {
	if _, ok := m.byPhase[phase]; !ok {
		m.byPhase[phase] = map[uint]repairRepoMetrics{}
	}
	repoMetrics, ok := m.byPhase[phase][repositoryID]
	if !ok {
		repoMetrics = repairRepoMetrics{
			RepositoryID: repositoryID,
			FullName:     fullName,
		}
	}
	if fullName != "" {
		repoMetrics.FullName = fullName
	}
	return repoMetrics
}

func (m *repairMetricsRegistry) snapshot(_ context.Context) map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := map[string]any{
		string(recentRepairPhase):      m.phaseSnapshotLocked(recentRepairPhase),
		string(fullHistoryRepairPhase): m.phaseSnapshotLocked(fullHistoryRepairPhase),
	}
	return out
}

func (m *repairMetricsRegistry) phaseSnapshotLocked(phase repairPhase) map[string]any {
	repositories := make([]repairRepoMetrics, 0, len(m.byPhase[phase]))
	totals := repairRepoMetrics{}
	for _, repoMetrics := range m.byPhase[phase] {
		repositories = append(repositories, repoMetrics)
		totals.Passes += repoMetrics.Passes
		totals.Failures += repoMetrics.Failures
		totals.PullsScanned += repoMetrics.PullsScanned
		totals.IssuesScanned += repoMetrics.IssuesScanned
		totals.PullsStale += repoMetrics.PullsStale
		totals.IssuesStale += repoMetrics.IssuesStale
		totals.PullsUnchanged += repoMetrics.PullsUnchanged
		totals.IssuesUnchanged += repoMetrics.IssuesUnchanged
		totals.PullFetches += repoMetrics.PullFetches
		totals.IssueFetches += repoMetrics.IssueFetches
		totals.PullsRepaired += repoMetrics.PullsRepaired
		totals.IssuesRepaired += repoMetrics.IssuesRepaired
		totals.ApplyWrites += repoMetrics.ApplyWrites
		totals.Timeouts += repoMetrics.Timeouts
		totals.TotalLeaseWaitMS += repoMetrics.TotalLeaseWaitMS
		totals.TotalDurationMS += repoMetrics.TotalDurationMS
	}
	return map[string]any{
		"totals":       totals,
		"repositories": repositories,
	}
}
