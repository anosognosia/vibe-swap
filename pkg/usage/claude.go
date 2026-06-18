package usage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/anosognosia/vibe-swap/pkg/config"
)

type ClaudeProfileUsage struct {
	ProfileName string
	Email       string
	Windows     []NamedUsageWindow
	Error       string
	UpdatedAt   time.Time
}

type ClaudeFetcher struct {
	Binary               string
	ProfilesRoot         string
	DesktopProfilesRoot  string
	BrowserCookieSources []ClaudeBrowserCookieSource
	TargetID             string
	Timeout              time.Duration
	Client               *http.Client
	OAuthURL             string
	WebBaseURL           string
	SafeStoragePassword  string
}

type ClaudeBrowserCookieSource struct {
	Name                string
	CookiesPath         string
	SafeStorageService  string
	SafeStoragePassword string
}

type claudeKeychainProfile struct {
	Token string `json:"token"`
}

type claudeStoredToken struct {
	ClaudeAIOAuth claudeOAuthCredentials `json:"claudeAiOauth"`
}

type claudeOAuthCredentials struct {
	AccessToken      string  `json:"accessToken"`
	RefreshToken     string  `json:"refreshToken"`
	ExpiresAt        float64 `json:"expiresAt"`
	SubscriptionType string  `json:"subscriptionType"`
	RateLimitTier    string  `json:"rateLimitTier"`
}

type claudeOAuthUsageResponse struct {
	FiveHour       *claudeOAuthUsageWindow `json:"five_hour"`
	SevenDay       *claudeOAuthUsageWindow `json:"seven_day"`
	SevenDaySonnet *claudeOAuthUsageWindow `json:"seven_day_sonnet"`
	SevenDayOpus   *claudeOAuthUsageWindow `json:"seven_day_opus"`
	ExtraUsage     *claudeOAuthUsageWindow `json:"extra_usage"`
}

type claudeOAuthUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type claudeWebOrganization struct {
	UUID             string   `json:"uuid"`
	Name             string   `json:"name"`
	Capabilities     []string `json:"capabilities"`
	OrganizationType string   `json:"organization_type"`
}

type claudeWebSession struct {
	CookieHeader         string
	TargetOrganizationID string
	Source               string
}

type claudeCLIConfig struct {
	OAuthAccount struct {
		OrganizationUUID string `json:"organizationUuid"`
	} `json:"oauthAccount"`
}

func FetchClaudeProfileUsages(ctx context.Context, targetID string, profileNames []string) map[string]ClaudeProfileUsage {
	fetcher := ClaudeFetcher{TargetID: targetID, Timeout: 8 * time.Second}
	return fetcher.FetchProfiles(ctx, profileNames)
}

func (f ClaudeFetcher) FetchProfiles(ctx context.Context, profileNames []string) map[string]ClaudeProfileUsage {
	results := make(map[string]ClaudeProfileUsage, len(profileNames))
	for _, profileName := range profileNames {
		results[profileName] = f.FetchProfile(ctx, profileName)
	}
	return results
}

func (f ClaudeFetcher) FetchProfile(ctx context.Context, profileName string) ClaudeProfileUsage {
	usage := ClaudeProfileUsage{
		ProfileName: profileName,
		UpdatedAt:   time.Now(),
	}
	profileDir, err := f.profileDir(profileName)
	if err != nil {
		usage.Error = err.Error()
		return usage
	}
	if f.TargetID == "claude_cli" || f.TargetID == "claude_desktop_oauth" {
		if webUsage, ok := f.fetchWebUsage(ctx, profileName); ok {
			usage.Windows = webUsage.Windows
			usage.Error = webUsage.Error
			return usage
		}
	}
	if _, err := os.Stat(profileDir); err != nil {
		usage.Error = "matching Claude CLI profile not found"
		return usage
	}
	if oauthUsage, ok := f.fetchOAuthUsage(ctx, profileDir); ok {
		usage.Windows = oauthUsage.Windows
		usage.Error = oauthUsage.Error
		return usage
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	binary := strings.TrimSpace(f.Binary)
	if binary == "" {
		binary = "claude"
	}
	cmd := exec.CommandContext(cmdCtx, binary, "/usage")
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+profileDir)
	output, err := cmd.CombinedOutput()
	if cmdCtx.Err() == context.DeadlineExceeded {
		usage.Error = "usage request timed out"
		return usage
	}
	if err != nil {
		usage.Error = fmt.Sprintf("usage request failed: %v", err)
		return usage
	}
	windows := claudeWindowsFromUsageText(string(output))
	if len(windows) == 0 {
		usage.Error = "usage unavailable"
		return usage
	}
	if claudeUsageTextLooksLikeSubscriptionZeroFallback(string(output), windows) {
		usage.Error = "usage unavailable"
		return usage
	}
	usage.Windows = windows
	return usage
}

