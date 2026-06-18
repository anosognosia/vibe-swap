package usage

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgyFetcherFetchProfile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	writeAgyProfile(t, tmpDir, "work", `{
		"access_token": "access-token",
		"expiry_date": 4102444800000,
		"project_id": "stored-project"
	}`)

	var gotAuth, gotUserAgent, gotModelsBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUserAgent = r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/load":
			_, _ = fmt.Fprint(w, `{
				"planInfo": {"planType": "pro"},
				"cloudaicompanionProject": "loaded-project"
			}`)
		case "/models":
			body, _ := io.ReadAll(r.Body)
			gotModelsBody = string(body)
			_, _ = fmt.Fprint(w, `{
				"models": {
					"gemini-3-pro-low": {
						"displayName": "Gemini 3 Pro Low",
						"quotaInfo": {
							"remainingFraction": 0.58,
							"resetTime": "2100-01-01T00:00:00Z"
						}
					},
					"claude-sonnet-4": {
						"displayName": "Claude Sonnet 4",
						"quotaInfo": {
							"remainingFraction": 0.82,
							"resetTime": "2100-01-08T00:00:00Z"
						}
					}
				}
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fetcher := AgyFetcher{
		Client:                  server.Client(),
		LoadCodeAssistURL:       server.URL + "/load",
		FetchAvailableModelsURL: server.URL + "/models",
	}
	usage := fetcher.FetchProfile(context.Background(), "work")
	if usage.Error != "" {
		t.Fatalf("unexpected agy usage error: %s", usage.Error)
	}
	if usage.Plan != "pro" {
		t.Fatalf("unexpected plan: %q", usage.Plan)
	}
	if len(usage.Windows) != 2 {
		t.Fatalf("expected 2 usage windows, got %#v", usage.Windows)
	}
	if usage.Windows[0].Label != "Gemini" || usage.Windows[0].UsedPercent != 42 {
		t.Fatalf("unexpected Gemini window: %#v", usage.Windows[0])
	}
	if usage.Windows[1].Label != "Claude+GPT" || usage.Windows[1].UsedPercent != 18 {
		t.Fatalf("unexpected Claude+GPT window: %#v", usage.Windows[1])
	}
	if gotAuth != "Bearer access-token" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if gotUserAgent != "antigravity" {
		t.Fatalf("user-agent = %q", gotUserAgent)
	}
	if !strings.Contains(gotModelsBody, "stored-project") {
		t.Fatalf("expected stored project id in models request, got %s", gotModelsBody)
	}
}

func TestAgyFetcherUsesMatchingLocalQuotaBeforeExpiredOAuth(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	writeAgyProfileWithAccounts(t, tmpDir, "personal", `{
		"access_token": "expired-token",
		"expiry_date": 1
	}`, `{"active":"person@example.com"}`)

	var sawQuotaSummary bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary":
			sawQuotaSummary = true
			if r.Header.Get("Connect-Protocol-Version") != "1" {
				t.Fatalf("missing Connect-Protocol-Version header")
			}
			_, _ = fmt.Fprint(w, `{
				"response": {
					"groups": [
						{
							"displayName": "Gemini",
							"buckets": [
								{
									"bucketId": "gemini-session-5h",
									"displayName": "Session",
									"remaining": {"case": "remainingFraction", "value": 0.58},
									"resetTime": "2100-01-01T00:00:00Z"
								},
								{
									"bucketId": "gemini-weekly",
									"displayName": "Weekly",
									"remainingFraction": 0.82,
									"resetTime": "2100-01-08T00:00:00Z"
								}
							]
						},
						{
							"displayName": "Claude + GPT",
							"buckets": [
								{
									"bucketId": "claude-gpt-session-5h",
									"displayName": "Session",
									"remainingFraction": 0.91
								}
							]
						}
					]
				}
			}`)
		case "/exa.language_server_pb.LanguageServerService/GetUserStatus":
			_, _ = fmt.Fprint(w, `{
				"userStatus": {
					"email": "person@example.com",
					"planStatus": {"planInfo": {"planDisplayName": "Pro"}}
				}
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fetcher := AgyFetcher{
		Client:            server.Client(),
		LocalEndpointURLs: []string{server.URL},
	}
	usage := fetcher.FetchProfile(context.Background(), "personal")
	if usage.Error != "" {
		t.Fatalf("unexpected agy usage error: %s", usage.Error)
	}
	if !sawQuotaSummary {
		t.Fatal("expected local quota summary request")
	}
	if usage.Plan != "Pro" {
		t.Fatalf("unexpected plan: %q", usage.Plan)
	}
	if len(usage.Windows) != 3 {
		t.Fatalf("expected 3 local usage windows, got %#v", usage.Windows)
	}
	if usage.Windows[0].Label != "Gemini 5h" || usage.Windows[0].UsedPercent != 42 {
		t.Fatalf("unexpected Gemini session window: %#v", usage.Windows[0])
	}
	if usage.Windows[1].Label != "Gemini wk" || usage.Windows[1].UsedPercent != 18 {
		t.Fatalf("unexpected Gemini weekly window: %#v", usage.Windows[1])
	}
	if usage.Windows[2].Label != "C+GPT 5h" || usage.Windows[2].UsedPercent != 9 {
		t.Fatalf("unexpected Claude+GPT window: %#v", usage.Windows[2])
	}
}

func TestAgyFetcherSkipsLocalQuotaForDifferentAccount(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	writeAgyProfileWithAccounts(t, tmpDir, "work", `{
		"access_token": "expired-token",
		"expiry_date": 1
	}`, `{"active":"work@example.com"}`)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary":
			_, _ = fmt.Fprint(w, `{
				"response": {
					"groups": [
						{
							"displayName": "Gemini",
							"buckets": [
								{"bucketId": "gemini-session-5h", "remainingFraction": 0.58}
							]
						}
					]
				}
			}`)
		case "/exa.language_server_pb.LanguageServerService/GetUserStatus":
			_, _ = fmt.Fprint(w, `{"userStatus": {"email": "personal@example.com"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fetcher := AgyFetcher{
		Client:            server.Client(),
		LocalEndpointURLs: []string{server.URL},
	}
	usage := fetcher.FetchProfile(context.Background(), "work")
	if !strings.Contains(usage.Error, "access token expired") {
		t.Fatalf("expected expired token fallback error, got %#v", usage)
	}
}

func TestAgyFetcherUsesLocalQuotaForActiveProfileWhenSavedEmailIsStale(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	writeAgyProfileWithAccounts(t, tmpDir, "personal", `{
		"access_token": "expired-token",
		"expiry_date": 1
	}`, `{"active":"work@example.com"}`)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary":
			_, _ = fmt.Fprint(w, `{
				"response": {
					"groups": [
						{
							"displayName": "Gemini",
							"buckets": [
								{"bucketId": "gemini-session-5h", "remainingFraction": 0.58}
							]
						}
					]
				}
			}`)
		case "/exa.language_server_pb.LanguageServerService/GetUserStatus":
			_, _ = fmt.Fprint(w, `{"userStatus": {"email": "personal@example.com"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fetcher := AgyFetcher{
		Client:            server.Client(),
		LocalEndpointURLs: []string{server.URL},
		ActiveProfileName: "personal",
		UseLocalForActive: true,
	}
	usage := fetcher.FetchProfile(context.Background(), "personal")
	if usage.Error != "" {
		t.Fatalf("unexpected agy usage error: %s", usage.Error)
	}
	if usage.Email != "personal@example.com" {
		t.Fatalf("expected live email, got %q", usage.Email)
	}
	if len(usage.Windows) != 1 || usage.Windows[0].UsedPercent != 42 {
		t.Fatalf("unexpected windows: %#v", usage.Windows)
	}
}

func TestAgyFetcherDoesNotRefreshExpiredToken(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	writeAgyProfile(t, tmpDir, "personal", `{
		"access_token": "expired-token",
		"expiry_date": 1
	}`)

	usage := (AgyFetcher{}).FetchProfile(context.Background(), "personal")
	if !strings.Contains(usage.Error, "access token expired") {
		t.Fatalf("expected expired token error, got %#v", usage)
	}
}

func writeAgyProfile(t *testing.T, home, profileName, oauthJSON string) {
	t.Helper()
	writeAgyProfileWithAccounts(t, home, profileName, oauthJSON, `{}`)
}

func writeAgyProfileWithAccounts(t *testing.T, home, profileName, oauthJSON, accountsJSON string) {
	t.Helper()
	profileDir := filepath.Join(home, ".config", "vibeswap", "profiles", "agy")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{
		"files": {
			"~/.gemini/oauth_creds.json": %q,
			"~/.gemini/google_accounts.json": %q
		}
	}`,
		base64.StdEncoding.EncodeToString([]byte(oauthJSON)),
		base64.StdEncoding.EncodeToString([]byte(accountsJSON)))
	if err := os.WriteFile(filepath.Join(profileDir, profileName+".json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
