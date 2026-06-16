package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type TargetType string

const (
	TypeFile     TargetType = "file"
	TypeJSONKey  TargetType = "json_key"
	TypeKeychain TargetType = "keychain"
	TypeSQLite   TargetType = "sqlite" // Deferred/future
)

type Target struct {
	Name         string     `json:"name"`
	Type         TargetType `json:"type"`
	Path         string     `json:"path,omitempty"`
	Key          string     `json:"key,omitempty"`          // For json_key type
	Service      string     `json:"service,omitempty"`      // For keychain type
	Account      string     `json:"account,omitempty"`      // For keychain type
	FallbackFile string     `json:"fallback_file,omitempty"` // For keychain type fallback
	Keys         []string   `json:"keys,omitempty"`         // For sqlite type (future)
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
				Name: "Codex CLI",
				Type: TypeFile,
				Path: "~/.codex/auth.json",
			},
			"claude_cli": {
				Name:         "Claude Code CLI",
				Type:         TypeKeychain,
				Service:      "Claude Code-credentials",
				Account:      "edgarwongbaxter",
				FallbackFile: "~/.claude/.credentials.json",
			},
			"claude_desktop": {
				Name: "Claude Desktop App",
				Type: TypeJSONKey,
				Path: "~/Library/Application Support/Claude/config.json",
				Key:  "oauth:tokenCache",
			},
			"agy": {
				Name: "Antigravity CLI (agy)",
				Type: TypeFile,
				Path: "~/.gemini/oauth_creds.json",
			},
			"pi": {
				Name: "Pi CLI",
				Type: TypeFile,
				Path: "~/.pi/agent/auth.json",
			},
			"opencode": {
				Name: "OpenCode CLI",
				Type: TypeFile,
				Path: "~/.local/share/opencode/auth.json",
			},
		},
	}
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
