package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultPoolConfigUsesLongerLivedIdleConnections(t *testing.T) {
	cfg := DefaultPoolConfig()
	require.Equal(t, 10, cfg.MaxOpenConns)
	require.Equal(t, 5, cfg.MaxIdleConns)
	require.Equal(t, 30*time.Minute, cfg.ConnMaxIdleTime)
	require.Equal(t, 2*time.Hour, cfg.ConnMaxLifetime)
}

func TestPrewarmPoolOpensAndReturnsIdleConnections(t *testing.T) {
	db, err := OpenWithPoolConfig("sqlite://file::memory:?cache=shared", PoolConfig{
		MaxOpenConns: 2,
		MaxIdleConns: 2,
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()

	require.NoError(t, PrewarmPool(context.Background(), sqlDB, 2))

	stats := sqlDB.Stats()
	require.LessOrEqual(t, 2, stats.MaxOpenConnections)
	require.GreaterOrEqual(t, stats.Idle, 1)
}