func (f ClaudeFetcher) fetchWebUsage(ctx context.Context, profileName string) (ClaudeProfileUsage, bool) {
	var usage ClaudeProfileUsage
	sessions := f.webSessions(profileName)
	if len(sessions) == 0 {
		return usage, false
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(f.WebBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://claude.ai/api"
	}
	var lastErr error
	for _, session := range sessions {
		orgID, err := f.fetchClaudeWebOrganizationID(ctx, client, baseURL, session.CookieHeader, session.TargetOrganizationID)
		if err != nil {
			lastErr = err
			continue
		}
		windows, err := f.fetchClaudeWebUsageWindows(ctx, client, baseURL, session.CookieHeader, orgID)
		if err != nil {
			lastErr = err
			continue
		}
		if len(windows) == 0 {
			lastErr = fmt.Errorf("usage unavailable")
			continue
		}
		usage.Windows = windows
		return usage, true
	}
	if lastErr == nil {
		return usage, false
	}
	return usage, false
}

func (f ClaudeFetcher) webSessions(profileName string) []claudeWebSession {
	targetOrganizationID := f.profileOrganizationID(profileName)
	sessions := make([]claudeWebSession, 0, 1+len(f.browserCookieSources()))
	if f.TargetID == "claude_desktop_oauth" {
		if profileDir, err := f.desktopProfileDir(profileName); err == nil {
			if sessionKey, err := readClaudeDesktopSessionKey(profileDir, f.SafeStoragePassword); err == nil && strings.TrimSpace(sessionKey) != "" {
				sessions = append(sessions, claudeWebSession{
					CookieHeader:         "sessionKey=" + sessionKey,
					TargetOrganizationID: targetOrganizationID,
					Source:               "Claude Desktop",
				})
			}
		}
	}
	for _, source := range f.browserCookieSources() {
		header, err := readClaudeBrowserCookieHeader(source)
		if err != nil || strings.TrimSpace(header) == "" {
			continue
		}
		sessions = append(sessions, claudeWebSession{
			CookieHeader:         header,
			TargetOrganizationID: targetOrganizationID,
			Source:               source.Name,
		})
	}
	return sessions
}

func (f ClaudeFetcher) browserCookieSources() []ClaudeBrowserCookieSource {
	if len(f.BrowserCookieSources) > 0 {
		return f.BrowserCookieSources
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []ClaudeBrowserCookieSource{
		{
			Name:               "Chrome",
			CookiesPath:        filepath.Join(home, "Library/Application Support/Google/Chrome/Default/Cookies"),
			SafeStorageService: "Chrome Safe Storage",
		},
		{
			Name:               "Microsoft Edge",
			CookiesPath:        filepath.Join(home, "Library/Application Support/Microsoft Edge/Default/Cookies"),
			SafeStorageService: "Microsoft Edge Safe Storage",
		},
	}
}

func (f ClaudeFetcher) profileOrganizationID(profileName string) string {
	profileDir, err := f.profileDir(profileName)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(profileDir, ".claude.json"))
	if err != nil {
		return ""
	}
	var cfg claudeCLIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.OAuthAccount.OrganizationUUID)
}

