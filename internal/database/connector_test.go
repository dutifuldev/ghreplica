package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildCloudSQLDSNFromURL(t *testing.T) {
	dsn, err := buildCloudSQLDSN(
		"postgres://test-user%40example.iam:sekret@localhost:5432/ghreplica?sslmode=disable&application_name=ghreplica&search_path=public",
		"proj:region:instance",
	)
	require.NoError(t, err)
	require.Equal(
		t,
		"host=proj:region:instance sslmode=disable user=test-user@example.iam password=sekret dbname=ghreplica application_name=ghreplica search_path=public",
		dsn,
	)
}

func TestBuildCloudSQLDSNRequiresInstanceName(t *testing.T) {
	_, err := buildCloudSQLDSN("postgres://user@localhost/db?sslmode=disable", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CLOUDSQL_INSTANCE_CONNECTION_NAME")
}

func TestNewConnectorRejectsUnsupportedDialer(t *testing.T) {
	_, err := NewConnector(ConnectConfig{
		DatabaseURL: "postgres://user@localhost/db?sslmode=disable",
		Dialer:      "weird",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported DB_DIALER")
}
