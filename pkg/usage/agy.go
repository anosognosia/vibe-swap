package usage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/anosognosia/vibe-swap/pkg/config"
)

const (
	agyLoadCodeAssistURL       = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	agyFetchAvailableModelsURL = "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
)

type AgyProfileUsage struct {
	ProfileName string
	Plan        string
	Windows     []NamedUsageWindow
	Error       string
	UpdatedAt   time.Time
}

type NamedUsageWindow struct {
	Label       string
	UsedPercent int
	ResetAt     time.Time
}

type AgyFetcher struct {
	Client                  *http.Client
	LoadCodeAssistURL       string
	FetchAvailableModelsURL string
	OAuthTokenURL           string
}

type agyProfileFile struct {
	Files map[string]string `json:"files"`
}

type agyOAuthCredentials struct {
	AccessToken            string  `json:"access_token"`
	RefreshToken           string  `json:"refresh_token"`
	ExpiryDateMilliseconds float64 `json:"expiry_date"`
	ProjectID              string  `json:"project_id"`
	ClientID               string  `json:"client_id"`
	ClientSecret           string  `json:"client_secret"`
	IDToken                string  `json:"id_token"`
}

type agyOAuthClient struct {
	ClientID     string
	ClientSecret string
}

type agyRefreshResponse struct {
	AccessToken string  `json:"access_token"`
	ExpiresIn   float64 `json:"expires_in"`
}