func (f ClaudeFetcher) fetchClaudeWebOrganizationID(ctx context.Context, client *http.Client, baseURL, cookieHeader, targetOrganizationID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/organizations", nil)
	if err != nil {
		return "", err
	}
	setClaudeWebHeaders(req, cookieHeader)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("Claude web session expired")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Claude web organizations request failed: %s", resp.Status)
	}
	var organizations []claudeWebOrganization
	if err := json.NewDecoder(resp.Body).Decode(&organizations); err != nil {
		return "", fmt.Errorf("decode Claude organizations response: %v", err)
	}
	if targetOrganizationID != "" {
		for _, organization := range organizations {
			if organization.UUID == targetOrganizationID {
				return organization.UUID, nil
			}
		}
		return "", fmt.Errorf("Claude web session does not include profile organization")
	}
	for _, organization := range organizations {
		if claudeOrganizationHasChat(organization) {
			return organization.UUID, nil
		}
	}
	for _, organization := range organizations {
		if organization.OrganizationType != "api" && strings.TrimSpace(organization.UUID) != "" {
			return organization.UUID, nil
		}
	}
	if len(organizations) > 0 && strings.TrimSpace(organizations[0].UUID) != "" {
		return organizations[0].UUID, nil
	}
	return "", fmt.Errorf("no Claude organization found")
}

func (f ClaudeFetcher) fetchClaudeWebUsageWindows(ctx context.Context, client *http.Client, baseURL, cookieHeader, orgID string) ([]NamedUsageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/organizations/"+orgID+"/usage", nil)
	if err != nil {
		return nil, err
	}
	setClaudeWebHeaders(req, cookieHeader)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("Claude web session expired")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Claude web usage request failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var response claudeOAuthUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode Claude web usage response: %v", err)
	}
	return claudeWindowsFromOAuth(response), nil
}

func (f ClaudeFetcher) fetchOAuthUsage(ctx context.Context, profileDir string) (ClaudeProfileUsage, bool) {
	var usage ClaudeProfileUsage
	credentials, err := readClaudeOAuthCredentials(profileDir)
	if err != nil || strings.TrimSpace(credentials.AccessToken) == "" {
		return usage, false
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	oauthURL := strings.TrimSpace(f.OAuthURL)
	if oauthURL == "" {
		oauthURL = "https://api.anthropic.com/api/oauth/usage"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, oauthURL, nil)
	if err != nil {
		usage.Error = err.Error()
		return usage, true
	}
	req.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "claude-code/2.1.0")
	resp, err := client.Do(req)
	if err != nil {
		usage.Error = err.Error()
		return usage, true
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return usage, false
	case http.StatusTooManyRequests:
		usage.Error = "usage rate limited"
		return usage, true
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		usage.Error = fmt.Sprintf("usage request failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
		return usage, true
	}
	var response claudeOAuthUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		usage.Error = fmt.Sprintf("decode usage response: %v", err)
		return usage, true
	}
	usage.Windows = claudeWindowsFromOAuth(response)
	if len(usage.Windows) == 0 {
		usage.Error = "usage unavailable"
	}
	return usage, true
}

func readClaudeOAuthCredentials(profileDir string) (claudeOAuthCredentials, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, ".vibeswap_keychain.json"))
	if err != nil {
		return claudeOAuthCredentials{}, err
	}
	var profile claudeKeychainProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return claudeOAuthCredentials{}, err
	}
	var token claudeStoredToken
	if err := json.Unmarshal([]byte(profile.Token), &token); err != nil {
		return claudeOAuthCredentials{}, err
	}
	return token.ClaudeAIOAuth, nil
}

func (f ClaudeFetcher) profileDir(profileName string) (string, error) {
	if strings.TrimSpace(f.ProfilesRoot) != "" {
		return filepath.Join(f.ProfilesRoot, profileName), nil
	}
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(profilesDir, "claude_cli", profileName), nil
}

func (f ClaudeFetcher) desktopProfileDir(profileName string) (string, error) {
	if strings.TrimSpace(f.DesktopProfilesRoot) != "" {
		return filepath.Join(f.DesktopProfilesRoot, profileName), nil
	}
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(profilesDir, "claude_desktop_oauth", profileName), nil
}

