package usage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeWindowsFromUsageText(t *testing.T) {
	windows := claudeWindowsFromUsageText(`You are currently using your subscription to power your Claude Code usage

Current session: 12% used
Current week (all models): 34% used
Current week (Sonnet only): 56% used`)
	if len(windows) != 3 {
		t.Fatalf("expected 3 windows, got %#v", windows)
	}
	if windows[0].Label != "5h" || windows[0].UsedPercent != 12 {
		t.Fatalf("unexpected session window: %#v", windows[0])
	}
	if windows[1].Label != "weekly" || windows[1].UsedPercent != 34 {
		t.Fatalf("unexpected weekly window: %#v", windows[1])
	}
	if windows[2].Label != "sonnet" || windows[2].UsedPercent != 56 {
		t.Fatalf("unexpected sonnet window: %#v", windows[2])
	}
}

func TestClaudeFetcherFetchProfile(t *testing.T) {
	tmpDir := t.TempDir()
	profilesRoot := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(filepath.Join(profilesRoot, "personal"), 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(tmpDir, "claude")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
printf '%s\n' 'Current session: 12% used'
printf '%s\n' 'Current week (all models): 34% used'
`), 0700); err != nil {
		t.Fatal(err)
	}
	fetcher := ClaudeFetcher{
		Binary:       binary,
		ProfilesRoot: profilesRoot,
		Timeout:      2 * time.Second,
	}
	usage := fetcher.FetchProfile(context.Background(), "personal")
	if usage.Error != "" {
		t.Fatalf("unexpected usage error: %s", usage.Error)
	}
	if len(usage.Windows) != 2 || usage.Windows[0].UsedPercent != 12 || usage.Windows[1].UsedPercent != 34 {
		t.Fatalf("unexpected windows: %#v", usage.Windows)
	}
}

func TestClaudeFetcherPrefersOAuthUsage(t *testing.T) {
	tmpDir := t.TempDir()
	profilesRoot := filepath.Join(tmpDir, "profiles")
	profileDir := filepath.Join(profilesRoot, "personal")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeClaudeKeychain(t, profileDir, "access-token")

	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = fmt.Fprint(w, `{
			"five_hour": {"utilization": 42, "resets_at": "2100-01-01T00:00:00Z"},
			"seven_day": {"utilization": 18, "resets_at": "2100-01-08T00:00:00Z"}
		}`)
	}))
	defer server.Close()

	fetcher := ClaudeFetcher{
		Binary:       filepath.Join(tmpDir, "missing-claude"),
		ProfilesRoot: profilesRoot,
		Client:       server.Client(),
		OAuthURL:     server.URL,
		Timeout:      2 * time.Second,
	}
	usage := fetcher.FetchProfile(context.Background(), "personal")
	if usage.Error != "" {
		t.Fatalf("unexpected usage error: %s", usage.Error)
	}
	if gotAuth != "Bearer access-token" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if len(usage.Windows) != 2 || usage.Windows[0].UsedPercent != 42 || usage.Windows[1].UsedPercent != 18 {
		t.Fatalf("unexpected windows: %#v", usage.Windows)
	}
}

func TestClaudeWindowsFromOAuthIncludesExtraUsage(t *testing.T) {
	extra := 79.2
	response := claudeOAuthUsageResponse{
		FiveHour:   &claudeOAuthUsageWindow{Utilization: floatPtr(0)},
		SevenDay:   &claudeOAuthUsageWindow{Utilization: floatPtr(0)},
		ExtraUsage: &claudeOAuthUsageWindow{Utilization: &extra},
	}
	windows := claudeWindowsFromOAuth(response)
	if len(windows) != 3 {
		t.Fatalf("expected 3 windows, got %#v", windows)
	}
	if windows[2].Label != "extra" || windows[2].UsedPercent != 79 {
		t.Fatalf("unexpected extra window: %#v", windows[2])
	}
}

func TestClaudeFetcherDoesNotShowSubscriptionZeroFallback(t *testing.T) {
	tmpDir := t.TempDir()
	profilesRoot := filepath.Join(tmpDir, "profiles")
	profileDir := filepath.Join(profilesRoot, "personal")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(tmpDir, "claude")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
printf '%s\n' 'You are currently using your subscription to power your Claude Code usage'
printf '%s\n' 'Current session: 0% used'
printf '%s\n' 'Current week (all models): 0% used'
`), 0700); err != nil {
		t.Fatal(err)
	}
	fetcher := ClaudeFetcher{
		Binary:       binary,
		ProfilesRoot: profilesRoot,
		Timeout:      2 * time.Second,
	}
	usage := fetcher.FetchProfile(context.Background(), "personal")
	if usage.Error != "usage unavailable" {
		t.Fatalf("expected usage unavailable, got %#v", usage)
	}
}