type agyCodeAssistResponse struct {
	PlanInfo struct {
		PlanType string `json:"planType"`
	} `json:"planInfo"`
	CurrentTier struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"currentTier"`
	CloudAICompanionProject agyProjectReference `json:"cloudaicompanionProject"`
}

type agyProjectReference struct {
	Value string
}

func (p *agyProjectReference) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		p.Value = value
		return nil
	}
	var object struct {
		ID        string `json:"id"`
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if object.ID != "" {
		p.Value = object.ID
	} else {
		p.Value = object.ProjectID
	}
	return nil
}

type agyAvailableModelsResponse struct {
	Models map[string]agyRemoteModel `json:"models"`
}

type agyRemoteModel struct {
	DisplayName string        `json:"displayName"`
	Label       string        `json:"label"`
	QuotaInfo   *agyQuotaInfo `json:"quotaInfo"`
}

type agyQuotaInfo struct {
	RemainingFraction *float64 `json:"remainingFraction"`
	ResetTime         string   `json:"resetTime"`
}

type agyModelQuota struct {
	Label             string
	ModelID           string
	RemainingFraction float64
	ResetAt           time.Time
}

func FetchAgyProfileUsages(ctx context.Context, profileNames []string) map[string]AgyProfileUsage {
	fetcher := AgyFetcher{
		Client: &http.Client{Timeout: 5 * time.Second},
	}
	return fetcher.FetchProfiles(ctx, profileNames)
}

func (f AgyFetcher) FetchProfiles(ctx context.Context, profileNames []string) map[string]AgyProfileUsage {
	results := make(map[string]AgyProfileUsage, len(profileNames))
	for _, profileName := range profileNames {
		results[profileName] = f.FetchProfile(ctx, profileName)
	}
	return results
}

func (f AgyFetcher) FetchProfile(ctx context.Context, profileName string) AgyProfileUsage {
	usage := AgyProfileUsage{
		ProfileName: profileName,
		UpdatedAt:   time.Now(),
	}
	credentials, err := ReadAgyProfileCredentials(profileName)
	if err != nil {
		usage.Error = err.Error()
		return usage
	}
	if strings.TrimSpace(credentials.AccessToken) == "" {
		usage.Error = "missing access token"
		return usage
	}
	if credentials.ExpiryDateMilliseconds > 0 {
		expiry := time.UnixMilli(int64(credentials.ExpiryDateMilliseconds))
		if time.Until(expiry) <= 0 {
			if err := f.refreshAccessToken(ctx, credentials); err != nil {
				usage.Error = fmt.Sprintf("access token expired: %v", err)
				return usage
			}
		}
	}

	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	loadURL := f.LoadCodeAssistURL
	if loadURL == "" {
		loadURL = agyLoadCodeAssistURL
	}
	modelsURL := f.FetchAvailableModelsURL
	if modelsURL == "" {
		modelsURL = agyFetchAvailableModelsURL
	}

	codeAssist, err := sendAgyRequest[agyCodeAssistResponse](ctx, client, loadURL, credentials.AccessToken, map[string]any{
		"metadata": map[string]any{
			"ideType":    "ANTIGRAVITY",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	})
	if err != nil {
		if isAgyUnauthorized(err) {
			if refreshErr := f.refreshAccessToken(ctx, credentials); refreshErr == nil {
				codeAssist, err = sendAgyRequest[agyCodeAssistResponse](ctx, client, loadURL, credentials.AccessToken, map[string]any{
					"metadata": map[string]any{
						"ideType":    "ANTIGRAVITY",
						"platform":   "PLATFORM_UNSPECIFIED",
						"pluginType": "GEMINI",
					},
				})
			}
		}
		if err != nil {
			usage.Error = err.Error()
			return usage
		}
	}
	usage.Plan = agyPlanName(codeAssist)
	projectID := strings.TrimSpace(credentials.ProjectID)
	if projectID == "" {
		projectID = strings.TrimSpace(codeAssist.CloudAICompanionProject.Value)
	}
	body := map[string]any{}
	if projectID != "" {
		body["project"] = projectID
	}
	models, err := sendAgyRequest[agyAvailableModelsResponse](ctx, client, modelsURL, credentials.AccessToken, body)
	if err != nil {
		usage.Error = err.Error()
		return usage
	}
	usage.Windows = agyWindowsFromModels(models)
	if len(usage.Windows) == 0 {
		usage.Error = "no quota models available"
	}
	return usage
}

func (f AgyFetcher) refreshAccessToken(ctx context.Context, credentials *agyOAuthCredentials) error {
	if strings.TrimSpace(credentials.RefreshToken) == "" {
		return fmt.Errorf("missing refresh token")
	}
	oauthClient, err := resolveAgyOAuthClient(credentials)
	if err != nil {
		return err
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	form := fmt.Sprintf(
		"client_id=%s&client_secret=%s&refresh_token=%s&grant_type=refresh_token",
		urlQueryEscape(oauthClient.ClientID),
		urlQueryEscape(oauthClient.ClientSecret),
		urlQueryEscape(credentials.RefreshToken),
	)
	tokenURL := f.OAuthTokenURL
	if tokenURL == "" {
		tokenURL = "https://oauth2.googleapis.com/token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh agy access token failed: %s", resp.Status)
	}
	var body agyRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	if strings.TrimSpace(body.AccessToken) == "" {
		return fmt.Errorf("refresh agy access token missing access token")
	}
	credentials.AccessToken = body.AccessToken
	if body.ExpiresIn > 0 {
		credentials.ExpiryDateMilliseconds = float64(time.Now().Add(time.Duration(body.ExpiresIn) * time.Second).UnixMilli())
	}
	return nil
}

func ReadAgyProfileCredentials(profileName string) (*agyOAuthCredentials, error) {
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(profilesDir, "agy", profileName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var profile agyProfileFile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("decode agy profile: %v", err)
	}
	raw := profile.Files["~/.gemini/oauth_creds.json"]
	if raw == "" {
		return nil, fmt.Errorf("missing ~/.gemini/oauth_creds.json in profile")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode agy oauth credentials: %v", err)
	}
	var credentials agyOAuthCredentials
	if err := json.Unmarshal(decoded, &credentials); err != nil {
		return nil, fmt.Errorf("parse agy oauth credentials: %v", err)
	}
	return &credentials, nil
}

func sendAgyRequest[T any](ctx context.Context, client *http.Client, url, accessToken string, body map[string]any) (T, error) {
	var zero T
	payload, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity")

	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("agy usage request failed: %s", resp.Status)
	}
	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return zero, fmt.Errorf("decode agy usage response: %v", err)
	}
	return result, nil
}

func isAgyUnauthorized(err error) bool {
	return err != nil && strings.Contains(err.Error(), "401")
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}

func resolveAgyOAuthClient(credentials *agyOAuthCredentials) (agyOAuthClient, error) {
	if strings.TrimSpace(credentials.ClientID) != "" && strings.TrimSpace(credentials.ClientSecret) != "" {
		return agyOAuthClient{ClientID: credentials.ClientID, ClientSecret: credentials.ClientSecret}, nil
	}
	idTokenClientID := agyClientIDFromIDToken(credentials.IDToken)
	if envID := strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_ID")); envID != "" {
		if envSecret := strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET")); envSecret != "" {
			return agyOAuthClient{ClientID: envID, ClientSecret: envSecret}, nil
		}
	}
	if client, ok := discoverAgyOAuthClient(); ok {
		if idTokenClientID != "" {
			client.ClientID = idTokenClientID
		}
		return client, nil
	}
	return agyOAuthClient{}, fmt.Errorf("antigravity OAuth client not found")
}

func agyClientIDFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload := strings.NewReplacer("-", "+", "_", "/").Replace(parts[1])
	if rem := len(payload) % 4; rem > 0 {
		payload += strings.Repeat("=", 4-rem)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	var claims struct {
		Audience        string `json:"aud"`
		AuthorizedParty string `json:"azp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	if strings.TrimSpace(claims.Audience) != "" {
		return strings.TrimSpace(claims.Audience)
	}
	return strings.TrimSpace(claims.AuthorizedParty)
}

