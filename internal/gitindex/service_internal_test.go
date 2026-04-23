package gitindex

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type countingLogger struct {
	mu     sync.Mutex
	counts map[string]int
}

func newCountingLogger() *countingLogger {
	return &countingLogger{counts: make(map[string]int)}
}

func (l *countingLogger) LogMode(logger.LogLevel) logger.Interface { return l }
func (l *countingLogger) Info(context.Context, string, ...any)     {}
func (l *countingLogger) Warn(context.Context, string, ...any)     {}
func (l *countingLogger) Error(context.Context, string, ...any)    {}

func (l *countingLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	lower := strings.ToLower(sql)
	if !strings.Contains(lower, "insert into") {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case strings.Contains(lower, "pull_request_change_files"):
		l.counts["pull_request_change_files"]++
	case strings.Contains(lower, "pull_request_change_hunks"):
		l.counts["pull_request_change_hunks"]++
	}
}

func (l *countingLogger) count(table string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[table]
}

func TestInsertPullRequestChangeRowsUsesBatches(t *testing.T) {
	db := openGitIndexTestDB(t, "insert-change-rows.db")
	counts := newCountingLogger()
	db = db.Session(&gorm.Session{Logger: counts})

	snapshot := database.PullRequestChangeSnapshot{
		RepositoryID:      1,
		PullRequestID:     1,
		PullRequestNumber: 101,
		HeadSHA:           "head",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		BaseRef:           "main",
		State:             "open",
		IndexedAs:         indexedAsFull,
		IndexFreshness:    freshnessCurrent,
	}
	require.NoError(t, db.Create(&snapshot).Error)

	fileRows := make([]database.PullRequestChangeFile, 0, pullRequestChangeFileBatchSize+5)
	for i := 0; i < pullRequestChangeFileBatchSize+5; i++ {
		fileRows = append(fileRows, database.PullRequestChangeFile{
			SnapshotID:        snapshot.ID,
			RepositoryID:      1,
			PullRequestNumber: 101,
			HeadSHA:           "head",
			BaseSHA:           "base",
			MergeBaseSHA:      "merge",
			Path:              "path-" + strconv.Itoa(i),
			Status:            "modified",
			FileKind:          "text",
			IndexedAs:         indexedAsPathOnly,
		})
	}

	hunkRows := make([]database.PullRequestChangeHunk, 0, pullRequestChangeHunkBatchSize+25)
	for i := 0; i < pullRequestChangeHunkBatchSize+25; i++ {
		hunkRows = append(hunkRows, database.PullRequestChangeHunk{
			SnapshotID:        snapshot.ID,
			RepositoryID:      1,
			PullRequestNumber: 101,
			HeadSHA:           "head",
			BaseSHA:           "base",
			MergeBaseSHA:      "merge",
			Path:              "path-" + strconv.Itoa(i%len(fileRows)),
			HunkIndex:         i,
			DiffHunk:          "@@",
		})
	}

	require.NoError(t, insertPullRequestChangeRows(db, fileRows, hunkRows))

	require.Greater(t, counts.count("pull_request_change_files"), 1)
	require.Greater(t, counts.count("pull_request_change_hunks"), 1)

	var storedFiles int64
	require.NoError(t, db.Model(&database.PullRequestChangeFile{}).Count(&storedFiles).Error)
	require.EqualValues(t, len(fileRows), storedFiles)

	var storedHunks int64
	require.NoError(t, db.Model(&database.PullRequestChangeHunk{}).Count(&storedHunks).Error)
	require.EqualValues(t, len(hunkRows), storedHunks)
}