func TestClaudeFetcherUsesBrowserCookieForProfileOrganization(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmpDir := t.TempDir()
	cliRoot := filepath.Join(tmpDir, "cli")
	profileDir := filepath.Join(cliRoot, "work")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, ".claude.json"), []byte(`{"oauthAccount":{"organizationUuid":"org-work"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	const password = "safe-storage-password"
	const sessionKey = "sk-ant-browser-session-key"
	cookiesPath := filepath.Join(tmpDir, "BrowserCookies")
	writeClaudeCookiesDB(t, cookiesPath, chromiumV10CookieForTest(t, sessionKey, password))

	var gotUsagePath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/organizations":
			_, _ = fmt.Fprint(w, `[
				{"uuid":"org-personal","name":"Personal","capabilities":["chat"]},
				{"uuid":"org-work","name":"Work","capabilities":["chat"]}
			]`)
		case "/api/organizations/org-work/usage":
			gotUsagePath = r.URL.Path
			_, _ = fmt.Fprint(w, `{
				"five_hour": {"utilization": 0, "resets_at": "2100-01-01T00:00:00Z"},
				"seven_day": {"utilization": 0, "resets_at": "2100-01-08T00:00:00Z"},
				"extra_usage": {"utilization": 79.2}
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fetcher := ClaudeFetcher{
		TargetID:     "claude_cli",
		ProfilesRoot: cliRoot,
		WebBaseURL:   server.URL + "/api",
		Client:       server.Client(),
		BrowserCookieSources: []ClaudeBrowserCookieSource{
			{
				Name:                "test-browser",
				CookiesPath:         cookiesPath,
				SafeStorageService:  "test-browser-safe-storage",
				SafeStoragePassword: password,
			},
		},
	}
	usage := fetcher.FetchProfile(context.Background(), "work")
	if usage.Error != "" {
		t.Fatalf("unexpected usage error: %s", usage.Error)
	}
	if gotUsagePath != "/api/organizations/org-work/usage" {
		t.Fatalf("usage path = %q", gotUsagePath)
	}
	if len(usage.Windows) != 3 || usage.Windows[2].Label != "extra" || usage.Windows[2].UsedPercent != 79 {
		t.Fatalf("unexpected windows: %#v", usage.Windows)
	}
}

func TestClaudeFetcherUsesDesktopWebSessionCookie(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	tmpDir := t.TempDir()
	cliRoot := filepath.Join(tmpDir, "cli")
	desktopRoot := filepath.Join(tmpDir, "desktop")
	if err := os.MkdirAll(filepath.Join(cliRoot, "personal"), 0700); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(desktopRoot, "personal")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	const password = "safe-storage-password"
	const sessionKey = "sk-ant-test-session-key"
	writeClaudeCookiesDB(t, filepath.Join(profileDir, "Cookies"), chromiumV10CookieForTest(t, sessionKey, password))

	var gotCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		switch r.URL.Path {
		case "/api/organizations":
			_, _ = fmt.Fprint(w, `[{"uuid":"org-1","name":"Work","capabilities":["chat"]}]`)
		case "/api/organizations/org-1/usage":
			_, _ = fmt.Fprint(w, `{
				"five_hour": {"utilization": 42, "resets_at": "2100-01-01T00:00:00Z"},
				"seven_day": {"utilization": 18, "resets_at": "2100-01-08T00:00:00Z"}
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	fetcher := ClaudeFetcher{
		TargetID:            "claude_desktop_oauth",
		ProfilesRoot:        cliRoot,
		DesktopProfilesRoot: desktopRoot,
		WebBaseURL:          server.URL + "/api",
		SafeStoragePassword: password,
		Client:              server.Client(),
	}
	usage := fetcher.FetchProfile(context.Background(), "personal")
	if usage.Error != "" {
		t.Fatalf("unexpected usage error: %s", usage.Error)
	}
	if gotCookie != "sessionKey="+sessionKey {
		t.Fatalf("cookie header = %q", gotCookie)
	}
	if len(usage.Windows) != 2 || usage.Windows[0].UsedPercent != 42 || usage.Windows[1].UsedPercent != 18 {
		t.Fatalf("unexpected windows: %#v", usage.Windows)
	}
}

func floatPtr(value float64) *float64 {
	return &value
}

func writeClaudeKeychain(t *testing.T, profileDir, accessToken string) {
	t.Helper()
	body := fmt.Sprintf(`{
		"account": "default",
		"token": %q
	}`, fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"refreshToken":"refresh","expiresAt":4102444800000,"scopes":[]}}`, accessToken))
	if err := os.WriteFile(filepath.Join(profileDir, ".vibeswap_keychain.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeCookiesDB(t *testing.T, path string, encrypted []byte) {
	t.Helper()
	sql := fmt.Sprintf(`CREATE TABLE cookies(host_key TEXT, name TEXT, value TEXT DEFAULT '', encrypted_value BLOB);
INSERT INTO cookies(host_key, name, value, encrypted_value) VALUES('.claude.ai', 'sessionKey', '', X'%s');`, hex.EncodeToString(encrypted))
	cmd := exec.Command("sqlite3", path, sql)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 failed: %v\n%s", err, output)
	}
}

func chromiumV10CookieForTest(t *testing.T, value, password string) []byte {
	t.Helper()
	key, err := pbkdf2.Key(sha1.New, password, []byte("saltysalt"), 1003, 16)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := pkcs7Pad([]byte(value), aes.BlockSize)
	out := make([]byte, len(plain))
	iv := bytes.Repeat([]byte{0x20}, aes.BlockSize)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
	return append([]byte("v10"), out...)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	out := append([]byte(nil), data...)
	return append(out, bytes.Repeat([]byte{byte(padding)}, padding)...)
}