func discoverAgyOAuthClient() (agyOAuthClient, bool) {
	home, _ := os.UserHomeDir()
	roots := []string{"/Applications"}
	if home != "" {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	relativePaths := []string{
		"Contents/Resources/app/extensions/antigravity/bin/language_server_macos_arm",
		"Contents/Resources/app/extensions/antigravity/bin/language_server_macos_x64",
		"Contents/Resources/app/extensions/antigravity/bin/language_server_macos",
		"Contents/Resources/app/out/main.js",
		"Contents/Resources/bin/language_server",
		"Contents/Resources/bin/language_server_macos",
	}
	for _, root := range roots {
		apps := []string{filepath.Join(root, "Antigravity.app")}
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if entry.IsDir() && strings.HasSuffix(entry.Name(), ".app") && strings.Contains(strings.ToLower(entry.Name()), "antigravity") {
				apps = append(apps, filepath.Join(root, entry.Name()))
			}
		}
		for _, app := range apps {
			for _, rel := range relativePaths {
				path := filepath.Join(app, rel)
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				if client, ok := parseAgyOAuthClient(data); ok {
					return client, true
				}
			}
		}
	}
	return agyOAuthClient{}, false
}

func parseAgyOAuthClient(data []byte) (agyOAuthClient, bool) {
	if content := string(data); content != "" {
		if client, ok := parseAgyOAuthClientText(content); ok {
			return client, true
		}
	}
	clientIDs := uniqueStrings(regexp.MustCompile(`[0-9]+-[A-Za-z0-9_-]+\.apps\.googleusercontent\.com`).FindAllString(string(data), -1))
	clientSecrets := uniqueStrings(regexp.MustCompile(`GOCSPX-[A-Za-z0-9_-]{28}`).FindAllString(string(data), -1))
	if len(clientIDs) == 0 || len(clientSecrets) == 0 {
		return agyOAuthClient{}, false
	}
	secret := clientSecrets[0]
	if len(clientSecrets) == len(clientIDs) && len(clientSecrets) > 1 {
		secret = clientSecrets[len(clientSecrets)-1]
	}
	id := clientIDs[0]
	if len(clientSecrets) == 1 && len(clientIDs) > 1 {
		id = clientIDs[len(clientIDs)-1]
	}
	return agyOAuthClient{ClientID: id, ClientSecret: secret}, true
}

