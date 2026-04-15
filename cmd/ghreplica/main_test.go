package main

import (
	"testing"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRunBackfillAcceptsDocumentedArgumentOrder(t *testing.T) {
	err := runBackfill(config.Config{}, []string{"repo", "acme/widgets", "--mode", "open_only", "--priority", "10"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}

func TestRunBackfillAcceptsFlagsBeforeTarget(t *testing.T) {
	err := runBackfill(config.Config{}, []string{"--mode", "open_only", "--priority", "10", "repo", "acme/widgets"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}
