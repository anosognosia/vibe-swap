package engine

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/anosognosia/vibe-swap/pkg/config"
)

type ProfileMetadata struct {
	Email string
}

func ListProfileMetadata(profiles map[string][]string) map[string]map[string]ProfileMetadata {
	result := make(map[string]map[string]ProfileMetadata, len(profiles))
	for targetID, names := range profiles {
		result[targetID] = make(map[string]ProfileMetadata, len(names))
		for _, name := range names {
			result[targetID][name] = ProfileMetadata{
				Email: ProfileEmail(targetID, name),
			}
		}
	}
	return result
}

func ProfileEmail(targetID, profileName string) string {
	profilesDir, err := config.GetProfilesDir()
	if err != nil {
		return ""
	}
	targetDir := filepath.Join(profilesDir, targetID)
	candidates := []string{
		filepath.Join(targetDir, profileName+".json"),
		filepath.Join(targetDir, profileName),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if email := emailFromProfileDir(candidate); email != "" {
				return email
			}
			continue
		}
		if email := emailFromProfileFile(candidate); email != "" {
			return email
		}
	}
	return ""
}

func emailFromProfileFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if email := emailFromJSONProfile(data); email != "" {
		return email
	}
	return firstEmailFromText(string(data))
}

func emailFromProfileDir(path string) string {
	var found []string
	preferred := map[string]bool{
		".claude.json":              true,
		"config.json":               true,
		"profile.json":              true,
		"settings.json":             true,
		"Preferences":               true,
		"Local State":               true,
		"google_accounts.json":      true,
		"oauth_creds.json":          true,
		"buddy-tokens.json":         true,
		"remote-settings.json":      true,
		"policy-limits.json":        true,
		"mcp-needs-auth-cache.json": true,
		"Cookies":                   true,
		"DIPS":                      true,
	}
	_ = filepath.WalkDir(path, func(filePath string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		relative, _ := filepath.Rel(path, filePath)
		if strings.Contains(relative, "node_modules"+string(filepath.Separator)) ||
			strings.Contains(relative, "Claude Extensions"+string(filepath.Separator)) ||
			strings.Contains(relative, "vm_bundles"+string(filepath.Separator)) {
			return nil
		}
		browserStorage := strings.Contains(relative, "Local Storage"+string(filepath.Separator)+"leveldb"+string(filepath.Separator)) ||
			strings.Contains(relative, "IndexedDB"+string(filepath.Separator))
		if !preferred[name] && !browserStorage && !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".ldb") && !strings.HasSuffix(name, ".log") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 10*1024*1024 {
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil
		}
		if email := emailFromJSONProfile(data); email != "" {
			found = append(found, email)
			return filepath.SkipAll
		}
		if email := firstEmailFromText(string(data)); email != "" {
			found = append(found, email)
			return filepath.SkipAll
		}
		return nil
	})
	if len(found) == 0 {
		return ""
	}
	return found[0]
}

func emailFromJSONProfile(data []byte) string {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return ""
	}
	if email := emailFromKnownJSON(value); email != "" {
		return email
	}
	if email := emailFromEncodedFiles(value); email != "" {
		return email
	}
	return firstEmailFromAny(value)
}

func emailFromKnownJSON(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if tokens, ok := object["tokens"].(map[string]any); ok {
		if email := emailFromTokenMap(tokens); email != "" {
			return email
		}
	}
	if email := emailFromTokenMap(object); email != "" {
		return email
	}
	if active := firstEmailFromAny(object["active"]); active != "" {
		return active
	}
	return ""
}

func emailFromTokenMap(tokens map[string]any) string {
	for _, key := range []string{"email", "account_email", "accountEmail", "login"} {
		if email := firstEmailFromAny(tokens[key]); email != "" {
			return email
		}
	}
	for _, key := range []string{"id_token", "idToken"} {
		token, _ := tokens[key].(string)
		if email := emailFromJWT(token); email != "" {
			return email
		}
	}
	return ""
}

func emailFromEncodedFiles(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	files, ok := object["files"].(map[string]any)
	if !ok {
		return ""
	}
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		return fileEmailPriority(keys[i]) < fileEmailPriority(keys[j])
	})
	for _, key := range keys {
		encoded, ok := files[key].(string)
		if !ok || encoded == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		if email := emailFromJSONProfile(decoded); email != "" {
			return email
		}
		if email := firstEmailFromText(string(decoded)); email != "" {
			return email
		}
	}
	return ""
}

func fileEmailPriority(path string) int {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "google_accounts"):
		return 0
	case strings.Contains(lower, "oauth") || strings.Contains(lower, "auth"):
		return 1
	case strings.Contains(lower, "config") || strings.Contains(lower, "settings"):
		return 2
	default:
		return 3
	}
}

func emailFromJWT(token string) string {
	parts := strings.Split(token, ".")
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
	return firstEmailFromText(string(decoded))
}

func firstEmailFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return firstEmailFromText(typed)
	case map[string]any:
		for _, key := range []string{"email", "account", "login", "user", "username", "active"} {
			if email := firstEmailFromAny(typed[key]); email != "" {
				return email
			}
		}
		for _, nested := range typed {
			if email := firstEmailFromAny(nested); email != "" {
				return email
			}
		}
	case []any:
		for _, nested := range typed {
			if email := firstEmailFromAny(nested); email != "" {
				return email
			}
		}
	}
	return ""
}

func firstEmailFromText(value string) string {
	for _, email := range regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`).FindAllString(value, -1) {
		if isLikelyAccountEmail(email) {
			return email
		}
	}
	return ""
}

func isLikelyAccountEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	domain := parts[1]
	blockedDomains := []string{
		"mcpcontent.com",
		"v.claude.ai",
		"claude.ai",
		"anthropic.com",
		"segment.io",
		"sentry.io",
	}
	for _, blocked := range blockedDomains {
		if domain == blocked || strings.HasSuffix(domain, "."+blocked) {
			return false
		}
	}
	if len(parts[0]) <= 1 && strings.Contains(domain, "claude") {
		return false
	}
	return true
}
