package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectorSQLiteOpenAndPaginationHelpers(t *testing.T) {
	connector, err := NewConnector(ConnectConfig{
		DatabaseURL: "sqlite://" + filepath.Join(t.TempDir(), "ghreplica.db"),
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, connector.Close()) }()

	handle, err := connector.Open(PoolConfig{
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	require.NoError(t, err)
	sqlDB := handle.SQLDB
	defer func() { require.NoError(t, sqlDB.Close()) }()

	require.NotNil(t, postgresDialector("postgres://user@localhost/db?sslmode=disable"))

	link := BuildLinkHeader("/repos/acme/widgets/issues", map[string]string{"state": "open"}, 2, 25, 80)
	require.Contains(t, link, `rel="first"`)
	require.Contains(t, link, `rel="prev"`)
	require.Contains(t, link, `rel="next"`)
	require.Contains(t, link, `rel="last"`)
	require.Equal(t, "", BuildLinkHeader("/repos/acme/widgets/issues", nil, 1, 25, 0))
	require.Equal(
		t,
		"/repos/acme/widgets/issues?page=3&per_page=50&state=open",
		buildPageURL("/repos/acme/widgets/issues", map[string]string{"state": "open"}, 3, 50),
	)
}

func TestRunMigrationsAndMigrationApplied(t *testing.T) {
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "migrations.db")
	db, err := Open(dbURL)
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	migrationsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(migrationsDir, "000001_create_widgets.up.sql"), []byte(`
CREATE TABLE widgets (
  id INTEGER PRIMARY KEY
);
`), 0o644))

	err = RunMigrations(db, migrationsDir)
	require.Error(t, err)

	_, err = sqlDB.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, "000001_create_widgets.up.sql", "2026-04-23T00:00:00Z")
	require.NoError(t, err)
	applied, err := migrationApplied(context.Background(), sqlDB, "000001_create_widgets.up.sql")
	require.NoError(t, err)
	require.True(t, applied)
}