func parseAgyOAuthClientText(content string) (agyOAuthClient, bool) {
	start := 0
	if idx := strings.Index(content, "vs/platform/cloudCode/common/oauthClient.js"); idx >= 0 {
		start = idx
	}
	end := start + 4000
	if end > len(content) {
		end = len(content)
	}
	haystack := content[start:end]
	id := regexp.MustCompile(`[0-9]+-[A-Za-z0-9_-]+\.apps\.googleusercontent\.com`).FindString(haystack)
	secret := regexp.MustCompile(`GOCSPX-[A-Za-z0-9_-]{28}`).FindString(haystack)
	if id == "" || secret == "" {
		return agyOAuthClient{}, false
	}
	return agyOAuthClient{ClientID: id, ClientSecret: secret}, true
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func agyPlanName(response agyCodeAssistResponse) string {
	if value := strings.TrimSpace(response.PlanInfo.PlanType); value != "" {
		return value
	}
	if value := strings.TrimSpace(response.CurrentTier.Name); value != "" {
		return value
	}
	return strings.TrimSpace(response.CurrentTier.ID)
}

func agyWindowsFromModels(response agyAvailableModelsResponse) []NamedUsageWindow {
	quotas := make([]agyModelQuota, 0, len(response.Models))
	for modelID, model := range response.Models {
		if model.QuotaInfo == nil || model.QuotaInfo.RemainingFraction == nil {
			continue
		}
		label := strings.TrimSpace(model.DisplayName)
		if label == "" {
			label = strings.TrimSpace(model.Label)
		}
		if label == "" {
			label = modelID
		}
		quotas = append(quotas, agyModelQuota{
			Label:             label,
			ModelID:           modelID,
			RemainingFraction: clampFloat(*model.QuotaInfo.RemainingFraction, 0, 1),
			ResetAt:           parseAgyResetTime(model.QuotaInfo.ResetTime),
		})
	}
	windows := make([]NamedUsageWindow, 0, 2)
	if quota, ok := representativeAgyQuota(quotas, "gemini"); ok {
		windows = append(windows, agyWindow("Gemini", quota))
	}
	if quota, ok := representativeAgyQuota(quotas, "claude-gpt"); ok {
		windows = append(windows, agyWindow("Claude+GPT", quota))
	}
	if len(windows) == 0 {
		sort.Slice(quotas, func(i, j int) bool {
			if quotas[i].RemainingFraction != quotas[j].RemainingFraction {
				return quotas[i].RemainingFraction < quotas[j].RemainingFraction
			}
			return quotas[i].Label < quotas[j].Label
		})
		for _, quota := range quotas {
			windows = append(windows, agyWindow(quota.Label, quota))
			if len(windows) == 2 {
				break
			}
		}
	}
	return windows
}

func representativeAgyQuota(quotas []agyModelQuota, family string) (agyModelQuota, bool) {
	candidates := make([]agyModelQuota, 0)
	for _, quota := range quotas {
		text := strings.ToLower(quota.ModelID + " " + quota.Label)
		switch family {
		case "gemini":
			if strings.Contains(text, "gemini") && !strings.Contains(text, "lite") && !strings.Contains(text, "image") && !strings.Contains(text, "autocomplete") {
				candidates = append(candidates, quota)
			}
		case "claude-gpt":
			if (strings.Contains(text, "claude") || strings.Contains(text, "gpt") || strings.Contains(text, "openai")) && !strings.Contains(text, "lite") && !strings.Contains(text, "image") && !strings.Contains(text, "autocomplete") {
				candidates = append(candidates, quota)
			}
		}
	}
	if len(candidates) == 0 {
		return agyModelQuota{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].RemainingFraction != candidates[j].RemainingFraction {
			return candidates[i].RemainingFraction < candidates[j].RemainingFraction
		}
		return candidates[i].Label < candidates[j].Label
	})
	return candidates[0], true
}

func agyWindow(label string, quota agyModelQuota) NamedUsageWindow {
	return NamedUsageWindow{
		Label:       label,
		UsedPercent: int((1-quota.RemainingFraction)*100 + 0.5),
		ResetAt:     quota.ResetAt,
	}
}

func parseAgyResetTime(value string) time.Time {
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

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
