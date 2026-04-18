package githubsync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetainedDirtyExprUsesBooleanLiterals(t *testing.T) {
	scanStartedAt := time.Date(2026, 4, 18, 8, 5, 45, 0, time.UTC)

	dirtyExpr := retainedDirtyExpr(scanStartedAt)
	require.Equal(t, "CASE WHEN dirty_since IS NOT NULL AND dirty_since > ? THEN TRUE ELSE FALSE END", dirtyExpr.SQL)
	require.Equal(t, []any{scanStartedAt}, dirtyExpr.Vars)

	dirtySinceExpr := retainedDirtySinceExpr(scanStartedAt)
	require.Equal(t, "CASE WHEN dirty_since IS NOT NULL AND dirty_since > ? THEN dirty_since ELSE NULL END", dirtySinceExpr.SQL)
	require.Equal(t, []any{scanStartedAt}, dirtySinceExpr.Vars)
}
