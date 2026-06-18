package usage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexFetcherFetchProfile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	profileDir := filepath.Join(tmpDir, ".config", "vibeswap", "profiles", "codex")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "work.json"), []byte(`{
		"tokens": {
			"access_token": "access-token",
			"account_id": "account-123"
		}
	}`), 0600); err != nil {
		t.Fatal(err)
	}

	var gotAuth, gotAccount, gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-Id")
		gotUserAgent = r.Header.Get("User-Agent")
		_, _ = fmt.Fprint(w, `{
			"plan_type": "pro",
			"rate_limit": {
				"primary_window": {
					"used_percent": 42,
					"reset_at": 4102444800
				},
				"secondary_window": {
					"used_percent": 18,
					"reset_at": 4103049600
				}
			}
		}`)
	}))
	defer server.Close()

	fetcher := CodexFetcher{Client: server.Client(), URL: server.URL}
	usage := fetcher.FetchProfile(context.Background(), "work")
	if usage.Error != "" {
		t.Fatalf("unexpected usage error: %s", usage.Error)
	}
	if usage.Plan != "pro" || usage.Session.UsedPercent != 42 || usage.Weekly.UsedPercent != 18 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	if gotAuth != "Bearer access-token" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if gotAccount != "account-123" {
		t.Fatalf("account header = %q", gotAccount)
	}
	if gotUserAgent != "codex-cli" {
		t.Fatalf("user-agent = %q", gotUserAgent)
	}
}

func TestCodexFetcherDoesNotRefreshMissingToken(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	profileDir := filepath.Join(tmpDir, ".config", "vibeswap", "profiles", "codex")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileDir, "personal.json")
	before := []byte(`{"tokens":{"refresh_token":"refresh-token"}}`)
	if err := os.WriteFile(profilePath, before, 0600); err != nil {
		t.Fatal(err)
	}

	usage := (CodexFetcher{}).FetchProfile(context.Background(), "personal")
	if !strings.Contains(usage.Error, "missing access token") {
		t.Fatalf("expected missing token error, got %#v", usage)
	}
	after, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("profile was mutated while fetching usage:\n%s", after)
	}
}