func claudeOrganizationHasChat(organization claudeWebOrganization) bool {
	for _, capability := range organization.Capabilities {
		if capability == "chat" {
			return true
		}
	}
	return false
}

func claudeWindowsFromUsageText(text string) []NamedUsageWindow {
	type candidate struct {
		label string
		keys  []string
	}
	candidates := []candidate{
		{label: "5h", keys: []string{"current session"}},
		{label: "weekly", keys: []string{"current week (all models)", "current week"}},
		{label: "sonnet", keys: []string{"current week (sonnet only)", "sonnet only"}},
	}
	lines := strings.Split(stripANSI(text), "\n")
	windows := make([]NamedUsageWindow, 0, len(candidates))
	for _, candidate := range candidates {
		if percent, ok := claudePercentForKeys(lines, candidate.keys); ok {
			windows = append(windows, NamedUsageWindow{
				Label:       candidate.label,
				UsedPercent: percent,
			})
		}
	}
	return windows
}

func claudeWindowsFromOAuth(response claudeOAuthUsageResponse) []NamedUsageWindow {
	windows := make([]NamedUsageWindow, 0, 4)
	if window, ok := claudeWindowFromOAuth("5h", response.FiveHour); ok {
		windows = append(windows, window)
	}
	if window, ok := claudeWindowFromOAuth("weekly", response.SevenDay); ok {
		windows = append(windows, window)
	}
	if window, ok := claudeWindowFromOAuth("sonnet", response.SevenDaySonnet); ok {
		windows = append(windows, window)
	} else if window, ok := claudeWindowFromOAuth("opus", response.SevenDayOpus); ok {
		windows = append(windows, window)
	}
	if window, ok := claudeWindowFromOAuth("extra", response.ExtraUsage); ok {
		windows = append(windows, window)
	}
	return windows
}

func claudeWindowFromOAuth(label string, window *claudeOAuthUsageWindow) (NamedUsageWindow, bool) {
	if window == nil || window.Utilization == nil {
		return NamedUsageWindow{}, false
	}
	used := int(clampFloat(*window.Utilization, 0, 100) + 0.5)
	return NamedUsageWindow{
		Label:       label,
		UsedPercent: used,
		ResetAt:     parseClaudeResetTime(window.ResetsAt),
	}, true
}

func readClaudeDesktopSessionKey(profileDir, safeStoragePassword string) (string, error) {
	cookiesPath := filepath.Join(profileDir, "Cookies")
	if _, err := os.Stat(cookiesPath); err != nil {
		return "", err
	}
	out, err := exec.Command("sqlite3", cookiesPath, "SELECT value || char(9) || hex(encrypted_value) FROM cookies WHERE name='sessionKey' AND host_key LIKE '%claude.ai' LIMIT 1;").Output()
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(strings.TrimRight(string(out), "\n"), "\t", 2)
	if len(parts) > 0 {
		if value := strings.TrimSpace(parts[0]); strings.HasPrefix(value, "sk-ant-") {
			return value, nil
		}
	}
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		return "", nil
	}
	password := safeStoragePassword
	if password == "" {
		var err error
		password, err = claudeSafeStoragePassword()
		if err != nil {
			return "", err
		}
	}
	encrypted, err := hex.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", err
	}
	value, err := decryptChromiumV10CookieValue(encrypted, password)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(value, "sk-ant-") {
		return "", fmt.Errorf("decrypted Claude cookie did not contain a session key")
	}
	return value, nil
}

