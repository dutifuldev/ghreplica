package github_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/stretchr/testify/require"
)

func TestClientUsesGitHubAppInstallationToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	tokenRequests := 0
	apiRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/123/access_tokens":
			tokenRequests++
			require.NotEmpty(t, r.Header.Get("Authorization"))
			require.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"token":      "installation-token",
				"expires_at": time.Now().UTC().Add(30 * time.Minute),
			}))
		case "/repos/acme/widgets":
			apiRequests++
			require.Equal(t, "Bearer installation-token", r.Header.Get("Authorization"))
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"id":         1,
				"node_id":    "repo",
				"name":       "widgets",
				"full_name":  "acme/widgets",
				"private":    false,
				"html_url":   "https://github.com/acme/widgets",
				"url":        "https://api.github.com/repos/acme/widgets",
				"created_at": time.Now().UTC(),
				"updated_at": time.Now().UTC(),
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := github.NewClient(server.URL, github.AuthConfig{
		AppID:          "42",
		InstallationID: "123",
		PrivateKeyPEM:  string(keyPEM),
	})

	_, err = client.GetRepository(context.Background(), "acme", "widgets")
	require.NoError(t, err)
	_, err = client.GetRepository(context.Background(), "acme", "widgets")
	require.NoError(t, err)

	require.Equal(t, 1, tokenRequests)
	require.Equal(t, 2, apiRequests)
}
