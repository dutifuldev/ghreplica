package gitindex

import (
	"path/filepath"
	"testing"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openGitIndexTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()

	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), name))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))
	return db
}