func readClaudeBrowserCookieHeader(source ClaudeBrowserCookieSource) (string, error) {
	if source.CookiesPath == "" || source.SafeStorageService == "" {
		return "", fmt.Errorf("browser cookie source is incomplete")
	}
	if _, err := os.Stat(source.CookiesPath); err != nil {
		return "", err
	}
	out, err := exec.Command("sqlite3", source.CookiesPath, "SELECT name || char(9) || value || char(9) || hex(encrypted_value) FROM cookies WHERE host_key LIKE '%claude.ai%' ORDER BY name;").Output()
	if err != nil {
		return "", err
	}
	password := source.SafeStoragePassword
	if password == "" {
		password, err = safeStoragePassword(source.SafeStorageService)
		if err != nil {
			return "", err
		}
	}
	var pairs []string
	hasSessionKey := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		value := parts[1]
		if value == "" && strings.TrimSpace(parts[2]) != "" {
			encrypted, err := hex.DecodeString(strings.TrimSpace(parts[2]))
			if err != nil {
				continue
			}
			value, err = decryptChromiumV10CookieValue(encrypted, password)
			if err != nil {
				continue
			}
		}
		if name == "" || value == "" {
			continue
		}
		if name == "sessionKey" && strings.HasPrefix(value, "sk-ant-") {
			hasSessionKey = true
		}
		pairs = append(pairs, name+"="+value)
	}
	if !hasSessionKey {
		return "", fmt.Errorf("sessionKey not found")
	}
	return strings.Join(pairs, "; "), nil
}

func claudeSafeStoragePassword() (string, error) {
	return safeStoragePassword("Claude Safe Storage")
}

func safeStoragePassword(service string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("Chromium cookie decryption is only supported on macOS")
	}
	out, err := exec.Command("security", "find-generic-password", "-w", "-s", service).Output()
	if err != nil {
		return "", fmt.Errorf("%s keychain item not available", service)
	}
	password := strings.TrimRight(string(out), "\r\n")
	if password == "" {
		return "", fmt.Errorf("%s keychain item is empty", service)
	}
	return password, nil
}

func decryptChromiumV10CookieValue(encrypted []byte, password string) (string, error) {
	if len(encrypted) <= 3 || string(encrypted[:3]) != "v10" {
		return "", fmt.Errorf("unsupported Chromium cookie encryption format")
	}
	key, err := pbkdf2.Key(sha1.New, password, []byte("saltysalt"), 1003, 16)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	payload := encrypted[3:]
	if len(payload)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid Claude cookie ciphertext")
	}
	plain := make([]byte, len(payload))
	iv := bytes.Repeat([]byte{0x20}, aes.BlockSize)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, payload)
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return "", err
	}
	value := string(plain)
	if len(plain) > 32 && !looksLikeCookieValue(value) {
		value = string(plain[32:])
	}
	return value, nil
}

func looksLikeCookieValue(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func setClaudeWebHeaders(req *http.Request, cookieHeader string) {
	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://claude.ai/")
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36")
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padding size")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	for _, value := range data[len(data)-pad:] {
		if int(value) != pad {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return data[:len(data)-pad], nil
}

func claudeUsageTextLooksLikeSubscriptionZeroFallback(text string, windows []NamedUsageWindow) bool {
	normalized := strings.ToLower(text)
	if !strings.Contains(normalized, "currently using your subscription") {
		return false
	}
	if len(windows) == 0 {
		return false
	}
	for _, window := range windows {
		if window.UsedPercent != 0 {
			return false
		}
	}
	return true
}

func parseClaudeResetTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

func claudePercentForKeys(lines []string, keys []string) (int, bool) {
	for _, line := range lines {
		lower := strings.ToLower(line)
		for _, key := range keys {
			if !strings.Contains(lower, key) {
				continue
			}
			if percent, ok := percentFromClaudeUsageLine(line); ok {
				return percent, true
			}
		}
	}
	return 0, false
}

func percentFromClaudeUsageLine(line string) (int, bool) {
	match := regexp.MustCompile(`([0-9]{1,3})(?:\.[0-9]+)?\s*%\s*(used|left|remaining|available)?`).FindStringSubmatch(strings.ToLower(line))
	if len(match) < 2 {
		return 0, false
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	if len(match) >= 3 && match[2] != "" && match[2] != "used" {
		value = 100 - value
	}
	return value, true
}

func stripANSI(value string) string {
	return regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`).ReplaceAllString(value, "")
}
