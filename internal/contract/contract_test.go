package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/stretchr/testify/require"
)

func TestGitHubCompatibilitySubset(t *testing.T) {
	repoFullName := strings.TrimSpace(os.Getenv("GHREPLICA_CONTRACT_REPO"))
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if repoFullName == "" || token == "" {
		t.Skip("set GHREPLICA_CONTRACT_REPO and GITHUB_TOKEN to run live contract tests")
	}

	owner, repo, err := config.ParseFullName(repoFullName)
	require.NoError(t, err)

	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	client := github.NewClient("https://api.github.com", github.AuthConfig{Token: token})
	require.NoError(t, githubsync.NewService(db, client).BootstrapRepository(context.Background(), owner, repo))

	server := httptest.NewServer(httpapi.NewServer(db, httpapi.Options{}).Echo())
	t.Cleanup(server.Close)

	issues, err := client.ListIssues(context.Background(), owner, repo, "all")
	require.NoError(t, err)
	require.NotEmpty(t, issues)
	pulls, err := client.ListPullRequests(context.Background(), owner, repo, "all")
	require.NoError(t, err)
	require.NotEmpty(t, pulls)

	paths := []struct {
		github string
		local  string
	}{
		{github: "/repos/" + repoFullName, local: "/v1/github/repos/" + repoFullName},
		{github: "/repos/" + repoFullName + "/issues?state=all&page=1&per_page=30", local: "/v1/github/repos/" + repoFullName + "/issues?state=all&page=1&per_page=30"},
		{github: "/repos/" + repoFullName + "/issues/" + jsonNumber(issues[0].Number), local: "/v1/github/repos/" + repoFullName + "/issues/" + jsonNumber(issues[0].Number)},
		{github: "/repos/" + repoFullName + "/pulls?state=all&page=1&per_page=30", local: "/v1/github/repos/" + repoFullName + "/pulls?state=all&page=1&per_page=30"},
		{github: "/repos/" + repoFullName + "/pulls/" + jsonNumber(pulls[0].Number), local: "/v1/github/repos/" + repoFullName + "/pulls/" + jsonNumber(pulls[0].Number)},
	}

	for _, path := range paths {
		githubStatus, githubHeader, githubBody := fetchJSON(t, http.DefaultClient, "https://api.github.com"+path.github, token)
		localStatus, localHeader, localBody := fetchJSON(t, http.DefaultClient, server.URL+path.local, "")
		shape := contractShape(path.github, localBody)

		require.Equal(t, githubStatus, localStatus, path.github)
		require.Equal(t, projectJSON(normalizeJSON(githubBody), shape), projectJSON(normalizeJSON(localBody), shape), path.github)
		require.Equal(t, normalizeLinkHeader(githubHeader.Get("Link")), normalizeLinkHeader(localHeader.Get("Link")), path.github)
	}
}

func fetchJSON(t *testing.T, client *http.Client, target, token string) (int, http.Header, any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghreplica-contract")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var payload any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	return resp.StatusCode, resp.Header.Clone(), payload
}

func normalizeJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = normalizeJSON(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, normalizeJSON(child))
		}
		return out
	case string:
		return normalizeTimeString(typed)
	default:
		return typed
	}
}

func normalizeTimeString(value string) string {
	if value == "" || !strings.Contains(value, "T") {
		return value
	}
	return strings.ReplaceAll(value, "+00:00", "Z")
}

func projectJSON(source, shape any) any {
	switch shapeTyped := shape.(type) {
	case map[string]any:
		sourceMap, ok := source.(map[string]any)
		if !ok {
			return source
		}
		out := make(map[string]any, len(shapeTyped))
		for key, childShape := range shapeTyped {
			out[key] = projectJSON(sourceMap[key], childShape)
		}
		return out
	case []any:
		sourceSlice, ok := source.([]any)
		if !ok {
			return source
		}
		out := make([]any, 0, len(shapeTyped))
		for i, childShape := range shapeTyped {
			if i >= len(sourceSlice) {
				break
			}
			out = append(out, projectJSON(sourceSlice[i], childShape))
		}
		return out
	default:
		if source == nil {
			switch shape.(type) {
			case string:
				return ""
			}
		}
		return source
	}
}

func contractShape(path string, fallback any) any {
	userShape := map[string]any{
		"login":      "",
		"id":         float64(0),
		"node_id":    "",
		"avatar_url": "",
		"html_url":   "",
		"type":       "",
		"site_admin": false,
		"url":        "",
	}
	repoShape := map[string]any{
		"id":             float64(0),
		"node_id":        "",
		"name":           "",
		"full_name":      "",
		"private":        false,
		"owner":          userShape,
		"html_url":       "",
		"description":    "",
		"fork":           false,
		"url":            "",
		"default_branch": "",
		"visibility":     "",
		"archived":       false,
		"disabled":       false,
		"created_at":     "",
		"updated_at":     "",
	}
	issueShape := map[string]any{
		"id":           float64(0),
		"node_id":      "",
		"number":       float64(0),
		"title":        "",
		"body":         "",
		"state":        "",
		"state_reason": "",
		"user":         userShape,
		"locked":       false,
		"comments":     float64(0),
		"pull_request": map[string]any{"url": ""},
		"html_url":     "",
		"url":          "",
		"created_at":   "",
		"updated_at":   "",
		"closed_at":    "",
	}
	pullShape := map[string]any{
		"id":         float64(0),
		"node_id":    "",
		"number":     float64(0),
		"state":      "",
		"title":      "",
		"body":       "",
		"user":       userShape,
		"draft":      false,
		"head":       map[string]any{"ref": "", "sha": ""},
		"base":       map[string]any{"ref": "", "sha": ""},
		"html_url":   "",
		"url":        "",
		"diff_url":   "",
		"patch_url":  "",
		"created_at": "",
		"updated_at": "",
	}

	switch {
	case strings.Contains(path, "/pulls?"):
		return []any{pullShape}
	case strings.Contains(path, "/pulls/"):
		return pullShape
	case strings.Contains(path, "/issues?"):
		return []any{issueShape}
	case strings.Contains(path, "/issues/"):
		return issueShape
	case strings.HasPrefix(path, "/repos/"):
		return repoShape
	default:
		return fallback
	}
}

func normalizeLinkHeader(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}

	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			target := part[start+1 : end]
			if idx := strings.Index(target, "?"); idx >= 0 {
				target = target[idx:]
			}
			part = "<" + target + ">" + part[end+1:]
		}
		out = append(out, part)
	}
	return strings.Join(out, ",")
}

func jsonNumber(value int) string {
	return strconv.Itoa(value)
}
