package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type TargetType string

const (
	TypeFile             TargetType = "file"
	TypeJSONKey          TargetType = "json_key"
	TypeKeychain         TargetType = "keychain"
	TypeSQLite           TargetType = "sqlite"
	TypeWrappedDir       TargetType = "wrapped_dir"
	TypeElectron         TargetType = "electron_profile"
	TypeClaudeDesk       TargetType = "claude_desktop_config"
	TypeElectronUserdata TargetType = "electron_userdata"
)

type KeychainItem struct {
	Service string `json:"service"`
	Account string `json:"account"`
}

type Target struct {
	Name            string         `json:"name"`
	Type            TargetType     `json:"type"`
	Path            string         `json:"path,omitempty"`
	Paths           []string       `json:"paths,omitempty"`            // For multiple files support
	Key             string         `json:"key,omitempty"`              // For json_key type
	Service         string         `json:"service,omitempty"`          // For keychain type
	Account         string         `json:"account,omitempty"`          // For keychain type
	FallbackFile    string         `json:"fallback_file,omitempty"`    // For keychain type fallback
	Keys            []string       `json:"keys,omitempty"`             // For sqlite type (future)
	EnvVar          string         `json:"env_var,omitempty"`          // For wrapped_dir type
	Binary          string         `json:"binary,omitempty"`           // For wrapped_dir type
	AppName         string         `json:"app_name,omitempty"`         // For macOS desktop app guards
	Processes       []string       `json:"processes,omitempty"`        // For desktop app process guards
	ProcessPatterns []string       `json:"process_patterns,omitempty"` // For desktop app full command-line guards
	KeychainItems   []KeychainItem `json:"keychain_items,omitempty"`   // For desktop app safe-storage entries
	SymlinkTarget   string         `json:"symlink_target,omitempty"`   // For electron_userdata: the live userData path that gets symlinked to the active profile
}

type Config struct {
	Targets map[string]Target `json:"targets"`
}

func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			if strings.HasPrefix(path, "~/") {
				return filepath.Join(home, path[2:])
			}
		}
	}
	return path
}

func GetConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "vibeswap")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func GetProfilesDir() (string, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func LoadConfig() (*Config, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(configDir, "config.json")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		cfg := GetDefaultConfig()
		if err := SaveConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	changed := normalizeConfig(&cfg)
	if changed {
		_ = SaveConfig(&cfg)
	}

	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return err
	}
	configPath := filepath.Join(configDir, "config.json")

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0600)
}

func GetDefaultConfig() *Config {
	return &Config{
		Targets: map[string]Target{
			"codex": {
				Name: "Codex CLI/Desktop",
				Type: TypeFile,
				Path: "~/.codex/auth.json",
			},
			"claude_cli": {
				Name:    "Claude Code CLI",
				Type:    TypeWrappedDir,
				Path:    "~/.claude",
				EnvVar:  "CLAUDE_CONFIG_DIR",
				Binary:  "claude",
				Service: "Claude Code-credentials",
			},
			"claude_desktop_oauth": {
				Name:          "Claude Desktop (OAuth Account)",
				Type:          TypeElectronUserdata,
				SymlinkTarget: "~/Library/Application Support/Claude",
				AppName:       "Claude",
				Processes:     []string{"Claude", "Claude Helper", "Claude Helper (Renderer)", "Claude Helper (GPU)", "Claude Helper (Plugin)"},
				ProcessPatterns: []string{
					"--user-data-dir=~/Library/Application Support/Claude",
					"Claude.app/Contents/MacOS/Claude",
				},
			},
			"agy": {
				Name:    "Antigravity CLI (agy)",
				Type:    TypeFile,
				Service: "gemini",
				Account: "antigravity",
				Paths: []string{
					"~/.gemini/antigravity-cli/antigravity-oauth-token",
					"~/.gemini/antigravity-cli/settings.json",
					"~/.gemini/oauth_creds.json",
					"~/.gemini/google_accounts.json",
				},
			},
		},
	}
}

func normalizeConfig(cfg *Config) bool {
	if cfg.Targets == nil {
		cfg.Targets = make(map[string]Target)
	}

	defaults := GetDefaultConfig()
	changed := false

	for _, deprecatedTargetID := range []string{"codex_desktop", "claude_desktop"} {
		if _, ok := cfg.Targets[deprecatedTargetID]; ok {
			delete(cfg.Targets, deprecatedTargetID)
			changed = true
		}
	}

	for id, target := range defaults.Targets {
		current, ok := cfg.Targets[id]
		if !ok {
			cfg.Targets[id] = target
			changed = true
			continue
		}

		switch id {
		case "codex":
			if current.Name == "Codex CLI" || current.Name == "" {
				current.Name = target.Name
				cfg.Targets[id] = current
				changed = true
			}
		case "claude_desktop_oauth":
			targetChanged := false
			if current.Type == "" {
				current.Type = target.Type
				targetChanged = true
			}
			if current.SymlinkTarget == "" {
				current.SymlinkTarget = target.SymlinkTarget
				targetChanged = true
			}
			if current.AppName == "" {
				current.AppName = target.AppName
				targetChanged = true
			}
			if len(current.Processes) == 0 {
				current.Processes = target.Processes
				targetChanged = true
			}
			if len(current.ProcessPatterns) == 0 {
				current.ProcessPatterns = target.ProcessPatterns
				targetChanged = true
			}
			if targetChanged {
				cfg.Targets[id] = current
				changed = true
			}
		}
	}

	return changed
}

func mergeMissingStrings(current, defaults []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(current)+len(defaults))
	merged := make([]string, 0, len(current)+len(defaults))
	for _, value := range current {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}

	changed := false
	for _, value := range defaults {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
		changed = true
	}
	return merged, changed
}

type ActiveState struct {
	Targets map[string]string `json:"targets"`
}

func LoadActiveState() (*ActiveState, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(configDir, "active.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &ActiveState{Targets: make(map[string]string)}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state ActiveState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Targets == nil {
		state.Targets = make(map[string]string)
	}
	return &state, nil
}

func SaveActiveState(state *ActiveState) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, "active.json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
