package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anosognosia/vibe-swap/pkg/config"
)

const codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

type CodexProfileUsage struct {
	ProfileName string
	Plan        string
	Session     UsageWindow
	Weekly      UsageWindow
	Error       string
	UpdatedAt   time.Time
}

type UsageWindow struct {
	UsedPercent int
	ResetAt     time.Time
}

type CodexFetcher struct {
	Client *http.Client
	URL    string
}

type codexAuthFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type codexUsageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		PrimaryWindow   codexRateWindow `json:"primary_window"`
		SecondaryWindow codexRateWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type codexRateWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     int64   `json:"reset_at"`
}

func FetchCodexProfileUsages(ctx context.Context, profileNames []string) map[string]CodexProfileUsage {
	fetcher := CodexFetcher{
		Client: &http.Client{Timeout: 4 * time.Second},
		URL:    codexUsageURL,
	}
	return fetcher.FetchProfiles(ctx, profileNames)
}

func (f CodexFetcher) FetchProfiles(ctx context.Context, profileNames []string) map[string]CodexProfileUsage {
	results := make(map[string]CodexProfileUsage, len(profileNames))
	for _, profileName := range profileNames {
		results[profileName] = f.FetchProfile(ctx, profileName)
	}
	return results
}

func (f CodexFetcher) FetchProfile(ctx context.Context, profileName string) CodexProfileUsage {
	usage := CodexProfileUsage{
		ProfileName: profileName,
		UpdatedAt:   time.Now(),
	}

	auth, err := ReadCodexProfileAuth(profileName)
	if err != nil {
		usage.Error = err.Error()
		return usage
	}
	if strings.TrimSpace(auth.Tokens.AccessToken) == "" {
		usage.Error = "missing access token"
		return usage
	}

	url := f.URL
	if url == "" {
		url = codexUsageURL
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		usage.Error = err.Error()
		return usage
	}
	req.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	req.Header.Set("User-Agent", "codex-cli")
	if accountID := strings.TrimSpace(auth.Tokens.AccountID); accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}

	resp, err := client.Do(req)
	if err != nil {
		usage.Error = err.Error()
		return usage
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		usage.Error = fmt.Sprintf("usage request failed: %s", resp.Status)
		return usage
	}

	var body codexUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		usage.Error = fmt.Sprintf("decode usage response: %v", err)
		return usage
	}

	usage.Plan = strings.TrimSpace(body.PlanType)
	usage.Session = usageWindowFromCodex(body.RateLimit.PrimaryWindow)
	usage.Weekly = usageWindowFromCodex(body.RateLimit.SecondaryWindow)
	return usage
}

func ReadCodexProfileAuth(profileName string) (*codexAuthFile, error) {
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(profilesDir, "codex", profileName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var auth codexAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("decode auth profile: %v", err)
	}
	return &auth, nil
}

func SortedUsageNames(usages map[string]CodexProfileUsage) []string {
	names := make([]string, 0, len(usages))
	for name := range usages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func usageWindowFromCodex(window codexRateWindow) UsageWindow {
	return UsageWindow{
		UsedPercent: int(window.UsedPercent + 0.5),
		ResetAt:     time.Unix(window.ResetAt, 0),
	}
}
