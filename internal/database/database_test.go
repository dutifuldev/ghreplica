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

func TestIsNonTransactionalMigration(t *testing.T) {
	require.True(t, isNonTransactionalMigration(nonTransactionalMigrationDirective+"\nSELECT 1;"))
	require.False(t, isNonTransactionalMigration("SELECT 1;"))
}

func TestAutoMigrateIsDisabled(t *testing.T) {
	db, err := Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)

	require.ErrorIs(t, AutoMigrate(db), ErrAutoMigrateDisabled)
}

func TestApplyTestSchemaCreatesSQLiteTables(t *testing.T) {
	db, err := Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)

	require.NoError(t, ApplyTestSchema(db))
	require.True(t, db.Migrator().HasTable((&WebhookDelivery{}).TableName()))
	require.True(t, db.Migrator().HasTable((&RepoChangeSyncState{}).TableName()))
	require.True(t, db.Migrator().HasTable((&SearchDocument{}).TableName()))
}

func TestSchemaModelsDeclareExplicitTableNames(t *testing.T) {
	type tableNamer interface {
		TableName() string
	}

	seen := make(map[string]struct{})
	for _, model := range schemaModels() {
		namer, ok := model.(tableNamer)
		require.True(t, ok, "model %T must declare TableName()", model)
		name := namer.TableName()
		require.NotEmpty(t, name, "model %T must return a non-empty table name", model)
		_, exists := seen[name]
		require.False(t, exists, "duplicate table name %q declared in schema model registry", name)
		seen[name] = struct{}{}
	}
}
